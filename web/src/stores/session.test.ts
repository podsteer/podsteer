import { beforeEach, describe, expect, it, vi } from 'vitest'

// The bindings do not exist outside the Wails runtime, and this suite is
// about what the session does with a list it already holds — so the manifest
// read is stubbed to fail. openDetail must still resolve the row object: a
// panel whose manifest is slow or refused still shows its live sections.
//
// listTable is a plain vi.fn() rather than a fixed stub, because the
// autoscaler tests below need to control what each call to it resolves or
// rejects with, per test.
const listTable = vi.fn()
const refreshCredentials = vi.fn()
const queryPods = vi.fn()
const listPodKeys = vi.fn()
const podUsageHistory = vi.fn().mockResolvedValue([])
vi.mock('$lib/api/client', async () => {
  const actual = await vi.importActual<Record<string, unknown>>('$lib/api/client')
  return {
    ...actual,
    getManifest: vi.fn().mockRejectedValue(new Error('no cluster in a test')),
    listTable: (...args: unknown[]) => listTable(...args),
    listTableIn: (...args: unknown[]) => listTable(...args),
    refreshCredentials: (...args: unknown[]) => refreshCredentials(...args),
    queryPods: (...args: unknown[]) => queryPods(...args),
    queryPodsIn: (...args: unknown[]) => queryPods(...args),
    listPodKeysIn: (...args: unknown[]) => listPodKeys(...args),
    podUsageHistory: (...args: unknown[]) => podUsageHistory(...args),
  }
})

import { ClusterSession, RICH_KIND_IDS } from './session.svelte'
import { ApiError } from '$lib/api/errors'
import { preferences } from './preferences.svelte'
import type { Cluster, Node, Pod, PodPage, ResourceKind, ResourceTable } from '$lib/api/client'

// Only the three fields the constructor reads. Cast through unknown because
// the DTO is a generated class with a dozen more, none of which this touches.
const cluster = { id: 'dev', name: 'dev', defaultNamespace: 'default' } as unknown as Cluster

function session(): ClusterSession {
  return new ClusterSession(cluster)
}

describe('a revealed Secret', () => {
  it('can be put back, without closing the panel', async () => {
    // THE GAP THIS FILLS. Revealing swapped the control for nothing, so the
    // only way to re-mask a Secret was to close the panel and open it again
    // — which is not a policy, it is a missing button, and the reveal on an
    // environment variable does not share it.
    const open = session()
    open.selectedKindId = 'core/v1/secrets'
    open.selectedName = 'db-secrets'

    await open.revealManifestSecrets()
    expect(open.secretsRevealed).toBe(true)

    await open.hideManifestSecrets()
    expect(open.secretsRevealed).toBe(false)
  })

  it('starts hidden on every object', async () => {
    // A reveal is a decision about ONE object. Carrying it to the next is how
    // a client ends up showing a value somebody unmasked in private on the
    // object they open in a meeting.
    const open = session()
    open.selectedKindId = 'core/v1/secrets'

    await open.openDetail('db-secrets', 'web')
    await open.revealManifestSecrets()
    expect(open.secretsRevealed).toBe(true)

    await open.openDetail('other-secrets', 'web')
    expect(open.secretsRevealed).toBe(false)
  })
})

