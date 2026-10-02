/**
 * What the topology draws, from what the backend sent: kind toggles, folding,
 * then grouping.
 *
 * THE GRAPH IS STILL NEVER INCOMPLETE. Everything here decides what is DRAWN —
 * which kinds, which sibling sets as one box, which groups as one box — and
 * every box that stands for several objects carries their complete counts and
 * their worst state, so a collapsed namespace with one crash-looping pod in it
 * is drawn as a namespace with a problem in it. The rules are graphFold.ts's,
 * applied one level up:
 *
 * - a set is as unwell as its worst member;
 * - every line on a set is a line one of its members has, re-pointed onto the
 *   set and deduplicated — never one invented for it;
 * - the count is on the box, so it never reads as one thing.
 */
import { fold, FOLD_THRESHOLD } from './graphFold'
import type { NodeState, TopologyEdgeKind, TopologyGraph } from './topology/contract'

/** One box as the topology draws it. */
export interface ViewNode {
  id: string
  kind: string
  /** The Kubernetes Kind, verbatim — '' for a set, which is not an object. */
  apiKind: string
  name: string
  namespace: string
  state: NodeState
  detail: string
  /** Sibling set for folding: the workload that manages a pod. */
  group: string
  labels?: Record<string, string>
  /** The ids of the topology's own nodes this box stands for — itself, for an object. */
  members: string[]
  /** How many objects of each Kind it stands for, counted completely. */
  counts: Record<string, number>
  /**
   * What kind of set the box is, when it is one: a FOLDED sibling set (opened
   * by clicking it), a COLLAPSED group (opened from its header or by
   * clicking it), or a set the BACKEND summarised (which does not open — the
   * pods were never sent).
   */
  set?: 'fold' | 'group' | 'summary'
}

export interface ViewEdge {
  id: string
  from: string
  to: string
  kind: TopologyEdgeKind
  /** Every kind of relationship the deduplicated line stands for. */
  kinds: TopologyEdgeKind[]
  label: string
}

export interface ViewGraph {
  nodes: ViewNode[]
  edges: ViewEdge[]
}

const STATE_RANK: Record<NodeState, number> = { neutral: 0, ok: 1, warn: 2, bad: 3 }

/** The worse of two states. `neutral` loses to anything that was checked. */
export function worstState(states: Iterable<NodeState>): NodeState {
  let worst: NodeState = 'neutral'
  for (const state of states) if (STATE_RANK[state] > STATE_RANK[worst]) worst = state
  return worst
}

/** Adds one count map into another. */
function addCounts(into: Record<string, number>, from: Record<string, number>): void {
  for (const [kind, count] of Object.entries(from)) into[kind] = (into[kind] ?? 0) + count
}

/** The objects a box stands for, as "3 Pods, 1 Service". */
export function describeCounts(counts: Record<string, number>): string {
  return Object.entries(counts)
    .sort(([a, x], [b, y]) => y - x || (a < b ? -1 : 1))
    .map(([kind, count]) => `${count} ${kind}${count === 1 ? '' : 's'}`)
    .join(', ')
}

/** How many objects a box stands for in total. */
export function totalOf(counts: Record<string, number>): number {
  let total = 0
  for (const count of Object.values(counts)) total += count
  return total
}

/**
 * The per-Kind counts the kind toggles show: the backend's COMPLETE counts,
 * plus any Kind drawn that the counts do not name.
 */
export function kindCounts(graph: TopologyGraph): { apiKind: string; count: number }[] {
  const counts: Record<string, number> = { ...(graph.counts ?? {}) }
  for (const node of graph.nodes ?? []) {
    if (node.apiKind && counts[node.apiKind] === undefined) counts[node.apiKind] = 0
  }
  for (const node of graph.nodes ?? []) {
    if (node.apiKind && (graph.counts ?? {})[node.apiKind] === undefined) counts[node.apiKind]++
  }
  return Object.entries(counts)
    .map(([apiKind, count]) => ({ apiKind, count }))
    .sort((a, b) => (a.apiKind < b.apiKind ? -1 : 1))
}

/**
 * The backend's graph as boxes, with the hidden Kinds taken out.
 *
 * A HIDDEN KIND TAKES ITS LINES WITH IT, with one exception that is still a
 * true statement: OWNERSHIP IS TRANSITIVE. Hiding ReplicaSets would otherwise
 * strand every pod away from its Deployment, and "this Deployment owns these
 * pods, via a ReplicaSet" is what Kubernetes' own garbage collector acts on.
 * The bridged line says "via ReplicaSet" so it is never read as direct.
 * Nothing else is bridged: a Service selecting pods says nothing about what an
 * Ingress in front of it routes to once the Service is hidden.
 */
