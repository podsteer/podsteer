import { describe, expect, it } from 'vitest'
import { exportScale, topologyFilename } from './pngExport'

describe('PNG export', () => {
  it('renders a small map at the preferred scale, uncapped', () => {
    expect(exportScale(1000, 500)).toEqual({ scale: 2, capped: false, width: 2000, height: 1000 })
  })

  it('caps a large map under the canvas limits and says so', () => {
    const size = exportScale(20_000, 18_000)
    expect(size.capped).toBe(true)
    expect(size.width * size.height).toBeLessThanOrEqual(16_777_216)
    expect(size.width).toBeLessThanOrEqual(16_384)
  })

  it('caps a very long map by its side', () => {
    const size = exportScale(40_000, 100)
    expect(size.width).toBeLessThanOrEqual(16_384)
    expect(size.capped).toBe(true)
  })

  it('names the file for the cluster and the scope', () => {
    const now = new Date(2026, 9, 2, 8, 5, 9)
    expect(topologyFilename('kind/dev', { namespaces: [], all: true }, now)).toBe('kind_dev-topology-all-20261002-080509.png')
    expect(topologyFilename('c', { namespaces: ['a', 'b'], all: false }, now)).toBe('c-topology-a_b-20261002-080509.png')
    expect(topologyFilename('c', { namespaces: ['a', 'b', 'c', 'd'], all: false }, now)).toBe('c-topology-4-namespaces-20261002-080509.png')
  })
})