describe('opening an object that was not clicked', () => {
  let open: ClusterSession

  beforeEach(() => {
    open = session()
  })

  it('finds the node behind a followed link', async () => {
    // THE BUG THIS GUARDS. Following the Node link from a pod's panel opened
    // a node panel with no usage charts, because the charts are read from the
    // row object and only a clicked row hands one in. Closing the panel and
    // clicking the node in the list fixed it, which is how it was noticed.
    open.selectedKindId = RICH_KIND_IDS.nodes
    open.nodes = [{ name: 'node-1', hasMetrics: true } as Node]

    await open.openDetail('node-1', '')

    expect(open.selectedNode?.name).toBe('node-1')
    expect(open.selectedNode?.hasMetrics).toBe(true)
  })

  it('finds the pod behind a followed link, in its own namespace', async () => {
    open.selectedKindId = RICH_KIND_IDS.pods
    open.pods = [
      { name: 'api', namespace: 'web' } as Pod,
      { name: 'api', namespace: 'staging' } as Pod,
    ]

    await open.openDetail('api', 'staging')

    // Name alone is not an identity: two namespaces routinely hold pods with
    // the same name, and the wrong one would show the wrong pod's findings.
    expect(open.selectedPod?.namespace).toBe('staging')
  })

  it('prefers the object it was handed over a lookup', async () => {
    // A clicked row's object is authoritative and newer than the list it came
    // from; re-finding it would be work for a worse answer.
    const clicked = { name: 'node-1', hasMetrics: false } as Node
    open.selectedKindId = RICH_KIND_IDS.nodes
    open.nodes = [{ name: 'node-1', hasMetrics: true } as Node]

    await open.openDetail('node-1', '', undefined, undefined, clicked)

    // Asserted on the field the two disagree about rather than on identity:
    // Svelte's state wraps what is stored, so the reference is not the one
    // that went in even when the value is.
    expect(open.selectedNode?.hasMetrics).toBe(false)
  })

  it('opens on the manifest alone when the list holds no such row', async () => {
    // Following a link to something the current list does not contain is not
    // an error — the panel simply shows what the manifest can supply.
    open.selectedKindId = RICH_KIND_IDS.nodes
    open.nodes = []

    await open.openDetail('node-9', '')

    expect(open.selectedNode).toBeNull()
    expect(open.selectedName).toBe('node-9')
  })

  it('does not mistake one kind of row for another', async () => {
    // The lists are cleared per view, but a stale name collision would be
    // worse than a missing row: a Pod named like a Node must not furnish a
    // node panel.
    open.selectedKindId = RICH_KIND_IDS.pods
    open.nodes = [{ name: 'shared', hasMetrics: true } as Node]
    open.pods = [{ name: 'shared', namespace: 'web' } as Pod]

    await open.openDetail('shared', 'web')

    expect(open.selectedNode).toBeNull()
    expect(open.selectedPod?.name).toBe('shared')
  })
})

/** A page of the pod table, as Go answers one. */
function page(names: string[], counts: Partial<PodPage> = {}): PodPage {
  return {
    rows: names.map((name) => ({ name, namespace: 'prod', controlledBy: `ReplicaSet/${name}-rs` }) as Pod),
    offset: 0,
    matched: names.length,
    total: names.length,
    unhealthy: 0,
    chipCounts: {},
    queryError: '',
    pinned: null,
    ...counts,
  }
}

/** A promise and the function that settles it, for ordering two in flight. */
function deferred<T>(): { promise: Promise<T>; resolve: (value: T) => void } {
  let resolve: (value: T) => void = () => {}
  const promise = new Promise<T>((settle) => (resolve = settle))
  return { promise, resolve }
}

