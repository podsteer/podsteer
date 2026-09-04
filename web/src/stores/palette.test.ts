import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

// Mocked the same way session.test.ts mocks it: getManifest rejects
// harmlessly (openDetail must still resolve without a live cluster behind
// it), and listTable is a plain vi.fn() so each test controls what the
// palette's on-demand kind search resolves or rejects with.
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
import { palette } from './palette.svelte'
import type { Cluster, Node, Pod, ResourceKind, ResourceTable } from '$lib/api/client'

const cluster = { id: 'dev', name: 'dev', defaultNamespace: 'default' } as unknown as Cluster

// Written out locally rather than imported from `$lib/api/client`, so the
// on-demand search tests' expectations are independent of the module under
// test's own constant.
const ALL_NAMESPACES_FOR_TEST = ''

const podsKind = {
  id: RICH_KIND_IDS.pods,
  title: 'Pods',
  singular: 'Pod',
  kind: 'Pod',
  group: '',
  namespaced: true,
} as ResourceKind

const deploymentsKind = {
  id: 'apps/v1/deployments',
  title: 'Deployments',
  singular: 'Deployment',
  kind: 'Deployment',
  group: 'apps',
  namespaced: true,
} as ResourceKind

function makeSession(): ClusterSession {
  const session = new ClusterSession(cluster)
  // Overridden rather than left to whatever the constructor read from
  // `preferences` (a real, persistent singleton) — the on-demand search
  // tests below assert exactly which namespace a ListTable call was made
  // with, and that has to be deterministic regardless of test order.
  session.namespace = ALL_NAMESPACES_FOR_TEST
  session.kinds = [podsKind, deploymentsKind]
  session.selectedKindId = RICH_KIND_IDS.pods
  session.pods = [
    { name: 'web-1', namespace: 'prod' } as Pod,
    { name: 'web-2', namespace: 'prod' } as Pod,
  ]
  return session
}

function table(rows: { name: string; namespace: string }[]): ResourceTable {
  return {
    kindId: deploymentsKind.id,
    title: 'Deployments',
    namespaced: true,
    columns: [],
    rows: rows.map((row) => ({ name: row.name, namespace: row.namespace, cells: [] })),
  } as unknown as ResourceTable
}

beforeEach(() => {
  listTable.mockReset()
  palette.sync(null, [], vi.fn())
  palette.hide()
})

afterEach(() => {
  palette.hide()
  palette.sync(null, [], vi.fn())
})

describe('open/close', () => {
  it('resets the query and selection on every show()', () => {
    palette.show()
    palette.setQuery('nginx')
    palette.selectedIndex = 3

    palette.hide()
    palette.show()

    expect(palette.query).toBe('')
    expect(palette.selectedIndex).toBe(0)
  })
})

describe('grouping with no active cluster', () => {
  it('offers only global commands from the picker', () => {
    palette.sync(null, [], vi.fn())
    palette.show()
    palette.setQuery('settings')

    const commands = palette.groups.find((g) => g.name === 'Commands')
    expect(commands?.entries.some((e) => e.title === 'Open Settings')).toBe(true)
    expect(palette.groups.find((g) => g.name === 'Kinds')).toBeUndefined()
    expect(palette.groups.find((g) => g.name === 'Objects')).toBeUndefined()
  })
})

describe('the Objects group', () => {
  it('searches the current view already in memory, with no ListTable call', () => {
    const session = makeSession()
    palette.sync(session, [{ id: 'dev' }], vi.fn())
    palette.show()

    palette.setQuery('web-1')

    const objects = palette.groups.find((g) => g.name === 'Objects')
    expect(objects?.entries.map((e) => e.title)).toEqual(['web-1'])
    expect(listTable).not.toHaveBeenCalled()
  })

  it('opens the matched object through session.openObject', async () => {
    const session = makeSession()
    const openObject = vi.spyOn(session, 'openObject').mockResolvedValue()
    palette.sync(session, [{ id: 'dev' }], vi.fn())
    palette.show()
    palette.setQuery('web-1')

    await palette.groups.find((g) => g.name === 'Objects')?.entries[0].run()

    expect(openObject).toHaveBeenCalledWith(RICH_KIND_IDS.pods, 'web-1', 'prod', true)
  })
})

