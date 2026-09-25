/**
 * What the multi-kind view shows, and what it refuses to imply.
 *
 * The claims worth pinning are the ones an operator would act on wrongly if
 * they were false: that a cell belongs to the kind its row names, that a
 * capped read says so, and that the request multiplier has a ceiling.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { render, cleanup, screen, fireEvent } from '@testing-library/svelte'

vi.mock('$lib/api/client', async () => {
  const actual = await vi.importActual<Record<string, unknown>>('$lib/api/client')
  return { ...actual, listTable: vi.fn() }
})

import MultiKindView from './MultiKindView.svelte'
import { preferences, MAX_MULTI_KINDS } from '$stores/preferences.svelte'

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
    multiKindTable: { columns: [], rows: [] },
    multiKindTables: [],
    multiKindColumnSources: () => 1,
    multiKindTruncated: false,
    pagedMultiKindRows: [],
    sortedMultiKindRows: [],
    multiKindTitle: (id: string) => KINDS.find((k) => k.id === id)?.title ?? id,
    multiKindNamespaced: () => true,
    openObject: async () => {},
    ...overrides,
  } as never
}

beforeEach(() => {
  preferences.multiKindSelection = {}
})

afterEach(() => cleanup())

describe('MultiKindView', () => {
  it('explains itself before any kind is chosen, rather than showing an empty table', () => {
    render(MultiKindView, { session: session() })

    const text = words()
    expect(text).toContain('Pick the kinds to show together')
    // The reason there is no multi-kind list is the reason for the cap, and
    // an operator is entitled to it rather than to a bare limit.
    expect(text).toContain('no way to list several kinds at once')
  })

  it('names each chosen kind with a way to remove it', () => {
    preferences.multiKindSelection = { dev: ['apps/v1/deployments', 'core/v1/services'] }
    render(MultiKindView, { session: session() })

    expect(screen.getByLabelText('Stop showing Deployments')).toBeTruthy()
    expect(screen.getByLabelText('Stop showing Services')).toBeTruthy()
  })

  it('stops offering more kinds at the cap, and says why', () => {
    // The limit is about the request rate this view costs on every tick, so
    // the sentence has to say that rather than state a bare number.
    preferences.multiKindSelection = {
      dev: Array.from({ length: MAX_MULTI_KINDS }, (_, i) => `k${i}`),
    }
    render(MultiKindView, { session: session() })

    expect(words()).toContain('another request on every refresh')
    expect(screen.queryByLabelText('Add a kind')).toBeNull()
  })

  it('puts each row under the kind it came from', () => {
    // The failure this prevents: a Service's row reading as a Deployment,
    // which would send an operator to the wrong object.
    preferences.multiKindSelection = { dev: ['apps/v1/deployments', 'core/v1/services'] }
    render(MultiKindView, {
      session: session({
        multiKindTable: {
          columns: [{ name: 'Name' }, { name: 'Age' }],
          rows: [],
        },
        pagedMultiKindRows: [
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
    preferences.multiKindSelection = { dev: ['apps/v1/deployments'] }
    render(MultiKindView, {
      session: session({
        multiKindTruncated: true,
        multiKindTable: { columns: [{ name: 'Name' }], rows: [] },
        pagedMultiKindRows: [
          { name: 'web', namespace: 'shop', cells: ['web'], source: 'apps/v1/deployments' },
        ],
      }),
    })

    const text = words()
    expect(text).toContain('a part of the picture rather than all of it')
  })

  it('does not claim a capped read when none was capped', () => {
    preferences.multiKindSelection = { dev: ['apps/v1/deployments'] }
    render(MultiKindView, {
      session: session({
        multiKindTable: { columns: [{ name: 'Name' }], rows: [] },
        pagedMultiKindRows: [
          { name: 'web', namespace: 'shop', cells: ['web'], source: 'apps/v1/deployments' },
        ],
      }),
    })

    expect(words()).not.toContain('a part of the picture')
  })
})

describe('MultiKindView — the merged column set', () => {
  beforeEach(() => {
    preferences.multiKindSelection = { dev: ['apps/v1/deployments', 'core/v1/services'] }
  })

  /**
   * The four kinds an operator actually picks together — Deployments, Pods,
   * Ingresses, Services — merge to SIXTEEN columns, twelve of which exactly
   * one kind prints. Left alone, the table arrives mostly blank and a Pod's
   * STATUS lands seventh, behind a horizontal scroll.
   */
  it('hides a column only one kind fills, and keeps the ones they share', () => {
    const columns = (names: string[]) => names.map((name) => ({ name }))
    render(MultiKindView, {
      session: session({
        // What the API server really prints for these two.
        multiKindTables: [
          { kindId: 'apps/v1/deployments', columns: columns(['Name', 'Ready', 'Up-to-date', 'Age']) },
          { kindId: 'core/v1/services', columns: columns(['Name', 'Type', 'Cluster-IP', 'Age']) },
        ],
        multiKindTable: {
          columns: columns(['Name', 'Ready', 'Up-to-date', 'Age', 'Type', 'Cluster-IP']),
          rows: [],
        },
        multiKindColumnSources: (name: string) =>
          name === 'Name' || name === 'Age' ? 2 : 1,
        pagedMultiKindRows: [
          { name: 'web', namespace: 'shop', cells: ['web', '3/3', '3', '5d', '', ''], source: 'apps/v1/deployments' },
        ],
      }),
    })

    const headers = [...document.querySelectorAll('thead th')].map((th) => th.textContent?.trim())
    // Shared by both kinds — kept.
    expect(headers).toContain('Name')
    expect(headers).toContain('Age')
    // Filled by one kind only — hidden, and one click away in the column menu.
    expect(headers).not.toContain('Up-to-date')
    expect(headers).not.toContain('Cluster-IP')
  })

  it('hides nothing when a single kind is chosen', () => {
    // With one kind there is no majority to be in a minority of, and the view
    // must behave exactly as that kind's own list does.
    preferences.multiKindSelection = { dev: ['apps/v1/deployments'] }
    const columns = (names: string[]) => names.map((name) => ({ name }))
    render(MultiKindView, {
      session: session({
        multiKindTable: { columns: columns(['Name', 'Ready', 'Up-to-date', 'Age']), rows: [] },
        multiKindColumnSources: () => 1,
        pagedMultiKindRows: [
          { name: 'web', namespace: 'shop', cells: ['web', '3/3', '3', '5d'], source: 'apps/v1/deployments' },
        ],
      }),
    })

    const headers = [...document.querySelectorAll('thead th')].map((th) => th.textContent?.trim())
    expect(headers).toContain('Up-to-date')
  })
})

