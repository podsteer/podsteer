import { describe, expect, it } from 'vitest'
import { currentRevision, isRollbackable, orderByNumberDescending, type RevisionLike } from './revisions'

function rev(number: number, current = false): RevisionLike {
  return { number, current }
}

describe('orderByNumberDescending', () => {
  it('sorts newest-first', () => {
    const ordered = orderByNumberDescending([rev(1), rev(3), rev(2)])
    expect(ordered.map((r) => r.number)).toEqual([3, 2, 1])
  })

  it('does not mutate the input array', () => {
    const input = [rev(1), rev(3), rev(2)]
    const copy = [...input]
    orderByNumberDescending(input)
    expect(input).toEqual(copy)
  })

  it('returns an empty array unchanged', () => {
    expect(orderByNumberDescending([])).toEqual([])
  })

  it('is stable-enough for an already-sorted list', () => {
    const ordered = orderByNumberDescending([rev(5), rev(4), rev(3)])
    expect(ordered.map((r) => r.number)).toEqual([5, 4, 3])
  })
})

describe('currentRevision', () => {
  it('returns the revision marked current', () => {
    const revisions = [rev(1), rev(2, true), rev(3)]
    expect(currentRevision(revisions)?.number).toBe(2)
  })

  it('returns null when nothing is marked current', () => {
    expect(currentRevision([rev(1), rev(2)])).toBeNull()
  })

  it('returns null for an empty list without throwing', () => {
    expect(currentRevision([])).toBeNull()
  })
})

describe('isRollbackable', () => {
  it('is true for a non-current revision', () => {
    expect(isRollbackable(rev(1, false))).toBe(true)
  })

  it('is false for the current revision', () => {
    expect(isRollbackable(rev(2, true))).toBe(false)
  })
})
