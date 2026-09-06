/**
 * Which columns stay put while the rest of a table scrolls sideways.
 *
 * Exactly two columns are candidates, and both are CONTROLS rather than
 * values: the tick box a bulk action is aimed with, and the row's own menu.
 * On a cluster with custom columns, or in a narrow window, both used to
 * scroll out of reach — and they are the two things somebody is aiming at
 * while READING a column further right, which is precisely when they are
 * gone. A value column has no such problem: it is read where it is.
 *
 * The arithmetic lives here rather than in DataTable because it has no DOM in
 * it, and because "which edge, how far in" is the half that fails silently. A
 * second column fixed to an edge whose offset is hard-coded to zero does not
 * throw and does not warn: it sits exactly on top of the first one.
 *
 * A NOTE ON THE WORD, because this codebase already uses the obvious one for
 * something else. `Column.pinned` in DataTable means "cannot be hidden" — the
 * name column, essentially. Nothing here changes what can be hidden. "Fixed"
 * is stickiness and only stickiness, and the two are independent: the row
 * menu is both, the status column is neither.
 */

/** The two columns whose stickiness the operator controls, by column flag. */
export const EDGE_COLUMNS = ['select', 'menu'] as const

export type EdgeColumn = (typeof EDGE_COLUMNS)[number]

/**
 * Which side of the table each one lives on.
 *
 * A map rather than a branch in the resolver: the tick box leads a row and
 * the menu ends it, and that pairing is the fact worth stating once where
 * both loops below read it.
 */
const SIDE: Record<EdgeColumn, 'left' | 'right'> = {
  select: 'left',
  menu: 'right',
}

/** The part of a column this module needs: what it is, and how wide. */
export interface EdgeCandidate {
  id: string
  /** The column's effective width in pixels, resizes included. */
  width: number
  select?: boolean
  menu?: boolean
}

/** One fixed column's resolved position. */
export interface FixedPlacement {
  id: string
  /** Which of the two controls this is, for the CSS variable it feeds. */
  kind: EdgeColumn
  edge: 'left' | 'right'
  /**
   * Pixels between that edge of the scrollport and this column's outer side.
   *
   * Zero for the outermost column on an edge, and the sum of the widths of
   * everything fixed outside it for the rest.
   */
  offset: number
}

/** Which control a column is, or null for a column holding a value. */
export function controlKindOf(column: Pick<EdgeCandidate, 'select' | 'menu'>): EdgeColumn | null {
  if (column.select) return 'select'
  if (column.menu) return 'menu'
  return null
}

/**
 * Whether a column is a control rather than something with text in it.
 *
 * The CSV export and the column chooser both ask this. Neither a tick box nor
 * a menu has a value to write into a cell or a heading worth offering to
 * hide, and both would otherwise arrive as an empty column in a spreadsheet.
 */
export function isControlColumn(column: Pick<EdgeCandidate, 'select' | 'menu'>): boolean {
  return controlKindOf(column) !== null
}

/**
 * Resolves where each fixed column sits, given the operator's choices.
 *
 * `columns` is the VISIBLE list in display order, because an offset is a sum
 * of the widths beside it and a hidden column occupies none. Left offsets
 * accumulate along the row and right offsets accumulate back along it, which
 * is why this is two passes rather than one.
 *
 * With the two columns that exist today every offset is zero, and that is not
 * a reason to return zero: the day a second control is fixed to an edge, the
 * bug is invisible on a still page and only appears once somebody scrolls.
 */
export function fixedPlacements(
  columns: readonly EdgeCandidate[],
  fixed: Readonly<Record<EdgeColumn, boolean>>,
): FixedPlacement[] {
  const placements: FixedPlacement[] = []

  let left = 0
  for (const column of columns) {
    const kind = controlKindOf(column)
    if (kind === null || SIDE[kind] !== 'left' || !fixed[kind]) continue
    placements.push({ id: column.id, kind, edge: 'left', offset: left })
    left += column.width
  }

  let right = 0
  for (let index = columns.length - 1; index >= 0; index -= 1) {
    const column = columns[index]
    const kind = controlKindOf(column)
    if (kind === null || SIDE[kind] !== 'right' || !fixed[kind]) continue
    placements.push({ id: column.id, kind, edge: 'right', offset: right })
    right += column.width
  }

  return placements
}

/** Whether there is content hidden past each edge of a scrollport. */
export interface EdgeBoundaries {
  left: boolean
  right: boolean
}

/**
 * Whether each pinned edge currently has content passing under it.
 *
 * This is what decides the hairline. Without one there is nothing to say the
 * table continues underneath the pinned column rather than ending at it; with
 * one drawn permanently, a table that does not scroll at all grows a rule
 * down the middle for no reason.
 *
 * THE THRESHOLD IS WHAT STOPS IT FLICKERING. A scrollport at rest routinely
 * reports a fractional `scrollLeft` — a fractional device pixel ratio, a
 * momentum scroll settling, a rubber-band snapping back — and a bare
 * `scrollLeft > 0` turns the hairline on and off across those fractions while
 * nothing visibly moves. A pixel of dead zone costs nothing: one pixel of
 * hidden content is not content anybody is looking for.
 *
 * Rubber-banding past either end (negative `scrollLeft` on macOS, or past the
 * maximum) falls out as false on both sides, which is right — there is
 * nothing under the pinned column at that moment either.
 */
export function edgeBoundaries(
  scrollLeft: number,
  scrollWidth: number,
  clientWidth: number,
  threshold = 1,
): EdgeBoundaries {
  const furthest = scrollWidth - clientWidth
  return {
    left: scrollLeft > threshold,
    right: furthest - scrollLeft > threshold,
  }
}
