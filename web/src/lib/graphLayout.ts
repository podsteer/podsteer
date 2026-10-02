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

// --- The topology's layout ---------------------------------------------------

/** What `layoutCompound` needs: ids and edges, nothing it would not use. */
export interface CompoundSource {
  nodes: { id: string }[]
  edges: { id?: string; from: string; to: string }[]
}

/** A box's place, by its centre — the same convention `layout` uses. */
export interface PlacedNode {
  id: string
  x: number
  y: number
  width: number
  height: number
}

export interface PlacedEdge {
  id: string
  from: string
  to: string
  /** The route's corners, kept so a redraw can move or cull it without parsing `path`. */
  points: { x: number; y: number }[]
  path: string
}

/** A group's frame, by its TOP-LEFT corner, with room for a header line. */
export interface PlacedGroup {
  id: string
  x: number
  y: number
  width: number
  height: number
}

export interface CompoundLayout {
  nodes: PlacedNode[]
  edges: PlacedEdge[]
  groups: PlacedGroup[]
  bounds: { x: number; y: number; width: number; height: number }
}

/** Inside a group's frame, around its contents. */
export const GROUP_PADDING = 16
/** The group's own line — its name and its counts — above its contents. */
export const GROUP_HEADER = 30
/** Between packed pieces: components inside a group, and groups themselves. */
const PACK_GAP = 40
/**
 * A box with more lines than this is a HUB for the purpose of splitting the
 * graph into pieces — see layoutCompound.
 */
const HUB_DEGREE = 24
/**
 * Above this many boxes, dagre is told to skip its crossing-reduction sweeps.
 * Those sweeps are most of its cost and grow far faster than the graph does;
 * a piece this large is unreadable as a whole anyway, and it is the folded
 * and grouped view somebody reads it through.
 */
const ORDER_SWEEP_LIMIT = 600

/**
 * Lays the topology out: groups as frames, everything else packed.
 *
 * NOT ONE DAGRE CALL, and that is a measured decision rather than a taste.
 * dagre on a whole namespace-wide topology — 5,000 boxes, 8,000 lines — took
 * twenty seconds with `compound: true` and ten without, because its crossing
 * reduction is far worse than linear and a cluster is mostly UNRELATED pieces
 * that have no crossings between them to reduce. So the graph is split before
 * dagre sees it:
 *
 * 1. by GROUP (`parents`: box id → group id; absent means ungrouped), because
 *    a group's frame has to enclose its members and nothing else;
 * 2. within a group, into CONNECTED PIECES, each laid out by dagre on its own
 *    — exactly what dagre is good at, a few dozen boxes with a direction;
 * 3. pieces are shelf-packed into the group's frame, and frames (with the
 *    ungrouped pieces) are shelf-packed onto the page.
 *
 * A HUB — a box with more than HUB_DEGREE lines, a namespace-wide
 * NetworkPolicy or the ConfigMap every pod mounts — would otherwise glue a
 * whole namespace into one piece. Hubs do not join pieces; each is laid out in
 * the piece it has the most lines into, which is where somebody looks for it.
 *
 * Lines inside a piece are dagre's. Lines between pieces or groups get a plain
 * three-segment route, rounded like the rest: there is no layout question
 * across a gap that a packer chose.
 *
 * Deterministic for the same input: everything is ordered by id or by size
 * before it is placed, which is what lets graphPositions keep a drawing still.
 */
