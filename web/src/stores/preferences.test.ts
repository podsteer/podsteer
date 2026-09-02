import { beforeEach, describe, expect, it } from 'vitest'

import {
  preferences,
  detailWidthBounds,
  DETAIL_MAX_REM,
  DETAIL_MAX_SHARE,
  DETAIL_MIN_REM,
  DETAIL_WIDTHS,
} from './preferences.svelte'

describe('detail panel bounds', () => {
  it('floors at a readable width and ceilings before the window is covered', () => {
    // A wide monitor: both rem bounds apply, and neither is anywhere near the
    // share of the window.
    const wide = detailWidthBounds(3440, 16)
    expect(wide.min).toBe(DETAIL_MIN_REM * 16)
    expect(wide.max).toBe(DETAIL_MAX_REM * 16)

    // A narrow one: the ceiling is the window, not the rem cap.
    const narrow = detailWidthBounds(1024, 16)
    expect(narrow.max).toBe(1024 * DETAIL_MAX_SHARE)
  })

  it('gives the floor up rather than covering the window', () => {
    // A window narrower than the floor. A panel wider than the window it is
    // in has hidden the list completely, which is the one thing the setting
    // exists to prevent — so the floor yields and min never exceeds max.
    const tiny = detailWidthBounds(360, 16)
    expect(tiny.min).toBeLessThanOrEqual(tiny.max)
    expect(tiny.max).toBeLessThanOrEqual(360)
  })

  it('scales with the interface, not with a hardcoded 16px', () => {
    // The floor exists to fit one row of a label and its value. Somebody who
    // has scaled their interface up has made that row taller AND wider, so a
    // floor measured in pixels would stop fitting it.
    const scaled = detailWidthBounds(3440, 20)
    expect(scaled.min).toBe(DETAIL_MIN_REM * 20)
  })
})

describe('detail panel width', () => {
  beforeEach(() => {
    preferences.setDetailWidth(DETAIL_WIDTHS[0].fraction)
  })

  it('keeps the stored share inside something sane', () => {
    // Stored preferences outlive the code that wrote them, and this one is a
    // raw number rather than one of three buttons — deliberately, so the drag
    // can write any width. The real limits are applied per window, in pixels;
    // this is only the guard against a value that is not a share at all.
    preferences.setDetailWidth(3)
    expect(preferences.detailWidthFraction).toBeLessThanOrEqual(0.95)

    preferences.setDetailWidth(0)
    expect(preferences.detailWidthFraction).toBeGreaterThan(0)
  })

  it('never lets a share store as one thing and render as another', () => {
    // THE FAILURE THIS GUARDS. The drag clamps in pixels and then divides by
    // the window width to store a share; if the setter clamped the share more
    // tightly than the pixel bounds do, a drag released at the edge of what
    // the panel allows would be stored as something else and the panel would
    // jump on release. On a small window 90% is a legitimate drag.
    const narrow = 1280
    const { max } = detailWidthBounds(narrow, 16)

    preferences.setDetailWidth(max / narrow)
    expect(preferences.detailWidthFraction * narrow).toBeCloseTo(max, 5)
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