describe('MultiKindView — keeping a set', () => {
  beforeEach(() => {
    preferences.pinnedKindSets = []
  })

  it('offers to keep a set once there is a combination to keep', () => {
    preferences.multiKindSelection = { dev: ['apps/v1/deployments', 'core/v1/services'] }
    render(MultiKindView, { session: session() })

    expect(screen.getByText('Keep this set')).toBeTruthy()
  })

  it('does not offer it for a single kind', () => {
    // One kind is not a combination, and offering to name it would teach the
    // wrong idea about what the shelf is for.
    preferences.multiKindSelection = { dev: ['apps/v1/deployments'] }
    render(MultiKindView, { session: session() })

    expect(screen.queryByText('Keep this set')).toBeNull()
  })

  it('keeps the chosen kinds under the typed name', async () => {
    preferences.multiKindSelection = { dev: ['apps/v1/deployments', 'core/v1/services'] }
    render(MultiKindView, { session: session() })

    await fireEvent.click(screen.getByText('Keep this set'))
    await fireEvent.input(screen.getByTestId('kind-set-name'), {
      target: { value: 'Notification stack' },
    })
    await fireEvent.click(screen.getByLabelText('Keep this set'))

    expect(preferences.pinnedKindSets).toHaveLength(1)
    expect(preferences.pinnedKindSets[0].name).toBe('Notification stack')
    expect(preferences.pinnedKindSets[0].kinds).toEqual([
      'apps/v1/deployments',
      'core/v1/services',
    ])
  })

  it('stops offering once this set is already kept', () => {
    // Regardless of the order the kinds were added in — see kindSetMatches.
    preferences.multiKindSelection = { dev: ['core/v1/services', 'apps/v1/deployments'] }
    preferences.pinnedKindSets = [
      { id: 'x', name: 'Stack', kinds: ['apps/v1/deployments', 'core/v1/services'] },
    ]
    render(MultiKindView, { session: session() })

    expect(screen.queryByText('Keep this set')).toBeNull()
  })
})