export function toView(graph: TopologyGraph, hidden: ReadonlySet<string>): ViewGraph {
  const nodes: ViewNode[] = []
  const removed = new Map<string, string>()
  for (const node of graph.nodes ?? []) {
    if (hidden.has(node.apiKind)) {
      removed.set(node.id, node.apiKind)
      continue
    }
    const summary = node.podSummary
    nodes.push({
      id: node.id,
      kind: node.kind,
      apiKind: summary ? '' : node.apiKind,
      name: node.name,
      namespace: node.namespace,
      state: node.state,
      detail: node.detail,
      group: node.group ?? '',
      labels: node.labels,
      members: [node.id],
      counts: summary ? { Pod: summary.total } : { [node.apiKind || node.kind]: 1 },
      set: summary ? 'summary' : undefined,
    })
  }

  const edges: ViewEdge[] = []
  /** Child id → the ids that own it. */
  const owners = new Map<string, string[]>()
  for (const edge of graph.edges ?? []) {
    if (edge.kind !== 'owns') continue
    const list = owners.get(edge.to) ?? []
    list.push(edge.from)
    owners.set(edge.to, list)
  }

  /** The nearest VISIBLE owners above a hidden node, following owns upward. */
  const visibleOwners = (id: string, seen: Set<string>): string[] => {
    const found: string[] = []
    for (const from of owners.get(id) ?? []) {
      if (seen.has(from)) continue
      seen.add(from)
      if (removed.has(from)) found.push(...visibleOwners(from, seen))
      else found.push(from)
    }
    return found
  }

  for (const edge of graph.edges ?? []) {
    const fromHidden = removed.has(edge.from)
    const toHidden = removed.has(edge.to)
    if (!fromHidden && !toHidden) {
      edges.push(viewEdge(edge.from, edge.to, edge.kind, edge.label))
      continue
    }
    // The bridge: a visible child of a hidden owner, joined to the visible
    // owner above it.
    if (edge.kind === 'owns' && fromHidden && !toHidden) {
      for (const owner of visibleOwners(edge.from, new Set([edge.from]))) {
        edges.push(viewEdge(owner, edge.to, 'owns', `via ${removed.get(edge.from)}`))
      }
    }
  }

  return { nodes, edges: dedupe(edges) }
}

function viewEdge(from: string, to: string, kind: TopologyEdgeKind, label: string): ViewEdge {
  return { id: `${from}->${to}`, from, to, kind, kinds: [kind], label }
}

/** One line per (from, to), carrying every kind of relationship it stands for. */
function dedupe(edges: ViewEdge[]): ViewEdge[] {
  const byKey = new Map<string, ViewEdge>()
  for (const edge of edges) {
    if (edge.from === edge.to) continue
    const key = `${edge.from}->${edge.to}`
    const held = byKey.get(key)
    if (!held) {
      byKey.set(key, { ...edge, id: key, kinds: [...edge.kinds] })
      continue
    }
    for (const kind of edge.kinds) if (!held.kinds.includes(kind)) held.kinds.push(kind)
  }
  return [...byKey.values()]
}

/** Re-points every line onto the box standing in for its ends, then dedupes. */
function repoint(edges: ViewEdge[], standIn: Map<string, string>, drawn: Set<string>): ViewEdge[] {
  const out: ViewEdge[] = []
  for (const edge of edges) {
    const from = standIn.get(edge.from) ?? edge.from
    const to = standIn.get(edge.to) ?? edge.to
    if (from === to) continue
    if (!drawn.has(from) || !drawn.has(to)) continue
    out.push({ ...edge, from, to })
  }
  return dedupe(out)
}

/** The sibling sets that could be folded, and which drawn box stands for which object. */
export interface FoldedView extends ViewGraph {
  /** Ids of every foldable set, open or not. */
  sets: string[]
  /** Object id → the box drawing it, for every object not drawn as itself. */
  standIn: Map<string, string>
}

/**
 * Folds sibling sets larger than FOLD_THRESHOLD, exactly as the dependency
 * map does — graphFold.ts decides which — and then gives each folded box the
 * topology's own facts: its members' worst STATE (not merely healthy or not),
 * their complete counts, and the namespace they share.
 */
