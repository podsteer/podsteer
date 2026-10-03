import { describe, expect, it } from 'vitest'
import { PREFIX_COLOURS, prefixColourClass, prefixColourIndex } from './logPrefixColour'

describe('prefixColourIndex', () => {
  it('is deterministic', () => {
    expect(prefixColourIndex('api-7d9f-abcde')).toBe(prefixColourIndex('api-7d9f-abcde'))
  })

  it('stays within the palette', () => {
    for (let i = 0; i < 500; i++) {
      const index = prefixColourIndex(`pod-${i}-${i * 31}`)
      expect(index).toBeGreaterThanOrEqual(0)
      expect(index).toBeLessThan(PREFIX_COLOURS.length)
    }
  })

  it('usually separates distinct pods', () => {
    const used = new Set(
      Array.from({ length: 30 }, (_, i) => prefixColourIndex(`web-5c8b7-${i}x`)),
    )
    expect(used.size).toBeGreaterThanOrEqual(4)
  })

  it('maps to a compiled group text class', () => {
    expect(prefixColourClass('api-1')).toMatch(/^text-group-(red|orange|yellow|green|blue|purple)$/)
  })
})
