import { describe, expect, it } from 'vitest'

import { pinnedFirst, visibleClusters } from './clusterPins'

const clusters = [{ id: 'a' }, { id: 'b' }, { id: 'c' }, { id: 'd' }]

describe('pinnedFirst', () => {
  it('lifts the pinned ones and keeps everything in its own order', () => {
    // Stability is the assertion. The order underneath is the kubeconfig's,
    // and twenty contexts named alike must not shuffle when one is pinned.
    expect(pinnedFirst(clusters, ['c', 'b']).map((item) => item.id)).toEqual(['b', 'c', 'a', 'd'])
  })

  it('changes nothing when nothing is pinned', () => {
    expect(pinnedFirst(clusters, []).map((item) => item.id)).toEqual(['a', 'b', 'c', 'd'])
  })

  it('ignores a pin for a cluster that is no longer in the kubeconfig', () => {
    // A context can be removed from the kubeconfig with its pin still on
    // record; that is a stale preference, not a missing cluster.
    expect(pinnedFirst(clusters, ['gone', 'd']).map((item) => item.id)).toEqual([
      'd',
      'a',
      'b',
      'c',
    ])
  })

  it('returns a new list rather than sorting the one it was handed', () => {
    const input = [{ id: 'a' }, { id: 'b' }]
    const sorted = pinnedFirst(input, ['b'])
    expect(input.map((item) => item.id)).toEqual(['a', 'b'])
    expect(sorted.map((item) => item.id)).toEqual(['b', 'a'])
  })
})

describe('visibleClusters', () => {
  it('narrows to the pinned ones when the filter is on', () => {
    expect(visibleClusters(clusters, ['c', 'a'], true).map((item) => item.id)).toEqual(['a', 'c'])
  })

  it('shows everything, pinned first, when the filter is off', () => {
    expect(visibleClusters(clusters, ['c'], false).map((item) => item.id)).toEqual([
      'c',
      'a',
      'b',
      'd',
    ])
  })

  it('shows everything when the filter is on and nothing is pinned', () => {
    // THE SCREEN THIS PREVENTS. A lit toggle over an empty page reads as
    // broken to somebody who pressed it before pinning anything — and "you
    // have no clusters" is not the answer to "show me my pinned ones".
    expect(visibleClusters(clusters, [], true).map((item) => item.id)).toEqual([
      'a',
      'b',
      'c',
      'd',
    ])
  })

  it('keeps the kubeconfig order among the pinned ones', () => {
    expect(visibleClusters(clusters, ['d', 'b'], true).map((item) => item.id)).toEqual(['b', 'd'])
  })
})
