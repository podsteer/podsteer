import { describe, expect, it } from 'vitest'

import {
  controlKindOf,
  edgeBoundaries,
  fixedPlacements,
  isControlColumn,
  type EdgeCandidate,
} from './fixedColumns'

/** Both on, which is what a fresh install holds. */
const BOTH = { select: true, menu: true }

/** A pod list's shape: tick box, three values, row menu. */
const COLUMNS: EdgeCandidate[] = [
  { id: 'select', width: 40, select: true },
  { id: 'status', width: 44 },
  { id: 'name', width: 320 },
  { id: 'age', width: 80 },
  { id: 'menu', width: 40, menu: true },
]

describe('which columns are fixed', () => {
  it('names the control a column is, and nothing for a column with a value in it', () => {
    expect(controlKindOf({ select: true })).toBe('select')
    expect(controlKindOf({ menu: true })).toBe('menu')
    expect(controlKindOf({})).toBeNull()

    expect(isControlColumn({ select: true })).toBe(true)
    expect(isControlColumn({ menu: true })).toBe(true)
    expect(isControlColumn({})).toBe(false)
  })

  it('fixes the tick box to the left edge and the row menu to the right', () => {
    expect(fixedPlacements(COLUMNS, BOTH)).toEqual([
      { id: 'select', kind: 'select', edge: 'left', offset: 0 },
      { id: 'menu', kind: 'menu', edge: 'right', offset: 0 },
    ])
  })

  it('leaves out the edge the operator has turned off', () => {
    expect(fixedPlacements(COLUMNS, { select: false, menu: true })).toEqual([
      { id: 'menu', kind: 'menu', edge: 'right', offset: 0 },
    ])
    expect(fixedPlacements(COLUMNS, { select: true, menu: false })).toEqual([
      { id: 'select', kind: 'select', edge: 'left', offset: 0 },
    ])
    expect(fixedPlacements(COLUMNS, { select: false, menu: false })).toEqual([])
  })

  it('fixes nothing on a table that declares neither control', () => {
    const values: EdgeCandidate[] = [
      { id: 'name', width: 300 },
      { id: 'age', width: 80 },
    ]
    expect(fixedPlacements(values, BOTH)).toEqual([])
  })

  it('never counts a value column towards an offset, however wide', () => {
    // The 320px name column sits between the two controls and must not push
    // either of them off its edge.
    const [select, menu] = fixedPlacements(COLUMNS, BOTH)
    expect(select.offset).toBe(0)
    expect(menu.offset).toBe(0)
  })

  it('stacks a second control fixed to the same edge beyond the first', () => {
    // Not a shape any view builds today. It is the shape a hard-coded zero
    // offset would render as one column sitting on top of another, which is
    // the reason the offset is a sum rather than a constant.
    const twoLeft: EdgeCandidate[] = [
      { id: 'select', width: 40, select: true },
      { id: 'also', width: 24, select: true },
      { id: 'name', width: 300 },
    ]
    expect(fixedPlacements(twoLeft, BOTH)).toEqual([
      { id: 'select', kind: 'select', edge: 'left', offset: 0 },
      { id: 'also', kind: 'select', edge: 'left', offset: 40 },
    ])

    const twoRight: EdgeCandidate[] = [
      { id: 'name', width: 300 },
      { id: 'also', width: 24, menu: true },
      { id: 'menu', width: 40, menu: true },
    ]
    // Right offsets accumulate back along the row, so the LAST column is the
    // one at zero and its neighbour sits one width in.
    expect(fixedPlacements(twoRight, BOTH)).toEqual([
      { id: 'menu', kind: 'menu', edge: 'right', offset: 0 },
      { id: 'also', kind: 'menu', edge: 'right', offset: 40 },
    ])
  })

  it('measures offsets in the widths it was handed, resizes included', () => {
    const widened = COLUMNS.map((column) =>
      column.id === 'select' ? { ...column, width: 96 } : column,
    )
    const second: EdgeCandidate = { id: 'also', width: 10, select: true }
    expect(fixedPlacements([widened[0], second, ...widened.slice(1)], BOTH)[1]).toEqual({
      id: 'also',
      kind: 'select',
      edge: 'left',
      offset: 96,
    })
  })
})

describe('where a pinned edge draws its hairline', () => {
  it('draws neither edge when the table fits its scrollport', () => {
    expect(edgeBoundaries(0, 800, 800)).toEqual({ left: false, right: false })
  })

  it('draws the right edge while content remains past it', () => {
    expect(edgeBoundaries(0, 1600, 800)).toEqual({ left: false, right: true })
  })

  it('draws the left edge once the table is scrolled past it', () => {
    expect(edgeBoundaries(400, 1600, 800)).toEqual({ left: true, right: true })
  })

  it('drops the right edge at the far end and keeps the left one', () => {
    expect(edgeBoundaries(800, 1600, 800)).toEqual({ left: true, right: false })
  })

  it('ignores a sub-pixel resting position rather than flickering', () => {
    // A fractional device pixel ratio leaves a scrollport at rest reporting
    // half a pixel. Without the dead zone that is a hairline appearing and
    // disappearing while nothing visibly moves.
    expect(edgeBoundaries(0.5, 1600, 800).left).toBe(false)
    expect(edgeBoundaries(799.5, 1600, 800).right).toBe(false)
  })

  it('draws neither edge while a rubber-band scroll is past an end', () => {
    // macOS reports a negative offset at the near end and one past the
    // maximum at the far end. There is nothing under the pinned column in
    // either case.
    expect(edgeBoundaries(-40, 1600, 800)).toEqual({ left: false, right: true })
    expect(edgeBoundaries(840, 1600, 800)).toEqual({ left: true, right: false })
  })
})
