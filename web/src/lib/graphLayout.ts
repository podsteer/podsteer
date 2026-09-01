/**
 * Where the dependency map's boxes and lines go.
 *
 * PURE GEOMETRY, no drawing, so the rules that decide whether a line clears a
 * box can be argued with in a test rather than squinted at in a screenshot.
 *
 * WHY NOT ECHARTS' GRAPH SERIES, which drew this until now. It connects node
 * CENTRES clipped to the symbol, and a node's label is not part of its symbol
 * — so a line always started in the middle of an icon and ran out through the
 * text underneath it. Routing through invisible waypoint nodes gave right
 * angles but made each edge three separate links, so hovering lit one segment
 * of a route, overlapping segments composited into a heavier stroke, and
 * corners could not be rounded. Every one of those is a consequence of using a
 * chart to draw a diagram.
 */

/** One box on the map: an icon and its two lines of text, as one object. */
export interface LaidOutNode {
  id: string
  kind: string
  apiKind: string
  name: string
  namespace: string
  healthy: boolean
  subject: boolean
  /** Centre of the box. */
  x: number
  y: number
  /** The box itself, which is what lines are routed between. */
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
    healthy: boolean
    subject: boolean
  }[]
  edges: { from: string; to: string }[]
}

/**
 * The box, and the room around it.
 *
 * WIDTH IS FIXED. A box sized to its own text makes every tier a ragged row
 * and every corridor a different length; one width means the lines between two
 * tiers are parallel, which is what makes the map readable at a glance. Names
 * that do not fit are truncated when drawn.
 */
export const NODE_WIDTH = 176
export const NODE_HEIGHT = 66
/** Between the box edge and where its line starts. Lines must not touch text. */
export const EDGE_GAP = 10
/** Between tiers, and between boxes within one. */
const TIER_GAP = 150
const SIBLING_GAP = 24
/** How tight the turns are. */
const CORNER = 12

/**
 * Lays a graph out in tiers along one axis.
 *
 * `horizontal` runs the chain left to right, which is the default because the
 * boxes are wider than they are tall and a wide box wants a wide gap beside
 * it, not above it.
 */
export function layout(source: GraphSource, horizontal: boolean): Layout {
  const byTier = new Map<number, GraphSource['nodes']>()
  for (const node of source.nodes) {
    const row = byTier.get(node.tier)
    if (row) row.push(node)
    else byTier.set(node.tier, [node])
  }

  // Only tiers with something in them take space: a pod with no ingress should
  // not sit under an empty row, because the gap reads as a missing thing
  // rather than an absent one.
  const present = [...byTier.keys()].sort((a, b) => a - b)

  const alongStep = horizontal ? NODE_WIDTH + TIER_GAP : NODE_HEIGHT + TIER_GAP
  const acrossStep = horizontal ? NODE_HEIGHT + SIBLING_GAP : NODE_WIDTH + SIBLING_GAP

  const placed = new Map<string, LaidOutNode>()
  const nodes: LaidOutNode[] = []

  present.forEach((tier, index) => {
    const row = byTier.get(tier)!
    row.forEach((node, position) => {
      // Centred within the tier, so a row of one sits opposite the middle of a
      // row of six rather than against its edge.
      const across = (position - (row.length - 1) / 2) * acrossStep
      const along = index * alongStep

      const out: LaidOutNode = {
        id: node.id,
        kind: node.kind,
        apiKind: node.apiKind,
        name: node.name,
        namespace: node.namespace,
        healthy: node.healthy,
        subject: node.subject,
        x: horizontal ? along : across,
        y: horizontal ? across : along,
        width: NODE_WIDTH,
        height: NODE_HEIGHT,
      }
      placed.set(node.id, out)
      nodes.push(out)
    })
  })

  const edges: LaidOutEdge[] = []
  for (const [index, edge] of source.edges.entries()) {
    const from = placed.get(edge.from)
    const to = placed.get(edge.to)
    if (!from || !to) continue

    edges.push({
      id: `${edge.from}->${edge.to}#${index}`,
      from: edge.from,
      to: edge.to,
      path: route(from, to, horizontal, nodes),
    })
  }

  return { nodes, edges, bounds: extent(nodes) }
}

