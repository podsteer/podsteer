import { describe, expect, it } from 'vitest'
import { availableForControls, headerWidth, overflowControls, type HeaderState } from './topologyHeader'

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

  it('still fits at 1200px with Changed showing and traffic on', () => {
    const busy = { ...plain, changed: true, trafficOn: true }
    const available = availableForControls(1200, NAVIGATOR)
    const menu = overflowControls(available, busy)
    expect(headerWidth(busy, menu)).toBeLessThanOrEqual(available)
    // Zoom goes before anything somebody reaches for more often.
    if (menu.has('refresh')) expect(menu.has('zoom')).toBe(true)
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
