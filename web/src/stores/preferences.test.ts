import { beforeEach, describe, expect, it } from 'vitest'

import {
  preferences,
  detailLabelBounds,
  detailLabelWidthCSS,
  detailWidthBounds,
  DETAIL_MAX_REM,
  DETAIL_MAX_SHARE,
  DETAIL_LABEL_MAX_REM,
  DETAIL_LABEL_MAX_SHARE,
  DETAIL_LABEL_MIN_REM,
  DETAIL_MIN_REM,
  DETAIL_WIDTHS,
  DEFAULT_DETAIL_LABEL_SHARE,
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

describe('the column divider', () => {
  it('is one width every section reads, live while it is dragged', () => {
    // THE POINT OF THE GESTURE. Dragging the divider in Identity has to move
    // the one in Labels and in Annotations, so the width being dragged cannot
    // live in the list holding the pointer — every list reads this one value,
    // and it answers with the drag before the drag has been committed.
    preferences.setDetailLabelShare(DEFAULT_DETAIL_LABEL_SHARE)
    expect(preferences.detailLabelShare).toBeCloseTo(DEFAULT_DETAIL_LABEL_SHARE, 5)

    preferences.labelShareDrag = 0.4
    expect(preferences.detailLabelShare).toBeCloseTo(0.4, 5)

    preferences.setDetailLabelShare(0.4)
    expect(preferences.labelShareDrag).toBeNull()
    expect(preferences.detailLabelShare).toBeCloseTo(0.4, 5)
  })

  it('never lets a dragged divider store as one thing and render as another', () => {
    // The same failure the panel's own drag guards against: the drag clamps
    // in pixels and stores a share, so a divider dropped at the end of its
    // travel must come back as the pixel width it was dropped at.
    const pane = 688
    const { min, max } = detailLabelBounds(pane, 16)

    for (const edge of [min, max]) {
      preferences.setDetailLabelShare(edge / pane)
      expect(preferences.detailLabelShare * pane).toBeCloseTo(edge, 5)
    }
  })

  it('leaves the values more room than the labels', () => {
    // Past 60% the labels have more room than the values they describe, which
    // is the wrong way round whatever the pane is — so the ceiling is a share
    // of the pane rather than a length.
    const narrow = detailLabelBounds(400, 16)
    expect(narrow.max).toBeLessThanOrEqual(400 * DETAIL_LABEL_MAX_SHARE)

    const wide = detailLabelBounds(2000, 16)
    expect(wide.max).toBe(DETAIL_LABEL_MAX_REM * 16)
    expect(wide.min).toBe(DETAIL_LABEL_MIN_REM * 16)
  })

  it('renders the same bounds it drags against', () => {
    // The CSS and detailLabelBounds are two spellings of one rule. If they
    // drift, the divider follows the pointer to a width the resting style
    // refuses, and lets go somewhere else.
    const css = detailLabelWidthCSS(0.26)
    expect(css).toContain(`${DETAIL_LABEL_MIN_REM}rem`)
    expect(css).toContain(`${DETAIL_LABEL_MAX_REM}rem`)
    expect(css).toContain(`${DETAIL_LABEL_MAX_SHARE * 100}%`)
    expect(css).toContain('26%')
  })
})