export function layoutCompound(
  source: CompoundSource,
  parents: Map<string, string>,
  horizontal: boolean,
): CompoundLayout {
  const ids = source.nodes.map((node) => node.id)
  const known = new Set(ids)
  const edges = source.edges.filter(
    (edge) => known.has(edge.from) && known.has(edge.to) && edge.from !== edge.to,
  )

  const degree = new Map<string, number>()
  for (const edge of edges) {
    degree.set(edge.from, (degree.get(edge.from) ?? 0) + 1)
    degree.set(edge.to, (degree.get(edge.to) ?? 0) + 1)
  }
  const isHub = (id: string) => (degree.get(id) ?? 0) > HUB_DEGREE
  const groupOf = (id: string) => parents.get(id) ?? ''

  // Pieces, by union-find over the non-hub lines inside one group.
  const root = new Map<string, string>(ids.map((id) => [id, id]))
  const find = (id: string): string => {
    let at = id
    while (root.get(at) !== at) {
      const up = root.get(root.get(at)!)!
      root.set(at, up)
      at = up
    }
    return at
  }
  const union = (a: string, b: string) => {
    const ra = find(a)
    const rb = find(b)
    if (ra === rb) return
    // The smaller id wins, so the representative never depends on edge order.
    if (ra < rb) root.set(rb, ra)
    else root.set(ra, rb)
  }
  for (const edge of edges) {
    if (isHub(edge.from) || isHub(edge.to)) continue
    if (groupOf(edge.from) !== groupOf(edge.to)) continue
    union(edge.from, edge.to)
  }

  // A hub joins the piece it has the most lines into, inside its own group.
  const hubPull = new Map<string, Map<string, number>>()
  for (const edge of edges) {
    for (const [hub, other] of [
      [edge.from, edge.to],
      [edge.to, edge.from],
    ]) {
      if (!isHub(hub) || isHub(other) || groupOf(hub) !== groupOf(other)) continue
      const tally = hubPull.get(hub) ?? new Map<string, number>()
      const piece = find(other)
      tally.set(piece, (tally.get(piece) ?? 0) + 1)
      hubPull.set(hub, tally)
    }
  }
  for (const [hub, tally] of [...hubPull].sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0))) {
    let best = ''
    let most = -1
    for (const [piece, count] of tally) {
      if (count > most || (count === most && piece < best)) {
        best = piece
        most = count
      }
    }
    if (best) union(hub, best)
  }

  // Members of each piece, and the pieces of each group.
  const pieceMembers = new Map<string, string[]>()
  for (const id of ids) {
    const piece = find(id)
    const members = pieceMembers.get(piece)
    if (members) members.push(id)
    else pieceMembers.set(piece, [id])
  }
  const groupPieces = new Map<string, string[]>()
  for (const [piece, members] of pieceMembers) {
    const group = groupOf(members[0])
    const pieces = groupPieces.get(group)
    if (pieces) pieces.push(piece)
    else groupPieces.set(group, [piece])
  }

  const placed = new Map<string, PlacedNode>()
  const routed = new Map<string, { x: number; y: number }[]>()
  const edgeKey = (edge: { id?: string; from: string; to: string }, index: number) =>
    edge.id ?? `${edge.from}->${edge.to}#${index}`

  const edgesByPiece = new Map<string, { edge: (typeof edges)[number]; key: string }[]>()
  edges.forEach((edge, index) => {
    const piece = find(edge.from)
    if (piece !== find(edge.to)) return
    const list = edgesByPiece.get(piece) ?? []
    list.push({ edge, key: edgeKey(edge, index) })
    edgesByPiece.set(piece, list)
  })

  /** One piece, laid out at the origin; returns its size. */
  const layPiece = (piece: string): Block => {
    const members = pieceMembers.get(piece)!
    const local = new Map<string, PlacedNode>()
    const localRoutes = new Map<string, { x: number; y: number }[]>()

    if (members.length === 1) {
      local.set(members[0], {
        id: members[0],
        x: NODE_WIDTH / 2,
        y: NODE_HEIGHT / 2,
        width: NODE_WIDTH,
        height: NODE_HEIGHT,
      })
      return { width: NODE_WIDTH, height: NODE_HEIGHT, nodes: local, routes: localRoutes }
    }

    const graph = new dagre.graphlib.Graph({ directed: true, multigraph: true })
    graph.setGraph({
      rankdir: horizontal ? 'LR' : 'TB',
      nodesep: 28,
      ranksep: 110,
      marginx: 0,
      marginy: 0,
      ranker: 'longest-path',
      disableOptimalOrderHeuristic: members.length > ORDER_SWEEP_LIMIT,
    })
    graph.setDefaultEdgeLabel(() => ({}))
    for (const id of [...members].sort()) graph.setNode(id, { width: NODE_WIDTH, height: NODE_HEIGHT })
    // In a fixed order, so the same graph sent in another order lays out the same.
    const pieceEdges = [...(edgesByPiece.get(piece) ?? [])].sort((a, b) =>
      a.edge.from < b.edge.from ? -1 : a.edge.from > b.edge.from ? 1 : a.edge.to < b.edge.to ? -1 : a.edge.to > b.edge.to ? 1 : 0,
    )
    for (const { edge, key } of pieceEdges) graph.setEdge(edge.from, edge.to, {}, key)

    dagre.layout(graph)

    for (const id of members) {
      const at = graph.node(id)
      local.set(id, { id, x: at.x, y: at.y, width: NODE_WIDTH, height: NODE_HEIGHT })
    }
    for (const { edge, key } of pieceEdges) {
      const points = graph.edge(edge.from, edge.to, key)?.points
      if (points?.length) localRoutes.set(key, points.map((p: { x: number; y: number }) => ({ x: p.x, y: p.y })))
    }
    const size = graph.graph()
    return {
      width: Math.max(size.width ?? NODE_WIDTH, NODE_WIDTH),
      height: Math.max(size.height ?? NODE_HEIGHT, NODE_HEIGHT),
      nodes: local,
      routes: localRoutes,
    }
  }

  // Each group's pieces, packed into a frame.
  const blocks: { group: string; block: Block; framed: boolean; order: string }[] = []
  for (const [group, pieces] of groupPieces) {
    const laid = pieces
      .map((piece) => ({ piece, block: layPiece(piece) }))
      .sort(
        (a, b) =>
          b.block.width * b.block.height - a.block.width * a.block.height ||
          (a.piece < b.piece ? -1 : 1),
      )

    if (group === '') {
      // Ungrouped pieces sit on the page beside the frames, unframed.
      for (const { piece, block } of laid) blocks.push({ group: '', block, framed: false, order: `~${piece}` })
      continue
    }

    const packed = pack(laid.map(({ block }) => block))
    const width = packed.width + GROUP_PADDING * 2
    const height = packed.height + GROUP_PADDING * 2 + GROUP_HEADER
    const frame: Block = { width, height, nodes: new Map(), routes: new Map() }
    laid.forEach(({ block }, index) => {
      const at = packed.offsets[index]
      shiftInto(frame, block, at.x + GROUP_PADDING, at.y + GROUP_PADDING + GROUP_HEADER)
    })
    blocks.push({ group, block: frame, framed: true, order: group })
  }

  // Frames by name, so a group is found where it was last time; loose pieces after.
  blocks.sort((a, b) => (a.order < b.order ? -1 : a.order > b.order ? 1 : 0))
  const page = pack(blocks.map(({ block }) => block))

  const groups: PlacedGroup[] = []
  blocks.forEach(({ group, block, framed }, index) => {
    const at = page.offsets[index]
    const x = at.x + MARGIN
    const y = at.y + MARGIN
    for (const node of block.nodes.values()) placed.set(node.id, { ...node, x: node.x + x, y: node.y + y })
    for (const [key, points] of block.routes) routed.set(key, points.map((p) => ({ x: p.x + x, y: p.y + y })))
    if (framed) groups.push({ id: group, x, y, width: block.width, height: block.height })
  })

  const out: PlacedEdge[] = []
  edges.forEach((edge, index) => {
    const key = edgeKey(edge, index)
    const from = placed.get(edge.from)!
    const to = placed.get(edge.to)!
    const inside = routed.get(key)
    const points = inside
      ? trimPlaced(inside, from, to, horizontal)
      : elbow(from, to, horizontal)
    out.push({ id: key, from: edge.from, to: edge.to, points, path: rounded(points) })
  })

  return {
    nodes: ids.map((id) => placed.get(id)!),
    edges: out,
    groups,
    bounds: {
      x: 0,
      y: 0,
      width: page.width + MARGIN * 2,
      height: page.height + MARGIN * 2,
    },
  }
}

