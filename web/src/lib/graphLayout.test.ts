import { describe, expect, it } from 'vitest'
import { layout, rounded, NODE_WIDTH, NODE_HEIGHT, type GraphSource } from './graphLayout'

function node(id: string, tier: number, extra: Partial<GraphSource['nodes'][0]> = {}) {
  return {
    id,
    kind: 'pod',
    apiKind: 'Pod',
    name: id,
    namespace: 'default',
    tier,
    detail: '',
    healthy: true,
    subject: false,
    missing: false,
    ...extra,
  }
}

/** Every point a path visits. */
function points(path: string): { x: number; y: number }[] {
  return [...path.matchAll(/(-?[\d.]+) (-?[\d.]+)/g)].map((m) => ({
    x: Number(m[1]),
    y: Number(m[2]),
  }))
}

/**
 * Whether any part of a path passes through a box.
 *
 * SEGMENTS, NOT CONTROL POINTS. An earlier version sampled only the points a
 * path visits, which a straight run through the middle of a box walks past —
 * the line enters one side and leaves the other with no control point between.
 * The test passed while the screenshot showed the line through the label.
 */
function pathCrosses(path: string, box: { x: number; y: number; width: number; height: number }) {
  const visited = points(path)

  for (let i = 1; i < visited.length; i++) {
    const a = visited[i - 1]
    const b = visited[i]
    const steps = Math.max(2, Math.ceil(Math.hypot(b.x - a.x, b.y - a.y) / 2))

    for (let step = 0; step <= steps; step++) {
      const t = step / steps
      const x = a.x + (b.x - a.x) * t
      const y = a.y + (b.y - a.y) * t
      // A small inset, because a line is allowed to touch the boundary it
      // leaves from — it is passing THROUGH a box that is the fault.
      if (Math.abs(x - box.x) < box.width / 2 - 2 && Math.abs(y - box.y) < box.height / 2 - 2) {
        return true
      }
    }
  }
  return false
}

/** A graph shaped like a real pod: a controller pair, a container, attachments. */
const REALISTIC: GraphSource = {
  nodes: [
    node('ingress', 0, { name: 'ingress', apiKind: 'Ingress' }),
    node('service', 1, { name: 'service', apiKind: 'Service' }),
    node('deploy', 2, { name: 'deploy', apiKind: 'Deployment' }),
    node('rs', 2, { name: 'rs', apiKind: 'ReplicaSet' }),
    node('pod', 3, { name: 'pod', subject: true }),
    node('container', 4, { name: 'container', apiKind: '' }),
    node('cm-a', 5, { name: 'cm-a', apiKind: 'ConfigMap' }),
    node('cm-b', 5, { name: 'cm-b', apiKind: 'ConfigMap' }),
    node('secret', 5, { name: 'secret', apiKind: 'Secret' }),
    node('worker', 5, { name: 'worker', apiKind: 'Node' }),
  ],
  edges: [
    { from: 'ingress', to: 'service' },
    { from: 'service', to: 'pod' },
    { from: 'deploy', to: 'rs' },
    { from: 'rs', to: 'pod' },
    { from: 'pod', to: 'container' },
    { from: 'pod', to: 'cm-a' },
    { from: 'pod', to: 'cm-b' },
    { from: 'pod', to: 'secret' },
    { from: 'pod', to: 'worker' },
  ],
}