describe('the pod table, paged in Go', () => {
  let open: ClusterSession

  beforeEach(() => {
    queryPods.mockReset()
    listPodKeys.mockReset()
    open = session()
    open.selectedKindId = RICH_KIND_IDS.pods
    open.kinds = [{ id: RICH_KIND_IDS.pods, group: '', version: 'v1', kind: 'Pod', namespaced: true } as ResourceKind]
  })

  it('asks for the search, the chips, the sort and the page together', () => {
    // A chip narrows what the search narrowed (AND) and chips OR among
    // themselves — that is now Go's rule, pinned by the shared fixture. What
    // is pinned here is that every one of them reaches the query.
    open.search = 'web'
    open.togglePodStatusFilter('pending')
    open.togglePodStatusFilter('restarting')
    open.toggleSort('name')
    open.toggleSort('name')
    open.page = 3

    expect(open.podQuery).toMatchObject({
      text: 'web',
      chips: ['pending', 'restarting'],
      sortColumn: 'name',
      descending: true,
      offset: 2 * preferences.pageSize,
      limit: preferences.pageSize,
      clusters: [],
    })
  })

  it('asks for the SETTLED search, not each keystroke', () => {
    open.setSearch('w')
    open.setSearch('we')

    expect(open.typedSearch).toBe('we')
    expect(open.podQuery.text).toBe('')
  })

  it('carries the custom columns, whose values are searchable', () => {
    const team = { source: 'label', key: 'team' } as const
    preferences.addCustomColumn(RICH_KIND_IDS.pods, team)
    try {
      expect(open.podQuery.columns).toEqual([team])
    } finally {
      preferences.removeCustomColumn(RICH_KIND_IDS.pods, team)
    }
  })

  it('holds the page and reads every count from Go', async () => {
    queryPods.mockResolvedValue(
      page(['web-1', 'web-2'], { matched: 120, total: 300, unhealthy: 9, chipCounts: { pending: 4 } }),
    )

    await open.refresh()

    expect(open.pagedPods.map((pod) => pod.name)).toEqual(['web-1', 'web-2'])
    expect(open.visibleCount).toBe(120)
    expect(open.pageCount).toBe(Math.ceil(120 / preferences.pageSize))
    expect(open.podSummary).toEqual({ total: 300, unhealthy: 9 })
    expect(open.podPage.chipCounts).toEqual({ pending: 4 })
  })

  it('a chip is a new page query, from the first page', () => {
    open.page = 4
    const before = open.pageQueryKey
    open.togglePodStatusFilter('failing')

    expect(open.page).toBe(1)
    expect(open.pageQueryKey).not.toBe(before)
  })

  it('lets the LAST query asked land, whichever answers first', async () => {
    // THE GENERATION GUARD. A chip pressed while a slow page query is in
    // flight asks again; the first answer arriving second must not replace
    // the page the chip asked for.
    const slow = deferred<PodPage>()
    const fast = deferred<PodPage>()
    queryPods.mockReturnValueOnce(slow.promise).mockReturnValueOnce(fast.promise)

    const first = open.requeryPods()
    open.togglePodStatusFilter('pending')
    const second = open.requeryPods()

    fast.resolve(page(['pending-1']))
    await second
    slow.resolve(page(['everything-1', 'everything-2']))
    await first

    expect(open.pagedPods.map((pod) => pod.name)).toEqual(['pending-1'])
  })

  it('selects every match across pages, and can plan them all', async () => {
    queryPods.mockResolvedValue(page(['web-1'], { matched: 3 }))
    await open.refresh()
    listPodKeys.mockResolvedValue([
      { namespace: 'prod', name: 'web-1', uid: '1', controlledBy: 'ReplicaSet/web-rs', cluster: 'dev' },
      { namespace: 'prod', name: 'web-2', uid: '2', controlledBy: 'ReplicaSet/web-rs', cluster: 'dev' },
      { namespace: 'staging', name: 'web-9', uid: '9', controlledBy: '', cluster: 'dev' },
    ])

    await open.selectAllMatchingPods()

    expect(listPodKeys).toHaveBeenCalledWith('dev', open.selectedNamespaces, [], [], open.podQuery)
    expect(open.selection.count).toBe(3)
    // Off-page pods are planned from what the keys said; the one on the page
    // from its row, which is this tick's.
    const planned = open.bulkItems.map((item) => `${item.namespace}/${item.name}:${item.controllerName}`)
    expect(planned.sort()).toEqual(['prod/web-1:web-1-rs', 'prod/web-2:web-rs', 'staging/web-9:'])
    expect(open.allMatchingSelected).toBe(true)

    // One untick, and "all matching" is no longer true.
    open.selection.toggle('prod/web-2')
    expect(open.allMatchingSelected).toBe(false)
  })

  it('drops keys that answer a query no longer on screen', async () => {
    let settle: (keys: unknown) => void = () => {}
    listPodKeys.mockReturnValue(new Promise((resolve) => (settle = resolve)))

    const pending = open.selectAllMatchingPods()
    open.togglePodStatusFilter('failing')
    settle([{ namespace: 'prod', name: 'web-1', uid: '1', controlledBy: '', cluster: 'dev' }])
    await pending

    expect(open.selection.count).toBe(0)
    expect(open.allMatchingSelected).toBe(false)
  })

  it('forgets an unticked pod’s facts, and every fact when the selection is cleared', async () => {
    queryPods.mockResolvedValue(page(['web-1']))
    await open.refresh()
    listPodKeys.mockResolvedValue([
      { namespace: 'staging', name: 'web-9', uid: '9', controlledBy: 'ReplicaSet/old', cluster: 'dev' },
    ])
    await open.selectAllMatchingPods()
    expect(open.bulkItems.map((item) => item.controllerName)).toEqual(['old'])

    // Unticked, then the tick prunes; ticked again by hand, the stale
    // controller is not served — there is nothing to plan it from.
    open.selection.toggle('staging/web-9')
    await open.refresh()
    open.selection.toggle('staging/web-9')
    expect(open.bulkItems).toEqual([])

    await open.selectAllMatchingPods()
    open.clearSelection()
    expect(open.selection.count).toBe(0)
    expect(open.bulkItems).toEqual([])
  })

  it('keeps the open drawer’s pod live when it is not on the page', async () => {
    // THE FREEZE THIS GUARDS. The drawer re-found its pod in the list each
    // tick; with only a page held, a pod opened from page 1 froze once the
    // operator paged on. Go now returns it beside the page.
    queryPods.mockResolvedValueOnce(page(['web-1']))
    await open.refresh()
    await open.openDetail('web-1', 'prod')
    expect(open.podQuery.pinned).toEqual({ namespace: 'prod', name: 'web-1' })

    const moved = { name: 'web-1', namespace: 'prod', cpu: '0.500', restarts: 7 } as Pod
    queryPods.mockResolvedValueOnce(page(['web-2'], { pinned: moved }))
    await open.refresh()

    expect(open.pagedPods.map((pod) => pod.name)).toEqual(['web-2'])
    expect(open.selectedPod?.restarts).toBe(7)
  })

  it('opens a pod no page held with the usage Go kept for it', async () => {
    queryPods.mockResolvedValueOnce(page(['web-1']))
    await open.refresh()
    podUsageHistory.mockResolvedValueOnce([
      { at: 1000, cpuCores: 0.1, memoryBytes: 100 },
      { at: 2000, cpuCores: 0.2, memoryBytes: 200 },
    ])

    await open.openDetail('web-9', 'prod')
    await vi.waitFor(() => expect(open.usage).toHaveLength(2))
    expect(open.podQuery.pinned).toEqual({ namespace: 'prod', name: 'web-9' })

    expect(podUsageHistory).toHaveBeenCalledWith('dev', 'prod', 'web-9')
    expect(open.usage.map((sample) => sample.cpuCores)).toEqual([0.1, 0.2])

    // And the pinned copy becomes its row object on the next tick.
    queryPods.mockResolvedValueOnce(page(['web-1'], { pinned: { name: 'web-9', namespace: 'prod' } as Pod }))
    await open.refresh()
    expect(open.selectedPod?.name).toBe('web-9')
  })

  it('says the open pod no longer exists, rather than freezing it as current', async () => {
    queryPods.mockResolvedValueOnce(page(['web-1']))
    await open.refresh()
    await open.openDetail('web-1', 'prod')
    expect(open.selectedGone).toBe(false)

    queryPods.mockResolvedValueOnce(page(['web-2'], { pinned: null }))
    await open.refresh()
    expect(open.selectedGone).toBe(true)
    // Still showing what it was last seen as.
    expect(open.selectedPod?.name).toBe('web-1')

    open.closeDetail()
    expect(open.selectedGone).toBe(false)
  })

  it('does not count opening a drawer as a new page', async () => {
    queryPods.mockResolvedValueOnce(page(['web-1']))
    await open.refresh()
    const before = open.pageQueryKey
    await open.openDetail('web-1', 'prod')

    expect(open.pageQueryKey).toBe(before)
  })

  it('plans a pod ticked on another page from when it was shown', async () => {
    queryPods.mockResolvedValueOnce(page(['web-1']))
    await open.refresh()
    open.selection.toggle('prod/web-1')

    queryPods.mockResolvedValueOnce(page(['web-2']))
    open.goToPage(2)
    await open.requeryPods()

    expect(open.pagedPods.map((pod) => pod.name)).toEqual(['web-2'])
    expect(open.bulkItems.map((item) => item.name)).toEqual(['web-1'])
  })
})

