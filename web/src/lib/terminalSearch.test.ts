import { describe, expect, it } from 'vitest'
import { matchFractions, type BufferLines } from './terminalSearch'

/** A buffer of literal lines, as xterm would report them. */
function buffer(lines: (string | undefined)[]): BufferLines {
  return { length: lines.length, lineAt: (row) => lines[row] }
}

describe('the search ruler', () => {
  it('marks matches anywhere in the buffer, not just the visible rows', () => {
    // The point of the ruler is to show what is OFF screen. A track that only
    // marked the viewport would agree with what somebody can already see.
    const lines = Array.from({ length: 100 }, (_, i) => (i === 3 || i === 97 ? 'error here' : 'ok'))

    const found = matchFractions(buffer(lines), 'error')

    expect(found).toHaveLength(2)
    expect(found[0]).toBeCloseTo(0.03, 5)
    expect(found[1]).toBeCloseTo(0.97, 5)
  })

  it('is case-insensitive, like the search it draws for', () => {
    // A ruler marking different rows from the ones Enter steps through would
    // be worse than no ruler.
    const found = matchFractions(buffer(['Fatal: disk full', 'fine']), 'fatal')

    expect(found).toHaveLength(1)
  })

  it('collapses matches that would land on the same pixel', () => {
    // Adjacent hits stacking into one heavier mark reads as a longer run than
    // it is.
    const lines = Array.from({ length: 1000 }, (_, i) => (i < 5 ? 'match' : 'no'))

    const found = matchFractions(buffer(lines), 'match')

    expect(found.length).toBeLessThan(5)
    expect(found.length).toBeGreaterThan(0)
  })

  it('keeps matches that are genuinely far apart', () => {
    const lines = Array.from({ length: 400 }, (_, i) => (i % 40 === 0 ? 'hit' : 'no'))

    expect(matchFractions(buffer(lines), 'hit')).toHaveLength(10)
  })

  it('returns nothing for an empty query', () => {
    // Clearing the box has to clear the track, not leave the last one drawn.
    expect(matchFractions(buffer(['anything']), '')).toEqual([])
  })

  it('survives an empty buffer and absent rows', () => {
    // xterm returns undefined past the end, and a terminal that has printed
    // nothing has no rows at all.
    expect(matchFractions(buffer([]), 'x')).toEqual([])
    expect(matchFractions(buffer([undefined, 'x', undefined]), 'x')).toHaveLength(1)
  })

  it('every fraction is inside the track', () => {
    const lines = Array.from({ length: 37 }, () => 'hit')

    for (const fraction of matchFractions(buffer(lines), 'hit')) {
      expect(fraction).toBeGreaterThanOrEqual(0)
      expect(fraction).toBeLessThan(1)
    }
  })
})
