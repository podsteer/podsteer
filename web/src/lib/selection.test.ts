import { describe, expect, it } from 'vitest'

import { RowSelection, rangeKeys } from './selection.svelte'

/** A selection over a five-row page. */
function page(): RowSelection {
  const selection = new RowSelection()
  selection.visible = ['ns/a', 'ns/b', 'ns/c', 'ns/d', 'ns/e']
  return selection
}

function ticked(selection: RowSelection): string[] {
  return [...selection.keys].sort()
}

describe('rangeKeys', () => {
  it('spans from the anchor to the target, inclusive, in either direction', () => {
    const visible = ['a', 'b', 'c', 'd']
    expect(rangeKeys(visible, 'b', 'd')).toEqual(['b', 'c', 'd'])
    expect(rangeKeys(visible, 'd', 'b')).toEqual(['b', 'c', 'd'])
    expect(rangeKeys(visible, 'c', 'c')).toEqual(['c'])
  })

  it('is empty when either end is not on screen', () => {
    // The anchor scrolled off with a page change: there is no stretch of
    // rows for the shift-click to mean, so the caller falls back to a plain
    // toggle rather than guessing at one.
    expect(rangeKeys(['a', 'b'], 'zz', 'b')).toEqual([])
    expect(rangeKeys(['a', 'b'], 'a', 'zz')).toEqual([])
  })
})

describe('RowSelection', () => {
  it('toggles a row on and off', () => {
    const selection = page()
    selection.toggle('ns/b')
    expect(ticked(selection)).toEqual(['ns/b'])
    expect(selection.has('ns/b')).toBe(true)
    expect(selection.count).toBe(1)

    selection.toggle('ns/b')
    expect(ticked(selection)).toEqual([])
    expect(selection.count).toBe(0)
  })

  it('shift-click selects the range from the last click, either way round', () => {
    const selection = page()
    selection.toggle('ns/b')
    selection.toggle('ns/d', true)
    expect(ticked(selection)).toEqual(['ns/b', 'ns/c', 'ns/d'])

    const upward = page()
    upward.toggle('ns/d')
    upward.toggle('ns/b', true)
    expect(ticked(upward)).toEqual(['ns/b', 'ns/c', 'ns/d'])
  })

  it('shift-click only ever adds — a range never punches holes', () => {
    const selection = page()
    selection.toggle('ns/a')
    selection.toggle('ns/c')
    selection.toggle('ns/e', true)
    expect(ticked(selection)).toEqual(['ns/a', 'ns/c', 'ns/d', 'ns/e'])
  })

  it('shift-click extends from the last click, not the first', () => {
    const selection = page()
    selection.toggle('ns/a')
    selection.toggle('ns/b', true)
    selection.toggle('ns/d', true)
    // b → d, not a → d: the anchor moved with the second click.
    expect(ticked(selection)).toEqual(['ns/a', 'ns/b', 'ns/c', 'ns/d'])
  })

  it('shift-click with no anchor, or an anchor off screen, is a plain toggle', () => {
    const fresh = page()
    fresh.toggle('ns/c', true)
    expect(ticked(fresh)).toEqual(['ns/c'])

    const paged = page()
    paged.toggle('ns/b')
    // The next page: b is no longer on screen.
    paged.visible = ['ns/f', 'ns/g', 'ns/h']
    paged.toggle('ns/h', true)
    expect(ticked(paged)).toEqual(['ns/b', 'ns/h'])
  })

  it('select all visible ticks the page and keeps ticks made elsewhere', () => {
    const selection = page()
    selection.toggle('ns/b')
    selection.visible = ['ns/f', 'ns/g']
    selection.selectAllVisible()
    expect(ticked(selection)).toEqual(['ns/b', 'ns/f', 'ns/g'])
  })

  it('reports whether all, some or none of the page is ticked', () => {
    const selection = page()
    expect(selection.allVisibleSelected).toBe(false)
    expect(selection.someVisibleSelected).toBe(false)

    selection.toggle('ns/a')
    expect(selection.allVisibleSelected).toBe(false)
    expect(selection.someVisibleSelected).toBe(true)

    selection.selectAllVisible()
    expect(selection.allVisibleSelected).toBe(true)
    expect(selection.someVisibleSelected).toBe(false)

    // An empty page has nothing to be all of.
    selection.visible = []
    expect(selection.allVisibleSelected).toBe(false)
  })

  it('the header checkbox unticks only the page it sits above', () => {
    const selection = page()
    selection.toggle('ns/z') // ticked on some other page
    selection.toggleAllVisible()
    expect(selection.allVisibleSelected).toBe(true)
    expect(selection.count).toBe(6)

    selection.toggleAllVisible()
    expect(ticked(selection)).toEqual(['ns/z'])
  })

  it('a partly ticked page selects the rest rather than clearing', () => {
    const selection = page()
    selection.toggle('ns/a')
    selection.toggleAllVisible()
    expect(selection.allVisibleSelected).toBe(true)
  })

  it('clear drops every tick and the shift-click anchor with it', () => {
    const selection = page()
    selection.toggle('ns/a')
    selection.toggle('ns/c', true)
    selection.clear()
    expect(selection.count).toBe(0)

    // No anchor survives: the next shift-click is a plain toggle.
    selection.toggle('ns/e', true)
    expect(ticked(selection)).toEqual(['ns/e'])
  })

  it('replaces the set rather than mutating it, so a held reference stays what it was', () => {
    const selection = page()
    selection.toggle('ns/a')
    const before = selection.keys
    selection.toggle('ns/b')
    expect([...before]).toEqual(['ns/a'])
    expect(ticked(selection)).toEqual(['ns/a', 'ns/b'])
  })
})
