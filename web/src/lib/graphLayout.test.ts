import { describe, expect, it } from 'vitest'
import { layout, rounded, NODE_WIDTH, NODE_HEIGHT, EDGE_GAP, type GraphSource } from './graphLayout'

function node(id: string, tier: number, extra: Partial<GraphSource['nodes'][0]> = {}) {
  return {
    id,
    kind: 'pod',
    apiKind: 'Pod',
    name: id,
    namespace: 'default',
    tier,
    healthy: true,
    subject: false,
    ...extra,
  }
}

/** Every point a path visits, for asserting where a line actually goes. */
function points(path: string): { x: number; y: number }[] {
  return [...path.matchAll(/(-?[\d.]+) (-?[\d.]+)/g)].map((m) => ({
    x: Number(m[1]),
    y: Number(m[2]),
  }))
}

describe('the dependency map layout', () => {
  it('starts a line outside the box, not at its centre', () => {
    // THE FAULT THAT PROMPTED THIS. A chart connects node centres, so every
    // line began inside the icon and ran out through the label under it. The
    // box — icon and both lines of text — is the object, and lines meet its
    // edge with a gap.
    const result = layout(
      { nodes: [node('a', 0), node('b', 1)], edges: [{ from: 'a', to: 'b' }] },
      true,
    )

    const a = result.nodes.find((n) => n.id === 'a')!
    const [start] = points(result.edges[0].path)

    expect(start.x).toBeGreaterThanOrEqual(a.x + a.width / 2 + EDGE_GAP - 0.01)
  })

  it('leaves a gap before the box it arrives at', () => {
    const result = layout(
      { nodes: [node('a', 0), node('b', 1)], edges: [{ from: 'a', to: 'b' }] },
      true,
    )

    const b = result.nodes.find((n) => n.id === 'b')!
    const path = points(result.edges[0].path)
    const end = path[path.length - 1]

    expect(end.x).toBeLessThanOrEqual(b.x - b.width / 2 - EDGE_GAP + 0.01)
  })

  it('gives every edge one path, not a segment each', () => {
    // Waypoints made each route three separate links, so hovering lit one
    // piece of it and overlapping pieces composited into a heavier stroke.
    const result = layout(
      {
        nodes: [node('a', 0), node('b', 1, { name: 'b' }), node('c', 1, { name: 'c' })],
        edges: [
          { from: 'a', to: 'b' },
          { from: 'a', to: 'c' },
        ],
      },
      true,
    )

    expect(result.edges).toHaveLength(2)
    for (const edge of result.edges) {
      expect(edge.path.split('M')).toHaveLength(2)
    }
  })

  it('rounds its corners', () => {
    const result = layout(
      { nodes: [node('a', 0), node('b', 1, { name: 'other' }), node('c', 1)], edges: [{ from: 'a', to: 'c' }] },
      true,
    )

    // A turn is a quadratic; a path with only L commands has square corners.
    expect(result.edges[0].path).toContain('Q')
  })

  it('draws a straight run with no curve at all', () => {
    // Two boxes on the same line need no corner, and a spurious one there
    // reads as a kink.
    const result = layout(
      { nodes: [node('a', 0), node('b', 1)], edges: [{ from: 'a', to: 'b' }] },
      true,
    )

    expect(result.edges[0].path).not.toContain('Q')
  })

/**
 * Whether any part of a path passes through a box.
 *
 * SEGMENTS, NOT CONTROL POINTS. The first version of this sampled the points a
 * path visits, which a straight run through the middle of a box walks straight
 * past — the line entered one side and left the other with no control point in
 * between, so the test passed while the screenshot showed the line drawn
 * through the label. Sampling along every run is the only version that catches
 * what a reader actually sees.
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
      if (Math.abs(x - box.x) < box.width / 2 && Math.abs(y - box.y) < box.height / 2) return true
    }
  }
  return false
}

  it('routes a multi-tier edge clear of the boxes it passes', () => {
    // The pod to its ConfigMaps passes the container tier, and the line was
    // drawn straight through that box and its label.
    const source: GraphSource = {
      nodes: [node('pod', 0), node('middle', 1), node('far', 2)],
      edges: [{ from: 'pod', to: 'far' }],
    }
    const result = layout(source, true)
    const middle = result.nodes.find((n) => n.id === 'middle')!

    expect(pathCrosses(result.edges[0].path, middle)).toBe(false)
  })

  it('routes a same-tier edge sideways rather than looping', () => {
    // A Deployment and its ReplicaSet share the owner tier. Treated like any
    // other edge the line left the source on the wrong side, dropped below
    // both boxes and arrived pointing backwards — a loop under two boxes that
    // sit side by side.
    const result = layout(
      {
        nodes: [node('deploy', 0, { name: 'deploy' }), node('rs', 0, { name: 'rs' })],
        edges: [{ from: 'deploy', to: 'rs' }],
      },
      true,
    )

    const path = points(result.edges[0].path)
    const deploy = result.nodes.find((n) => n.id === 'deploy')!
    const rs = result.nodes.find((n) => n.id === 'rs')!

    // Laid out horizontally, tiers advance along x — so siblings share an x
    // and stack. The line between them is a straight run across, with no
    // corner and no excursion past either box.
    expect(result.edges[0].path).not.toContain('Q')
    for (const point of path) {
      expect(point.x).toBeCloseTo(deploy.x, 5)
      expect(point.y).toBeGreaterThan(Math.min(deploy.y, rs.y))
      expect(point.y).toBeLessThan(Math.max(deploy.y, rs.y))
    }
  })

  it('never draws a line through any box in the graph', () => {
    // The general form of the fault, over a shape close to a real pod: a
    // controller pair, a container tier, and several attached resources that
    // the pod reaches past it.
    const source: GraphSource = {
      nodes: [
        node('ingress', 0),
        node('service', 1),
        node('deploy', 2, { name: 'deploy' }),
        node('rs', 2, { name: 'rs' }),
        node('pod', 3),
        node('container', 4),
        node('cm-a', 5, { name: 'cm-a' }),
        node('cm-b', 5, { name: 'cm-b' }),
        node('secret', 5, { name: 'secret' }),
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
      ],
    }

    for (const horizontal of [true, false]) {
      const result = layout(source, horizontal)

      for (const edge of result.edges) {
        for (const box of result.nodes) {
          // Its own ends are allowed to touch their boxes; everything else
          // must be clear.
          if (box.id === edge.from || box.id === edge.to) continue
          expect(
            pathCrosses(edge.path, box),
            `${edge.from} -> ${edge.to} crosses ${box.id} (horizontal=${horizontal})`,
          ).toBe(false)
        }
      }
    }
  })

  it('centres a tier of one against a tier of many', () => {
    const result = layout(
      {
        nodes: [node('one', 0), node('a', 1), node('b', 1), node('c', 1)],
        edges: [],
      },
      true,
    )

    const one = result.nodes.find((n) => n.id === 'one')!
    const b = result.nodes.find((n) => n.id === 'b')!

    expect(one.y).toBeCloseTo(b.y, 5)
  })

  it('gives empty tiers no space', () => {
    // A pod with no ingress should not be drawn under an empty row: the gap
    // reads as a missing thing rather than an absent one.
    const dense = layout({ nodes: [node('a', 0), node('b', 1)], edges: [] }, true)
    const sparse = layout({ nodes: [node('a', 0), node('b', 5)], edges: [] }, true)

    expect(sparse.nodes[1].x - sparse.nodes[0].x).toBeCloseTo(
      dense.nodes[1].x - dense.nodes[0].x,
      5,
    )
  })

  it('lays out along the other axis when asked', () => {
    const across = layout({ nodes: [node('a', 0), node('b', 1)], edges: [] }, true)
    const down = layout({ nodes: [node('a', 0), node('b', 1)], edges: [] }, false)

    expect(across.nodes[1].x).toBeGreaterThan(across.nodes[0].x)
    expect(across.nodes[1].y).toBeCloseTo(across.nodes[0].y, 5)

    expect(down.nodes[1].y).toBeGreaterThan(down.nodes[0].y)
    expect(down.nodes[1].x).toBeCloseTo(down.nodes[0].x, 5)
  })

  it('reports bounds that contain every box', () => {
    const result = layout(
      { nodes: [node('a', 0), node('b', 1), node('c', 1)], edges: [] },
      true,
    )

    for (const n of result.nodes) {
      expect(n.x - n.width / 2).toBeGreaterThanOrEqual(result.bounds.x)
      expect(n.y - n.height / 2).toBeGreaterThanOrEqual(result.bounds.y)
      expect(n.x + n.width / 2).toBeLessThanOrEqual(result.bounds.x + result.bounds.width)
      expect(n.y + n.height / 2).toBeLessThanOrEqual(result.bounds.y + result.bounds.height)
    }
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

  it('gives every node the same box, so tiers read as rows', () => {
    const result = layout(
      { nodes: [node('short', 0), node('a-very-long-generated-name-here', 1)], edges: [] },
      true,
    )

    for (const n of result.nodes) {
      expect(n.width).toBe(NODE_WIDTH)
      expect(n.height).toBe(NODE_HEIGHT)
    }
  })
})
