import { describe, expect, it } from 'vitest'
import { elbow, layoutCompound, pack, GROUP_HEADER, NODE_HEIGHT, NODE_WIDTH, type CompoundLayout } from './graphLayout'

function overlaps(layout: CompoundLayout): string[] {
  const out: string[] = []
  const boxes = layout.nodes
  for (let i = 0; i < boxes.length; i++) {
    for (let j = i + 1; j < boxes.length; j++) {
      const a = boxes[i]
      const b = boxes[j]
      if (Math.abs(a.x - b.x) < (a.width + b.width) / 2 && Math.abs(a.y - b.y) < (a.height + b.height) / 2) {
        out.push(`${a.id}/${b.id}`)
      }
    }
  }
  return out
}

/** Two namespaces of chains, plus a loose box and a cross-namespace line. */
function sample() {
  const nodes: { id: string }[] = []
  const edges: { from: string; to: string }[] = []
  const parents = new Map<string, string>()
  for (const ns of ['a', 'b']) {
    for (let app = 0; app < 3; app++) {
      const d = `${ns}/d${app}`
      nodes.push({ id: d })
      parents.set(d, `group/${ns}`)
      for (let p = 0; p < 3; p++) {
        const pod = `${ns}/d${app}/p${p}`
        nodes.push({ id: pod })
        parents.set(pod, `group/${ns}`)
        edges.push({ from: d, to: pod })
      }
    }
  }
  nodes.push({ id: 'loose' })
  edges.push({ from: 'a/d0', to: 'b/d1/p0' })
  return { nodes, edges, parents }
}

describe('layoutCompound', () => {
  it('places every box once, without overlaps, inside its own frame', () => {
    const { nodes, edges, parents } = sample()
    const layout = layoutCompound({ nodes, edges }, parents, true)

    expect(layout.nodes.map((n) => n.id)).toEqual(nodes.map((n) => n.id))
    expect(overlaps(layout)).toEqual([])
    expect(layout.groups.map((g) => g.id)).toEqual(['group/a', 'group/b'])

    for (const node of layout.nodes) {
      const parent = parents.get(node.id)
      if (!parent) continue
      const frame = layout.groups.find((g) => g.id === parent)!
      expect(node.x - NODE_WIDTH / 2).toBeGreaterThanOrEqual(frame.x)
      expect(node.x + NODE_WIDTH / 2).toBeLessThanOrEqual(frame.x + frame.width)
      // Below the header line, which is where the group's name is written.
      expect(node.y - NODE_HEIGHT / 2).toBeGreaterThanOrEqual(frame.y + GROUP_HEADER)
      expect(node.y + NODE_HEIGHT / 2).toBeLessThanOrEqual(frame.y + frame.height)
    }

    // Frames never overlap one another, and the loose box is outside both.
    const [ga, gb] = layout.groups
    expect(ga.x + ga.width <= gb.x || gb.x + gb.width <= ga.x || ga.y + ga.height <= gb.y || gb.y + gb.height <= ga.y).toBe(true)
  })

  it('routes every line, including one between frames', () => {
    const { nodes, edges, parents } = sample()
    const layout = layoutCompound({ nodes, edges }, parents, true)
    expect(layout.edges).toHaveLength(edges.length)
    const cross = layout.edges.find((e) => e.from === 'a/d0' && e.to === 'b/d1/p0')!
    expect(cross.points.length).toBeGreaterThanOrEqual(2)
    expect(cross.path.startsWith('M ')).toBe(true)
  })

  it('is deterministic, so an unchanged graph is drawn in the same place', () => {
    const { nodes, edges, parents } = sample()
    const one = layoutCompound({ nodes, edges }, parents, true)
    const two = layoutCompound({ nodes: [...nodes].reverse(), edges: [...edges].reverse() }, parents, true)
    const pos = (l: CompoundLayout) => Object.fromEntries(l.nodes.map((n) => [n.id, [n.x, n.y]]))
    expect(pos(two)).toEqual(pos(one))
  })

  it('does not let a hub glue a namespace into one piece', () => {
    const nodes = [{ id: 'policy' }]
    const edges: { from: string; to: string }[] = []
    for (let i = 0; i < 40; i++) {
      nodes.push({ id: `d${i}` }, { id: `p${i}` })
      edges.push({ from: `d${i}`, to: `p${i}` }, { from: 'policy', to: `p${i}` })
    }
    const layout = layoutCompound({ nodes, edges }, new Map(), true)
    expect(overlaps(layout)).toEqual([])
    // Forty separate pieces packed into rows: far squarer than one dagre rank of forty.
    expect(layout.bounds.width / layout.bounds.height).toBeLessThan(6)
    expect(layout.bounds.height / layout.bounds.width).toBeLessThan(6)
  })

  it('ignores lines to boxes it was not given, and self-lines', () => {
    const layout = layoutCompound({ nodes: [{ id: 'a' }], edges: [{ from: 'a', to: 'ghost' }, { from: 'a', to: 'a' }] }, new Map(), false)
    expect(layout.edges).toEqual([])
    expect(layout.nodes).toHaveLength(1)
  })

  it('packs blocks in reading order without overlaps', () => {
    const packed = pack([{ width: 100, height: 50 }, { width: 100, height: 80 }, { width: 300, height: 20 }])
    expect(packed.offsets[0]).toEqual({ x: 0, y: 0 })
    expect(packed.offsets).toHaveLength(3)
  })

  it('routes an elbow out of the facing side', () => {
    const route = elbow({ x: 0, y: 0, width: 100, height: 50 }, { x: 400, y: 200, width: 100, height: 50 }, true)
    expect(route[0].x).toBeGreaterThan(50)
    expect(route[route.length - 1].x).toBeLessThan(350)
    expect(route[route.length - 1].y).toBe(200)
  })
})
