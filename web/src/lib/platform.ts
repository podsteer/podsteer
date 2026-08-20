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

/**
 * Renders a keyboard shortcut the way the host platform writes it.
 *
 * macOS uses the symbol, everything else spells out Ctrl. Showing "Ctrl+B" on
 * a Mac — or "⌘B" on Linux — reads as a shortcut for a different application.
 */
export function accelerator(key: string): string {
  return isMac ? `\u2318${key.toUpperCase()}` : `Ctrl+${key.toUpperCase()}`
}
