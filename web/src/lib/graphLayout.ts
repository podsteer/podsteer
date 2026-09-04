/**
 * Where the dependency map's boxes and lines go.
 *
 * THE LAYOUT IS DAGRE'S, NOT OURS, and that was a correction worth recording.
 * This file previously hand-rolled a layered layout and an orthogonal edge
 * router: tiers by hand, corridors by hand, and a search over candidate routes
 * to keep a line from crossing a box. Every fix traded one geometric case for
 * another — siblings looping, a line through the container, a route arriving
 * backwards — because that is a hard, well-studied problem being solved badly.
 * It is what Graphviz's `dot` does, dagre is its port, and ArgoCD draws its own
 * resource tree with it.
 *
 * What stays ours is the drawing: the boxes, the icons, the labels, the
 * rounding of the corners, and what the map means. dagre only answers where
 * things go.
 */

import dagre from '@dagrejs/dagre'

/** One box on the map: an icon and its two lines of text, as one object. */
export interface LaidOutNode {
  id: string
  kind: string
  apiKind: string
  name: string
  namespace: string
  healthy: boolean
  subject: boolean
  /**
   * The qualifier under the name — a pod's phase, "not found", how many of a
   * folded set are. Carried through the layout because the drawing is what
   * shows it, and the layout is the only thing that reaches the drawing.
   */
  detail: string
  /** Named by something and not there, as opposed to present and unwell. */
  missing: boolean
  /** Centre of the box. */
  x: number
  y: number
  width: number
  height: number
}

/** One route, already rounded, as a single SVG path. */
export interface LaidOutEdge {
  id: string
  from: string
  to: string
  path: string
}

export interface Layout {
  nodes: LaidOutNode[]
  edges: LaidOutEdge[]
  /** The extent of everything drawn, for fitting the view to it. */
  bounds: { x: number; y: number; width: number; height: number }
}

export interface GraphSource {
  nodes: {
    id: string
    kind: string
    apiKind: string
    name: string
    namespace: string
    tier: number
    detail: string
    healthy: boolean
    subject: boolean
    missing: boolean
  }[]
  edges: { from: string; to: string }[]
}

/**
 * The box.
 *
 * ONE SIZE FOR EVERY NODE. A box sized to its own text makes every rank a
 * ragged row; one width means the runs between two ranks are parallel, which
 * is what makes the map readable at a glance. Names that do not fit are
 * truncated when drawn.
 */
export const NODE_WIDTH = 176
export const NODE_HEIGHT = 66
/** Between the box edge and where its line starts, so lines never touch text. */
export const EDGE_GAP = 8
/** How tight the turns are. */
const CORNER = 10
/** Around the drawing, so a box at the edge is not flush against the pane. */
const MARGIN = 32

/**
 * Lays a graph out in ranks along one axis.
 *
 * `horizontal` runs the chain left to right, which is the default because the
 * boxes are wider than they are tall and a wide box wants a wide gap beside it
 * rather than above it.
 *
 * RANKS COME FROM THE EDGES, not from the domain's tier numbers. dagre derives
 * them from what points at what, which is the same information said once
 * instead of twice — and it means a graph that gains a kind of edge does not
 * need a tier assigned for it here.
 */
