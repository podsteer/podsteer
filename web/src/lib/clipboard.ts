/**
 * Putting text on the operator's clipboard, and SAYING WHETHER IT WORKED.
 *
 * THE BUG THIS EXISTS FOR. Every copy control in the application used to be
 * `navigator.clipboard?.writeText(text).catch(() => {})` — written to be
 * silent, on the argument that a permissioned API can refuse and the text is
 * on screen either way. That argument is fine for a refusal and wrong for
 * this: `navigator.clipboard` is only defined in a SECURE CONTEXT, and the
 * webview serves this page over the framework's own scheme rather than https
 * or localhost. Where the property is undefined the optional chain makes the
 * whole expression `undefined`, so there is no promise, no rejection and
 * nothing for `.catch` to see — the copy never happens at all. The row menu
 * then flashed "Copied!" beside it, and an operator who trusted that pasted
 * whatever had been on their clipboard before, which on a shared machine is
 * somebody else's text.
 *
 * So this returns a BOOLEAN rather than swallowing the outcome, and every
 * confirmation in the application is now downstream of that boolean. A copy
 * control may still be quiet about failing; it may not claim to have
 * succeeded.
 *
 * WHY THE GO PROCESS IS TRIED FIRST. The runtime ships a clipboard that goes
 * through the Go side, and its availability matches the application's own:
 * if PodSteer is running, that path exists. `navigator.clipboard` does not —
 * it is absent outside a secure context, and even where it is present it
 * additionally wants the document focused and, on some engines, a live user
 * activation, so a copy fired as a menu closes can be refused for reasons
 * that have nothing to do with what the operator asked for. Ordering it the
 * other way would put the mechanism that is CONDITIONALLY THERE ahead of the
 * one that is ALWAYS there, and every copy in the shipped application would
 * pay a failure before working.
 *
 * WHY THE DOM PATH IS KEPT AT ALL. `vite dev` serves the frontend over
 * http://localhost, which IS a secure context and where no Go process is
 * attached to answer the runtime call — so the fallback is what makes a copy
 * work in a plain browser tab during development, and it is the path the
 * unit tests exercise for real rather than through a mock.
 *
 * Both are tried on every call rather than one being probed once and
 * remembered: a probe is a second source of truth about the same question,
 * and the cost here is one failed call on a gesture a human made by hand.
 */

import { Clipboard } from '@wailsio/runtime'

/**
 * Puts `text` on the clipboard, reporting whether it got there.
 *
 * `true` means one of the two mechanisms accepted the text. `false` means
 * neither did, and the caller must not tell anybody the copy happened.
 *
 * Never throws: a caller is a button handler, and there is nothing useful for
 * one to do with an exception that it cannot do with `false`.
 */
export async function copyText(text: string): Promise<boolean> {
  try {
    await Clipboard.SetText(text)
    return true
  } catch {
    // Not an error worth reporting on its own — it is the expected answer in
    // a browser tab with no Go process behind it, and the DOM path below is
    // what that case is for.
  }

  try {
    // Read through a local, because the property is genuinely absent outside
    // a secure context and reading it twice invites the check and the use to
    // disagree.
    const api = navigator.clipboard
    if (!api) return false
    await api.writeText(text)
    return true
  } catch {
    return false
  }
}
