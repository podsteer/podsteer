import { describe, expect, it } from 'vitest'
import { buildCullIndex, edgeBounds, viewportOf, visible } from './graphCull'
import type { CompoundLayout } from './graphLayout'

function box(id: string, x: number, y: number) {
  return { id, x, y, width: 100, height: 50 }
}

const layout: Pick<CompoundLayout, 'nodes' | 'edges' | 'groups'> = {
  nodes: [box('near', 100, 100), box('far', 5000, 5000), box('edge-of-view', 820, 100)],
  edges: [
    { id: 'long', from: 'near', to: 'far', points: [{ x: 150, y: 100 }, { x: 4950, y: 100 }, { x: 4950, y: 5000 }], path: '' },
    { id: 'away', from: 'far', to: 'far2', points: [{ x: 6000, y: 6000 }, { x: 6100, y: 6000 }], path: '' },
  ],
  groups: [{ id: 'g', x: 0, y: 0, width: 300, height: 300 }],
}

describe('culling', () => {
  it('draws what intersects the viewport and nothing else', () => {
    const index = buildCullIndex(layout)
    const seen = visible(index, { x: 0, y: 0, width: 800, height: 600 })
    expect([...seen.nodes].sort()).toEqual(['edge-of-view', 'near'])
    expect([...seen.edges]).toEqual(['long'])
    expect([...seen.groups]).toEqual(['g'])
  })

  it('finds a long line from a viewport in the middle of it', () => {
    const index = buildCullIndex(layout, 256)
    const seen = visible(index, { x: 2000, y: 0, width: 400, height: 300 })
    expect(seen.edges.has('long')).toBe(true)
    expect(seen.nodes.size).toBe(0)
  })

  it('grows the viewport by the margin', () => {
    const index = buildCullIndex(layout)
    expect(visible(index, { x: 0, y: 0, width: 700, height: 300 }).nodes.has('edge-of-view')).toBe(false)
    expect(visible(index, { x: 0, y: 0, width: 700, height: 300 }, 100).nodes.has('edge-of-view')).toBe(true)
  })

  it('turns pan and zoom into layout coordinates', () => {
    expect(viewportOf({ x: -200, y: -100 }, 0.5, { width: 800, height: 600 })).toEqual({
      x: 400,
      y: 200,
      width: 1600,
      height: 1200,
    })
  })

  it('bounds a route by its corners', () => {
    expect(edgeBounds([{ x: 5, y: 9 }, { x: 1, y: 20 }])).toEqual({ x: 1, y: 9, width: 4, height: 11 })
  })
})