/** A laid-out piece or frame, at its own origin. */
interface Block {
  width: number
  height: number
  nodes: Map<string, PlacedNode>
  routes: Map<string, { x: number; y: number }[]>
}

function shiftInto(target: Block, block: Block, dx: number, dy: number): void {
  for (const node of block.nodes.values()) target.nodes.set(node.id, { ...node, x: node.x + dx, y: node.y + dy })
  for (const [key, points] of block.routes) target.routes.set(key, points.map((p) => ({ x: p.x + dx, y: p.y + dy })))
}

/**
 * Shelf packing: rows of blocks, the row width chosen so the result is
 * roughly as wide as a screen is in proportion.
 *
 * Blocks arrive in the order they should be read; the packer never reorders
 * them, so the same input always lands in the same place.
 */
export function pack(blocks: { width: number; height: number }[]): {
  offsets: { x: number; y: number }[]
  width: number
  height: number
} {
  if (blocks.length === 0) return { offsets: [], width: 0, height: 0 }
  const area = blocks.reduce((sum, b) => sum + (b.width + PACK_GAP) * (b.height + PACK_GAP), 0)
  const widest = Math.max(...blocks.map((b) => b.width))
  const rowWidth = Math.max(widest, Math.sqrt(area * 1.6))

  const offsets: { x: number; y: number }[] = []
  let x = 0
  let y = 0
  let rowHeight = 0
  let width = 0
  for (const block of blocks) {
    if (x > 0 && x + block.width > rowWidth) {
      y += rowHeight + PACK_GAP
      x = 0
      rowHeight = 0
    }
    offsets.push({ x, y })
    x += block.width + PACK_GAP
    rowHeight = Math.max(rowHeight, block.height)
    width = Math.max(width, x - PACK_GAP)
  }
  return { offsets, width, height: y + rowHeight }
}

/** dagre's route with its ends pulled back from both boxes, as `layout` does. */
function trimPlaced(
  points: { x: number; y: number }[],
  from: PlacedNode,
  to: PlacedNode,
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
 * A three-segment route between two boxes nothing laid out together: out of
 * the side facing the other box, across at the midpoint, and in.
 */
export function elbow(
  from: { x: number; y: number; width: number; height: number },
  to: { x: number; y: number; width: number; height: number },
  horizontal: boolean,
): { x: number; y: number }[] {
  if (horizontal) {
    const direction = Math.sign(to.x - from.x) || 1
    const sx = from.x + direction * (from.width / 2 + EDGE_GAP)
    const ex = to.x - direction * (to.width / 2 + EDGE_GAP)
    const mx = (sx + ex) / 2
    return [
      { x: sx, y: from.y },
      { x: mx, y: from.y },
      { x: mx, y: to.y },
      { x: ex, y: to.y },
    ]
  }
  const direction = Math.sign(to.y - from.y) || 1
  const sy = from.y + direction * (from.height / 2 + EDGE_GAP)
  const ey = to.y - direction * (to.height / 2 + EDGE_GAP)
  const my = (sy + ey) / 2
  return [
    { x: from.x, y: sy },
    { x: from.x, y: my },
    { x: to.x, y: my },
    { x: to.x, y: ey },
  ]
}