/**
 * Builds one route between two boxes.
 *
 * FROM THE EDGE OF THE BOX, NOT ITS CENTRE, and set back by EDGE_GAP so the
 * line never touches the text.
 *
 * Three cases, and the two special ones are special because a screenshot said
 * so:
 *
 *   - SAME TIER. A Deployment and its ReplicaSet sit side by side. Treated
 *     like any other edge the line left on the wrong axis entirely, dropped
 *     below both boxes and arrived pointing backwards. Siblings connect
 *     directly, across.
 *   - CROSSING A TIER. The pod reaches its ConfigMaps past the container
 *     tier, and the long run down the middle went straight through that box
 *     and its label. Two corridors are possible — just after the source, or
 *     just before the target — so both are tried and the one that clears
 *     every box wins.
 *   - Everything else is the ordinary one-tier step.
 */
function route(
  from: LaidOutNode,
  to: LaidOutNode,
  horizontal: boolean,
  obstacles: LaidOutNode[],
): string {
  const fromAlong = horizontal ? from.x : from.y
  const toAlong = horizontal ? to.x : to.y

  // Siblings: the tiers are the same, so the whole journey is across.
  if (fromAlong === toAlong) {
    const forward = horizontal ? to.y > from.y : to.x > from.x
    const sign = forward ? 1 : -1

    const start = horizontal
      ? { x: from.x, y: from.y + sign * (from.height / 2 + EDGE_GAP) }
      : { x: from.x + sign * (from.width / 2 + EDGE_GAP), y: from.y }
    const end = horizontal
      ? { x: to.x, y: to.y - sign * (to.height / 2 + EDGE_GAP) }
      : { x: to.x - sign * (to.width / 2 + EDGE_GAP), y: to.y }

    return `M ${round(start.x)} ${round(start.y)} L ${round(end.x)} ${round(end.y)}`
  }

  const sign = toAlong > fromAlong ? 1 : -1

  const start = horizontal
    ? { x: from.x + sign * (from.width / 2 + EDGE_GAP), y: from.y }
    : { x: from.x, y: from.y + sign * (from.height / 2 + EDGE_GAP) }

  const end = horizontal
    ? { x: to.x - sign * (to.width / 2 + EDGE_GAP), y: to.y }
    : { x: to.x, y: to.y - sign * (to.height / 2 + EDGE_GAP) }

  const others = obstacles.filter((box) => box.id !== from.id && box.id !== to.id)

  // Every way this edge could be drawn, best first. The first one that clears
  // every other box wins, which is how a route past an intervening tier is
  // found without hand-placing it.
  const routes: { x: number; y: number }[][] = []

  // Directly opposite: one straight run, and the nicest answer when the space
  // between is empty. It frequently is not — a pod and one of its ConfigMaps
  // are both centred, with the container tier between them — so this is a
  // candidate rather than a shortcut. Returning it unchecked is what drew a
  // line through the container box and its label.
  const opposite = horizontal ? start.y === end.y : start.x === end.x
  if (opposite) routes.push([start, end])

  if (!opposite) {
    // Turning just before the target reads better, because the lines converge
    // as they arrive; turning just after the source is the fallback.
    routes.push(
      corridorPoints(start, end, toAlong - sign * (TIER_GAP / 2 - EDGE_GAP), horizontal),
      corridorPoints(
        start,
        end,
        fromAlong + sign * (TIER_GAP / 2 - EDGE_GAP + (horizontal ? from.width : from.height) / 2),
        horizontal,
      ),
    )
  }

  // A LANE BESIDE THE OBSTACLES, for when the direct routes are blocked. It
  // sits exactly halfway between two rows of the tier it passes, so it is in
  // the gap by construction rather than by luck. Both sides are offered
  // because either may be the occupied one.
  const lane = horizontal ? (NODE_HEIGHT + SIBLING_GAP) / 2 : (NODE_WIDTH + SIBLING_GAP) / 2
  for (const side of [1, -1]) {
    const offset = (horizontal ? start.y : start.x) + side * lane
    routes.push(
      horizontal
        ? [start, { x: start.x, y: offset }, { x: end.x, y: offset }, end]
        : [start, { x: offset, y: start.y }, { x: offset, y: end.y }, end],
    )
  }

  for (const points of routes) {
    const blocked = points.some((_, i) => i > 0 && crosses(points[i - 1], points[i], others))
    if (!blocked) return rounded(points)
  }

  // Nothing was clear. The first candidate is still the most readable, and a
  // line crossing a box is better than no line at all.
  return rounded(routes[0])
}

