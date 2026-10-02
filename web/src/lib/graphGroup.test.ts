import { describe, expect, it } from 'vitest'
import { foldView, group, groupId, kindCounts, toView, worstState, type ViewGraph } from './graphGroup'
import type { TopologyEdge, TopologyGraph, TopologyNode } from './topology/contract'

function node(id: string, extra: Partial<TopologyNode> = {}): TopologyNode {
  const [namespace, apiKind, name] = id.split('/')
  return {
    id,
    kind: (apiKind ?? 'pod').toLowerCase(),
    apiKind: apiKind ?? 'Pod',
    name: name ?? id,
    namespace: namespace ?? '',
    state: 'ok',
    detail: '',
    group: '',
    ...extra,
  }
}

function edge(from: string, to: string, kind: TopologyEdge['kind'] = 'owns'): TopologyEdge {
  return { from, to, kind, label: '' }
}

function graph(nodes: TopologyNode[], edges: TopologyEdge[], counts?: Record<string, number>): TopologyGraph {
  const tally: Record<string, number> = {}
  for (const n of nodes) tally[n.apiKind] = (tally[n.apiKind] ?? 0) + 1
  return { nodes, edges, counts: counts ?? tally, unreadable: [], bounded: '', summarised: false, generatedAt: '' }
}

/** A Deployment, its ReplicaSet, n pods, a Service selecting them and a ConfigMap they mount. */
function app(ns: string, name: string, pods: number, labels = { 'app.kubernetes.io/name': name }) {
  const deploy = node(`${ns}/Deployment/${name}`, { kind: 'workload', labels })
  const rs = node(`${ns}/ReplicaSet/${name}-1`, { kind: 'replicaset', group: deploy.id })
  const svc = node(`${ns}/Service/${name}`, { kind: 'service', state: 'neutral', labels })
  const cm = node(`${ns}/ConfigMap/${name}-cfg`, { kind: 'config', state: 'neutral' })
  const nodes = [deploy, rs, svc, cm]
  const edges = [edge(deploy.id, rs.id)]
  for (let i = 0; i < pods; i++) {
    const pod = node(`${ns}/Pod/${name}-${i}`, { group: deploy.id })
    nodes.push(pod)
    edges.push(edge(rs.id, pod.id), edge(svc.id, pod.id, 'selects'), edge(pod.id, cm.id, 'attaches'))
  }
  return { nodes, edges }
}

describe('worstState', () => {
  it('lets anything that was checked beat neutral, and bad beat everything', () => {
    expect(worstState([])).toBe('neutral')
    expect(worstState(['neutral', 'ok'])).toBe('ok')
    expect(worstState(['ok', 'warn', 'neutral'])).toBe('warn')
    expect(worstState(['warn', 'bad', 'ok'])).toBe('bad')
  })
})

describe('kind toggles', () => {
  it('shows the backend counts, which stay complete when pods are summarised', () => {
    const g = graph([node('a/Pod/x')], [], { Pod: 4000, Service: 3 })
    expect(kindCounts(g)).toEqual([
      { apiKind: 'Pod', count: 4000 },
      { apiKind: 'Service', count: 3 },
    ])
  })

  it('bridges ownership through a hidden kind, and says it is indirect', () => {
    const { nodes, edges } = app('a', 'web', 2)
    const view = toView(graph(nodes, edges), new Set(['ReplicaSet']))

    expect(view.nodes.some((n) => n.apiKind === 'ReplicaSet')).toBe(false)
    const bridged = view.edges.filter((e) => e.from === 'a/Deployment/web')
    expect(bridged.map((e) => e.to).sort()).toEqual(['a/Pod/web-0', 'a/Pod/web-1'])
    expect(bridged.every((e) => e.label === 'via ReplicaSet' && e.kind === 'owns')).toBe(true)
  })

  it('bridges nothing but ownership: a hidden Service takes its lines with it', () => {
    const ingress = node('a/Ingress/in', { kind: 'ingress' })
    const { nodes, edges } = app('a', 'web', 1)
    const view = toView(
      graph([...nodes, ingress], [...edges, edge(ingress.id, 'a/Service/web', 'routes')]),
      new Set(['Service']),
    )
    expect(view.edges.some((e) => e.from === ingress.id)).toBe(false)
  })
})

describe('folding', () => {
  it('folds a large pod set into one box with the worst state and complete counts', () => {
    const { nodes, edges } = app('a', 'web', 6)
    nodes.find((n) => n.id === 'a/Pod/web-3')!.state = 'bad'
    const folded = foldView(toView(graph(nodes, edges), new Set()), new Set())

    const set = folded.nodes.find((n) => n.set === 'fold')!
    expect(set.counts).toEqual({ Pod: 6 })
    expect(set.state).toBe('bad')
    expect(set.members).toHaveLength(6)
    expect(set.namespace).toBe('a')
    // Six pods mounting one ConfigMap draw one line, not six.
    expect(folded.edges.filter((e) => e.from === set.id && e.to === 'a/ConfigMap/web-cfg')).toHaveLength(1)
    expect(folded.edges.filter((e) => e.from === 'a/Service/web' && e.to === set.id)).toHaveLength(1)
  })

  it('never folds a set the backend already summarised', () => {
    const summary = node('a/Pod/fold', { podSummary: { total: 4000, ready: 3990, unhealthy: 10 }, group: 'a/Deployment/web' })
    const view = toView(graph([summary], []), new Set())
    expect(view.nodes[0].set).toBe('summary')
    expect(view.nodes[0].counts).toEqual({ Pod: 4000 })
    expect(view.nodes[0].apiKind).toBe('')
  })
})

