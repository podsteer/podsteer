/**
 * All clusters has ONE namespace for the window.
 *
 * Its rows are one set for the window, and they used to be read in whichever
 * tab was in front's own namespace — so two tabs on different namespaces
 * overwrote each other: the second tab showed the first tab's rows and then
 * its own poll wiped them. These pin the store half of the fix: the scope is
 * the store's, a change of scope drops the old scope's rows, and a read still
 * in flight for the old scope cannot land afterwards.
 */
import { beforeEach, describe, expect, it, vi } from 'vitest'

const queryFleetPods = vi.fn()
vi.mock('$lib/api/client', async () => {
  const actual = await vi.importActual<Record<string, unknown>>('$lib/api/client')
  return {
    ...actual,
    queryFleetPods: (...args: unknown[]) => queryFleetPods(...args),
  }
})

import { ALL_NAMESPACES } from '$lib/api/client'
import { fleet } from './fleet.svelte'

/** One cluster's page of the merged pod table, as Go answers it. */
function answer(cluster: string, names: string[]) {
  return {
    clusters: [{ cluster, status: 'ok', reason: '', missing: [], rows: names.length, rowsAt: 1, stale: false }],
    page: {
      rows: names.map((name) => ({ name, namespace: 'shop', labels: {}, clusterId: cluster })),
      offset: 0,
      matched: names.length,
      total: names.length,
      unhealthy: 0,
      chipCounts: {},
      queryError: '',
    },
  }
}

const query = {
  pinned: { namespace: '', name: '' },
  text: '',
  chips: [],
  sortColumn: '',
  descending: false,
  columns: [],
  clusters: [],
  offset: 0,
  limit: 50,
}

beforeEach(() => {
  queryFleetPods.mockReset()
  fleet.openClusters = () => ['prod', 'staging']
  fleet.tab = 'pods'
  fleet.chooseNamespace(ALL_NAMESPACES)
  fleet.pods = []
})

describe('the All clusters namespace', () => {
  it('starts on every namespace, whatever any tab is filtered to', () => {
    expect(fleet.namespace).toBe(ALL_NAMESPACES)
  })

  it('drops the old scope’s rows when the namespace changes', async () => {
    queryFleetPods.mockResolvedValue(answer('prod', ['a', 'b']))
    await fleet.refresh(fleet.namespace, query)
    expect(fleet.podRows).toHaveLength(2)
    expect(fleet.podRows[0].cluster).toBe('prod')
    expect(fleet.podCounts.matched).toBe(2)

    fleet.chooseNamespace('default')

    expect(fleet.namespace).toBe('default')
    expect(fleet.podRows).toHaveLength(0)
    expect(fleet.podCounts.matched).toBe(0)
  })

  it('discards a read still in flight for the old namespace', async () => {
    let settle: (value: unknown) => void = () => {}
    queryFleetPods.mockReturnValue(new Promise((resolve) => (settle = resolve)))

    const inFlight = fleet.refresh(fleet.namespace, query)
    fleet.chooseNamespace('default')
    settle(answer('prod', ['stale']))
    await inFlight

    expect(fleet.podRows).toHaveLength(0)
  })

  it('turns Go’s per-cluster verdicts into the strip, rows counted rather than held', async () => {
    queryFleetPods.mockResolvedValue({
      clusters: [
        { cluster: 'prod', status: 'unreachable', reason: 'no route', missing: null, rows: 7, rowsAt: 1000, stale: true },
        { cluster: 'staging', status: 'forbidden', reason: 'denied', missing: [], rows: 0, rowsAt: 0, stale: false },
      ],
      page: { rows: [], offset: 0, matched: 7, total: 7, unhealthy: 0, chipCounts: {}, queryError: '' },
    })
    await fleet.refresh(fleet.namespace, query)

    const [prod, staging] = fleet.strip
    expect(prod).toMatchObject({ cluster: 'prod', status: 'unreachable', rows: 7, stale: true })
    expect(staging).toMatchObject({ cluster: 'staging', status: 'forbidden', rows: 0, stale: false, ageSeconds: null })
  })
})