describe('the Kinds group', () => {
  it('ranks kinds by fuzzy match and can navigate to one', async () => {
    const session = makeSession()
    const selectKind = vi.spyOn(session, 'selectKind').mockResolvedValue()
    palette.sync(session, [{ id: 'dev' }], vi.fn())
    palette.show()
    palette.setQuery('dp')

    const kinds = palette.groups.find((g) => g.name === 'Kinds')
    expect(kinds?.entries.map((e) => e.title)).toEqual(['Deployments'])

    await kinds!.entries[0].run()
    expect(selectKind).toHaveBeenCalledWith(deploymentsKind.id)
  })
})

describe('the on-demand kind search', () => {
  it('makes exactly ONE ListTable call for a kind scoped by the kind: pill, after the debounce', async () => {
    vi.useFakeTimers()
    try {
      const session = makeSession()
      listTable.mockResolvedValue(table([{ name: 'web', namespace: 'prod' }]))
      palette.sync(session, [{ id: 'dev' }], vi.fn())
      palette.show()

      palette.setQuery('kind:deployments w')
      // A burst of further typing inside the same debounce window must not
      // fire a second request — this is what makes the debounce real rather
      // than cosmetic.
      palette.setQuery('kind:deployments we')
      palette.setQuery('kind:deployments web')

      expect(listTable).not.toHaveBeenCalled()

      await vi.advanceTimersByTimeAsync(200)

      expect(listTable).toHaveBeenCalledTimes(1)
      expect(listTable).toHaveBeenCalledWith('dev', deploymentsKind.id, ALL_NAMESPACES_FOR_TEST)

      const objects = palette.groups.find((g) => g.name === 'Objects')
      expect(objects?.entries.map((e) => e.title)).toEqual(['web'])
    } finally {
      vi.useRealTimers()
    }
  })

  it('reuses the cached result for a repeated scope within the same palette instance', async () => {
    vi.useFakeTimers()
    try {
      const session = makeSession()
      listTable.mockResolvedValue(table([{ name: 'web', namespace: 'prod' }]))
      palette.sync(session, [{ id: 'dev' }], vi.fn())
      palette.show()

      palette.setQuery('kind:deployments')
      await vi.advanceTimersByTimeAsync(200)
      expect(listTable).toHaveBeenCalledTimes(1)

      // Un-scope, then scope back to the same kind — still one call total.
      palette.setQuery('')
      palette.setQuery('kind:deployments')
      await vi.advanceTimersByTimeAsync(200)

      expect(listTable).toHaveBeenCalledTimes(1)
    } finally {
      vi.useRealTimers()
    }
  })

  it('never caches a failure — the next scope attempt retries', async () => {
    vi.useFakeTimers()
    try {
      const session = makeSession()
      listTable.mockRejectedValueOnce(new Error('[forbidden] nope'))
      listTable.mockResolvedValueOnce(table([{ name: 'web', namespace: 'prod' }]))
      palette.sync(session, [{ id: 'dev' }], vi.fn())
      palette.show()

      palette.setQuery('kind:deployments')
      await vi.advanceTimersByTimeAsync(200)
      expect(listTable).toHaveBeenCalledTimes(1)
      expect(palette.groups.find((g) => g.name === 'Objects')).toBeUndefined()

      // Re-typing the same scope retries rather than reusing the refusal.
      palette.setQuery('')
      palette.setQuery('kind:deployments')
      await vi.advanceTimersByTimeAsync(200)

      expect(listTable).toHaveBeenCalledTimes(2)
      expect(palette.groups.find((g) => g.name === 'Objects')?.entries.map((e) => e.title)).toEqual([
        'web',
      ])
    } finally {
      vi.useRealTimers()
    }
  })

  it('makes no request at all when the scoped kind is the one already on screen', async () => {
    vi.useFakeTimers()
    try {
      const session = makeSession()
      palette.sync(session, [{ id: 'dev' }], vi.fn())
      palette.show()

      // Pods is already session.selectedKindId — scoping to it must read the
      // in-memory rows, not fetch them again.
      palette.setQuery('kind:core/v1/pods web')
      await vi.advanceTimersByTimeAsync(200)

      expect(listTable).not.toHaveBeenCalled()
      expect(palette.groups.find((g) => g.name === 'Objects')?.entries.map((e) => e.title)).toEqual([
        'web-1',
        'web-2',
      ])
    } finally {
      vi.useRealTimers()
    }
  })
})