describe('recent objects', () => {
  // No object names are ever written to disk (see SECURITY.md), so this is
  // tracked in memory on the session rather than in preferences — these
  // tests are about that in-memory list, not about persistence.
  let open: ClusterSession

  beforeEach(() => {
    open = session()
    open.selectedKindId = RICH_KIND_IDS.pods
  })

  it('is bounded at twelve, dropping the oldest first', async () => {
    for (let index = 0; index < 15; index++) {
      await open.openDetail(`pod-${index}`, 'web')
    }

    expect(open.recentObjects).toHaveLength(12)
    // Most recent first: the last one opened leads, and the three oldest
    // (pod-0, pod-1, pod-2) fell off the end.
    expect(open.recentObjects[0]).toEqual({
      kindId: RICH_KIND_IDS.pods,
      name: 'pod-14',
      namespace: 'web',
    })
    expect(open.recentObjects.map((entry) => entry.name)).not.toContain('pod-2')
  })

  it('moves a reopened entry to the top instead of duplicating it', async () => {
    await open.openDetail('api', 'web')
    await open.openDetail('worker', 'web')
    await open.openDetail('cache', 'web')
    // Reopening the first one must surface it again, not leave a second copy
    // behind at the bottom of the list.
    await open.openDetail('api', 'web')

    expect(open.recentObjects.map((entry) => entry.name)).toEqual(['api', 'cache', 'worker'])
  })

  it('tells apart two objects that share a name in different namespaces', async () => {
    await open.openDetail('api', 'staging')
    await open.openDetail('api', 'production')

    expect(open.recentObjects).toHaveLength(2)
    expect(open.recentObjects.map((entry) => entry.namespace)).toEqual(['production', 'staging'])
  })

  it('clears on Clear, and on disconnect', async () => {
    await open.openDetail('api', 'web')
    expect(open.recentObjects).toHaveLength(1)

    open.clearRecents()
    expect(open.recentObjects).toEqual([])

    await open.openDetail('api', 'web')
    expect(open.recentObjects).toHaveLength(1)

    // dispose() is what a tab close (a disconnect) runs — see
    // workspace.svelte.ts's close(). Nothing about a past connection should
    // outlive it.
    open.dispose()
    expect(open.recentObjects).toEqual([])
  })
})

