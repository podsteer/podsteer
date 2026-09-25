/**
 * What a keyboard shortcut IS, once it can be changed.
 *
 * The table in shortcuts.ts used to hold two hand-written halves per entry: a
 * display string built by `accelerator()`, and a `matches` closure over a
 * KeyboardEvent. They agreed because one person wrote both lines at the same
 * moment — which is exactly the arrangement that stops working the day an
 * operator picks their own key, since neither half can then be a literal.
 *
 * So a shortcut carries a BINDING: the modifiers and the key, as data. The
 * display string and the predicate are both DERIVED from it, here, which is
 * what makes a rebound shortcut impossible to show wrongly — the sheet, the
 * tooltips and the handler all read the same object through one pair of
 * functions.
 *
 * A plain module, no Svelte: what counts as a valid binding, how one is
 * rendered, and when two of them collide are the rules worth arguing with in
 * a table-driven test. The reactive half — which bindings are in force right
 * now — is $stores/shortcuts.svelte.
 */

import { isMac } from './platform'

/**
 * One key combination.
 *
 * `accel` is Cmd on macOS and Ctrl everywhere else, kept as one flag rather
 * than two because the application has always accepted either modifier and
 * only ever DISPLAYED the platform's own — a binding that distinguished them
 * would be a binding that stops working when somebody moves machine.
 */
export interface Binding {
  accel: boolean
  shift: boolean
  alt: boolean
  /** The key itself, lower-cased. Never a modifier. */
  key: string
}

/** The keys a binding may not be, because they only ever qualify another. */
const MODIFIER_KEYS = new Set(['control', 'meta', 'shift', 'alt', 'altgraph', 'capslock', 'os'])

/**
 * Keys PodSteer will not let a shortcut take.
 *
 * ESCAPE IS THE ONE THAT MATTERS. Every dialog, drawer and menu in the
 * application unwinds on Escape through one layered handler ($lib/escape),
 * and a shortcut bound to it would fire while a dialog was closing — or
 * instead of it. The others are the keys a list is navigated with, which are
 * not this table's to take.
 */
const RESERVED_KEYS = new Set(['escape', 'tab', 'enter', ' ', 'arrowup', 'arrowdown', 'arrowleft', 'arrowright'])

/** Whether a keystroke can stand as a binding at all. */
export function bindableEvent(event: KeyboardEvent): boolean {
  const key = event.key.toLowerCase()
  if (MODIFIER_KEYS.has(key) || RESERVED_KEYS.has(key)) return false

  // A BARE LETTER IS REFUSED, and this is the rule that keeps the search box
  // usable: an unmodified "r" bound to Refresh would fire every time somebody
  // typed the letter into a field, and the handler cannot tell the two apart
  // without knowing what has focus. A function key needs no modifier — it is
  // not a character anybody types into anything.
  return event.metaKey || event.ctrlKey || event.altKey || isFunctionKey(key)
}

function isFunctionKey(key: string): boolean {
  return /^f([1-9]|1[0-9]|2[0-4])$/.test(key)
}

/** The binding a keystroke expresses. */
export function bindingFromEvent(event: KeyboardEvent): Binding {
  return {
    accel: event.metaKey || event.ctrlKey,
    // Shift is only part of a binding when the key is not a character it
    // already changes: ⇧2 arrives as "@" on a US layout and as something else
    // elsewhere, so recording both the shift and the shifted character would
    // make a binding that only matches on one keyboard layout.
    shift: event.shiftKey && event.key.length > 1,
    alt: event.altKey,
    key: event.key.toLowerCase(),
  }
}

/** Whether a keystroke triggers this binding. */
export function matchesBinding(binding: Binding, event: KeyboardEvent): boolean {
  if (event.key.toLowerCase() !== binding.key) return false
  if (binding.accel !== (event.metaKey || event.ctrlKey)) return false
  if (binding.alt !== event.altKey) return false

  // Shift is checked only when the binding asked for it, for the reason
  // bindingFromEvent gives: on a character key the shift is already in the
  // character, and requiring shift:false would break ⌘⇧P — which is how half
  // the world opens a command palette.
  if (binding.shift && !event.shiftKey) return false
  return true
}

/** Whether two bindings are the same combination. */
export function sameBinding(left: Binding, right: Binding): boolean {
  return (
    left.key === right.key &&
    left.accel === right.accel &&
    left.shift === right.shift &&
    left.alt === right.alt
  )
}

/**
 * How a binding reads on this platform.
 *
 * The modifier order is the platform's own — macOS writes ⌃⌥⇧⌘ and everything
 * else writes Ctrl+Alt+Shift — so a shortcut looks like the ones in every
 * other application on the same machine rather than like this one's idea of
 * an order.
 */
export function formatBinding(binding: Binding): string {
  const parts: string[] = []

  if (isMac) {
    if (binding.alt) parts.push('⌥')
    if (binding.shift) parts.push('⇧')
    if (binding.accel) parts.push('⌘')
    return parts.join('') + keyLabel(binding.key)
  }

  if (binding.accel) parts.push('Ctrl')
  if (binding.alt) parts.push('Alt')
  if (binding.shift) parts.push('Shift')
  parts.push(keyLabel(binding.key))
  return parts.join('+')
}

/** How one key reads: a letter in capitals, a named key spelled out. */
function keyLabel(key: string): string {
  const NAMED: Record<string, string> = {
    ',': ',',
    '.': '.',
    '/': '/',
    '[': '[',
    ']': ']',
    backspace: '⌫',
    delete: 'Del',
    home: 'Home',
    end: 'End',
    pageup: 'PgUp',
    pagedown: 'PgDn',
  }
  if (NAMED[key]) return NAMED[key]
  if (isFunctionKey(key)) return key.toUpperCase()
  return key.length === 1 ? key.toUpperCase() : key
}

/**
 * Reads a stored binding back, or null when it is not one.
 *
 * Storage outlives the code that wrote it and may have been hand-edited, so
 * the shape is checked rather than trusted — a shortcut with a corrupt
 * override falls back to its default instead of becoming unusable, which is
 * the one failure mode that would leave somebody unable to reach Settings to
 * fix it.
 */
export function readBinding(value: unknown): Binding | null {
  if (!value || typeof value !== 'object') return null
  const candidate = value as Partial<Binding>

  if (typeof candidate.key !== 'string' || candidate.key === '') return null
  const key = candidate.key.toLowerCase()
  if (MODIFIER_KEYS.has(key) || RESERVED_KEYS.has(key)) return null

  const binding: Binding = {
    accel: candidate.accel === true,
    shift: candidate.shift === true,
    alt: candidate.alt === true,
    key,
  }

  // The same rule a fresh keystroke has to pass: a stored bare letter would
  // fire inside the search field, however it got there.
  if (!binding.accel && !binding.alt && !isFunctionKey(key)) return null
  return binding
}
