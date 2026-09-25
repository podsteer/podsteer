/**
 * Every keyboard shortcut the application has, in one place.
 *
 * BEFORE THIS, each shortcut was a handful of lines duplicated between the
 * component that handled the keystroke (App.svelte, ClusterWorkspace.svelte,
 * ClusterTabs.svelte) and whatever tooltip or Settings copy tried to describe
 * it — "⌘B" typed out by hand in three places, none of which the compiler
 * checks against the others. ShortcutSheet.svelte exists to list them all in
 * one screen, which only tells the truth if it reads from the exact same
 * table the handlers act on. So this module is that table, and every
 * consumer — the handlers and the sheet alike — goes through `shortcut(id)`
 * rather than re-typing a key.
 *
 * WHAT THIS MODULE HOLDS IS THE DEFAULTS. Each entry carries a BINDING as
 * data — modifiers and a key — and both halves everything else uses are
 * derived from it: `formatBinding` for the display, `matchesBinding` for the
 * predicate (see shortcutBinding.ts). They cannot disagree because neither is
 * written down.
 *
 * An operator's own bindings live in preferences and are applied by
 * $stores/shortcuts.svelte, which is what every consumer reads. This module
 * stays plain and pure so the table itself — the ids, the defaults, the
 * scopes — is arguable in a test with no store around it.
 */

import { isMac } from './platform'
import {
  formatBinding,
  matchesBinding,
  sameBinding,
  type Binding,
} from './shortcutBinding'

export type ShortcutScope = 'global' | 'cluster'

export interface Shortcut {
  /** Unique across the whole table — see shortcuts.test.ts. */
  id: string
  /** How this reads on the host platform, e.g. "⌘B" or "Ctrl+B". */
  keys: string
  /** What it does, in the same voice the sheet shows it in. */
  description: string
  /** The combination in force. Derived from the default or the operator's. */
  binding: Binding
  /**
   * Whether this one can be rebound.
   *
   * FALSE FOR THE TWO THAT ARE NOT A COMBINATION. "Switch to the Nth tab" is
   * nine keys behind one id, and the shortcut sheet's own ⌘/ has a bare "?"
   * alternative that depends on what has focus rather than on the combo — a
   * rebinding field can express neither, and offering one that silently
   * dropped half the behaviour would be worse than saying so.
   */
  rebindable: boolean
  /**
   * Where it applies. GLOBAL shortcuts work from any tab, including the
   * cluster picker, where no ClusterSession exists yet. CLUSTER shortcuts act
   * on the tab in front and are wired up inside ClusterWorkspace, which is
   * unmounted on the picker — so they simply do not fire there.
   */
  scope: ShortcutScope
  /** Whether a KeyboardEvent triggers this shortcut. */
  matches: (event: KeyboardEvent) => boolean
}

/** One entry of the defaults table, before a binding is resolved. */
export interface ShortcutDefault {
  id: string
  description: string
  scope: ShortcutScope
  binding: Binding
  rebindable?: boolean
  /**
   * A second combination this shortcut also answers to, kept only while it
   * is at its default.
   *
   * ⌘P alongside ⌘⇧P for the palette: k9s and Lens both train the bare
   * accelerator, and the convention elsewhere is the shifted one. An operator
   * who picks their own key gets exactly what they picked — an alternate they
   * did not choose, surviving a rebinding, is a key they cannot get rid of.
   */
  alternate?: Binding
  /** How the pair reads when both are in force. */
  alternateKeys?: string
  /**
   * A predicate of its own, for the entry that is not a combination.
   *
   * Only switch-tab has one: nine keys behind one id cannot be expressed as a
   * Binding, which is the same fact `rebindable: false` states. Every other
   * entry's predicate is DERIVED from its binding and cannot be written down
   * — that is the point of the refactor this field is the exception to.
   */
  matches?: (event: KeyboardEvent) => boolean
}

/** Whether Cmd (macOS) or Ctrl (everywhere else) is held — the app accepts
    either modifier on any platform, and only the DISPLAYED label changes. */
function accelerated(event: KeyboardEvent): boolean {
  return event.metaKey || event.ctrlKey
}

/** An accelerator plus one case-insensitive key, which is what almost every
    shortcut here is. */
function accel(key: string): (event: KeyboardEvent) => boolean {
  return (event) => accelerated(event) && event.key.toLowerCase() === key
}

/** A binding of accelerator plus one key, which is what most of these are. */
function accelKey(key: string): Binding {
  return { accel: true, shift: false, alt: false, key }
}

/**
 * The defaults, in the order the sheet lists them.
 *
 * THE KEYS ARE NOT WRITTEN DOWN HERE, only the combination — what an entry
 * displays as is formatBinding's answer, on whichever platform is asking.
 */