describe('autoscalersFor', () => {
  // Only the field autoscalersFor reads. A CRD discovered in a real cluster
  // carries several more, none of which this touches.
  const kedaKind = { id: 'keda.sh/v1alpha1/scaledobjects', group: 'keda.sh', kind: 'ScaledObject' } as ResourceKind

  const hpaTable = (rows: { name: string; cells: string[] }[]): ResourceTable =>
    ({
      kindId: 'autoscaling/v2/horizontalpodautoscalers',
      title: 'Autoscalers',
      namespaced: true,
      columns: [
        { name: 'NAME', type: 'string', wide: false, description: '' },
        { name: 'REFERENCE', type: 'string', wide: false, description: '' },
        { name: 'MINPODS', type: 'string', wide: false, description: '' },
        { name: 'MAXPODS', type: 'string', wide: false, description: '' },
      ],
      rows: rows.map((row) => ({ name: row.name, namespace: 'web', cells: row.cells })),
    }) as ResourceTable

  const scaledObjectTable = (rows: { name: string; cells: string[] }[]): ResourceTable =>
    ({
      kindId: 'keda.sh/v1alpha1/scaledobjects',
      title: 'ScaledObject',
      namespaced: true,
      columns: [
        { name: 'NAME', type: 'string', wide: false, description: '' },
        { name: 'SCALETARGETKIND', type: 'string', wide: false, description: '' },
        { name: 'SCALETARGETNAME', type: 'string', wide: false, description: '' },
        { name: 'MIN', type: 'string', wide: false, description: '' },
        { name: 'MAX', type: 'string', wide: false, description: '' },
      ],
      rows: rows.map((row) => ({ name: row.name, namespace: 'web', cells: row.cells })),
    }) as ResourceTable

  const emptyTable = (kindId: string): ResourceTable =>
    ({ kindId, title: '', namespaced: true, columns: [], rows: [] }) as unknown as ResourceTable

  beforeEach(() => {
    listTable.mockReset()
  })

  it('finds an HPA targeting the workload', async () => {
    const cluster = session()
    cluster.kinds = [] // no KEDA in this cluster's catalog
    listTable.mockResolvedValueOnce(
      hpaTable([{ name: 'web-hpa', cells: ['web-hpa', 'Deployment/web', '2', '10'] }]),
    )

    const result = await cluster.autoscalersFor('Deployment', 'web', 'web')

    expect(result).toEqual({
      status: 'known',
      autoscalers: [{ name: 'web-hpa', kind: 'HorizontalPodAutoscaler', minReplicas: '2', maxReplicas: '10' }],
    })
    // Asked for the HPA table alone — this cluster's catalog carries no KEDA
    // kind, so asking for one would be a request for a kind it does not serve.
    expect(listTable).toHaveBeenCalledTimes(1)
    expect(listTable).toHaveBeenCalledWith('dev', 'autoscaling/v2/horizontalpodautoscalers', 'web')
  })

  it('also checks KEDA, but only when the catalog carries a ScaledObject kind', async () => {
    const cluster = session()
    cluster.kinds = [kedaKind]
    listTable.mockImplementation((_clusterId: string, kindId: string) =>
      kindId === kedaKind.id
        ? Promise.resolve(scaledObjectTable([{ name: 'web-so', cells: ['web-so', 'Deployment', 'web', '1', '20'] }]))
        : Promise.resolve(hpaTable([])),
    )

    const result = await cluster.autoscalersFor('Deployment', 'web', 'web')

    expect(result).toEqual({
      status: 'known',
      autoscalers: [{ name: 'web-so', kind: 'ScaledObject', minReplicas: '1', maxReplicas: '20' }],
    })
    expect(listTable).toHaveBeenCalledTimes(2)
    expect(listTable).toHaveBeenCalledWith('dev', kedaKind.id, 'web')
  })

  it('answers "known" with nothing when neither table has a match', async () => {
    const cluster = session()
    cluster.kinds = [kedaKind]
    listTable.mockResolvedValue(emptyTable('unused'))

    const result = await cluster.autoscalersFor('Deployment', 'web', 'web')

    expect(result).toEqual({ status: 'known', autoscalers: [] })
  })

  it('reports "unknown", never "known" with an empty list, when a read is refused', async () => {
    // The distinction domain.MetricsStatus draws for the overview: an absent
    // answer and a refused one are different things, and only one of them is
    // safe to read as "nothing manages this workload".
    const cluster = session()
    cluster.kinds = []
    listTable.mockRejectedValue(new Error('[forbidden] your account may not list horizontalpodautoscalers'))

    const result = await cluster.autoscalersFor('Deployment', 'web', 'web')

    expect(result).toEqual({
      status: 'unknown',
      reason: 'your account may not list horizontalpodautoscalers',
    })
  })

  it('reads each namespace once, however many workloads in it are checked', async () => {
    const cluster = session()
    cluster.kinds = []
    listTable.mockResolvedValue(hpaTable([]))

    await Promise.all([
      cluster.autoscalersFor('Deployment', 'web', 'api'),
      cluster.autoscalersFor('Deployment', 'web', 'worker'),
      cluster.autoscalersFor('StatefulSet', 'web', 'db'),
    ])

    expect(listTable).toHaveBeenCalledTimes(1)
  })

  it('does not reuse a refusal — the next check retries', async () => {
    // The same rule readcache.go holds the backend's poll cache to: caching a
    // failure would leave the dialog reporting "could not check" for a
    // namespace whose permission was granted a moment ago.
    const cluster = session()
    cluster.kinds = []
    listTable.mockRejectedValueOnce(new Error('[forbidden] nope'))
    listTable.mockResolvedValueOnce(hpaTable([]))

    const first = await cluster.autoscalersFor('Deployment', 'web', 'api')
    const second = await cluster.autoscalersFor('Deployment', 'web', 'api')

    expect(first.status).toBe('unknown')
    expect(second).toEqual({ status: 'known', autoscalers: [] })
    expect(listTable).toHaveBeenCalledTimes(2)
  })
})

