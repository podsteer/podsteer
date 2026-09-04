import { describe, expect, it } from 'vitest'
import { fold, FOLD_THRESHOLD, type FoldableGraph, type FoldNode, type FoldEdge } from './graphFold'

function node(id: string, over: Partial<FoldNode> = {}): FoldNode {
  return {
    id,
    kind: 'pod',
    apiKind: 'Pod',
    name: id,
    namespace: 'default',
    tier: 3,
    detail: '',
    healthy: true,
    subject: false,
    missing: false,
    group: '',
    ...over,
  }
}

/** A workload with `count` replicas, each reading the same ConfigMap. */
function workload(count: number): FoldableGraph {
  const nodes = [
    node('deployment/api', { kind: 'workload', apiKind: 'Deployment', group: '', subject: true }),
    node('config/settings', { kind: 'config', apiKind: 'ConfigMap' }),
  ]
  const edges: FoldEdge[] = []

  for (let i = 0; i < count; i++) {
    const pod = `pod/api-${i}`
    nodes.push(node(pod, { group: 'deployment/api' }))
    edges.push({ from: 'deployment/api', to: pod, label: 'manages' })
    edges.push({ from: pod, to: 'config/settings', label: 'environment' })

    nodes.push(node(`container/api-${i}/app`, { kind: 'container', apiKind: '', group: pod }))
    edges.push({ from: pod, to: `container/api-${i}/app`, label: '' })
  }

  return { nodes, edges }
}

describe('folding a busy map', () => {
  it('leaves a small set alone', () => {
    // Three replicas are worth seeing.
    const graph = workload(3)
    const folded = fold(graph, new Set())

    expect(folded.nodes).toHaveLength(graph.nodes.length)
    expect(folded.groups).toHaveLength(0)
  })

  it('folds a large set into one box that says how many', () => {
    const folded = fold(workload(30), new Set())
    const group = folded.nodes.find((n) => n.fold)

    expect(group?.name).toBe('30 Pods')
    expect(folded.nodes.some((n) => n.id === 'pod/api-0')).toBe(false)
  })

  it('collapses thirty identical edges into one', () => {
    // THE WHOLE POINT. Thirty pods reading one ConfigMap is thirty true edges
    // and one thing worth drawing.
    const folded = fold(workload(30), new Set())

    const toConfig = folded.edges.filter((e) => e.to === 'config/settings')
    expect(toConfig).toHaveLength(1)
    expect(toConfig[0].from).toBe('fold/deployment/api/pod')
  })

  it('takes the containers with it', () => {
    // They belong to something no longer drawn; left behind they would hang
    // under nothing.
    const folded = fold(workload(30), new Set())

    expect(folded.nodes.some((n) => n.kind === 'container')).toBe(false)
  })

  it('never hides a problem', () => {
    // A set with one failing pod in thirty must be drawn as a set with a
    // problem, or folding becomes a way to lose the thing somebody opened the
    // map to find.
    const graph = workload(30)
    graph.nodes = graph.nodes.map((n) =>
      n.id === 'pod/api-7' ? { ...n, healthy: false } : n,
    )

    const group = fold(graph, new Set()).nodes.find((n) => n.fold)

    expect(group?.healthy).toBe(false)
    expect(group?.detail).toBe('1 not ready')
  })

  it('puts the members back when expanded', () => {
    const folded = fold(workload(30), new Set(['fold/deployment/api/pod']))

    expect(folded.nodes.some((n) => n.id === 'pod/api-0')).toBe(true)
    expect(folded.nodes.some((n) => n.fold)).toBe(false)
    expect(folded.edges.filter((e) => e.to === 'config/settings')).toHaveLength(30)
  })

  it('still reports a set it is not folding, so it can be folded again', () => {
    const folded = fold(workload(30), new Set(['fold/deployment/api/pod']))

    expect(folded.groups.map((g) => g.id)).toContain('fold/deployment/api/pod')
  })

  it('folds at the threshold and not below it', () => {
    expect(fold(workload(FOLD_THRESHOLD - 1), new Set()).groups).toHaveLength(0)
    expect(fold(workload(FOLD_THRESHOLD), new Set()).groups).toHaveLength(1)
  })

  it('drops an edge that runs inside a folded set', () => {
    // Pod-to-its-own-container, once both are folded away, joins nothing.
    const folded = fold(workload(30), new Set())

    expect(folded.edges.some((e) => e.from === e.to)).toBe(false)
    expect(folded.edges.every((e) => folded.nodes.some((n) => n.id === e.from))).toBe(true)
    expect(folded.edges.every((e) => folded.nodes.some((n) => n.id === e.to))).toBe(true)
  })

  it('leaves a graph with no sets exactly as it was', () => {
    const plain: FoldableGraph = {
      nodes: [node('pod/one', { subject: true }), node('config/c', { kind: 'config' })],
      edges: [{ from: 'pod/one', to: 'config/c', label: 'environment' }],
    }

    const folded = fold(plain, new Set())
    expect(folded.nodes).toHaveLength(2)
    expect(folded.edges).toHaveLength(1)
  })
})

