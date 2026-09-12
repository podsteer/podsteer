/**
 * What the combined view shows, and what it refuses to imply.
 *
 * The claims worth pinning are the ones an operator would act on wrongly if
 * they were false: that a cell belongs to the kind its row names, that a
 * capped read says so, and that the request multiplier has a ceiling.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { render, cleanup, screen } from '@testing-library/svelte'

vi.mock('$lib/api/client', async () => {
  const actual = await vi.importActual<Record<string, unknown>>('$lib/api/client')
  return { ...actual, listTable: vi.fn() }
})

import CombinedView from './CombinedView.svelte'
import { preferences, MAX_COMBINED_KINDS } from '$stores/preferences.svelte'

const words = () => (document.body.textContent ?? '').replace(/\s+/g, ' ')

const KINDS = [
  { id: 'apps/v1/deployments', title: 'Deployments', namespaced: true },
  { id: 'core/v1/services', title: 'Services', namespaced: true },
  { id: 'core/v1/configmaps', title: 'ConfigMaps', namespaced: true },
]

function session(overrides: Record<string, unknown> = {}) {
  return {
    cluster: { id: 'dev' },
    kinds: KINDS,
    customColumns: [],
    sort: null,
    toggleSort: () => {},
    combinedTable: { columns: [], rows: [] },
    combinedTruncated: false,
    pagedCombinedRows: [],
    sortedCombinedRows: [],
    combinedKindTitle: (id: string) => KINDS.find((k) => k.id === id)?.title ?? id,
    combinedKindNamespaced: () => true,
    openObject: async () => {},
    ...overrides,
  } as never
}

beforeEach(() => {
  preferences.combinedKinds = {}
})

afterEach(() => cleanup())

describe('CombinedView', () => {
  it('explains itself before any kind is chosen, rather than showing an empty table', () => {
    render(CombinedView, { session: session() })

    const text = words()
    expect(text).toContain('Pick the kinds to show together')
    // The reason there is no multi-kind list is the reason for the cap, and
    // an operator is entitled to it rather than to a bare limit.
    expect(text).toContain('no way to list several kinds at once')
  })

  it('names each chosen kind with a way to remove it', () => {
    preferences.combinedKinds = { dev: ['apps/v1/deployments', 'core/v1/services'] }
    render(CombinedView, { session: session() })

    expect(screen.getByLabelText('Stop showing Deployments')).toBeTruthy()
    expect(screen.getByLabelText('Stop showing Services')).toBeTruthy()
  })

  it('stops offering more kinds at the cap, and says why', () => {
    // The limit is about the request rate this view costs on every tick, so
    // the sentence has to say that rather than state a bare number.
    preferences.combinedKinds = {
      dev: Array.from({ length: MAX_COMBINED_KINDS }, (_, i) => `k${i}`),
    }
    render(CombinedView, { session: session() })

    expect(words()).toContain('another request on every refresh')
    expect(screen.queryByLabelText('Add a kind')).toBeNull()
  })

  it('puts each row under the kind it came from', () => {
    // The failure this prevents: a Service's row reading as a Deployment,
    // which would send an operator to the wrong object.
    preferences.combinedKinds = { dev: ['apps/v1/deployments', 'core/v1/services'] }
    render(CombinedView, {
      session: session({
        combinedTable: {
          columns: [{ name: 'Name' }, { name: 'Age' }],
          rows: [],
        },
        pagedCombinedRows: [
          { name: 'web', namespace: 'shop', cells: ['web', '5d'], source: 'apps/v1/deployments' },
          { name: 'web', namespace: 'shop', cells: ['web', '5d'], source: 'core/v1/services' },
        ],
      }),
    })

    // SCOPED TO THE TABLE BODY. Both names also appear in the chip row above
    // it, so asserting against the whole document passes whether or not the
    // rows carry their kind at all — which is how the first version of this
    // test passed with the Kind cell replaced by a dash.
    const cells = [...document.querySelectorAll('tbody tr')].map((row) =>
      [...row.querySelectorAll('td')].map((cell) => cell.textContent?.trim()),
    )
    expect(cells[0]).toContain('Deployments')
    expect(cells[1]).toContain('Services')
    // And the two rows are otherwise identical, which is the point: name and
    // age alone cannot tell them apart.
    expect(cells[0]).toContain('web')
    expect(cells[1]).toContain('web')
  })

  it('says when a read was capped, because everything below it is then a prefix', () => {
    preferences.combinedKinds = { dev: ['apps/v1/deployments'] }
    render(CombinedView, {
      session: session({
        combinedTruncated: true,
        combinedTable: { columns: [{ name: 'Name' }], rows: [] },
        pagedCombinedRows: [
          { name: 'web', namespace: 'shop', cells: ['web'], source: 'apps/v1/deployments' },
        ],
      }),
    })

    const text = words()
    expect(text).toContain('a part of the picture rather than all of it')
  })

  it('does not claim a capped read when none was capped', () => {
    preferences.combinedKinds = { dev: ['apps/v1/deployments'] }
    render(CombinedView, {
      session: session({
        combinedTable: { columns: [{ name: 'Name' }], rows: [] },
        pagedCombinedRows: [
          { name: 'web', namespace: 'shop', cells: ['web'], source: 'apps/v1/deployments' },
        ],
      }),
    })

    expect(words()).not.toContain('a part of the picture')
  })
})
