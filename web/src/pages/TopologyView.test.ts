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
import HelpPanel from '$lib/components/HelpPanel.svelte'
import { help } from '$stores/help.svelte'
import { HELP_TOPICS } from '$lib/help'
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
    selectedNamespaces: ['shop'],
    namespaces: [{ name: 'shop' }, { name: 'other' }],
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
    // Said in Help, not under the map — and the (?) carries a mark, since an
    // unreadable kind must not go unseen.
    expect(words()).not.toContain('Unreadable:')
    const said = (help.provided.topology ?? []).flatMap((section) => section.body).join(' ')
    expect(said).toContain('Unreadable: could not read ingresses')
    expect(said).toContain('Bounded: Config, Secrets and claims are named from templates, not read')
    expect(screen.getByRole('button', { name: /^Help with the topology — could not read ingresses/ })).toBeTruthy()
    expect(document.querySelector('[data-help-notice]')).toBeTruthy()
  })

  it('offers no toggle for a kind with nothing of it in the scope', async () => {
    useTopologyBackend(fixtureBackend({ ...GRAPH, counts: { ...GRAPH.counts, Ingress: 0 } }))
    render(TopologyView, { session: session() })
    await drawn()
    expect(screen.queryByRole('button', { name: /^Ingress\s*0$/ })).toBeNull()
    expect(screen.getByRole('button', { name: /^Service\s*1$/ })).toBeTruthy()
  })

  it('draws a bridged line with its "via Pod" label and says it in the title', async () => {
    render(TopologyView, { session: session() })
    await drawn()
    // Hide Pods: the Service's selection is re-pointed to the Deployment.
    await fireEvent.click(screen.getByRole('button', { name: /^Pod\s*12$/ }))
    await vi.waitFor(() =>
      expect([...document.querySelectorAll('[data-edge-label]')].map((t) => t.textContent?.trim())).toContain('via Pod'),
    )
    const titles = [...document.querySelectorAll('[data-edge] title')].map((t) => t.textContent ?? '')
    expect(titles).toContain('Service web selects Deployment web (via Pod)')
  })

  it('says Changed instead of redrawing, and reads again only when asked', async () => {
    render(TopologyView, { session: session() })
    await drawn()

    backend.emit({ clusterId: 'dev', namespaces: ['shop'] })
    const badge = await screen.findByRole('button', { name: /Changed · Refresh/ })
    expect(backend.calls).toHaveLength(1)

    // A change elsewhere says nothing.
    backend.emit({ clusterId: 'dev', namespaces: ['other'] })

    await fireEvent.click(badge)
    await vi.waitFor(() => expect(backend.calls).toHaveLength(2))
    await vi.waitFor(() => expect(screen.queryByRole('button', { name: /Changed · Refresh/ })).toBeNull())
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

describe('TopologyView, as the toolbar now has it', () => {
  /** Two applications in one namespace, one with an extra Service. */
  function twoApps(): TopologyGraph {
    const app = (name: string) => ({ 'app.kubernetes.io/name': name })
    return {
      nodes: [
        { id: 'shop/Deployment/web', kind: 'workload', apiKind: 'Deployment', name: 'web', namespace: 'shop', state: 'ok', detail: '', group: '', labels: app('web') },
        { id: 'shop/Service/web', kind: 'service', apiKind: 'Service', name: 'web', namespace: 'shop', state: 'neutral', detail: '', group: '', labels: app('web') },
        { id: 'shop/Deployment/api', kind: 'workload', apiKind: 'Deployment', name: 'api', namespace: 'shop', state: 'ok', detail: '', group: '', labels: app('api') },
      ],
      edges: [],
      counts: { Deployment: 2, Service: 1 },
      unreadable: [],
      bounded: '',
      summarised: false,
      generatedAt: '',
    }
  }

  async function groupByApplication() {
    const trigger = document.querySelector('[data-select-trigger]') as HTMLElement
    await fireEvent.click(trigger)
    await fireEvent.click(screen.getByRole('option', { name: /By application/ }))
  }

  it('hides unticked applications and keeps the kind counts complete', async () => {
    useTopologyBackend(fixtureBackend(twoApps()))
    const s = session()
    render(TopologyView, { session: s })
    await drawn()
    await groupByApplication()

    await fireEvent.click(screen.getByRole('button', { name: /All applications/ }))
    const menu = screen.getByRole('dialog', { name: 'Applications drawn' })
    const web = [...menu.querySelectorAll('label')].find((l) => l.textContent?.includes('web'))!
    await fireEvent.click(web.querySelector('input')!)

    await vi.waitFor(() => expect(screen.queryByRole('button', { name: /^Open Deployment web/ })).toBeNull())
    expect(screen.getByRole('button', { name: /^Open Deployment api/ })).toBeTruthy()
    // Counts are the backend's, whatever is hidden.
    expect(screen.getByRole('button', { name: /^Deployment\s*2$/ })).toBeTruthy()
    expect(words()).toContain('1 application hidden (2 objects)')
    expect((s as unknown as { topologyHiddenApps: Set<string> }).topologyHiddenApps.size).toBe(1)
  })

  it('searches in the toolbar field every pane uses, with no suggestion list', async () => {
    render(TopologyView, { session: session() })
    await drawn()
    const field = screen.getByRole('textbox', { name: 'Find on the topology' }) as HTMLInputElement
    expect(field.type).toBe('text')
    expect(field.getAttribute('autocomplete')).toBe('off')
    expect(field.getAttribute('list')).toBeNull()
    await fireEvent.input(field, { target: { value: 'kind:Service' } })
    expect(document.querySelector('[role="listbox"], datalist')).toBeNull()
    expect(words()).toContain('1/1')
  })

  it('turns observed traffic on from a toolbar toggle, its controls in a popover', async () => {
    render(TopologyView, { session: session() })
    await drawn()
    const toggle = screen.getByRole('button', { name: 'Observed traffic' })
    expect(toggle.getAttribute('aria-pressed')).toBe('false')
    expect(document.querySelector('[data-traffic-popover]')).toBeNull()

    await fireEvent.click(toggle)
    expect(toggle.getAttribute('aria-pressed')).toBe('true')
    const popover = document.querySelector('[data-traffic-popover]') as HTMLElement
    expect(popover.hidden).toBe(false)

    // Closing the popover keeps the layer on.
    await fireEvent.click(screen.getByRole('button', { name: 'Traffic options' }))
    expect(popover.hidden).toBe(true)
    expect(toggle.getAttribute('aria-pressed')).toBe('true')

    await fireEvent.click(toggle)
    expect(toggle.getAttribute('aria-pressed')).toBe('false')
    expect(document.querySelector('[data-traffic-popover]')).toBeNull()
  })

  it('registers its help: the standing topic, and this drawing in the panel', async () => {
    expect(HELP_TOPICS.topology.sections.map((s) => s.heading)).toEqual(
      expect.arrayContaining(['Bounded', 'Unreadable', 'Summarised', 'Observed traffic']),
    )
    render(TopologyView, { session: session() })
    render(HelpPanel)
    await drawn()
    await fireEvent.click(screen.getByRole('button', { name: /^Help with the topology/ }))
    const live = document.querySelector('[data-help-live]')!
    expect(live.textContent).toContain('This drawing')
    expect(live.textContent).toContain('Unreadable: could not read ingresses')
    help.close()
  })

  it('animates lines, except for anybody who asked for less motion', async () => {
    vi.stubGlobal('matchMedia', () => ({ matches: false, addEventListener() {}, removeEventListener() {} }))
    const first = render(TopologyView, { session: session() })
    await drawn()
    await vi.waitFor(() => expect(document.querySelector('[data-edge].flow')).toBeTruthy())
    first.unmount()

    vi.stubGlobal('matchMedia', () => ({ matches: true, addEventListener() {}, removeEventListener() {} }))
    render(TopologyView, { session: session() })
    await drawn()
    expect(document.querySelector('[data-edge]')).toBeTruthy()
    expect(document.querySelector('[data-edge].flow')).toBeNull()
  })
})

describe('TopologyView popovers and Help, reviewed', () => {
  it('forgets the traffic explanation once the layer is turned off', async () => {
    render(TopologyView, { session: session() })
    await drawn()
    const toggle = screen.getByRole('button', { name: 'Observed traffic' })
    await fireEvent.click(toggle)
    // No traffic backend in a unit test: the layer reports a problem.
    await vi.waitFor(() =>
      expect((help.provided.topology ?? []).map((s) => s.heading)).toContain('Observed traffic now'),
    )
    await fireEvent.click(toggle)
    await vi.waitFor(() =>
      expect((help.provided.topology ?? []).map((s) => s.heading)).not.toContain('Observed traffic now'),
    )
  })

  it('counts hidden applications from the drawing, not from remembered ids', async () => {
    const s = session({ topologyHiddenApps: new Set(['group/app:elsewhere/gone']) })
    render(TopologyView, { session: s })
    await drawn()
    const trigger = document.querySelector('[data-select-trigger]') as HTMLElement
    await fireEvent.click(trigger)
    await fireEvent.click(screen.getByRole('option', { name: /By application/ }))
    const apps = screen.getByRole('button', { name: /All applications/ })
    await fireEvent.click(apps)
    const all = screen.getByRole('dialog', { name: 'Applications drawn' }).querySelector('input') as HTMLInputElement
    expect(all.checked).toBe(true)
  })

  it('opens the traffic options as a dialog: focus in, Escape out, focus back', async () => {
    render(TopologyView, { session: session() })
    await drawn()
    await fireEvent.click(screen.getByRole('button', { name: 'Observed traffic' }))
    const options = screen.getByRole('button', { name: 'Traffic options' })
    expect(options.getAttribute('aria-haspopup')).toBe('dialog')
    expect(options.getAttribute('aria-expanded')).toBe('true')
    const popover = document.querySelector('[data-traffic-popover]') as HTMLElement
    await vi.waitFor(() => expect(popover.contains(document.activeElement)).toBe(true))

    await fireEvent.keyDown(popover, { key: 'Escape' })
    expect(popover.hidden).toBe(true)
    expect(options.getAttribute('aria-expanded')).toBe('false')
    expect(document.activeElement).toBe(options)

    // A press outside closes it too.
    await fireEvent.click(options)
    expect(popover.hidden).toBe(false)
    await fireEvent.pointerDown(document.body)
    expect(popover.hidden).toBe(true)
  })
})

describe('TopologyView toolbar polish', () => {
  it('keeps the kind badges on one row that a wheel scrolls sideways, with a fade', async () => {
    render(TopologyView, { session: session() })
    await drawn()
    const row = screen.getByRole('group', { name: 'Kinds drawn' }) as HTMLElement
    expect(row.className).toContain('overflow-x-auto')
    expect(row.className).not.toContain('flex-wrap')

    // Wider content than the row: the right edge fades, and a vertical wheel
    // moves it sideways.
    Object.defineProperty(row, 'scrollWidth', { configurable: true, value: 900 })
    Object.defineProperty(row, 'clientWidth', { configurable: true, value: 300 })
    await fireEvent.scroll(row)
    expect(row.dataset.fade).toBe('right')

    await fireEvent.wheel(row, { deltaY: 120, deltaX: 0 })
    expect(row.scrollLeft).toBe(120)
    expect(row.dataset.fade).toBe('both')
  })

  it('keeps Changed on one line and the search placeholder short, its syntax in the title', async () => {
    render(TopologyView, { session: session() })
    await drawn()
    const field = screen.getByRole('textbox', { name: 'Find on the topology' }) as HTMLInputElement
    expect(field.placeholder).toBe('Find… kind: ns:')
    expect(field.title).toContain('kind:Service')
    backend.emit({ clusterId: 'dev', namespaces: ['shop'] })
    const badge = await screen.findByRole('button', { name: /Changed · Refresh/ })
    expect(badge.className).toContain('whitespace-nowrap')
  })
})

describe('TopologyView kind row buttons and search width', () => {
  it('shows a chevron only where there is more, and scrolls by most of the row', async () => {
    render(TopologyView, { session: session() })
    await drawn()
    const row = screen.getByRole('group', { name: 'Kinds drawn' }) as HTMLElement
    expect(screen.queryByRole('button', { name: 'Scroll kinds left' })).toBeNull()
    expect(screen.queryByRole('button', { name: 'Scroll kinds right' })).toBeNull()

    Object.defineProperty(row, 'scrollWidth', { configurable: true, value: 1000 })
    Object.defineProperty(row, 'clientWidth', { configurable: true, value: 300 })
    // Instant, so the test reads the position at once.
    row.scrollTo = ((options: ScrollToOptions) => {
      row.scrollLeft = options.left ?? 0
    }) as typeof row.scrollTo
    await fireEvent.scroll(row)

    expect(screen.queryByRole('button', { name: 'Scroll kinds left' })).toBeNull()
    await fireEvent.click(screen.getByRole('button', { name: 'Scroll kinds right' }))
    expect(row.scrollLeft).toBe(240)
    await fireEvent.scroll(row)
    expect(screen.getByRole('button', { name: 'Scroll kinds left' })).toBeTruthy()

    await fireEvent.click(screen.getByRole('button', { name: 'Scroll kinds left' }))
    expect(row.scrollLeft).toBe(0)
  })

  it('lets the search field fill the header and the controls keep their size', async () => {
    render(TopologyView, { session: session() })
    await drawn()
    const field = screen.getByRole('textbox', { name: 'Find on the topology' })
    // The same SearchField the list pages' header uses, ⌘K hint and all.
    const box = field.closest('label') as HTMLElement
    expect(box.className).toContain('flex-1')
    expect(box.className).toContain('min-w-48')
    const live = screen.getByRole('switch')
    expect(live.className).toContain('shrink-0')
  })
})

describe('TopologyView in the workspace header', () => {
  it('hands its count and controls to the header, and draws no row of its own', async () => {
    const header = vi.fn()
    render(TopologyView, { session: session(), header })
    await vi.waitFor(() => expect(header).toHaveBeenCalled())
    const content = header.mock.calls.at(-1)?.[0]
    expect(typeof content.count).toBe('function')
    expect(typeof content.controls).toBe('function')
    expect(typeof content.focusSearch).toBe('function')
    expect(document.querySelector('[data-topology-controls]')).toBeNull()
  })

  it('steps through matches on Enter in the shared search field', async () => {
    render(TopologyView, { session: session() })
    await drawn()
    const field = screen.getByRole('textbox', { name: 'Find on the topology' }) as HTMLInputElement
    await fireEvent.input(field, { target: { value: 'web' } })
    await fireEvent.keyDown(field, { key: 'Enter' })
    expect(words()).toMatch(/1\/\d/)
    // Enter finds; it does not leave the field.
    expect(document.activeElement === field || document.activeElement === document.body).toBe(true)
  })
})

describe('the applications menu, like every other dropdown', () => {
  it('turns its chevron when open, and puts focus in the app search field', async () => {
    useTopologyBackend(fixtureBackend({
      ...GRAPH,
      nodes: GRAPH.nodes.map((n) => (n.labels ? n : { ...n })),
    }))
    render(TopologyView, { session: session() })
    await drawn()
    const trigger = document.querySelector('[data-select-trigger]') as HTMLElement
    await fireEvent.click(trigger)
    await fireEvent.click(screen.getByRole('option', { name: /By application/ }))

    const apps = screen.getByRole('button', { name: /All applications/ })
    const chevron = apps.querySelector('svg:last-of-type') as SVGElement
    expect(apps.getAttribute('aria-expanded')).toBe('false')
    expect(chevron.getAttribute('class')).not.toContain('rotate-180')

    await fireEvent.click(apps)
    expect(apps.getAttribute('aria-expanded')).toBe('true')
    expect(chevron.getAttribute('class')).toContain('rotate-180')

    const field = screen.getByRole('textbox', { name: 'Filter applications' })
    await vi.waitFor(() => expect(document.activeElement).toBe(field))
    // The search field's look, without the ⌘K hint a menu cannot honour.
    expect(field.className).toContain('field')
    expect(field.closest('label')?.textContent).not.toMatch(/K/)
  })
})

