/** Host platform detection, for the few places native chrome differs. */

/**
 * Whether the app is running on macOS.
 *
 * The Go side asks for an inset-hidden title bar there, which leaves the
 * window controls floating over the top-left of our own header — so the header
 * has to reserve space for them. No other platform needs the inset.
 */
export const isMac: boolean =
  typeof navigator !== 'undefined' && /Mac|iPhone|iPad/.test(navigator.userAgent)
