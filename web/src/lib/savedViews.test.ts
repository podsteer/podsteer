import { describe, expect, it } from 'vitest'

import {
  cleanViewName,
  describeView,
  MAX_SAVED_VIEWS,
  MAX_VIEW_NAME,
  sanitiseViews,
  savedViewId,
  viewMatches,
  type SavedView,
} from './savedViews'

const view = (over: Partial<SavedView> = {}): SavedView => ({
  id: 'crashing-pods',
  name: 'Crashing pods',
  kindId: 'core/v1/pods',
  namespaces: ['kube-system'],
  search: 're:crash',
  statusFilters: ['crashing'],
  ...over,
})

describe('savedViewId', () => {
  it('reads as its name, because it is what an exported file shows', () => {
    expect(savedViewId('Crashing pods', [])).toBe('crashing-pods')
    expect(savedViewId('  Prod — API errors!  ', [])).toBe('prod-api-errors')
  })

  it('never collides, however many share a name', () => {
    const taken = ['crashing-pods']
    const second = savedViewId('Crashing pods', taken)
    taken.push(second)
    const third = savedViewId('Crashing pods', taken)

    expect(second).toBe('crashing-pods-2')
    expect(third).toBe('crashing-pods-3')
  })

  it('falls back to a word for a name that slugs to nothing', () => {
    // A name in a script this cannot transliterate is still a name somebody
    // typed, and two of them are still two views.
    const first = savedViewId('日本語', [])
    const second = savedViewId('!!!', [first])

    expect(first).toBe('view')
    expect(second).toBe('view-2')
  })
})

describe('cleanViewName', () => {
  it('collapses whitespace and cuts what a row cannot hold', () => {
    expect(cleanViewName('  crashing   pods ')).toBe('crashing pods')
    expect(cleanViewName('x'.repeat(200))).toHaveLength(MAX_VIEW_NAME)
  })

  it('leaves an empty name empty, for the caller to refuse', () => {
    expect(cleanViewName('   ')).toBe('')
  })
})

describe('viewMatches', () => {
  it('ticks the view that is on screen', () => {
    expect(
      viewMatches(view(), {
        kindId: 'core/v1/pods',
        namespaces: ['kube-system'],
        search: 're:crash',
        statusFilters: ['crashing'],
      }),
    ).toBe(true)
  })

  it('ignores the order the chips were pressed in', () => {
    // Pressing "pending" then "crashing" is the same view as the other way
    // round; a menu that failed to tick it would look broken.
    expect(
      viewMatches(view({ statusFilters: ['crashing', 'pending'] }), {
        kindId: 'core/v1/pods',
        namespaces: ['kube-system'],
        search: 're:crash',
        statusFilters: ['pending', 'crashing'],
      }),
    ).toBe(true)
  })

  it('compares the namespaces as a set', () => {
    const current = {
      kindId: 'core/v1/pods',
      namespaces: ['keda', 'billing'],
      search: 're:crash',
      statusFilters: ['crashing'],
    }
    expect(viewMatches(view({ namespaces: ['billing', 'keda'] }), current)).toBe(true)
    expect(viewMatches(view({ namespaces: ['billing'] }), current)).toBe(false)
    expect(viewMatches(view({ namespaces: [] }), current)).toBe(false)
  })

  it('does not tick a view that differs in any of the four things it holds', () => {
    const current = {
      kindId: 'core/v1/pods',
      namespaces: ['kube-system'],
      search: 're:crash',
      statusFilters: ['crashing'],
    }

    expect(viewMatches(view({ kindId: 'apps/v1/deployments' }), current)).toBe(false)
    expect(viewMatches(view({ namespaces: ['default'] }), current)).toBe(false)
    expect(viewMatches(view({ search: '' }), current)).toBe(false)
    expect(viewMatches(view({ statusFilters: [] }), current)).toBe(false)
    expect(viewMatches(view({ statusFilters: ['crashing', 'pending'] }), current)).toBe(false)
  })
})

