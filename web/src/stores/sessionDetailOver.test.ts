/**
 * The topology opens an object's drawer OVER itself: the page on screen stays
 * the topology, and the drawer reads the object's own kind.
 */
import { beforeEach, describe, expect, it, vi } from 'vitest'

const getManifest = vi.fn()
const listTable = vi.fn()
const queryPods = vi.fn()
const listWorkloads = vi.fn()
const scaleWorkload = vi.fn()
vi.mock('$lib/api/client', async () => {
  const actual = await vi.importActual<Record<string, unknown>>('$lib/api/client')
  return {
    ...actual,
    getManifest: (...args: unknown[]) => getManifest(...args),
    listTable: (...args: unknown[]) => listTable(...args),
    queryPods: (...args: unknown[]) => queryPods(...args),
    listWorkloads: (...args: unknown[]) => listWorkloads(...args),
    scaleWorkload: (...args: unknown[]) => scaleWorkload(...args),
    podUsageHistory: vi.fn().mockResolvedValue([]),
    getOverview: vi.fn().mockRejectedValue(new Error('[unknown] no cluster in a test')),
    listKinds: vi.fn().mockRejectedValue(new Error('[unknown] no cluster in a test')),
  }
})

import { ClusterSession, TOPOLOGY_KIND_ID } from './session.svelte'
import type { Cluster } from '$lib/api/client'

const cluster = { id: 'dev', name: 'dev', defaultNamespace: 'default' } as unknown as Cluster

const pod = (name: string) => ({
  name,
  namespace: 'shop',
  containers: [{ name: 'app' }, { name: 'sidecar' }],
})

describe('a drawer over the topology', () => {
  beforeEach(() => {
    queryPods.mockReset()
    listWorkloads.mockReset()
    scaleWorkload.mockReset()
    queryPods.mockResolvedValue({ rows: [], pinned: null })
    listWorkloads.mockResolvedValue([])
  })

  it('keeps the topology on screen and reads the object by its own kind', async () => {
    getManifest.mockResolvedValue('kind: Deployment')
    const session = new ClusterSession(cluster)
    session.selectedKindId = TOPOLOGY_KIND_ID

    await session.openDetailOver('apps/v1/deployments', 'web', 'shop')

    expect(session.viewMode).toBe('topology')
    expect(session.selectedKindId).toBe(TOPOLOGY_KIND_ID)
    expect(session.drawerKindId).toBe('apps/v1/deployments')
    expect(session.selectedName).toBe('web')
    expect(getManifest).toHaveBeenLastCalledWith('dev', 'apps/v1/deployments', 'shop', 'web', false)
    expect(listTable).not.toHaveBeenCalled()

    session.closeDetail()
    expect(session.drawerKindId).toBe(TOPOLOGY_KIND_ID)
    expect(session.viewMode).toBe('topology')
  })

  it('follows a reference from that drawer over the map too', async () => {
    getManifest.mockResolvedValue('kind: Pod')
    const session = new ClusterSession(cluster)
    session.selectedKindId = TOPOLOGY_KIND_ID

    await session.openObject('core/v1/pods', 'web-1', 'shop', true)

    expect(session.selectedKindId).toBe(TOPOLOGY_KIND_ID)
    expect(session.drawerKindId).toBe('core/v1/pods')
    expect(session.selectedName).toBe('web-1')
  })

  it('opens a pod with its row: its containers, for Logs and Terminal', async () => {
    getManifest.mockResolvedValue('kind: Pod')
    queryPods.mockResolvedValue({ rows: [], pinned: pod('web-1') })
    const session = new ClusterSession(cluster)
    session.selectedKindId = TOPOLOGY_KIND_ID

    await session.openDetailOver('core/v1/pods', 'web-1', 'shop')

    // Read through the pinned slot: one pod, whatever page the pod list is on.
    expect(queryPods.mock.calls[0][4]).toMatchObject({ pinned: { namespace: 'shop', name: 'web-1' } })
    expect(session.selectedPod?.containers?.map((c) => c.name)).toEqual(['app', 'sidecar'])
    expect(session.selectedGone).toBe(false)
  })

  it('opens a workload with its row, so its actions reach the backend', async () => {
    getManifest.mockResolvedValue('kind: Deployment')
    listWorkloads.mockResolvedValue([
      { name: 'other', namespace: 'shop', kind: 'Deployment' },
      { name: 'web', namespace: 'shop', kind: 'Deployment', replicas: 2 },
    ])
    const session = new ClusterSession(cluster)
    session.selectedKindId = TOPOLOGY_KIND_ID

    await session.openDetailOver('apps/v1/deployments', 'web', 'shop')

    expect(listWorkloads).toHaveBeenCalledWith('dev', 'Deployment', 'shop')
    const workload = session.selectedWorkload!
    expect(workload.name).toBe('web')
    // What the drawer's Scale does with that row.
    await session.scaleWorkload('Deployment', workload.name, workload.namespace, 3)
    expect(scaleWorkload).toHaveBeenCalledWith('dev', 'Deployment', 'shop', 'web', 3)
  })

  it('says when the object is gone, and keeps the row fresh on the tick', async () => {
    getManifest.mockResolvedValue('kind: Pod')
    queryPods.mockResolvedValueOnce({ rows: [], pinned: pod('web-1') })
    const session = new ClusterSession(cluster)
    session.selectedKindId = TOPOLOGY_KIND_ID
    await session.openDetailOver('core/v1/pods', 'web-1', 'shop')
    expect(session.selectedGone).toBe(false)

    // The next tick finds it deleted.
    queryPods.mockResolvedValue({ rows: [], pinned: null })
    await session.refresh()
    await vi.waitFor(() => expect(session.selectedGone).toBe(true))
    // Last seen copy kept on screen.
    expect(session.selectedPod?.name).toBe('web-1')

    // Opening one that is already gone says so at once.
    await session.openDetailOver('core/v1/pods', 'web-9', 'shop')
    expect(session.selectedGone).toBe(true)
  })
})

describe('following a findings badge', () => {
  it('opens the overview with its details shown, and brings the card into view', async () => {
    const { preferences } = await import('./preferences.svelte')
    preferences.findingsExpanded = false
    const session = new ClusterSession(cluster)
    session.selectedKindId = TOPOLOGY_KIND_ID

    const card = document.createElement('article')
    card.dataset.findingId = 'crash'
    card.tabIndex = -1
    const scrolled = vi.fn()
    card.scrollIntoView = scrolled
    document.body.append(card)
    vi.stubGlobal('matchMedia', () => ({ matches: true }))

    await session.openFinding('crash')

    expect(preferences.findingsExpanded).toBe(true)
    expect(session.viewMode).toBe('overview')
    expect(scrolled).toHaveBeenCalled()
    expect(document.activeElement).toBe(card)
    card.remove()
    vi.unstubAllGlobals()
  })
})
