import { describe, expect, it } from 'vitest'
import { group } from './graphGroup'
import { anchorShift, anyVisible, preserve, shapeKey, type Drawn, refitsOnLayout } from './graphPositions'
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

describe('refitsOnLayout', () => {
  const shape = 'v|a,b'
  it('fits the first drawing of a scope', () => {
    expect(refitsOnLayout({ fitNext: true, fitHeld: false, drawnShape: null, shape })).toBe(true)
  })

  it('refits a view nobody has moved when the map changes shape — another grouping', () => {
    expect(refitsOnLayout({ fitNext: false, fitHeld: true, drawnShape: 'v|a,b,group/app', shape })).toBe(true)
  })

  it('keeps the view of a redraw of the same shape', () => {
    expect(refitsOnLayout({ fitNext: false, fitHeld: true, drawnShape: shape, shape })).toBe(false)
  })

  it('keeps the anchor once somebody has panned or zoomed', () => {
    expect(refitsOnLayout({ fitNext: false, fitHeld: false, drawnShape: 'v|other', shape })).toBe(false)
  })
})

describe('collapse all, as the grouping rule sees it', () => {
  // Two namespaces, each one Deployment; "collapse all" draws two group boxes
  // instead of the two Deployments — a different shape, like a regrouping.
  const view = {
    nodes: ['a', 'b'].map((ns) => ({
      id: `${ns}/Deployment/web`, kind: 'workload', apiKind: 'Deployment', name: 'web', namespace: ns,
      state: 'ok' as const, detail: '', group: '', members: [`${ns}/Deployment/web`], counts: { Deployment: 1 },
    })),
    edges: [],
  }
  const open = group(view, 'namespace', new Set())
  const closed = group(view, 'namespace', new Set(open.groups.map((g) => g.id)))
  const shape = (g: typeof open) => shapeKey(g.nodes.map((n) => n.id), g.parents)

  it('refits when nobody has panned or zoomed since the last fit', () => {
    expect(refitsOnLayout({ fitNext: false, fitHeld: true, drawnShape: shape(open), shape: shape(closed) })).toBe(true)
  })

  it('keeps the anchor once the view has been moved', () => {
    expect(refitsOnLayout({ fitNext: false, fitHeld: false, drawnShape: shape(open), shape: shape(closed) })).toBe(false)
  })
})

describe('following the anchor through collapse and expand', () => {
  // Thirteen deployments in two namespaces, laid out grouped.
  const nodes = Array.from({ length: 13 }, (_, i) => {
    const ns = i < 7 ? 'a' : 'b'
    return {
      id: `${ns}/Deployment/d${i}`, kind: 'workload', apiKind: 'Deployment', name: `d${i}`, namespace: ns,
      state: 'ok' as const, detail: '', group: '', members: [`${ns}/Deployment/d${i}`], counts: { Deployment: 1 },
    }
  })
  const view = { nodes, edges: [] }
  const open = group(view, 'namespace', new Set())
  const closed = group(view, 'namespace', new Set(open.groups.map((g) => g.id)))
  const lay = (g: typeof open) => layoutCompound({ nodes: g.nodes, edges: [] }, g.parents, true)
  const drawnOf = (g: typeof open): Drawn => ({ layout: lay(g), shape: shapeKey(g.nodes.map((n) => n.id), g.parents) })
  const before = drawnOf(open)
  const after = drawnOf(closed)
  /** A box's group, or itself: what the page's representative does. */
  const intoGroup = (id: string) => closed.standIn.get(id) ?? null

  it('keeps the anchor’s group where the anchor was, zoomed in', () => {
    // Zoomed in on d9, in namespace b.
    const d9 = before.layout.nodes.find((n) => n.id === 'b/Deployment/d9')!
    const zoom = 2.5
    const pane = { width: 800, height: 600 }
    const viewNow = { zoom, ...pane, panX: pane.width / 2 - d9.x * zoom, panY: pane.height / 2 - d9.y * zoom }

    const kept = preserve(before, after, viewNow, true, intoGroup)
    const box = after.layout.nodes.find((n) => n.id === closed.standIn.get('b/Deployment/d9'))!
    expect(kept.panX + box.x * zoom).toBeCloseTo(pane.width / 2, 6)
    expect(kept.panY + box.y * zoom).toBeCloseTo(pane.height / 2, 6)
    expect(anyVisible(kept.layout, { ...viewNow, panX: kept.panX, panY: kept.panY })).toBe(true)
  })

  it('keeps an expanded group’s first member where the group box was', () => {
    const groupBox = after.layout.nodes.find((n) => n.id === closed.standIn.get('a/Deployment/d0'))!
    const zoom = 2
    const pane = { width: 800, height: 600 }
    const viewNow = { zoom, ...pane, panX: pane.width / 2 - groupBox.x * zoom, panY: pane.height / 2 - groupBox.y * zoom }
    const firstMember = (id: string) => closed.groups.find((g) => g.id === id)?.members[0] ?? null

    const kept = preserve(after, before, viewNow, true, firstMember)
    const member = before.layout.nodes.find((n) => n.id === firstMember(groupBox.id))!
    expect(kept.panX + member.x * zoom).toBeCloseTo(pane.width / 2, 6)
  })

  it('finds nothing to follow without a representative, and nothing is on screen: refit', () => {
    const d9 = before.layout.nodes.find((n) => n.id === 'b/Deployment/d9')!
    const zoom = 3
    const viewNow = { zoom, width: 800, height: 600, panX: 400 - d9.x * zoom - 50_000, panY: 300 - d9.y * zoom }
    expect(anchorShift(before.layout, after.layout, viewNow)).toBeNull()
    const kept = preserve(before, after, viewNow, true)
    expect(anyVisible(kept.layout, { ...viewNow, panX: kept.panX, panY: kept.panY })).toBe(false)
  })
})

