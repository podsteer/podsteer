/**
 * The topology page, mounted against the fixture backend.
 *
 * What this pins is the class the component suite exists for — things that
 * compile and then throw or draw nothing — plus the three promises the page
 * makes out loud: the kind counts are complete, a change is SAID rather than
 * redrawn, and a box opens its object by the Kubernetes Kind, verbatim.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { cleanup, fireEvent, render, screen } from '@testing-library/svelte'
import TopologyView from './TopologyView.svelte'
import { fixtureBackend, useTopologyBackend, type FixtureBackend } from '$lib/topology/api'
import type { TopologyGraph, TrafficEdge, TrafficEndpoint, TrafficLayer } from '$lib/topology/contract'

const words = () => (document.body.textContent ?? '').replace(/\s+/g, ' ')

const GRAPH: TopologyGraph = {
  nodes: [
    { id: 'shop/Deployment/web', kind: 'workload', apiKind: 'Deployment', name: 'web', namespace: 'shop', state: 'warn', detail: '', group: '', labels: { 'app.kubernetes.io/name': 'web' } },
    { id: 'shop/Pod/web-1', kind: 'pod', apiKind: 'Pod', name: 'web-1', namespace: 'shop', state: 'bad', detail: 'CrashLoopBackOff', group: 'shop/Deployment/web' },
    { id: 'shop/Service/web', kind: 'service', apiKind: 'Service', name: 'web', namespace: 'shop', state: 'neutral', detail: '', group: '', labels: { 'app.kubernetes.io/name': 'web' } },
  ],
  edges: [
    { from: 'shop/Deployment/web', to: 'shop/Pod/web-1', kind: 'owns', label: '' },
    { from: 'shop/Service/web', to: 'shop/Pod/web-1', kind: 'selects', label: '' },
  ],
  // Complete counts: more pods exist than were sent.
  counts: { Deployment: 1, Pod: 12, Service: 1 },
  unreadable: ['ingresses'],
  bounded: 'Config, Secrets and claims are named from templates, not read',
  summarised: false,
  generatedAt: '',
}

let backend: FixtureBackend

function session(overrides: Record<string, unknown> = {}) {
  return {
    cluster: { id: 'dev' },
    namespace: 'shop',
    namespaces: [{ name: 'shop' }, { name: 'other' }],
    topologyScope: null,
    topologyScopeNow: { namespaces: ['shop'], all: false },
    kinds: [
      { id: 'apps/v1/deployments', kind: 'Deployment', namespaced: true },
      { id: 'core/v1/pods', kind: 'Pod', namespaced: true },
    ],
    activeIssues: [
      { id: 'crash', severity: 'critical', title: 'Pods crash-looping', subjects: [{ kind: 'Pod', namespace: 'shop', name: 'web-1' }] },
    ],
    openObject: vi.fn(async () => {}),
    openDetailOver: vi.fn(async () => {}),
    openFinding: vi.fn(async () => {}),
    refreshNamespaces: async () => {},
    ...overrides,
  } as never
}

beforeEach(() => {
  // No worker in this DOM: the layout runs inline, under the same rules.
  vi.stubGlobal('Worker', undefined)
  backend = fixtureBackend(GRAPH)
  useTopologyBackend(backend)
})

afterEach(() => {
  cleanup()
  useTopologyBackend(null)
  vi.unstubAllGlobals()
})

async function drawn() {
  await vi.waitFor(() => expect(document.querySelector('[data-node]')).toBeTruthy())
}

describe('TopologyView', () => {
  it('reads the scope once and draws its boxes as SVG', async () => {
    render(TopologyView, { session: session() })
    await drawn()

    expect(backend.calls).toEqual([{ clusterId: 'dev', namespaces: ['shop'], all: false }])
    const box = document.querySelector('[data-node]')!
    expect(box.namespaceURI).toBe('http://www.w3.org/2000/svg')
    expect(document.querySelector('rect')!.namespaceURI).toBe('http://www.w3.org/2000/svg')
  })

  it('shows complete counts on the kind toggles and says what it could not read', async () => {
    render(TopologyView, { session: session() })
    await drawn()

    const pods = screen.getByRole('button', { name: /^Pod\s*12$/ })
    expect(pods.getAttribute('aria-pressed')).toBe('true')
    expect(words()).toContain('Unreadable: could not read ingresses')
    expect(words()).toContain('Bounded: Config, Secrets and claims are named from templates, not read')
  })

  it('offers no toggle for a kind with nothing of it in the scope', async () => {
    useTopologyBackend(fixtureBackend({ ...GRAPH, counts: { ...GRAPH.counts, Ingress: 0 } }))
    render(TopologyView, { session: session() })
    await drawn()
    expect(screen.queryByRole('button', { name: /^Ingress\s*0$/ })).toBeNull()
    expect(screen.getByRole('button', { name: /^Service\s*1$/ })).toBeTruthy()
  })

  it('says Changed instead of redrawing, and reads again only when asked', async () => {
    render(TopologyView, { session: session() })
    await drawn()

    backend.emit({ clusterId: 'dev', namespaces: ['shop'] })
    const badge = await screen.findByRole('button', { name: /Changed — Refresh/ })
    expect(backend.calls).toHaveLength(1)

    // A change elsewhere says nothing.
    backend.emit({ clusterId: 'dev', namespaces: ['other'] })

    await fireEvent.click(badge)
    await vi.waitFor(() => expect(backend.calls).toHaveLength(2))
    await vi.waitFor(() => expect(screen.queryByRole('button', { name: /Changed — Refresh/ })).toBeNull())
  })

  it('opens a box by its Kind, verbatim, and a findings badge on the overview', async () => {
    const s = session()
    render(TopologyView, { session: s })
    await drawn()

    const deployment = screen.getByRole('button', { name: /^Open Deployment web in shop/ })
    await fireEvent.click(deployment)
    // OVER the map: the drawer opens, and the page is not swapped for a list.
    const calls = s as unknown as { openDetailOver: ReturnType<typeof vi.fn>; openObject: ReturnType<typeof vi.fn> }
    expect(calls.openDetailOver).toHaveBeenCalledWith('apps/v1/deployments', 'web', 'shop')
    expect(calls.openObject).not.toHaveBeenCalled()
    expect(document.querySelector('[data-node]')).toBeTruthy()

    const badge = screen.getByRole('button', { name: /Finding: Pods crash-looping/ })
    await fireEvent.click(badge)
    expect((s as unknown as { openFinding: ReturnType<typeof vi.fn> }).openFinding).toHaveBeenCalledWith('crash')
  })

  it('collapses a namespace into one box that keeps the worst state', async () => {
    render(TopologyView, { session: session() })
    await drawn()

    await fireEvent.click(screen.getByRole('button', { name: /^Collapse shop/ }))
    const group = await screen.findByRole('button', { name: /^Group shop, .*Press to expand, failing, 1 finding$/ })
    expect(group).toBeTruthy()
  })
})

describe('TopologyView traffic overlay', () => {
  /** A Deployment with six pods (folded by default) and a Service. */
  function sixPods(): TopologyGraph {
    const nodes: TopologyGraph['nodes'] = [
      { id: 'shop/Deployment/web', kind: 'workload', apiKind: 'Deployment', name: 'web', namespace: 'shop', state: 'ok', detail: '', group: '' },
      { id: 'shop/Deployment/api', kind: 'workload', apiKind: 'Deployment', name: 'api', namespace: 'shop', state: 'ok', detail: '', group: '' },
    ]
    const edges: TopologyGraph['edges'] = []
    for (let i = 0; i < 6; i++) {
      const id = `shop/Pod/web-${i}`
      nodes.push({ id, kind: 'pod', apiKind: 'Pod', name: `web-${i}`, namespace: 'shop', state: 'ok', detail: '', group: 'shop/Deployment/web' })
      edges.push({ from: 'shop/Deployment/web', to: id, kind: 'owns', label: '' })
    }
    return { nodes, edges, counts: { Deployment: 2, Pod: 6 }, unreadable: [], bounded: '', summarised: false, generatedAt: '' }
  }

  const ep = (p: Partial<TrafficEndpoint>): TrafficEndpoint => ({
    namespace: 'shop', workload: '', service: '', external: '', unknown: false, nodeId: '', ...p,
  })
  const edge = (source: TrafficEndpoint, dest: TrafficEndpoint, p: Partial<TrafficEdge> = {}): TrafficEdge => ({
    source, dest, protocol: 'http', requestsPerSec: 10, errorsPerSec: 0, bytesPerSec: 0, connections: 0,
    p50: 5, p95: 9, p99: -1, latencyBeyondBuckets: false, ...p,
  })
  const layer = (edges: TrafficEdge[]): TrafficLayer => ({
    source: 'istio', window: '5m', edges, unmapped: [], status: 'enabled', message: '', provenance: null, expressions: [],
  })

  it('re-points traffic on folded pods to their fold and draws outside hosts as overlay boxes', async () => {
    useTopologyBackend(fixtureBackend(sixPods()))
    const traffic = layer([
      // A pod folded into "6 Pods" calls the api Deployment.
      edge(ep({ workload: 'web', nodeId: 'shop/Pod/web-3' }), ep({ workload: 'api', nodeId: 'shop/Deployment/api' }), {
        latencyBeyondBuckets: true,
      }),
      // And the internet.
      edge(ep({ workload: 'web', nodeId: 'shop/Pod/web-1' }), ep({ namespace: '', external: 'example.com' })),
    ])
    render(TopologyView, { session: session({ activeIssues: [] }), traffic })
    await drawn()

    await vi.waitFor(() => expect(document.querySelectorAll('[data-traffic-edge]')).toHaveLength(2))
    const titles = [...document.querySelectorAll('[data-traffic-edge] title')].map((t) => t.textContent ?? '')
    expect(titles.some((t) => t.includes('p99 > largest bucket'))).toBe(true)
    const outside = document.querySelector('[data-traffic-node]')!
    expect(outside.getAttribute('aria-label')).toBe('Outside the cluster: example.com')
    expect(words()).not.toContain('not drawn')
  })

  it('re-points traffic onto a collapsed group', async () => {
    useTopologyBackend(fixtureBackend(sixPods()))
    const traffic = layer([
      edge(ep({ workload: 'web', nodeId: 'shop/Pod/web-3' }), ep({ workload: 'api', nodeId: 'shop/Deployment/api' })),
    ])
    render(TopologyView, { session: session({ activeIssues: [] }), traffic })
    await drawn()
    await fireEvent.click(screen.getByRole('button', { name: /^Collapse shop/ }))
    await screen.findByRole('button', { name: /^Group shop/ })
    // Both ends are inside the one group now: a loop on its box, still drawn.
    await vi.waitFor(() => expect(document.querySelectorAll('[data-traffic-edge]')).toHaveLength(1))
  })
})
