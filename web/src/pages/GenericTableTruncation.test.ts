/**
 * What a capped generic list has to say about itself.
 *
 * A truncated read comes back looking exactly like a complete one — same
 * columns, same shape, a plausible count — so every question the interface
 * answers from it is wrong in the same silent direction: the search misses a
 * match past the cut, the sort names the wrong newest, the count is a floor
 * shown as a total.
 */
import { afterEach, describe, expect, it, vi } from 'vitest'
import { render, cleanup } from '@testing-library/svelte'

vi.mock('$lib/api/client', async () => {
  const actual = await vi.importActual<Record<string, unknown>>('$lib/api/client')
  return { ...actual, getManifest: vi.fn().mockRejectedValue(new Error('no cluster in a test')) }
})

import GenericTableView from './GenericTableView.svelte'
import { ClusterSession } from '$stores/session.svelte'
import type { Cluster, ResourceTable } from '$lib/api/client'

const cluster = { id: 'dev', name: 'dev', defaultNamespace: 'default' } as unknown as Cluster

const KIND = 'acme.io/v1/widgets'

function table(truncated: boolean, rows: number): ResourceTable {
  return {
    kindId: KIND,
    title: 'Widgets',
    namespaced: true,
    columns: [{ name: 'Name', type: 'string', wide: false, description: '' }],
    rows: Array.from({ length: rows }, (_, index) => ({
      name: `widget-${index}`,
      namespace: 'shop',
      cells: [`widget-${index}`],
      labels: {},
      annotations: {},
      custom: {},
    })),
    truncated,
    cap: truncated ? 1000 : 0,
  } as unknown as ResourceTable
}

/** A session showing one generic kind, with the table already read. */
function session(truncated: boolean, rows = 3): ClusterSession {
  const open = new ClusterSession(cluster)
  open.selectedKindId = KIND
  open.kinds = [
    {
      id: KIND,
      group: 'acme.io',
      version: 'v1',
      resource: 'widgets',
      kind: 'Widget',
      title: 'Widgets',
      singular: 'widget',
      namespaced: true,
    },
  ] as unknown as ClusterSession['kinds']
  open.table = table(truncated, rows)
  return open
}

afterEach(cleanup)

describe('a generic table that stopped at its cap', () => {
  it('says so, above the rows', () => {
    const { container } = render(GenericTableView, { session: session(true) })

    expect(container.textContent).toContain('More than 1,000 widgets')
    expect(container.textContent).toContain('listed the first 1,000 and stopped')
  })

  it('says nothing when the collection ended', () => {
    const { container } = render(GenericTableView, { session: session(false) })

    expect(container.textContent).not.toContain('stopped')
  })

  it('does not report a search miss as an absence', () => {
    // The search runs over the rows that ARRIVED. On a capped read a match
    // sitting past the cut is reported as "nothing matches", which is the one
    // case where the honest answer is that PodSteer does not know.
    const open = session(true, 0)
    open.search = 'widget-4000'

    const { container } = render(GenericTableView, { session: open })

    expect(container.textContent).toContain('there are more that were not read')
  })
})