/**
 * A neighbourhood map's own sibling set: the references one object names, all
 * grouped under it. They fold exactly as a workload's replicas do, and a
 * reference to something that is not there must not be hidden by the fold any
 * more than an unwell pod is.
 */
function neighbourhood(count: number, missing: number): FoldableGraph {
  const nodes = [
    node('service/shop/web', { kind: 'service', apiKind: 'Service', subject: true }),
  ]
  const edges: FoldEdge[] = []

  for (let i = 0; i < count; i++) {
    const id = `secret/shop/token-${i}`
    nodes.push(
      node(id, {
        kind: 'secret',
        apiKind: 'Secret',
        group: 'service/shop/web',
        missing: i < missing,
        healthy: i >= missing,
        detail: i < missing ? 'not found' : '',
      }),
    )
    edges.push({ from: 'service/shop/web', to: id, label: 'token' })
  }

  return { nodes, edges }
}

describe('folding a set that names something absent', () => {
  it('counts the missing members separately from the unwell ones', () => {
    // COUNTED SEPARATELY even though a missing node is drawn unwell: an object
    // that exists and is failing and one that was named and is absent call for
    // opposite next steps.
    const folded = fold(neighbourhood(FOLD_THRESHOLD + 2, 2), new Set())

    expect(folded.groups).toHaveLength(1)
    expect(folded.groups[0].missing).toBe(2)
    expect(folded.groups[0].unhealthy).toBe(2)
    expect(folded.groups[0].count).toBe(FOLD_THRESHOLD + 2)
  })

  it('says how many are missing rather than how many are unwell', () => {
    const folded = fold(neighbourhood(FOLD_THRESHOLD + 2, 2), new Set())
    const box = folded.nodes.find((n) => n.fold)

    expect(box?.detail).toBe('2 not found')
    // Folding must never hide the thing somebody opened the map to find.
    expect(box?.healthy).toBe(false)
  })

  it('falls back to the readiness count when nothing is missing', () => {
    const graph = neighbourhood(FOLD_THRESHOLD + 1, 0)
    graph.nodes[1] = { ...graph.nodes[1], healthy: false }

    const box = fold(graph, new Set()).nodes.find((n) => n.fold)
    expect(box?.detail).toBe('1 not ready')
  })

  it('never marks the group box itself as missing', () => {
    // A GROUP IS NOT ITSELF A MISSING OBJECT: it stands for its members, and
    // the count says how many of them are. Drawing the box as missing would
    // claim the set does not exist.
    const box = fold(neighbourhood(FOLD_THRESHOLD + 3, 3), new Set()).nodes.find((n) => n.fold)
    expect(box?.missing).toBe(false)
  })

  it('puts a missing member back, still marked, when the set is expanded', () => {
    const key = 'fold/service/shop/web/secret'
    const folded = fold(neighbourhood(FOLD_THRESHOLD + 1, 1), new Set([key]))

    const member = folded.nodes.find((n) => n.id === 'secret/shop/token-0')
    expect(member?.missing).toBe(true)
    expect(folded.nodes.some((n) => n.fold)).toBe(false)
  })
})