describe('grouping', () => {
  function twoNamespaces(): ViewGraph {
    const one = app('a', 'web', 2)
    const two = app('b', 'api', 2)
    return toView(graph([...one.nodes, ...two.nodes], [...one.edges, ...two.edges]), new Set())
  }

  it('frames every box by namespace when open', () => {
    const grouped = group(twoNamespaces(), 'namespace', new Set())
    expect(grouped.groups.map((g) => g.label)).toEqual(['a', 'b'])
    expect(grouped.parents.get('a/Pod/web-0')).toBe(groupId('ns:a'))
    expect(grouped.parents.get('b/Service/api')).toBe(groupId('ns:b'))
  })

  it('collapses a group into one box with complete counts, worst state and re-pointed, deduplicated lines', () => {
    const one = app('a', 'web', 2)
    const two = app('b', 'api', 1)
    one.nodes.find((n) => n.id === 'a/Pod/web-1')!.state = 'warn'
    // Two lines from a's pods into b's Service: collapsed, they are one.
    const cross = [edge('a/Pod/web-0', 'b/Service/api', 'routes'), edge('a/Pod/web-1', 'b/Service/api', 'routes')]
    const view = toView(graph([...one.nodes, ...two.nodes], [...one.edges, ...two.edges, ...cross]), new Set())

    const grouped = group(view, 'namespace', new Set([groupId('ns:a')]))
    const box = grouped.nodes.find((n) => n.id === groupId('ns:a'))!

    expect(box.set).toBe('group')
    expect(box.counts).toEqual({ Deployment: 1, ReplicaSet: 1, Service: 1, ConfigMap: 1, Pod: 2 })
    expect(box.state).toBe('warn')
    expect(grouped.nodes.some((n) => n.namespace === 'a' && n.id !== box.id)).toBe(false)
    expect(grouped.edges.filter((e) => e.from === box.id && e.to === 'b/Service/api')).toHaveLength(1)
    // Nothing inside the collapsed group has a line to itself.
    expect(grouped.edges.some((e) => e.from === box.id && e.to === box.id)).toBe(false)
    expect(grouped.standIn.get('a/Pod/web-0')).toBe(box.id)
  })

  it('counts a backend summary by its total inside a collapsed group', () => {
    const deploy = node('a/Deployment/web', { kind: 'workload' })
    const summary = node('a/Pod/fold', { podSummary: { total: 3000, ready: 3000, unhealthy: 0 } })
    const view = toView(graph([deploy, summary], [edge(deploy.id, summary.id)]), new Set())
    const grouped = group(view, 'namespace', new Set([groupId('ns:a')]))
    expect(grouped.nodes[0].counts).toEqual({ Deployment: 1, Pod: 3000 })
  })

  it('groups by application, inheriting through ownership and agreeing neighbours', () => {
    const grouped = group(twoNamespaces(), 'app', new Set())
    const web = groupId('app:a/web')
    for (const id of ['a/Deployment/web', 'a/ReplicaSet/web-1', 'a/Pod/web-0', 'a/Service/web', 'a/ConfigMap/web-cfg']) {
      expect(grouped.parents.get(id)).toBe(web)
    }
    expect(grouped.groups.find((g) => g.id === web)!.label).toBe('web · a')
  })

  it('leaves a box whose neighbours disagree outside every group', () => {
    const one = app('a', 'web', 1)
    const two = app('a', 'api', 1)
    const policy = node('a/NetworkPolicy/deny', { kind: 'policy', state: 'neutral' })
    const view = toView(
      graph(
        [...one.nodes, ...two.nodes, policy],
        [...one.edges, ...two.edges, edge(policy.id, 'a/Pod/web-0', 'policy-selects'), edge(policy.id, 'a/Pod/api-0', 'policy-selects')],
      ),
      new Set(),
    )
    const grouped = group(view, 'app', new Set())
    expect(grouped.parents.has(policy.id)).toBe(false)
    expect(grouped.nodes.some((n) => n.id === policy.id)).toBe(true)
  })

  it('groups by a label across namespaces', () => {
    const one = app('a', 'web', 1, { 'app.kubernetes.io/name': 'web', team: 'pay' } as never)
    const two = app('b', 'api', 1, { 'app.kubernetes.io/name': 'api', team: 'pay' } as never)
    const view = toView(graph([...one.nodes, ...two.nodes], [...one.edges, ...two.edges]), new Set())
    const grouped = group(view, { label: 'team' }, new Set())
    expect(grouped.groups.map((g) => g.label)).toEqual(['team=pay'])
    expect(grouped.parents.get('b/Pod/api-0')).toBe(groupId('label:pay'))
  })

  it('draws no frames when grouping is off', () => {
    const view = twoNamespaces()
    const grouped = group(view, 'none', new Set())
    expect(grouped.groups).toEqual([])
    expect(grouped.nodes).toHaveLength(view.nodes.length)
  })
})
