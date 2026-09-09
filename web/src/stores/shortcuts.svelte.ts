/**
 * The shortcuts as they are RIGHT NOW — defaults, with the operator's own
 * bindings applied.
 *
 * A THIN REACTIVE LAYER OVER A PURE TABLE. $lib/shortcuts holds the defaults
 * and the rules; this holds the one derived value everything else reads, so
 * a rebinding reaches the handlers, the tooltips and the sheet in the same
 * tick without any of them knowing that preferences exist.
 *
 * Every consumer imports `shortcut` from HERE rather than from $lib/shortcuts.
 * The pure module's own `shortcut` still exists and still answers from the
 * defaults, which is what the tests want and what nothing on screen does.
 */

import { conflictWith, resolveShortcuts, shortcut as lookup, type Shortcut } from '$lib/shortcuts'
import type { Binding } from '$lib/shortcutBinding'
import { preferences } from './preferences.svelte'

class Shortcuts {
  /** The whole table, resolved. Recomputed when a binding changes. */
  readonly all = $derived(resolveShortcuts(preferences.shortcutBindings))

  /** One shortcut by id, as it is bound now. Throws on an unknown id, for
      the reason the pure lookup does: a typo here is this codebase's bug. */
  get = (id: string): Shortcut => lookup(id, this.all)

  /**
   * The shortcut a proposed binding would collide with, or null.
   *
   * Asked against the RESOLVED table, so a key freed by rebinding something
   * else is free at once rather than after a reload.
   */
  conflict = (id: string, binding: Binding): Shortcut | null =>
    conflictWith(this.all, id, binding)
}

/** The application-wide shortcut table. A module singleton for the reason
    `preferences` is one: there is one keyboard. */
export const shortcuts = new Shortcuts()

/**
 * The accessor every consumer calls.
 *
 * Deliberately the same shape as $lib/shortcuts' own `shortcut(id)`, so the
 * call sites that were already written this way changed their import and
 * nothing else.
 */
export function shortcut(id: string): Shortcut {
  return shortcuts.get(id)
}
