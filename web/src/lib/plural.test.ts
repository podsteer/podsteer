import { describe, expect, it } from 'vitest'

import { countedKind, pluralKind } from './plural'

describe('naming a kind for how many there are', () => {
  it('agrees with the count', () => {
    // "Pod 1" and "Pod 4" are wrong in the same place: a count and a noun
    // that disagree read as a label somebody forgot to finish.
    expect(countedKind('Pod', 1)).toBe('Pod')
    expect(countedKind('Pod', 4)).toBe('Pods')
    expect(countedKind('Pod', 0)).toBe('Pods')
  })

  it('handles the endings Kubernetes actually uses', () => {
    // Kubernetes names its kinds in ordinary singular English nouns, which is
    // why these come out right without a table of them.
    expect(pluralKind('Deployment')).toBe('Deployments')
    expect(pluralKind('Ingress')).toBe('Ingresses')
    expect(pluralKind('NetworkPolicy')).toBe('NetworkPolicies')
    expect(pluralKind('StorageClass')).toBe('StorageClasses')
    expect(pluralKind('CronJob')).toBe('CronJobs')
    expect(pluralKind('PersistentVolumeClaim')).toBe('PersistentVolumeClaims')
  })

  it('does not turn a vowel-y into ies', () => {
    // Gateway, not Gatewaies. The rule is a CONSONANT before the y.
    expect(pluralKind('Gateway')).toBe('Gateways')
  })

  it('leaves a kind that is already plural alone', () => {
    // Endpoints is the one in the built-in set, and "Endpointses" is not a
    // word anybody wants in a navigator.
    expect(pluralKind('Endpoints')).toBe('Endpoints')
    expect(countedKind('Endpoints', 3)).toBe('Endpoints')
  })
})
