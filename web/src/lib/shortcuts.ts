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
 * `keys` is a DISPLAY string, built once at module load via platform.ts's
 * `accelerator`, so a Mac reads "⌘B" and everything else reads "Ctrl+B"
 * without either place having to know which platform it is.
 *
 * `matches` is the other half — a predicate over the actual KeyboardEvent —
 * so a handler that calls `shortcut('refresh').matches(event)` cannot drift
 * from what the sheet displays for the same id: change one, the other
 * follows, because they are read from the same object.
 */

import { accelerator, isMac } from './platform'

export type ShortcutScope = 'global' | 'cluster'

export interface Shortcut {
  /** Unique across the whole table — see shortcuts.test.ts. */
  id: string
  /** How this reads on the host platform, e.g. "⌘B" or "Ctrl+B". */
  keys: string
  /** What it does, in the same voice the sheet shows it in. */
  description: string
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

export const SHORTCUTS: Shortcut[] = [
  {
    id: 'toggle-navigator',
    keys: accelerator('B'),
    description: 'Show or hide the resource navigator',
    scope: 'cluster',
    matches: accel('b'),
  },
  {
    id: 'refresh',
    keys: accelerator('R'),
    description: 'Refresh the active view',
    scope: 'cluster',
    matches: accel('r'),
  },
  {
    id: 'focus-search',
    keys: accelerator('K'),
    description: 'Focus the search field',
    scope: 'cluster',
    matches: accel('k'),
  },
  {
    id: 'command-palette',
    // Two accelerators for one action, both spelled out because
    // accelerator() only ever formats a single combo — see 'switch-tab'
    // above for the same reason. ⌘⇧P matches every other application's
    // "command palette" convention (VS Code, Slack, Linear); ⌘P is offered
    // alongside it because k9s and Lens both train the same muscle memory
    // on a bare accelerator+P, and neither collides with anything already
    // in this table. ⌘K is deliberately left alone — see focus-search.
    keys: isMac ? '⌘⇧P or ⌘P' : 'Ctrl+Shift+P or Ctrl+P',
    description: 'Open the command palette',
    scope: 'global',
    // Shift is not checked: a letter's `event.key` case already differs
    // when Shift is held, and matching case-insensitively (as accel() does)
    // is what makes ⌘P and ⌘⇧P both land here without two separate ids
    // fighting over which one the sheet displays.
    matches: accel('p'),
  },
  {
    id: 'next-tab',
    keys: accelerator(']'),
    description: 'Switch to the next tab',
    scope: 'global',
    matches: accel(']'),
  },
  {
    id: 'previous-tab',
    keys: accelerator('['),
    description: 'Switch to the previous tab',
    scope: 'global',
    matches: accel('['),
  },
  {
    id: 'switch-tab',
    // Its own spelling rather than accelerator('1') — this covers nine keys,
    // not one, and accelerator() only ever formats a single character.
    keys: isMac ? '⌘1–9' : 'Ctrl+1–9',
    description: 'Switch to the Nth open cluster tab',
    scope: 'global',
    matches: (event) => accelerated(event) && /^[1-9]$/.test(event.key),
  },
  {
    id: 'new-cluster',
    keys: accelerator('N'),
    description: 'Go to the cluster picker',
    scope: 'global',
    matches: accel('n'),
  },
  {
    id: 'settings',
    keys: accelerator(','),
    description: 'Open Settings',
    scope: 'global',
    matches: accel(','),
  },
  {
    id: 'shortcut-sheet',
    // The bare "?" alternative is deliberately not part of `matches`: it only
    // applies when focus is not inside a text field, which is a fact about
    // the DOM at the moment of the keystroke, not about the combo itself. See
    // isTypingTarget below and its one caller in App.svelte.
    keys: `${accelerator('/')} or ?`,
    description: 'Show this list of keyboard shortcuts',
    scope: 'global',
    matches: accel('/'),
  },
]

/**
 * Looks a shortcut up by id.
 *
 * Throws on a miss rather than returning undefined: an id typo here is a
 * mistake in this codebase, not a condition a caller should have to guard —
 * every call site names a literal id that is meant to exist, and finding out
 * at once beats a handler that silently never fires.
 */
export function shortcut(id: string): Shortcut {
  const found = SHORTCUTS.find((entry) => entry.id === id)
  if (!found) throw new Error(`Unknown shortcut id: ${id}`)
  return found
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
