import { beforeEach, describe, expect, it, vi } from 'vitest'

vi.mock('$lib/api/client', async () => {
  const actual = await vi.importActual<Record<string, unknown>>('$lib/api/client')
  return { ...actual, getManifest: vi.fn().mockRejectedValue(new Error('no cluster in a test')) }
})

import { ClusterSession } from './session.svelte'
import { fleet } from './fleet.svelte'
import type { Cluster, Pod } from '$lib/api/client'
import type { ClusterAnswer } from '$lib/fleet'

const cluster = { id: 'alpha', name: 'alpha', defaultNamespace: 'default' } as unknown as Cluster

/** One cluster's answer, with one pod in it. */
function answer(id: string, podName: string): ClusterAnswer<Pod> {
  return {
    cluster: id,
    status: 'ok',
    reason: '',
    missing: [],
    rows: [{ name: podName, namespace: 'web', phase: 'Running', labels: {} } as unknown as Pod],
    rowsAt: Date.now(),
    stale: false,
  }
}

describe('the merged table, narrowed by the strip chips', () => {
  let open: ClusterSession

  beforeEach(() => {
    // READ OUT OF REACTIVE STATE, exactly as the real one is: $stores/workspace
    // assigns `openClusters` a closure over `workspace.sessions`, so a derived
    // that calls it tracks the tabs closing. A stub returning a frozen literal
    // would be read once and cached, and this file's whole subject is what
    // happens to the selection when the open set changes.
    fleet.openClusters = () => fleet.pods.map((entry) => entry.cluster)
    fleet.pods = [answer('alpha', 'api'), answer('beta', 'worker'), answer('gamma', 'cache')]
    open = new ClusterSession(cluster)
    open.selectedKindId = 'podsteer/fleet'
  })

  /**
   * Which clusters the merged pod table asks Go for — the selection, as it
   * reaches the page query. The rows are filtered in Go (FleetService.
   * QueryPods); what this file pins is what the chips SEND.
   */
  const names = (session: ClusterSession) => [...(session.fleetPodQuery.clusters ?? [])].sort()

  it('asks for every cluster with no chip pressed', () => {
    expect(names(open)).toEqual([])
  })

  it('narrows to one', () => {
    open.toggleFleetCluster('beta')

    expect(names(open)).toEqual(['beta'])
  })

  it('SENDS BOTH when two are pressed', () => {
    // THE BUG. Through the search box this became `cluster:beta cluster:gamma`
    // — two ANDed terms over rows that each belong to one cluster — so the
    // table emptied while both chips rendered pressed. A selection is a list
    // of clusters beside the query, never terms in it.
    open.toggleFleetCluster('beta')
    open.toggleFleetCluster('gamma')

    expect(names(open)).toEqual(['beta', 'gamma'])
    expect(open.fleetPodQuery.text).toBe('')
  })

  it('goes back to everything when the last selected chip is released', () => {
    open.toggleFleetCluster('beta')
    open.toggleFleetCluster('beta')

    expect(open.fleetClusters).toEqual([])
    expect(names(open)).toEqual([])
  })

  it('goes back to everything when all three are pressed', () => {
    open.toggleFleetCluster('alpha')
    open.toggleFleetCluster('beta')
    open.toggleFleetCluster('gamma')

    expect(open.fleetClusters).toEqual([])
    expect(names(open)).toEqual([])
  })

  it('stops narrowing to a cluster whose tab has closed', () => {
    // THE BUG. The "only clusters still open may be selected" rule lived in
    // the toggle, so between closing that tab and the next press it was not
    // true at all: the table filtered to a cluster with no rows in it and
    // went empty, no chip anywhere rendered pressed — the chip's cluster was
    // gone — and the empty state read "across the 1 selected cluster",
    // naming a selection nothing on screen could show or release.
    open.toggleFleetCluster('beta')
    expect(names(open)).toEqual(['beta'])

    fleet.pods = [answer('alpha', 'api'), answer('gamma', 'cache')]

    expect(open.selectedFleetClusters).toEqual([])
    expect(names(open)).toEqual([])
  })

  it('brings the selection back with the tab, which the strip can show', () => {
    // The stored selection is not pruned, so reopening the cluster restores a
    // state the chip strip renders and the operator can release.
    open.toggleFleetCluster('beta')
    fleet.pods = [answer('alpha', 'api'), answer('gamma', 'cache')]
    expect(open.selectedFleetClusters).toEqual([])

    fleet.pods = [answer('alpha', 'api'), answer('beta', 'worker'), answer('gamma', 'cache')]

    expect(open.selectedFleetClusters).toEqual(['beta'])
    expect(names(open)).toEqual(['beta'])
  })

  it('returns to the first page, because page 4 may no longer exist', () => {
    open.page = 4
    open.toggleFleetCluster('beta')

    expect(open.page).toBe(1)
    expect(open.fleetPodQuery.offset).toBe(0)
  })

  it('makes the selection a new page to ask for', () => {
    const before = open.pageQueryKey
    open.toggleFleetCluster('beta')

    expect(open.pageQueryKey).not.toBe(before)
    expect(open.pageQueryKey).toContain('"clusters":["beta"]')
  })
})
