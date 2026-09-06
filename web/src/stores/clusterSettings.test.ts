/**
 * The two pure rules this store carries, both of which have to agree with Go.
 *
 * `defaultClusterSettings` is what a context with no stored entry looks like,
 * and `isDefault` is what decides whether a row is worth showing for a cluster
 * whose tab is closed. Both restate a rule the Go side already applies before
 * writing — an entry equal to the defaults is dropped rather than persisted —
 * so if they drift, Settings starts listing clusters whose entries are not in
 * the file, or hides ones that are.
 */

import { describe, expect, it } from 'vitest'

import { clusterSettings, defaultClusterSettings, isDefault } from './clusterSettings.svelte'

describe('defaultClusterSettings', () => {
  it('is off, unpinned and filtered — the state in which nothing is sent', () => {
    // Every default has to be the behaviour PodSteer had before this setting
    // existed, or turning the feature on becomes something that happens to
    // somebody rather than something they chose.
    expect(defaultClusterSettings('prod')).toEqual({
      clusterId: 'prod',
      nodeHistory: false,
      metricsQueryMode: 'off',
      preferredNamespace: '',
      preferredService: '',
      fleetPolicy: 'filter',
    })
  })

  it('names no service, which is what keeps the object name off the disk', () => {
    const entry = defaultClusterSettings('prod')

    expect(entry.preferredNamespace).toBe('')
    expect(entry.preferredService).toBe('')
  })
})

describe('isDefault', () => {
  it('is true for an entry that says nothing', () => {
    expect(isDefault(defaultClusterSettings('prod'))).toBe(true)
  })

  it('is false once any single field carries a decision', () => {
    // One case per field, because a row shown for a closed tab is a row the
    // file genuinely holds — and a field left out of this test is a setting
    // an operator could make and then never find again.
    const changes = [
      { nodeHistory: true },
      { metricsQueryMode: 'manual' },
      { metricsQueryMode: 'auto' },
      { preferredNamespace: 'monitoring' },
      { preferredService: 'prometheus-operated' },
      { fleetPolicy: 'refuse' },
    ]

    for (const change of changes) {
      expect(isDefault({ ...defaultClusterSettings('prod'), ...change })).toBe(false)
    }
  })
})

describe('the store before anything is loaded', () => {
  it('reports the defaults for a cluster it has never heard of', () => {
    // The same totality domain.Settings.Cluster has in Go: a component asking
    // about a cluster mid-load must not get undefined and decide for itself
    // what an absent entry means.
    expect(clusterSettings.for('never-loaded')).toEqual(defaultClusterSettings('never-loaded'))
  })
})