/** The four points of a two-bend route through one corridor. */
function corridorPoints(
  start: { x: number; y: number },
  end: { x: number; y: number },
  corridor: number,
  horizontal: boolean,
): { x: number; y: number }[] {
  return horizontal
    ? [start, { x: corridor, y: start.y }, { x: corridor, y: end.y }, end]
    : [start, { x: start.x, y: corridor }, { x: end.x, y: corridor }, end]
}

/**
 * Whether an axis-aligned run passes through any box.
 *
 * Sampled along the run rather than tested at its ends: a line that enters one
 * side of a box and leaves the other has no endpoint inside it, which is
 * exactly the case that reached a screenshot.
 */
function crosses(
  a: { x: number; y: number },
  b: { x: number; y: number },
  boxes: LaidOutNode[],
): boolean {
  const steps = Math.max(2, Math.ceil(Math.hypot(b.x - a.x, b.y - a.y) / 4))

  for (let step = 0; step <= steps; step++) {
    const t = step / steps
    const x = a.x + (b.x - a.x) * t
    const y = a.y + (b.y - a.y) * t

    for (const box of boxes) {
      if (Math.abs(x - box.x) < box.width / 2 && Math.abs(y - box.y) < box.height / 2) return true
    }
  }
  return false
}

/**
 * Joins points with straight runs and rounded turns.
 *
 * A quadratic through each corner, pulled back along both approaches by the
 * radius — or by half the shorter run when a segment is too short to give up
 * a full radius, which is what stops a tight turn folding back on itself.
 */
export function rounded(points: { x: number; y: number }[], radius = CORNER): string {
  if (points.length < 2) return ''

  const parts = [`M ${round(points[0].x)} ${round(points[0].y)}`]

  for (let i = 1; i < points.length - 1; i++) {
    const previous = points[i - 1]
    const corner = points[i]
    const next = points[i + 1]

    const inLength = distance(previous, corner)
    const outLength = distance(corner, next)
    const r = Math.min(radius, inLength / 2, outLength / 2)

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

/** Two decimals is well past what a screen can show, and keeps paths short. */
function round(value: number): number {
  return Math.round(value * 100) / 100
}

/** The extent of every box, with room for the lines that leave them. */
function extent(nodes: LaidOutNode[]): Layout['bounds'] {
  if (nodes.length === 0) return { x: 0, y: 0, width: 1, height: 1 }

  const left = Math.min(...nodes.map((n) => n.x - n.width / 2))
  const right = Math.max(...nodes.map((n) => n.x + n.width / 2))
  const top = Math.min(...nodes.map((n) => n.y - n.height / 2))
  const bottom = Math.max(...nodes.map((n) => n.y + n.height / 2))

  const margin = 24
  return {
    x: left - margin,
    y: top - margin,
    width: right - left + margin * 2,
    height: bottom - top + margin * 2,
  }
}
