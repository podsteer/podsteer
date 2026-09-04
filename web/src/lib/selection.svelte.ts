/**
 * Which rows of a list are ticked, for a bulk action.
 *
 * Distinct from the row the detail drawer is OPEN on (`session.selectedName`):
 * that is one object being read, this is a set of objects about to be acted
 * on together. Keeping them apart is what lets somebody tick five pods, open
 * a sixth to check something, and still have the five ticked when they come
 * back to the action bar.
 *
 * Keys are opaque strings the view chooses — "namespace/name" for anything
 * namespaced, the bare name for a node — so one class serves every list
 * without knowing what a row is. The keys of the rows ON SCREEN, in display
 * order, are handed in by the view (`visible`), because two things depend
 * on that order and nothing else here can know it: a shift-click ranges
 * across it, and "select all visible" selects exactly it.
 */

/**
 * The keys between anchor and target in `visible`, inclusive, in either
 * direction. Empty when either end is not on screen — a shift-click whose
 * anchor scrolled off with a page change has no range to mean, and the
 * caller falls back to a plain toggle.
 */
export function rangeKeys(visible: readonly string[], anchor: string, target: string): string[] {
  const from = visible.indexOf(anchor)
  const to = visible.indexOf(target)
  if (from === -1 || to === -1) return []
  const [start, end] = from <= to ? [from, to] : [to, from]
  return visible.slice(start, end + 1)
}

export class RowSelection {
  /**
   * The ticked keys.
   *
   * `$state.raw` and replaced wholesale on every change, the same way the
   * session's row arrays are: nothing mutates the set in place, so deep
   * proxying would buy a signal per member and nothing else.
   */
  keys = $state.raw<ReadonlySet<string>>(new Set())

  /** The keys of the rows on screen, in display order. Set by the view. */
  visible = $state.raw<readonly string[]>([])

  /**
   * The last key toggled on its own — what the next shift-click ranges
   * from. Plain state rather than reactive: nothing renders it.
   */
  #anchor: string | null = null

  get count(): number {
    return this.keys.size
  }

  has(key: string): boolean {
    return this.keys.has(key)
  }

  /**
   * Toggles one row — or, with `range`, selects every visible row between
   * the previous click and this one, the way every file manager reads a
   * shift-click. A range only ever ADDS: shift-clicking into an already
   * selected stretch is asking for the stretch, not for holes in it.
   *
   * The anchor moves to `key` either way, so a second shift-click extends
   * from where the operator last clicked, not from where they started.
   */
  toggle(key: string, range = false): void {
    const span = range && this.#anchor !== null ? rangeKeys(this.visible, this.#anchor, key) : []
    const next = new Set(this.keys)
    if (span.length > 0) {
      for (const member of span) next.add(member)
    } else if (next.has(key)) {
      next.delete(key)
    } else {
      next.add(key)
    }
    this.keys = next
    this.#anchor = key
  }

  /** Whether every row on screen is ticked. False for an empty page, which
      has nothing to be all of. */
  get allVisibleSelected(): boolean {
    return this.visible.length > 0 && this.visible.every((key) => this.keys.has(key))
  }

  /** Whether some but not all rows on screen are ticked — the header
      checkbox's indeterminate state. */
  get someVisibleSelected(): boolean {
    return !this.allVisibleSelected && this.visible.some((key) => this.keys.has(key))
  }

  /** Ticks every row on screen, keeping whatever is ticked off screen. */
  selectAllVisible(): void {
    const next = new Set(this.keys)
    for (const key of this.visible) next.add(key)
    this.keys = next
  }

  /**
   * The header checkbox: ticks every row on screen, or — when they all
   * already are — unticks THOSE rows and no others. A selection made on
   * page 1 survives a header click on page 2 either way, because the
   * checkbox describes the page it sits above and nothing else.
   */
  toggleAllVisible(): void {
    if (!this.allVisibleSelected) {
      this.selectAllVisible()
      return
    }
    const next = new Set(this.keys)
    for (const key of this.visible) next.delete(key)
    this.keys = next
  }

  /** Drops every tick, and the anchor with it. */
  clear(): void {
    this.keys = new Set()
    this.#anchor = null
  }
}