export const SHORTCUT_DEFAULTS: ShortcutDefault[] = [
  {
    id: 'toggle-navigator',
    description: 'Show or hide the resource navigator',
    scope: 'cluster',
    binding: accelKey('b'),
  },
  {
    id: 'refresh',
    description: 'Refresh the active view',
    scope: 'cluster',
    binding: accelKey('r'),
  },
  {
    id: 'focus-search',
    description: 'Focus the search field',
    scope: 'cluster',
    binding: accelKey('k'),
  },
  {
    id: 'command-palette',
    description: 'Open the command palette',
    scope: 'global',
    // ⌘⇧P is what every other application trains; ⌘P is what k9s and Lens
    // train. Both are offered by default and neither collides with anything
    // else in this table. ⌘K is deliberately left to focus-search.
    binding: { accel: true, shift: false, alt: false, key: 'p' },
    alternate: { accel: true, shift: true, alt: false, key: 'p' },
    alternateKeys: isMac ? '⌘⇧P or ⌘P' : 'Ctrl+Shift+P or Ctrl+P',
  },
  {
    id: 'next-tab',
    description: 'Switch to the next tab',
    scope: 'global',
    binding: accelKey(']'),
  },
  {
    id: 'previous-tab',
    description: 'Switch to the previous tab',
    scope: 'global',
    binding: accelKey('['),
  },
  {
    id: 'switch-tab',
    description: 'Switch to the Nth open cluster tab',
    scope: 'global',
    // Nine keys behind one id. The binding names the first of them so the
    // table has one, and `rebindable: false` is why nothing ever offers to
    // change it — see Shortcut.rebindable.
    binding: accelKey('1'),
    rebindable: false,
    alternateKeys: isMac ? '⌘1–9' : 'Ctrl+1–9',
    matches: (event) => (event.metaKey || event.ctrlKey) && /^[1-9]$/.test(event.key),
  },
  {
    id: 'new-cluster',
    description: 'Go to the cluster picker',
    scope: 'global',
    binding: accelKey('n'),
  },
  {
    id: 'settings',
    description: 'Open Settings',
    scope: 'global',
    binding: accelKey(','),
  },
  {
    id: 'shortcut-sheet',
    description: 'Show this list of keyboard shortcuts',
    scope: 'global',
    // The bare "?" alternative lives in App.svelte rather than here: it
    // applies only when focus is outside a text field, which is a fact about
    // the DOM at the moment of the keystroke and not about the combination.
    // That is also why this one is not rebindable.
    binding: accelKey('/'),
    rebindable: false,
    alternateKeys: isMac ? '⌘/ or ?' : 'Ctrl+/ or ?',
  },
]

/**
 * Resolves the defaults against an operator's own bindings.
 *
 * ONE FUNCTION, AND EVERY CONSUMER READS ITS OUTPUT. The display string and
 * the predicate are both computed here from the same binding, so a rebound
 * shortcut cannot be shown as one key and fire on another.
 *
 * An override on a shortcut that is not rebindable is IGNORED rather than
 * refused: storage outlives the code that wrote it, a future build may make
 * one of them rebindable, and neither case is worth leaving somebody unable
 * to switch tabs over.
 */
export function resolveShortcuts(overrides: Record<string, Binding> = {}): Shortcut[] {
  return SHORTCUT_DEFAULTS.map((entry) => {
    const rebindable = entry.rebindable !== false
    const override = rebindable ? overrides[entry.id] : undefined
    const binding = override ?? entry.binding
    const custom = override !== undefined

    // The alternate survives only at the default: an operator who chose a key
    // gets exactly the key they chose. Switch-tab keeps its own spelling
    // because it was never one combination to begin with.
    const alternate = custom ? undefined : entry.alternate
    const keys = custom || !entry.alternateKeys ? formatBinding(binding) : entry.alternateKeys

    return {
      id: entry.id,
      keys,
      description: entry.description,
      scope: entry.scope,
      binding,
      rebindable,
      matches:
        entry.matches ??
        ((event: KeyboardEvent) =>
          matchesBinding(binding, event) ||
          (alternate !== undefined && matchesBinding(alternate, event))),
    }
  })
}

/**
 * The shortcuts as they are with no overrides at all.
 *
 * Kept for the places that legitimately want the defaults — the tests, and
 * the reset in Settings. Everything an operator SEES goes through
 * $stores/shortcuts.svelte instead, or it would not follow a rebinding.
 */
export const SHORTCUTS: Shortcut[] = resolveShortcuts()

/**
 * Looks a shortcut up by id.
 *
 * Throws on a miss rather than returning undefined: an id typo here is a
 * mistake in this codebase, not a condition a caller should have to guard —
 * every call site names a literal id that is meant to exist, and finding out
 * at once beats a handler that silently never fires.
 */
export function shortcut(id: string, table: Shortcut[] = SHORTCUTS): Shortcut {
  const found = table.find((entry) => entry.id === id)
  if (!found) throw new Error(`Unknown shortcut id: ${id}`)
  return found
}

/**
 * The shortcut an override would collide with, or null when it is free.
 *
 * A COLLISION IS REFUSED RATHER THAN RESOLVED, and the refusal names the
 * other shortcut: two handlers on one combination is not a preference an
 * operator can have — whichever fired first would look like the other one
 * being broken. Compared against the table AS RESOLVED, so a key freed by
 * rebinding something else is immediately available.
 */
export function conflictWith(table: Shortcut[], id: string, binding: Binding): Shortcut | null {
  for (const entry of table) {
    if (entry.id === id) continue
    if (sameBinding(entry.binding, binding)) return entry
  }
  return null
}

/**
 * Whether a keystroke should defer to an input the operator is typing into.
 *
 * The one caller is the bare "?" alternative for opening the shortcut sheet
 * — every other shortcut here needs Cmd/Ctrl, which no text field consumes,
 * so this check would be dead weight on them. An unmodified "?" is different:
 * without this, typing a literal question mark into the search field or the
 * YAML editor would pop the sheet open instead.
 */
export function isTypingTarget(target: EventTarget | null): boolean {
  return (
    target instanceof HTMLElement &&
    (target instanceof HTMLInputElement ||
      target instanceof HTMLTextAreaElement ||
      target.isContentEditable)
  )
}