describe('typing a kind name then a space, without the pill syntax', () => {
  it('scopes the object search the same way the kind: pill does', async () => {
    vi.useFakeTimers()
    try {
      const session = makeSession()
      listTable.mockResolvedValue(table([{ name: 'web', namespace: 'prod' }]))
      palette.sync(session, [{ id: 'dev' }], vi.fn())
      palette.show()

      palette.setQuery('Deployments web')
      await vi.advanceTimersByTimeAsync(200)

      expect(listTable).toHaveBeenCalledWith('dev', deploymentsKind.id, ALL_NAMESPACES_FOR_TEST)
    } finally {
      vi.useRealTimers()
    }
  })

  it('does not scope while the kind name is still being typed, with no space yet', () => {
    const session = makeSession()
    palette.sync(session, [{ id: 'dev' }], vi.fn())
    palette.show()

    palette.setQuery('Deploy')

    expect(listTable).not.toHaveBeenCalled()
    // Still unscoped: the Kinds group offers it as a suggestion instead.
    expect(palette.groups.find((g) => g.name === 'Kinds')?.entries.map((e) => e.title)).toEqual([
      'Deployments',
    ])
  })
})

describe('Tab-accepting a Kinds suggestion', () => {
  it('rewrites the query as a kind: pill', () => {
    const session = makeSession()
    palette.sync(session, [{ id: 'dev' }], vi.fn())
    palette.show()
    palette.setQuery('dp')

    palette.acceptKindPill()

    expect(palette.query).toBe(`kind:${deploymentsKind.id} `)
    expect(palette.parsed.kind).toBe(deploymentsKind.id)
  })

  it('does nothing once a kind: pill is already present', () => {
    const session = makeSession()
    palette.sync(session, [{ id: 'dev' }], vi.fn())
    palette.show()
    palette.setQuery('kind:core/v1/pods')

    palette.acceptKindPill()

    expect(palette.query).toBe('kind:core/v1/pods')
  })
})

describe('">" restricts the palette to commands', () => {
  it('shows only the Commands group, even with kinds and objects that would otherwise match', () => {
    const session = makeSession()
    palette.sync(session, [{ id: 'dev' }], vi.fn())
    palette.show()

    palette.setQuery('> refresh')

    expect(palette.groups.map((g) => g.name)).toEqual(['Commands'])
    expect(palette.groups[0].entries.map((e) => e.title)).toEqual(['Refresh'])
  })
})

describe('keyboard navigation', () => {
  it('wraps around both ends', () => {
    const session = makeSession()
    palette.sync(session, [{ id: 'dev' }], vi.fn())
    palette.show()
    palette.setQuery('web')

    const count = palette.flatEntries.length
    expect(count).toBeGreaterThan(0)

    palette.selectedIndex = 0
    palette.moveSelection(-1)
    expect(palette.selectedIndex).toBe(count - 1)

    palette.moveSelection(1)
    expect(palette.selectedIndex).toBe(0)
  })

  it('runs the highlighted entry and closes the palette', async () => {
    const session = makeSession()
    const openObject = vi.spyOn(session, 'openObject').mockResolvedValue()
    palette.sync(session, [{ id: 'dev' }], vi.fn())
    palette.show()
    palette.setQuery('web-1')
    palette.selectedIndex = 0

    palette.runSelected()
    await Promise.resolve()

    expect(palette.open).toBe(false)
    expect(openObject).toHaveBeenCalled()
  })
})

describe('result limits', () => {
  it('caps every group at eight entries', () => {
    const session = makeSession()
    session.pods = Array.from({ length: 20 }, (_, i) => ({ name: `web-${i}`, namespace: 'prod' }) as Pod)
    palette.sync(session, [{ id: 'dev' }], vi.fn())
    palette.show()
    palette.setQuery('web')

    const objects = palette.groups.find((g) => g.name === 'Objects')
    expect(objects?.entries.length).toBe(8)
  })
})

describe('the Clusters group', () => {
  it('offers every OTHER open tab, and focuses it when chosen', async () => {
    const focusCluster = vi.fn()
    const session = makeSession()
    palette.sync(session, [{ id: 'dev' }, { id: 'staging' }], focusCluster)
    palette.show()
    palette.setQuery('stag')

    const clusters = palette.groups.find((g) => g.name === 'Clusters')
    expect(clusters?.entries.map((e) => e.title)).toEqual(['staging'])

    await clusters!.entries[0].run()
    expect(focusCluster).toHaveBeenCalledWith('staging')
  })
})