export function foldView(view: ViewGraph, expanded: ReadonlySet<string>): FoldedView {
  const byId = new Map(view.nodes.map((node) => [node.id, node]))
  const folded = fold(
    {
      nodes: view.nodes.map((node) => ({
        id: node.id,
        kind: node.kind,
        apiKind: node.apiKind,
        name: node.name,
        namespace: node.namespace,
        tier: 0,
        detail: node.detail,
        healthy: node.state === 'ok' || node.state === 'neutral',
        subject: false,
        missing: false,
        // A backend summary is already a set and is never folded again.
        group: node.set ? '' : node.group,
      })),
      edges: [],
    },
    expanded as Set<string>,
  )

  const drawnIds = new Set(folded.nodes.map((node) => node.id))
  const standIn = new Map<string, string>()
  const membersOf = new Map<string, ViewNode[]>()
  for (const node of view.nodes) {
    if (drawnIds.has(node.id)) continue
    const key = `fold/${node.group}/${node.kind}`
    if (!drawnIds.has(key)) continue
    standIn.set(node.id, key)
    const list = membersOf.get(key) ?? []
    list.push(node)
    membersOf.set(key, list)
  }

  const nodes: ViewNode[] = folded.nodes.map((drawn) => {
    const own = byId.get(drawn.id)
    if (own) return own
    const members = membersOf.get(drawn.id) ?? []
    const counts: Record<string, number> = {}
    for (const member of members) addCounts(counts, member.counts)
    const namespaces = new Set(members.map((member) => member.namespace))
    const unwell = members.filter((member) => member.state === 'bad' || member.state === 'warn').length
    return {
      id: drawn.id,
      kind: drawn.kind,
      apiKind: '',
      name: drawn.name,
      namespace: namespaces.size === 1 ? [...namespaces][0] : '',
      state: worstState(members.map((member) => member.state)),
      detail: unwell > 0 ? `${unwell} not ready` : '',
      group: '',
      members: members.flatMap((member) => member.members),
      counts,
      set: 'fold',
    }
  })

  return {
    nodes,
    edges: repoint(view.edges, standIn, drawnIds),
    sets: folded.groups.map((group) => group.id),
    standIn,
  }
}

/** How boxes are grouped into frames. */
export type GroupBy = 'none' | 'namespace' | 'app' | { label: string }

/** The labels an application is recognised by, in the order they are trusted. */
const APP_LABELS = ['app.kubernetes.io/name', 'app.kubernetes.io/instance', 'app', 'k8s-app']

/** One group, whether drawn as a frame or collapsed to a box. */
export interface GroupInfo {
  id: string
  label: string
  collapsed: boolean
  /** Complete counts of everything in it, by Kind. */
  counts: Record<string, number>
  state: NodeState
  /** The topology's own node ids inside it. */
  members: string[]
}

export interface GroupedGraph extends ViewGraph {
  /** Box id → group id, for boxes inside an OPEN group. */
  parents: Map<string, string>
  groups: GroupInfo[]
  /** Box id → the collapsed group drawing it. */
  standIn: Map<string, string>
}

/** The id a group is drawn under. */
export function groupId(key: string): string {
  return `group/${key}`
}

/**
 * Groups boxes into frames, collapsing the ones in `collapsed`.
 *
 * A box's group comes from its own labels (or namespace) when it has them. A
 * box that does not — a pod, a ReplicaSet, a ConfigMap, a folded set: the
 * backend sends labels for top-level objects only — INHERITS one, in a fixed
 * order of trust: first from what OWNS it, then from anything pointing at it
 * if all of those agree, then from anything it points at if all of those
 * agree. A box whose neighbours disagree stays outside every group rather than
 * being filed arbitrarily: a namespace-wide NetworkPolicy belongs to no one
 * application, and putting it in the first one found would say it did.
 */