describe('retrying after an error', () => {
  const cases: [string, ApiError | null, boolean][] = [
    ['unauthenticated drops the cached client first', new ApiError('unauthenticated', 'Your credentials were rejected'), true],
    ['unreachable just retries', new ApiError('unreachable', 'The cluster is unreachable'), false],
    ['forbidden just retries', new ApiError('forbidden', 'Not allowed'), false],
    ['no error just retries', null, false],
  ]

  it.each(cases)('%s', async (_name, error, wantRefresh) => {
    refreshCredentials.mockReset().mockResolvedValue(undefined)
    const open = session()
    const refresh = vi.spyOn(open, 'refresh').mockResolvedValue(undefined)
    open.error = error

    await open.retry()

    expect(refreshCredentials).toHaveBeenCalledTimes(wantRefresh ? 1 : 0)
    if (wantRefresh) expect(refreshCredentials).toHaveBeenCalledWith('dev')
    expect(refresh).toHaveBeenCalledTimes(1)
  })

  it('still refreshes when the credential refresh itself fails', async () => {
    refreshCredentials.mockReset().mockRejectedValue(new Error('not connected'))
    const open = session()
    const refresh = vi.spyOn(open, 'refresh').mockResolvedValue(undefined)
    open.error = new ApiError('unauthenticated', 'Your credentials were rejected')

    await open.retry()

    expect(refresh).toHaveBeenCalledTimes(1)
  })
})

