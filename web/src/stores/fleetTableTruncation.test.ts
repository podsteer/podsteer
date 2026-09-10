/**
 * The truncation caveat and the rows it qualifies must move together.
 *
 * A cluster's share of the merged table is kept across a tick it did not
 * answer — see mergeFleet — so anything stored beside those rows has to
 * change on exactly the ticks the rows do. A caveat cleared while the rows it
 * describes are still on screen is worse than none: it turns a prefix into a
 * table that claims to be complete.
 */
import { beforeEach, describe, expect, it, vi } from 'vitest'

const listFleetTable = vi.fn()
vi.mock('$lib/api/client', async () => {
  const actual = await vi.importActual<Record<string, unknown>>('$lib/api/client')
  return {
    ...actual,
    listFleetTable: (...args: unknown[]) => listFleetTable(...args),
  }
})

import { fleet } from './fleet.svelte'

/** One cluster's share of the merged table. */
function share(cluster: string, status: string, rows: number, truncated: boolean) {
  return {
    cluster,
    status,
    reason: '',
    missing: [],
    columns: [{ name: 'Name', type: 'string', wide: false, description: '' }],
    rows: Array.from({ length: rows }, (_, index) => ({
      name: `w-${index}`,
      namespace: 'shop',
      cells: [`w-${index}`],
      labels: {},
      annotations: {},
      custom: {},
    })),
    truncated,
    cap: truncated ? 1000 : 0,
  }
}

const KIND = { group: 'acme.io', resource: 'widgets', title: 'Widgets' }

beforeEach(() => {
  listFleetTable.mockReset()
  fleet.openClusters = () => ['prod', 'staging']
  fleet.tab = 'kinds'
  fleet.chooseKind(KIND as never)
})

describe('the merged table’s truncation caveat', () => {
  it('records the cap per cluster, because the cap is per read', async () => {
    listFleetTable.mockResolvedValue([
      share('prod', 'ok', 3, true),
      share('staging', 'ok', 2, false),
    ])

    await fleet.refresh('shop')

    expect(fleet.tableTruncated).toEqual({ prod: 1000 })
  })

  it('clears it when that cluster comes back complete', async () => {
    listFleetTable.mockResolvedValueOnce([share('prod', 'ok', 3, true)])
    await fleet.refresh('shop')
    expect(fleet.tableTruncated.prod).toBe(1000)

    listFleetTable.mockResolvedValueOnce([share('prod', 'ok', 3, false)])
    await fleet.refresh('shop')

    expect(fleet.tableTruncated.prod).toBeUndefined()
  })

  it('keeps it while that cluster’s rows are being kept', async () => {
    // THE RULE. An unreachable cluster keeps its previous rows on screen, so
    // its caveat has to stay up with them — cleared here, the operator would
    // be looking at a prefix presented as the whole set.
    listFleetTable.mockResolvedValueOnce([share('prod', 'ok', 3, true)])
    await fleet.refresh('shop')

    listFleetTable.mockResolvedValueOnce([share('prod', 'unreachable', 0, false)])
    await fleet.refresh('shop')

    expect(fleet.tableTruncated.prod).toBe(1000)
  })

  it('is dropped entirely when a different kind is chosen', async () => {
    listFleetTable.mockResolvedValueOnce([share('prod', 'ok', 3, true)])
    await fleet.refresh('shop')

    fleet.chooseKind({ group: 'acme.io', resource: 'gadgets', title: 'Gadgets' } as never)

    expect(fleet.tableTruncated).toEqual({})
  })
})