export function group(view: ViewGraph, by: GroupBy, collapsed: ReadonlySet<string>): GroupedGraph {
  if (by === 'none') {
    return { ...view, parents: new Map(), groups: [], standIn: new Map() }
  }

  const keyOf = new Map<string, string>()
  const labelOf = new Map<string, string>()
  const namespaces = new Set(view.nodes.map((node) => node.namespace).filter(Boolean))

  for (const node of view.nodes) {
    const own = ownKey(node, by, namespaces.size > 1)
    if (own) {
      keyOf.set(node.id, own.key)
      labelOf.set(own.key, own.label)
    }
  }

  inherit(view, keyOf)

  // Collapsed groups first, so the frames know their members.
  const info = new Map<string, GroupInfo>()
  for (const node of view.nodes) {
    const key = keyOf.get(node.id)
    if (key === undefined) continue
    const id = groupId(key)
    let entry = info.get(id)
    if (!entry) {
      entry = {
        id,
        label: labelOf.get(key) ?? key,
        collapsed: collapsed.has(id),
        counts: {},
        state: 'neutral',
        members: [],
      }
      info.set(id, entry)
    }
    addCounts(entry.counts, node.counts)
    entry.state = worstState([entry.state, node.state])
    for (const member of node.members) entry.members.push(member)
  }

  const nodes: ViewNode[] = []
  const parents = new Map<string, string>()
  const standIn = new Map<string, string>()
  for (const node of view.nodes) {
    const key = keyOf.get(node.id)
    if (key === undefined) {
      nodes.push(node)
      continue
    }
    const id = groupId(key)
    if (info.get(id)!.collapsed) standIn.set(node.id, id)
    else {
      parents.set(node.id, id)
      nodes.push(node)
    }
  }

  const groups = [...info.values()].sort((a, b) => (a.label < b.label ? -1 : a.label > b.label ? 1 : 0))
  for (const entry of groups) {
    if (!entry.collapsed) continue
    const total = totalOf(entry.counts)
    nodes.push({
      id: entry.id,
      kind: 'group',
      apiKind: '',
      name: entry.label,
      namespace: '',
      state: entry.state,
      detail: `${total} object${total === 1 ? '' : 's'}`,
      group: '',
      members: entry.members,
      counts: entry.counts,
      set: 'group',
    })
  }

  const drawn = new Set(nodes.map((node) => node.id))
  return { nodes, edges: repoint(view.edges, standIn, drawn), parents, groups, standIn }
}

function ownKey(
  node: ViewNode,
  by: Exclude<GroupBy, 'none'>,
  manyNamespaces: boolean,
): { key: string; label: string } | null {
  if (by === 'namespace') {
    return node.namespace ? { key: `ns:${node.namespace}`, label: node.namespace } : null
  }
  const labels = node.labels
  if (!labels) return null
  if (by === 'app') {
    for (const name of APP_LABELS) {
      const value = labels[name]
      if (!value) continue
      // An application is per namespace: "web" in two namespaces is two of them.
      return {
        key: `app:${node.namespace}/${value}`,
        label: manyNamespaces && node.namespace ? `${value} · ${node.namespace}` : value,
      }
    }
    return null
  }
  const value = labels[by.label]
  // A label's value groups across namespaces: team=payments is one team.
  return value ? { key: `label:${value}`, label: `${by.label}=${value}` } : null
}

function inherit(view: ViewGraph, keyOf: Map<string, string>): void {
  const unanimous = (candidates: (string | undefined)[]): string | undefined => {
    const known = candidates.filter((key): key is string => key !== undefined)
    if (known.length === 0) return undefined
    return known.every((key) => key === known[0]) ? known[0] : undefined
  }

  const incoming = new Map<string, ViewEdge[]>()
  const outgoing = new Map<string, ViewEdge[]>()
  for (const edge of view.edges) {
    ;(incoming.get(edge.to) ?? incoming.set(edge.to, []).get(edge.to)!).push(edge)
    ;(outgoing.get(edge.from) ?? outgoing.set(edge.from, []).get(edge.from)!).push(edge)
  }

  let changed = true
  while (changed) {
    changed = false
    for (const pass of ['owner', 'incoming', 'outgoing'] as const) {
      for (const node of view.nodes) {
        if (keyOf.has(node.id)) continue
        let key: string | undefined
        if (pass === 'owner') {
          key = unanimous(
            (incoming.get(node.id) ?? []).filter((e) => e.kinds.includes('owns')).map((e) => keyOf.get(e.from)),
          )
        } else if (pass === 'incoming') {
          key = unanimous((incoming.get(node.id) ?? []).map((e) => keyOf.get(e.from)))
        } else {
          key = unanimous((outgoing.get(node.id) ?? []).map((e) => keyOf.get(e.to)))
        }
        if (key !== undefined) {
          keyOf.set(node.id, key)
          changed = true
        }
      }
      // Ownership settles before weaker evidence is consulted.
      if (changed && pass === 'owner') break
    }
  }
}

export { FOLD_THRESHOLD }
