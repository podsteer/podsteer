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
vi.mock('$lib/api/client', async () => {
  const actual = await vi.importActual<Record<string, unknown>>('$lib/api/client')
  return {
    ...actual,
    getManifest: vi.fn().mockRejectedValue(new Error('no cluster in a test')),
    listTable: (...args: unknown[]) => listTable(...args),
  }
})

import { ClusterSession, RICH_KIND_IDS } from './session.svelte'
import type { Cluster, Node, Pod, ResourceKind, ResourceTable } from '$lib/api/client'

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