export function layout(source: GraphSource, horizontal: boolean): Layout {
  const graph = new dagre.graphlib.Graph({ directed: true })

  graph.setGraph({
    rankdir: horizontal ? 'LR' : 'TB',
    // Between siblings in a rank, and between the ranks themselves. The rank
    // separation is generous because it is the space every edge turns in.
    nodesep: 28,
    ranksep: 110,
    marginx: MARGIN,
    marginy: MARGIN,
    // 'longest-path' rather than the default network simplex: it is stable
    // under small changes to the graph, so a pod gaining a Secret does not
    // reshuffle the ranks somebody had learned.
    ranker: 'longest-path',
  })
  graph.setDefaultEdgeLabel(() => ({}))

  const known = new Set(source.nodes.map((node) => node.id))
  for (const node of source.nodes) {
    graph.setNode(node.id, { width: NODE_WIDTH, height: NODE_HEIGHT })
  }
  for (const edge of source.edges) {
    // An edge naming something absent would make dagre invent a node for it,
    // which draws as an empty box.
    if (known.has(edge.from) && known.has(edge.to)) graph.setEdge(edge.from, edge.to)
  }

  dagre.layout(graph)

  const nodes: LaidOutNode[] = source.nodes.map((node) => {
    const placed = graph.node(node.id)
    return {
      id: node.id,
      kind: node.kind,
      apiKind: node.apiKind,
      name: node.name,
      namespace: node.namespace,
      detail: node.detail,
      healthy: node.healthy,
      subject: node.subject,
      missing: node.missing,
      x: placed.x,
      y: placed.y,
      width: NODE_WIDTH,
      height: NODE_HEIGHT,
    }
  })

  const placed = new Map(nodes.map((node) => [node.id, node]))
  const edges: LaidOutEdge[] = []

  for (const [index, edge] of source.edges.entries()) {
    const from = placed.get(edge.from)
    const to = placed.get(edge.to)
    if (!from || !to) continue

    const routed = graph.edge(edge.from, edge.to)
    if (!routed?.points?.length) continue

    edges.push({
      id: `${edge.from}->${edge.to}#${index}`,
      from: edge.from,
      to: edge.to,
      path: rounded(trim(routed.points, from, to, horizontal)),
    })
  }

  const size = graph.graph()
  return {
    nodes,
    edges,
    bounds: { x: 0, y: 0, width: size.width ?? 1, height: size.height ?? 1 },
  }
}

/**
 * Pulls a route back from the boxes at both ends.
 *
 * dagre lands its first and last points ON the node boundary, and the map
 * treats an icon and its two lines of text as one object — so a line drawn to
 * the boundary runs into the label underneath. The ends are moved out along
 * the axis the rank runs on, which is where the box's own edge is.
 */
function trim(
  points: { x: number; y: number }[],
  from: LaidOutNode,
  to: LaidOutNode,
  horizontal: boolean,
): { x: number; y: number }[] {
  const trimmed = points.map((point) => ({ ...point }))

  const first = trimmed[0]
  const last = trimmed[trimmed.length - 1]

  if (horizontal) {
    first.x = from.x + Math.sign(last.x - first.x || 1) * (from.width / 2 + EDGE_GAP)
    last.x = to.x - Math.sign(last.x - first.x || 1) * (to.width / 2 + EDGE_GAP)
  } else {
    first.y = from.y + Math.sign(last.y - first.y || 1) * (from.height / 2 + EDGE_GAP)
    last.y = to.y - Math.sign(last.y - first.y || 1) * (to.height / 2 + EDGE_GAP)
  }

  return trimmed
}

/**
 * Joins points with straight runs and rounded turns.
 *
 * A quadratic through each corner, pulled back along both approaches by the
 * radius — or by half the shorter run when a segment is too short to give up a
 * full one, which is what stops a tight turn folding back on itself.
 */
export function rounded(points: { x: number; y: number }[], radius = CORNER): string {
  if (points.length < 2) return ''

  const parts = [`M ${round(points[0].x)} ${round(points[0].y)}`]

  for (let i = 1; i < points.length - 1; i++) {
    const previous = points[i - 1]
    const corner = points[i]
    const next = points[i + 1]

    const r = Math.min(radius, distance(previous, corner) / 2, distance(corner, next) / 2)
    if (r < 0.5) {
      parts.push(`L ${round(corner.x)} ${round(corner.y)}`)
      continue
    }

    const enter = along(corner, previous, r)
    const leave = along(corner, next, r)
    parts.push(`L ${round(enter.x)} ${round(enter.y)}`)
    parts.push(`Q ${round(corner.x)} ${round(corner.y)} ${round(leave.x)} ${round(leave.y)}`)
  }

  const last = points[points.length - 1]
  parts.push(`L ${round(last.x)} ${round(last.y)}`)
  return parts.join(' ')
}

/** A point `by` units from `origin` in the direction of `towards`. */
function along(origin: { x: number; y: number }, towards: { x: number; y: number }, by: number) {
  const length = distance(origin, towards) || 1
  return {
    x: origin.x + ((towards.x - origin.x) / length) * by,
    y: origin.y + ((towards.y - origin.y) / length) * by,
  }
}

function distance(a: { x: number; y: number }, b: { x: number; y: number }): number {
  return Math.hypot(b.x - a.x, b.y - a.y)
}

/** Two decimals is past what a screen can show, and keeps paths short. */
function round(value: number): number {
  return Math.round(value * 100) / 100
}
