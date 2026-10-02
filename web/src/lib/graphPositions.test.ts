import { describe, expect, it } from 'vitest'
import { anchorShift, preserve, shapeKey, type Drawn } from './graphPositions'
import { layoutCompound, type CompoundLayout } from './graphLayout'

function drawn(layout: CompoundLayout, ids: string[], parents = new Map<string, string>()): Drawn {
  return { layout, shape: shapeKey(ids, parents) }
}

function at(layout: CompoundLayout, id: string) {
  const node = layout.nodes.find((n) => n.id === id)!
  return { x: node.x, y: node.y }
}

const view = { panX: 40, panY: -20, zoom: 0.75, width: 1000, height: 700 }

describe('position preservation', () => {
  it('moves nothing when the node set is identical, even if the new layout differs', () => {
    const ids = ['a', 'b', 'c']
    const first = layoutCompound({ nodes: ids.map((id) => ({ id })), edges: [{ from: 'a', to: 'b' }] }, new Map(), true)
    // Same boxes, a new line: dagre would place them differently.
    const second = layoutCompound(
      { nodes: ids.map((id) => ({ id })), edges: [{ from: 'a', to: 'b' }, { from: 'c', to: 'a' }] },
      new Map(),
      true,
    )
    expect(at(second, 'a')).not.toEqual(at(first, 'a'))

    const result = preserve(drawn(first, ids), drawn(second, ids), view, true)

    expect(result.kept).toBe(true)
    expect(result.panX).toBe(view.panX)
    expect(result.panY).toBe(view.panY)
    for (const id of ids) expect(at(result.layout, id)).toEqual(at(first, id))
    // The new line is drawn, between the boxes where they are.
    expect(result.layout.edges.map((e) => `${e.from}->${e.to}`).sort()).toEqual(['a->b', 'c->a'])
  })

  it('keeps the box nearest the centre where it was on screen when the set changes', () => {
    const before = ['a', 'b', 'c', 'd']
    const after = ['a', 'b', 'c', 'd', 'e', 'f']
    const first = layoutCompound(
      { nodes: before.map((id) => ({ id })), edges: [{ from: 'a', to: 'b' }, { from: 'b', to: 'c' }] },
      new Map(),
      true,
    )
    const second = layoutCompound(
      {
        nodes: after.map((id) => ({ id })),
        edges: [{ from: 'e', to: 'f' }, { from: 'f', to: 'a' }, { from: 'a', to: 'b' }, { from: 'b', to: 'c' }],
      },
      new Map(),
      true,
    )

    const shift = anchorShift(first, second, view)!
    const result = preserve(drawn(first, before), drawn(second, after), view, true)
    expect(result.kept).toBe(false)

    const screen = (p: { x: number; y: number }, pan: { panX: number; panY: number }) => ({
      x: pan.panX + p.x * view.zoom,
      y: pan.panY + p.y * view.zoom,
    })
    const was = screen(at(first, shift.anchor), view)
    const now = screen(at(result.layout, shift.anchor), result)
    expect(now.x).toBeCloseTo(was.x, 6)
    expect(now.y).toBeCloseTo(was.y, 6)
  })

  it('accepts a first drawing as it is', () => {
    const layout = layoutCompound({ nodes: [{ id: 'a' }], edges: [] }, new Map(), true)
    const result = preserve(null, drawn(layout, ['a']), view, true)
    expect(result.layout).toBe(layout)
    expect(result.kept).toBe(false)
  })

  it('treats a box moving between frames as a different shape', () => {
    expect(shapeKey(['a'], new Map([['a', 'g1']]))).not.toBe(shapeKey(['a'], new Map([['a', 'g2']])))
    expect(shapeKey(['b', 'a'], new Map())).toBe(shapeKey(['a', 'b'], new Map()))
  })
})
