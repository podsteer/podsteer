import { afterEach, describe, expect, it, vi } from 'vitest'

import { Clipboard } from '@wailsio/runtime'

import { copyText } from './clipboard'

/**
 * Takes the DOM clipboard away for the length of one test.
 *
 * THE CASE THE SHIPPED APPLICATION IS IN. `navigator.clipboard` is only
 * defined in a secure context and the webview serves this page over the
 * framework's own scheme, so the property is simply absent there — which is
 * why the old `navigator.clipboard?.writeText(...)` did nothing at all and
 * had nothing to catch. happy-dom does provide one, so a test that wants the
 * real condition has to remove it.
 *
 * Returns the undo, and the undo handles "there was nothing there" the way
 * DetailList's layout stub does: restoring an absent descriptor is a no-op
 * that would leave the deletion in place for every test that followed.
 */
function withoutDomClipboard(): () => void {
  const original = Object.getOwnPropertyDescriptor(Navigator.prototype, 'clipboard')

  Object.defineProperty(navigator, 'clipboard', {
    configurable: true,
    get: () => undefined,
  })

  return () => {
    delete (navigator as unknown as Record<string, unknown>).clipboard
    if (original) Object.defineProperty(Navigator.prototype, 'clipboard', original)
  }
}

describe('copyText', () => {
  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('goes through the Go process first, and never touches the DOM when it takes', async () => {
    // THE ORDER IS THE FIX, not an implementation detail: the runtime's
    // clipboard exists whenever the application is running, while
    // navigator.clipboard is the one that is conditionally absent. Asserting
    // the DOM path was not reached is what pins the order — a test that only
    // checked the boolean would pass with the two swapped.
    const wails = vi.spyOn(Clipboard, 'SetText').mockResolvedValue(undefined)
    const dom = vi.spyOn(navigator.clipboard, 'writeText')

    await expect(copyText('kubectl get pods')).resolves.toBe(true)

    expect(wails).toHaveBeenCalledWith('kubectl get pods')
    expect(dom).not.toHaveBeenCalled()
  })

  it('falls back to the DOM when the Go process is not there', async () => {
    // The unit-test stub refuses every runtime call by design, which is
    // exactly the shape of a browser tab with no Go process behind it — so
    // this test is the `vite dev` case, taken for real rather than mocked.
    const dom = vi.spyOn(navigator.clipboard, 'writeText')

    await expect(copyText('kubectl get nodes')).resolves.toBe(true)

    expect(dom).toHaveBeenCalledWith('kubectl get nodes')
  })

  it('reports failure when neither mechanism exists, rather than nothing', async () => {
    // THE BUG. Both are unavailable, the optional chain used to make the whole
    // expression `undefined`, and every caller then said "Copied!" over an
    // untouched clipboard. `false` is the whole of the fix.
    const restore = withoutDomClipboard()
    try {
      await expect(copyText('kubectl get pods')).resolves.toBe(false)
    } finally {
      restore()
    }
  })

  it('reports failure when the DOM clipboard is present and refuses', async () => {
    // Distinct from the case above: the API exists, so the optional chain was
    // never the problem here — a document that is not focused, or a denied
    // permission, rejects. It must not be reported as a copy either.
    vi.spyOn(navigator.clipboard, 'writeText').mockRejectedValue(new Error('not focused'))

    await expect(copyText('kubectl get pods')).resolves.toBe(false)
  })

  it('never throws, whatever both mechanisms do', async () => {
    // Callers are button handlers. There is nothing one can do with an
    // exception that it cannot do with `false`, and an unhandled rejection
    // out of a click is a console error nobody sees.
    vi.spyOn(navigator.clipboard, 'writeText').mockImplementation(() => {
      throw new Error('thrown, not rejected')
    })

    await expect(copyText('anything')).resolves.toBe(false)
  })
})
