import { describe, expect, it } from 'vitest'

import { BADGE_COUNT_LIMIT, formatBadgeCount } from './format'

describe('formatBadgeCount', () => {
  /**
   * The cap exists so a navigator badge cannot widen the sidebar, and the plus
   * exists so the badge does not lie about having counted. Both properties are
   * asserted here rather than left to the reader of the class list, because
   * only one of them is visible in the markup.
   */
  it('prints an ordinary count as itself', () => {
    expect(formatBadgeCount(0)).toBe('0')
    expect(formatBadgeCount(1)).toBe('1')
    expect(formatBadgeCount(71)).toBe('71')
    expect(formatBadgeCount(2000)).toBe('2000')
  })

  it('prints the cap exactly, because it counted exactly that many', () => {
    expect(formatBadgeCount(BADGE_COUNT_LIMIT)).toBe('9999')
  })

  it('adds the plus only once past the cap, which is a different claim', () => {
    // 10000 is the first count the badge cannot state, so it stops stating one.
    expect(formatBadgeCount(BADGE_COUNT_LIMIT + 1)).toBe('9999+')
    expect(formatBadgeCount(250_000)).toBe('9999+')
  })

  it('is a count, so it renders neither a fraction nor a negative', () => {
    expect(formatBadgeCount(12.7)).toBe('12')
    expect(formatBadgeCount(-3)).toBe('0')
  })

  it('reads NaN as no number and infinity as more than the cap', () => {
    // Not the same answer, because they are not the same claim: one is the
    // absence of a count, the other is a count past anything the badge states.
    expect(formatBadgeCount(Number.NaN)).toBe('0')
    expect(formatBadgeCount(Number.POSITIVE_INFINITY)).toBe('9999+')
  })
})
