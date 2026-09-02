/**
 * Folds a complete graph into one somebody can read.
 *
 * THE GRAPH IS NEVER INCOMPLETE — that is the point of doing this here. The
 * backend emits every pod, every container and every edge, because a map that
 * quietly omitted a replica would be a map nobody could trust. A Deployment
 * with thirty pods and seven ConfigMaps genuinely has two hundred and ten
 * edges, and all of them are real.
 *
 * What it does not have to be is drawn all at once. A sibling set larger than
 * a handful is collapsed into ONE box that stands for its members, with their
 * edges rewritten onto it and deduplicated — so thirty pods reading the same
 * ConfigMap draw one line instead of thirty. Expanding puts the members back.
 *
 * NOTHING IS INVENTED BY FOLDING. A group node IS its members; every edge on
 * it is an edge one of them has. The count is on the box so the map never
 * implies there is one of something when there are thirty.
 */

/**
 * The shape this needs, declared rather than imported from the generated
 * bindings.
 *
 * The Wails class carries a `convertValues` method, so nothing but an instance
 * of it satisfies the generated type — which would make every fixture in a
 * test a construction of a class it does not need. Structural types let the
 * real graph pass and a literal pass equally, which is the correct dependency
 * for a pure function anyway.
 */
export interface FoldNode {
  id: string
  kind: string
  apiKind: string
  name: string
  namespace: string
  tier: number
  detail: string
  healthy: boolean
  subject: boolean
  group: string
}

export interface FoldEdge {
  from: string
  to: string
  label: string
}

export interface FoldableGraph {
  nodes: FoldNode[]
  edges: FoldEdge[]
}

type Node = FoldNode
type Edge = FoldEdge

/** A folded set, drawn as one box until it is opened. */
export interface FoldedGroup {
  id: string
  /** What the members are — "Pod", "Container". */
  apiKind: string
  kind: string
  /** How many it stands for. */
  count: number
  /** How many of them are unwell, so a fold never hides a problem. */
  unhealthy: number
}

export interface FoldedGraph {
  nodes: (Node & { fold?: FoldedGroup })[]
  edges: Edge[]
  /** Every set that could be folded, whether or not it currently is. */
  groups: FoldedGroup[]
}

/**
 * Sets smaller than this are left alone.
 *
 * Three replicas are worth seeing; thirty are worth counting. The number is
 * low because the cost of folding is one click and the cost of not folding is
 * a map nobody can read.
 */
export const FOLD_THRESHOLD = 5

/** The id a folded set is drawn under. */
function groupID(parent: string, kind: string): string {
  return `fold/${parent}/${kind}`
}

/**
 * Folds every sibling set above the threshold that is not expanded.
 *
 * `expanded` holds the ids of groups the operator has opened, so the fold is
 * their choice after the first look rather than a fixed rule.
 */
export function fold(graph: FoldableGraph, expanded: Set<string>): FoldedGraph {
  const sets = new Map<string, Node[]>()
  for (const node of graph.nodes) {
    if (!node.group) continue
    const key = groupID(node.group, node.kind)
    const members = sets.get(key)
    if (members) members.push(node)
    else sets.set(key, [node])
  }

  const groups: FoldedGroup[] = []
  /** Member id -> the group standing in for it, for the sets being folded. */
  const standIn = new Map<string, string>()

  for (const [key, members] of sets) {
    if (members.length < FOLD_THRESHOLD) continue

    const group: FoldedGroup = {
      id: key,
      apiKind: members[0].apiKind || 'Container',
      kind: members[0].kind,
      count: members.length,
      unhealthy: members.filter((member) => !member.healthy).length,
    }
    groups.push(group)

    if (expanded.has(key)) continue
    for (const member of members) standIn.set(member.id, key)
  }

  if (standIn.size === 0) {
    return { nodes: graph.nodes.map((node) => ({ ...node })), edges: graph.edges, groups }
  }

  // A folded pod takes its containers with it: they belong to something that
  // is no longer drawn, and leaving them would strand them under nothing.
  const hidden = new Set(standIn.keys())
  let grew = true
  while (grew) {
    grew = false
    for (const node of graph.nodes) {
      if (!hidden.has(node.id) && node.group && hidden.has(node.group)) {
        hidden.add(node.id)
        grew = true
      }
    }
  }

  const nodes: (Node & { fold?: FoldedGroup })[] = []
  for (const node of graph.nodes) {
    if (hidden.has(node.id)) continue
    nodes.push({ ...node })
  }

  for (const group of groups) {
    if (expanded.has(group.id)) continue

    nodes.push({
      id: group.id,
      kind: group.kind,
      apiKind: group.apiKind,
      // Plural, and counted. "30 Pods" never reads as one thing.
      name: `${group.count} ${group.apiKind}${group.count === 1 ? '' : 's'}`,
      namespace: '',
      tier: 0,
      detail: group.unhealthy > 0 ? `${group.unhealthy} not ready` : '',
      // UNWELL IF ANY MEMBER IS. Folding must never hide a problem: a set with
      // one failing pod in thirty is drawn as a set with a problem.
      healthy: group.unhealthy === 0,
      subject: false,
      group: '',
      fold: group,
    })
  }

  // Edges are rewritten onto the group and deduplicated: thirty pods reading
  // one ConfigMap become one line, which is the whole point.
  const seen = new Set<string>()
  const edges: Edge[] = []

  for (const edge of graph.edges) {
    const from = standIn.get(edge.from) ?? edge.from
    const to = standIn.get(edge.to) ?? edge.to

    // An edge wholly inside a folded set has nothing left to join.
    if (from === to) continue
    if (hidden.has(from) || hidden.has(to)) continue

    const key = `${from}->${to}`
    if (seen.has(key)) continue
    seen.add(key)

    edges.push({ ...edge, from, to })
  }

  return { nodes, edges, groups }
}
