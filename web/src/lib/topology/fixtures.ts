/**
 * A synthetic topology, shaped like a real namespace and deterministic.
 *
 * For the tests, the layout benchmark and the dev-only fixture backend in
 * api.ts — never for anything an operator sees as a cluster. Every edge it
 * draws is one the backend would draw for the same objects: an Ingress routes
 * to a Service, the Service selects pods, a Deployment owns a ReplicaSet that
 * owns them, the HPA scales the Deployment, the PDB protects the pods, the pods
 * mount a ConfigMap and a Secret by name, and a namespace-wide NetworkPolicy
 * selects every pod — the hub that a real default-deny policy is.
 */
import type { NodeState, TopologyEdge, TopologyGraph, TopologyNode } from './contract'

export interface FixtureOptions {
  /** Stop adding applications once this many boxes exist. */
  nodes: number
  /** Trim lines past this many, dropping from the end. */
  edges?: number
  namespaces?: number
  /** Pods per application cycle through 2..(podsMax). */
  podsMax?: number
  /** Mark some pods unwell, so state aggregation has something to find. */
  unwellEvery?: number
}

function stateFor(index: number, every: number): NodeState {
  if (every <= 0) return 'ok'
  if (index % every === 0) return 'bad'
  if (index % every === 1) return 'warn'
  return 'ok'
}

export function fixtureTopology(options: FixtureOptions): TopologyGraph {
  const namespaces = options.namespaces ?? 10
  const podsMax = Math.max(2, options.podsMax ?? 5)
  const every = options.unwellEvery ?? 0

  const nodes: TopologyNode[] = []
  const edges: TopologyEdge[] = []
  const counts: Record<string, number> = {}

  const add = (node: Omit<TopologyNode, 'detail' | 'group'> & Partial<TopologyNode>) => {
    nodes.push({ detail: '', group: '', ...node })
    counts[node.apiKind] = (counts[node.apiKind] ?? 0) + 1
    return node.id
  }
  const link = (from: string, to: string, kind: TopologyEdge['kind'], label = '') =>
    edges.push({ from, to, kind, label })

  const policies = new Map<string, string>()
  for (let n = 0; n < namespaces; n++) {
    const ns = `team-${n}`
    policies.set(
      ns,
      add({ id: `${ns}/NetworkPolicy/default-deny`, kind: 'policy', apiKind: 'NetworkPolicy', name: 'default-deny', namespace: ns, state: 'neutral' }),
    )
  }

  let app = 0
  let podIndex = 0
  while (nodes.length < options.nodes) {
    const ns = `team-${app % namespaces}`
    const name = `app-${app}`
    const labels = { 'app.kubernetes.io/name': name, 'app.kubernetes.io/part-of': `suite-${app % 7}` }
    const id = (kind: string, objectName = name) => `${ns}/${kind}/${objectName}`

    const deploy = add({ id: id('Deployment'), kind: 'workload', apiKind: 'Deployment', name, namespace: ns, state: 'ok', labels })
    const rs = add({ id: id('ReplicaSet', `${name}-7d4b9`), kind: 'replicaset', apiKind: 'ReplicaSet', name: `${name}-7d4b9`, namespace: ns, state: 'ok', group: deploy })
    const svc = add({ id: id('Service'), kind: 'service', apiKind: 'Service', name, namespace: ns, state: 'neutral', labels })
    const ing = add({ id: id('Ingress'), kind: 'ingress', apiKind: 'Ingress', name, namespace: ns, state: 'neutral', labels })
    const cm = add({ id: id('ConfigMap', `${name}-config`), kind: 'config', apiKind: 'ConfigMap', name: `${name}-config`, namespace: ns, state: 'neutral' })
    const secret = add({ id: id('Secret', `${name}-tls`), kind: 'secret', apiKind: 'Secret', name: `${name}-tls`, namespace: ns, state: 'neutral' })
    const hpa = add({ id: id('HorizontalPodAutoscaler'), kind: 'scaler', apiKind: 'HorizontalPodAutoscaler', name, namespace: ns, state: 'ok', labels })
    const pdb = add({ id: id('PodDisruptionBudget'), kind: 'budget', apiKind: 'PodDisruptionBudget', name, namespace: ns, state: 'ok', labels })

    link(ing, svc, 'routes')
    link(deploy, rs, 'owns')
    link(hpa, deploy, 'scales')

    const pods = 2 + (app % (podsMax - 1))
    for (let p = 0; p < pods; p++) {
      const podName = `${name}-7d4b9-${p}`
      const pod = add({ id: id('Pod', podName), kind: 'pod', apiKind: 'Pod', name: podName, namespace: ns, state: stateFor(podIndex++, every), group: deploy })
      link(rs, pod, 'owns')
      link(svc, pod, 'selects')
      link(pdb, pod, 'protects')
      link(pod, cm, 'attaches')
      link(pod, secret, 'attaches')
      link(policies.get(ns)!, pod, 'policy-selects', 'selects')
    }
    app++
  }

  return {
    nodes,
    edges: options.edges === undefined ? edges : edges.slice(0, options.edges),
    counts,
    unreadable: [],
    bounded: 'Config, Secrets and claims are named from templates, not read',
    summarised: false,
    generatedAt: new Date(0).toISOString(),
  }
}
