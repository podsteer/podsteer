import { describe, expect, it } from 'vitest'

import { escapeLayer, escapeUnclaimed } from './escape'

describe('who Escape belongs to', () => {
  it('gives it to the innermost open layer', () => {
    // THE BUG: a row menu open inside the detail drawer, and one Escape
    // closed both — with the drawer's Escape also discarding an unsaved YAML
    // draft, so a keystroke aimed at a menu could throw work away.
    const drawer = escapeLayer()
    expect(drawer.owns()).toBe(true)

    const menu = escapeLayer()
    expect(menu.owns()).toBe(true)
    expect(drawer.owns()).toBe(false)

    menu.release()
    expect(drawer.owns()).toBe(true)
    drawer.release()
  })

  it('copes with layers closing out of order', () => {
    // A component unmounting takes its claim with it wherever it sits, so the
    // outer layer can go first — a drawer closed by something other than
    // Escape while a menu inside it is still open.
    const outer = escapeLayer()
    const inner = escapeLayer()

    outer.release()
    expect(inner.owns()).toBe(true)

    inner.release()
    expect(escapeUnclaimed()).toBe(true)
  })

  it('says nothing has claimed it when nothing has', () => {
    // What keeps the unconverted components working: with no claims
    // outstanding they act exactly as they did.
    expect(escapeUnclaimed()).toBe(true)
  })
})
