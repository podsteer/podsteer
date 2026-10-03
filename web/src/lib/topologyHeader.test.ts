import { describe, expect, it } from 'vitest'
import { availableForControls, decideHeader, headerWidth, overflowControls, SEARCH_MIN, type HeaderState } from './topologyHeader'

const plain: HeaderState = { apps: false, labelField: false, changed: false, trafficOn: false, groups: true }
const NAVIGATOR = 240

describe('topology header overflow', () => {
  it('keeps every control in the header at 1500px', () => {
    const available = availableForControls(1500, NAVIGATOR)
    expect(overflowControls(available, plain).size).toBe(0)
    expect(headerWidth(plain, new Set())).toBeLessThanOrEqual(available)
  })

  it('folds the lowest-priority controls at 1200px, and what is left fits on one row', () => {
    const available = availableForControls(1200, NAVIGATOR)
    const menu = overflowControls(available, plain)
    expect(menu.has('zoom')).toBe(true)
    expect(headerWidth(plain, menu)).toBeLessThanOrEqual(available)
  })

  it('folds in priority order: zoom, orientation, layers, then the rest', () => {
    const narrow = overflowControls(headerWidth(plain, new Set(['zoom'])), plain)
    expect([...narrow]).toEqual(['zoom'])
    const narrower = overflowControls(headerWidth(plain, new Set(['zoom', 'orientation', 'layers'])), plain)
    expect([...narrower]).toEqual(['zoom', 'orientation', 'layers'])
  })

  it('skips the layers control when there are no groups to collapse', () => {
    const flat = { ...plain, groups: false }
    const menu = overflowControls(headerWidth(flat, new Set(['zoom', 'orientation'])), flat)
    expect(menu.has('layers')).toBe(false)
  })

  it('folds nothing before the header is measured', () => {
    expect(overflowControls(0, plain).size).toBe(0)
  })
})

describe('the header at 1200px', () => {
  const available = availableForControls(1200, NAVIGATOR)

  /** The width the search ends up with: whatever the rest leaves, never under its minimum. */
  const searchGets = (state: HeaderState, menu: Set<string>, compact: boolean) =>
    SEARCH_MIN + (available - headerWidth({ ...state, changedCompact: compact }, menu as never))

  it('keeps the search at 12rem or more without Changed', () => {
    const decision = decideHeader(available, plain)
    expect(searchGets(plain, decision.menu, decision.changedCompact)).toBeGreaterThanOrEqual(SEARCH_MIN)
  })

  it('shows Changed as an icon and folds at most one more control', () => {
    const before = decideHeader(available, plain)
    const after = decideHeader(available, { ...plain, changed: true })
    expect(after.changedCompact).toBe(true)
    expect(after.menu.size - before.menu.size).toBeLessThanOrEqual(1)
    for (const control of before.menu) expect(after.menu.has(control)).toBe(true)
    expect(searchGets({ ...plain, changed: true }, after.menu, true)).toBeGreaterThanOrEqual(SEARCH_MIN)
  })

  it('keeps the search at 12rem with the applications menu and Changed both showing', () => {
    const busy = { ...plain, apps: true, changed: true }
    const decision = decideHeader(available, busy)
    expect(searchGets(busy, decision.menu, decision.changedCompact)).toBeGreaterThanOrEqual(SEARCH_MIN)
  })

  it('gives Changed its full label where there is room', () => {
    const wide = availableForControls(1920, NAVIGATOR)
    expect(decideHeader(wide, { ...plain, changed: true })).toEqual({ menu: new Set(), changedCompact: false })
  })
})