describe('the namespace filter is a set', () => {
  let count = 0
  function fresh(defaultNamespace = ''): ClusterSession {
    count += 1
    return new ClusterSession({ id: `ns-set-${count}`, name: 'x', defaultNamespace } as unknown as Cluster)
  }

  beforeEach(() => {
    listTable.mockReset()
    listTable.mockResolvedValue({ kindId: 'x', title: '', namespaced: true, columns: [], rows: [] })
  })

  it('starts on the kubeconfig default for a cluster never opened, else on what was left', () => {
    expect(fresh('billing').selectedNamespaces).toEqual(['billing'])
    expect(fresh('').isAllNamespaces).toBe(true)

    preferences.setClusterNamespaces(`ns-set-${count + 1}`, ['shop', 'keda'])
    expect(fresh('billing').selectedNamespaces).toEqual(['keda', 'shop'])
  })

  it('applies a set sorted and unique, remembers it, and names it by the label rule', async () => {
    const open = fresh()
    open.selectedKindId = 'core/v1/configmaps'
    await open.selectNamespaces(['shop', 'billing', 'shop', 'keda'])

    expect(open.selectedNamespaces).toEqual(['billing', 'keda', 'shop'])
    expect(preferences.getClusterNamespaces(open.cluster.id)).toEqual(['billing', 'keda', 'shop'])
    expect(open.scopeKey).toBe('billing,keda,shop')
    expect(open.singleNamespace).toBe('')
    expect(open.namespaceLabel.label).toBe('billing +2')
    expect(open.inScope('keda')).toBe(true)
    expect(open.inScope('default')).toBe(false)
    // One read over the set, not one per namespace from here.
    expect(listTable).toHaveBeenLastCalledWith(open.cluster.id, 'core/v1/configmaps', ['billing', 'keda', 'shop'], [], [])
  })

  it('selectNamespace filters to exactly one, and ALL_NAMESPACES to every one', async () => {
    const open = fresh()
    await open.selectNamespaces(['a', 'b'])
    await open.selectNamespace('c')
    expect(open.selectedNamespaces).toEqual(['c'])
    expect(open.singleNamespace).toBe('c')
    await open.selectNamespace('')
    expect(open.scope).toEqual({ namespaces: [], all: true })
  })

  it('toggles a namespace in and out, and taking the last out is All', async () => {
    const open = fresh('shop')
    await open.toggleNamespace('keda')
    expect(open.selectedNamespaces).toEqual(['keda', 'shop'])
    await open.toggleNamespace('keda')
    await open.toggleNamespace('shop')
    expect(open.isAllNamespaces).toBe(true)
  })

  it('ADDS the namespace of an object opened from outside the set, never swapping the set', async () => {
    const open = fresh()
    await open.selectNamespaces(['shop', 'keda'])
    await open.openObject('core/v1/configmaps', 'settings', 'billing', true)

    expect(open.selectedNamespaces).toEqual(['billing', 'keda', 'shop'])
    expect(open.selectedKindId).toBe('core/v1/configmaps')
  })

  it('leaves All alone when opening an object, and ignores cluster-scoped ones', async () => {
    const open = fresh()
    await open.openObject('core/v1/configmaps', 'settings', 'billing', true)
    expect(open.isAllNamespaces).toBe(true)

    await open.selectNamespaces(['shop'])
    await open.openObject('core/v1/nodes', 'node-1', '', false)
    expect(open.selectedNamespaces).toEqual(['shop'])
  })
})
