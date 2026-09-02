import { beforeEach, describe, expect, it } from 'vitest'

import { preferences, DETAIL_WIDTHS } from './preferences.svelte'

describe('detail panel width', () => {
  beforeEach(() => {
    preferences.setDetailWidth(DETAIL_WIDTHS[0].fraction)
  })

  it('keeps the width a panel can actually be', () => {
    // Stored preferences outlive the code that wrote them, and this one is a
    // raw number rather than one of three buttons — deliberately, so a drag
    // handle can write any width later. That is exactly why the value has to
    // be clamped on the way in rather than trusted: a panel over the whole
    // window has no list left behind it, and one at a sliver has no content.
    preferences.setDetailWidth(3)
    expect(preferences.detailWidthFraction).toBeLessThanOrEqual(0.75)

    preferences.setDetailWidth(0.01)
    expect(preferences.detailWidthFraction).toBeGreaterThanOrEqual(0.2)
  })

  it('offers three shares, all of them within what is allowed', () => {
    // A preset the setter would clamp is a button that does not do what it
    // says — it would set a width and then show a different one as selected.
    for (const choice of DETAIL_WIDTHS) {
      preferences.setDetailWidth(choice.fraction)
      expect(preferences.detailWidthFraction).toBeCloseTo(choice.fraction, 5)
    }
  })
})