describe('the dependency map layout', () => {
  it('never draws a line through a box', () => {
    // THE FAULT THAT KEPT COMING BACK while this was hand-rolled: a line from
    // the pod to a ConfigMap ran straight through the container box and its
    // label. Routing around nodes is what dagre is for.
    for (const horizontal of [true, false]) {
      const result = layout(REALISTIC, horizontal)

      for (const edge of result.edges) {
        for (const box of result.nodes) {
          if (box.id === edge.from || box.id === edge.to) continue
          expect(
            pathCrosses(edge.path, box),
            `${edge.from} -> ${edge.to} crosses ${box.id} (horizontal=${horizontal})`,
          ).toBe(false)
        }
      }
    }
  })

  it('starts and ends a line outside the boxes it joins', () => {
    // The box is the icon AND its two lines of text, as one object. dagre
    // lands its ends on the node boundary, which for us is inside the label.
    const result = layout(REALISTIC, true)

    for (const edge of result.edges) {
      const from = result.nodes.find((n) => n.id === edge.from)!
      const to = result.nodes.find((n) => n.id === edge.to)!
      const path = points(edge.path)

      expect(pathCrosses(edge.path, from)).toBe(false)
      expect(pathCrosses(edge.path, to)).toBe(false)
      expect(path.length).toBeGreaterThan(1)
    }
  })

  it('gives every edge exactly one path', () => {
    // Routing through invisible waypoint nodes used to make each edge three
    // separate links, so hovering lit one segment and overlaps composited.
    const result = layout(REALISTIC, true)

    expect(result.edges).toHaveLength(REALISTIC.edges.length)
    for (const edge of result.edges) {
      expect(edge.path.split('M')).toHaveLength(2)
    }
  })

  it('rounds the corners it has', () => {
    const result = layout(REALISTIC, true)
    const turning = result.edges.filter((e) => points(e.path).length > 2)

    expect(turning.length).toBeGreaterThan(0)
    for (const edge of turning) {
      expect(edge.path).toContain('Q')
    }
  })

  it('draws the same picture twice', () => {
    // A map that reshuffles between refreshes is worse than one a few seconds
    // stale: nothing can be found where it was last seen.
    const first = layout(REALISTIC, true)
    const second = layout(REALISTIC, true)

    expect(second.nodes.map((n) => [n.id, n.x, n.y])).toEqual(
      first.nodes.map((n) => [n.id, n.x, n.y]),
    )
    expect(second.edges.map((e) => e.path)).toEqual(first.edges.map((e) => e.path))
  })

  it('runs the chain along the axis it was asked for', () => {
    const across = layout(REALISTIC, true)
    const down = layout(REALISTIC, false)

    const ingressAcross = across.nodes.find((n) => n.id === 'ingress')!
    const podAcross = across.nodes.find((n) => n.id === 'pod')!
    expect(podAcross.x).toBeGreaterThan(ingressAcross.x)

    const ingressDown = down.nodes.find((n) => n.id === 'ingress')!
    const podDown = down.nodes.find((n) => n.id === 'pod')!
    expect(podDown.y).toBeGreaterThan(ingressDown.y)
  })

  it('gives every node the same box, so ranks read as rows', () => {
    for (const n of layout(REALISTIC, true).nodes) {
      expect(n.width).toBe(NODE_WIDTH)
      expect(n.height).toBe(NODE_HEIGHT)
    }
  })

  it('reports bounds that contain every box', () => {
    const result = layout(REALISTIC, true)

    for (const n of result.nodes) {
      expect(n.x - n.width / 2).toBeGreaterThanOrEqual(result.bounds.x - 1)
      expect(n.y - n.height / 2).toBeGreaterThanOrEqual(result.bounds.y - 1)
      expect(n.x + n.width / 2).toBeLessThanOrEqual(result.bounds.x + result.bounds.width + 1)
      expect(n.y + n.height / 2).toBeLessThanOrEqual(result.bounds.y + result.bounds.height + 1)
    }
  })

  it('ignores an edge naming something that is not there', () => {
    // dagre would invent the missing node, which draws as an empty box.
    const result = layout(
      { nodes: [node('a', 0)], edges: [{ from: 'a', to: 'ghost' }] },
      true,
    )

    expect(result.nodes).toHaveLength(1)
    expect(result.edges).toHaveLength(0)
  })

  it('lays out a single node without an edge to guide it', () => {
    const result = layout({ nodes: [node('lonely', 0)], edges: [] }, true)

    expect(result.nodes).toHaveLength(1)
    expect(Number.isFinite(result.nodes[0].x)).toBe(true)
    expect(Number.isFinite(result.nodes[0].y)).toBe(true)
  })

  it('never lets a corner overshoot a short segment', () => {
    // A radius larger than half the run it is cut from folds the curve back on
    // itself, which draws as a loop.
    const path = rounded(
      [
        { x: 0, y: 0 },
        { x: 4, y: 0 },
        { x: 4, y: 4 },
      ],
      40,
    )

    for (const point of points(path)) {
      expect(point.x).toBeGreaterThanOrEqual(-0.01)
      expect(point.x).toBeLessThanOrEqual(4.01)
    }
  })
})