describe('describeView', () => {
  it('says what it selects, in the order somebody reads it', () => {
    expect(describeView(view(), 'Pods')).toBe(
      'Pods · kube-system · re:crash · crashing',
    )
  })

  it('says all namespaces for an empty set', () => {
    expect(describeView(view({ namespaces: [], search: '', statusFilters: [] }), 'Pods')).toBe(
      'Pods · all namespaces',
    )
  })

  it('names a set by the trigger rule, or by a label function given', () => {
    expect(describeView(view({ namespaces: ['keda', 'billing', 'shop'], search: '', statusFilters: [] }), 'Pods')).toBe(
      'Pods · billing +2',
    )
    expect(
      describeView(view({ namespaces: ['a', 'b', 'c', 'd'], search: '', statusFilters: [] }), 'Pods'),
    ).toBe('Pods · 4 namespaces')
    expect(describeView(view({ search: '', statusFilters: [] }), 'Pods', (names) => names.join('+'))).toBe(
      'Pods · kube-system',
    )
  })

  it('counts chips rather than listing them once there are several', () => {
    expect(
      describeView(view({ search: '', statusFilters: ['crashing', 'pending'] }), 'Pods'),
    ).toContain('2 status filters')
  })

  it('falls back to the kind id for a kind this cluster does not serve', () => {
    // The list is not keyed by cluster, so a view can name a CRD that only
    // one of the open clusters has. Its id is still a true answer.
    expect(describeView(view({ kindId: 'acme.io/v1/widgets' }), '')).toContain(
      'acme.io/v1/widgets',
    )
  })
})

describe('sanitiseViews', () => {
  it('keeps what is whole and drops what cannot be applied', () => {
    const kept = sanitiseViews([
      view(),
      { name: 'No kind' },
      { kindId: 'core/v1/pods' },
      null,
      'nonsense',
    ])

    expect(kept).toHaveLength(1)
    expect(kept[0].name).toBe('Crashing pods')
  })

  it('fills in what is merely missing rather than dropping the view', () => {
    const [only] = sanitiseViews([{ name: 'Bare', kindId: 'core/v1/pods' }])

    expect(only).toMatchObject({ id: 'bare', namespaces: [], search: '', statusFilters: [] })
  })

  it('migrates the single namespace every view held before the filter was a set', () => {
    const [named, all, both] = sanitiseViews([
      { name: 'Old', kindId: 'core/v1/pods', namespace: 'kube-system' },
      { name: 'Old all', kindId: 'core/v1/pods', namespace: '' },
      { name: 'New', kindId: 'core/v1/pods', namespaces: ['shop', 'keda', 'shop', 7], namespace: 'ignored' },
    ])

    expect(named.namespaces).toEqual(['kube-system'])
    expect(all.namespaces).toEqual([])
    // The new field wins, sorted and deduplicated, and the old one is never kept.
    expect(both.namespaces).toEqual(['keda', 'shop'])
    for (const view of [named, all, both]) expect(view).not.toHaveProperty('namespace')
  })

  it('gives two entries with one id two ids, so neither shadows the other', () => {
    const both = sanitiseViews([
      { id: 'dupe', name: 'First', kindId: 'core/v1/pods' },
      { id: 'dupe', name: 'Second', kindId: 'core/v1/pods' },
    ])

    expect(both).toHaveLength(2)
    expect(both[0].id).not.toBe(both[1].id)
  })

  it('drops chips that are not strings rather than the view holding them', () => {
    const [only] = sanitiseViews([
      { name: 'Odd', kindId: 'core/v1/pods', statusFilters: ['crashing', 7, null] },
    ])
    expect(only.statusFilters).toEqual(['crashing'])
  })

  it('stops at the ceiling, so an imported file cannot grow the menu without bound', () => {
    const many = Array.from({ length: MAX_SAVED_VIEWS + 10 }, (_, index) => ({
      name: `View ${index}`,
      kindId: 'core/v1/pods',
    }))

    expect(sanitiseViews(many)).toHaveLength(MAX_SAVED_VIEWS)
  })

  it('reads anything that is not a list as no views at all', () => {
    expect(sanitiseViews(undefined)).toEqual([])
    expect(sanitiseViews({ views: [] })).toEqual([])
  })
})
