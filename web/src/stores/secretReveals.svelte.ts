/**
 * Secret values that are on screen right now.
 *
 * Everything here is a reaction to how the incumbents get this wrong.
 *
 * Freelens renders the raw base64 in a plain text input and calls it masked.
 * Base64 is an encoding, not a cipher; a screenshot of that pane leaks the
 * credential to anyone who can type `base64 -d`. So nothing is ever rendered
 * encoded: a value is either hidden or it is plaintext somebody asked for.
 *
 * Freelens's reveal is also one-way — once shown, the control is replaced by
 * the value with no way back short of remounting the pane — and its reveal
 * state is global, so a value unmasked in private is still unmasked when an
 * unrelated pod is opened on a shared screen. Reveal here is scoped to one
 * key, re-hideable, and CLEARS ITSELF: on a timer, and whenever the window
 * loses focus, which is the moment a screen share usually starts.
 *
 * Headlamp has an open bug where the secret flashes on screen before the
 * authorisation error arrives. Nothing is recorded here until the call has
 * resolved, so a denial shows a denial and never a glimpse.
 *
 * A STORE RATHER THAN A COMPONENT, which it used to be. The control that
 * reveals a value is now one item in a row's menu, and a menu item cannot own
 * a timer — so the timing, the hiding and the one thing that must never be
 * got wrong live here, and the row simply asks.
 *
 * IN MEMORY AND NOWHERE ELSE. Not persisted, not written to disk, and cleared
 * wholesale when the window is not being looked at.
 */

import { revealSecretKey, setSecretKey } from '$lib/api/client'
import { toApiError } from '$lib/api/errors'

/** How long a revealed value stays on screen. */
const HIDE_AFTER_MS = 30_000

/** One revealed value, or the refusal that came back instead. */
export interface Revealed {
  value: string | null
  error: string
  loading: boolean
}

const EMPTY: Revealed = { value: null, error: '', loading: false }

class SecretReveals {
  /**
   * Reactive, because a row's value cell renders straight out of it — unlike
   * usageHistory, which is copied out once when a drawer opens.
   */
  #shown = $state<Record<string, Revealed>>({})
  #timers = new Map<string, ReturnType<typeof setTimeout>>()

  /** What is on screen for one key. Never undefined, so callers need no guard. */
  at(key: string): Revealed {
    return this.#shown[key] ?? EMPTY
  }

  /** Whether anything is revealed for one key. */
  isShown(key: string): boolean {
    return this.at(key).value !== null
  }

  /**
   * Reads one key of one Secret, on explicit request.
   *
   * One key, never the whole Secret, and never as a side effect of anything
   * else — this is only ever called because somebody chose the menu item.
   * Reading Secrets is an audited action that Kubernetes' own good-practices
   * page tells cluster operators to alert on, and Falco ships an enabled rule
   * for; a client that resolves every referenced Secret when a pane opens
   * generates exactly that signature on somebody else's dashboard.
   */
  reveal = async (
    key: string,
    clusterId: string,
    namespace: string,
    secret: string,
    secretKey: string,
  ): Promise<void> => {
    this.#shown[key] = { value: null, error: '', loading: true }

    try {
      // Assigned only after the call resolves. Rendering optimistically is
      // how a client shows a secret to somebody who was not allowed to read
      // it, for the moment before the error lands.
      const value = await revealSecretKey(clusterId, namespace, secret, secretKey)
      this.#shown[key] = { value, error: '', loading: false }
      this.#timers.set(key, setTimeout(() => this.hide(key), HIDE_AFTER_MS))
    } catch (cause) {
      this.#shown[key] = { value: null, error: toApiError(cause).message, loading: false }
    }
  }

  /**
   * Writes one key of a Secret, then re-reveals it through the same audited
   * read `reveal` uses — so what ends up on screen afterwards is what the
   * cluster now holds, never merely what was typed.
   *
   * REFUSES UNLESS THE KEY IS ALREADY SHOWN. Editing a value nobody has
   * looked at is the mistake the whole reveal-before-edit ordering exists to
   * prevent, and this is the last place that ordering can be enforced before
   * the write actually happens — a caller offering the control too early is
   * a bug here, not merely in the caller.
   */
  write = async (
    key: string,
    clusterId: string,
    namespace: string,
    secret: string,
    secretKey: string,
    value: string,
  ): Promise<void> => {
    if (!this.isShown(key)) {
      throw new Error('reveal this key before editing it')
    }

    await setSecretKey(clusterId, namespace, secret, secretKey, value)
    await this.reveal(key, clusterId, namespace, secret, secretKey)
  }

  /** Puts one value away. */
  hide = (key: string): void => {
    const timer = this.#timers.get(key)
    if (timer) {
      clearTimeout(timer)
      this.#timers.delete(key)
    }
    delete this.#shown[key]
  }

  /**
   * Puts everything away.
   *
   * Called when the window loses focus, which in practice is the moment
   * somebody alt-tabs to start a screen share or accepts a call. It costs a
   * click to get back and removes the failure mode where a credential sits
   * revealed behind a window nobody remembers is open.
   */
  hideAll = (): void => {
    for (const timer of this.#timers.values()) clearTimeout(timer)
    this.#timers.clear()
    this.#shown = {}
  }
}

export const secretReveals = new SecretReveals()

if (typeof window !== 'undefined') {
  window.addEventListener('blur', () => secretReveals.hideAll())
}
