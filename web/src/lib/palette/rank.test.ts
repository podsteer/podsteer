import { describe, expect, it } from 'vitest'

import { fuzzyMatch, rank } from './rank'

describe('fuzzyMatch', () => {
  it('matches a subsequence typed out of the word, in order', () => {
    // "dp" as D...p, not adjacent — this is the whole point of a fuzzy
    // scorer over a plain substring one.
    expect(fuzzyMatch('dp', 'Deployments')).not.toBeNull()
  })

  it('is case-insensitive', () => {
    expect(fuzzyMatch('DP', 'deployments')).not.toBeNull()
  })

  it('returns null when a query character is missing from the target', () => {
    // Neither "y" nor "z" appears in "Deployments" at all.
    expect(fuzzyMatch('xyz', 'Deployments')).toBeNull()
  })

  it('returns null when the characters are present but out of order', () => {
    // "pd" needs a 'p' before a 'd' — "Deployments" has its 'd' first.
    expect(fuzzyMatch('pd', 'Deployments')).toBeNull()
  })

  it('returns a zero score and no indices for an empty query', () => {
    expect(fuzzyMatch('', 'anything')).toEqual({ score: 0, indices: [] })
  })

  it('returns null for an empty target with a non-empty query', () => {
    expect(fuzzyMatch('a', '')).toBeNull()
  })

  it('scores an exact match higher than a mere prefix', () => {
    const exact = fuzzyMatch('pod', 'Pod')
    const prefix = fuzzyMatch('pod', 'PodDisruptionBudget')
    expect(exact).not.toBeNull()
    expect(prefix).not.toBeNull()
    expect(exact!.score).toBeGreaterThan(prefix!.score)
  })

  it('scores a prefix match higher than the same letters scattered later', () => {
    const prefix = fuzzyMatch('con', 'ConfigMap')
    // "con" as a scattered subsequence of "ReplicationController": R-e-p-l-i-
    // c-a-t-i-o-n-C-o-n-t-r-o-l-l-e-r has a 'c', later an 'o', later an 'n'.
    const scattered = fuzzyMatch('con', 'ReplicationController')
    expect(prefix).not.toBeNull()
    expect(scattered).not.toBeNull()
    expect(prefix!.score).toBeGreaterThan(scattered!.score)
  })

  it('rewards a word-boundary match — a camelCase capital — over a mid-word one', () => {
    // "cm": ConfigMap's C(0) and M(6, a camelCase transition) are both
    // boundaries. CustomResourceDefinition's C(0) is a boundary but its only
    // 'm' (in "Custom") is mid-word.
    const boundary = fuzzyMatch('cm', 'ConfigMap')
    const midWord = fuzzyMatch('cm', 'CustomResourceDefinition')
    expect(boundary).not.toBeNull()
    expect(midWord).not.toBeNull()
    expect(boundary!.score).toBeGreaterThan(midWord!.score)
  })

  it('rewards a consecutive run over the same letters scattered apart', () => {
    const consecutive = fuzzyMatch('dep', 'Deployments')
    const scattered = fuzzyMatch('dep', 'DaemonSetsReplicas')
    expect(consecutive).not.toBeNull()
    expect(scattered).not.toBeNull()
    expect(consecutive!.score).toBeGreaterThan(scattered!.score)
  })

  it('reports the matched indices', () => {
    expect(fuzzyMatch('dp', 'Deployments')?.indices).toEqual([0, 2])
  })
})

describe('rank', () => {
  it('ranks "dp" as Deployments over other workload kinds', () => {
    // DaemonSets has no 'p' at all; StatefulSets has neither letter; Pods
    // has a 'd' but nothing after it — Deployments is the only one where
    // "dp" is a genuine in-order subsequence.
    const candidates = [
      { label: 'Pods' },
      { label: 'Deployments' },
      { label: 'DaemonSets' },
      { label: 'StatefulSets' },
    ]
    expect(rank('dp', candidates).map((c) => c.label)).toEqual(['Deployments'])
  })

  it('ranks "kube-sys" as the kube-system namespace over its siblings', () => {
    const candidates = [
      { label: 'default' },
      { label: 'kube-public' },
      { label: 'kube-system' },
      { label: 'kube-node-lease' },
    ]
    expect(rank('kube-sys', candidates).map((c) => c.label)).toEqual(['kube-system'])
  })

  it('matches nothing for a query that is not a subsequence of anything', () => {
    const candidates = [{ label: 'Pods' }, { label: 'Deployments' }, { label: 'Namespaces' }]
    expect(rank('xqz', candidates)).toEqual([])
  })

  it('falls back to a keyword when the label itself does not match', () => {
    // The label "Ingresses" has no 'n','e','t' in that order after its own
    // letters run out — the API group keyword is what "networking" finds.
    const candidates = [
      { label: 'Ingresses', keywords: ['networking.k8s.io'] },
      { label: 'Secrets', keywords: ['core'] },
    ]
    expect(rank('networking', candidates).map((c) => c.label)).toEqual(['Ingresses'])
  })

  it('breaks a tied score by recency, then alphabetically', () => {
    // An empty query ties every candidate at score 0.
    const candidates = [
      { label: 'web-3' },
      { label: 'web-1', recency: 5 },
      { label: 'web-2', recency: 5 },
    ]
    expect(rank('', candidates).map((c) => c.label)).toEqual(['web-1', 'web-2', 'web-3'])
  })

  it('preserves nothing about input order beyond the tie-break rules', () => {
    const candidates = [{ label: 'zeta' }, { label: 'alpha' }, { label: 'mid' }]
    expect(rank('', candidates).map((c) => c.label)).toEqual(['alpha', 'mid', 'zeta'])
  })
})
