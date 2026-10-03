import { describe, expect, it } from 'vitest'
import { badgeFor, indexFindings } from './findingsOverlay'

const findings = [
  {
    id: 'crashloop',
    severity: 'critical',
    title: 'Pods crash-looping',
    subjects: [
      { kind: 'Pod', namespace: 'shop', name: 'web-1' },
      { kind: 'Pod', namespace: 'shop', name: 'web-2' },
    ],
  },
  {
    id: 'replicas',
    severity: 'warning',
    title: 'Deployment short of replicas',
    subjects: [{ kind: 'Deployment', namespace: 'shop', name: 'web' }],
  },
  { id: 'empty', severity: 'warning', title: 'Names nothing', subjects: null },
]

describe('findings overlay', () => {
  const index = indexFindings(findings)

  it('badges an object a finding names', () => {
    const badge = badgeFor(index, [{ apiKind: 'Pod', namespace: 'shop', name: 'web-1' }])!
    expect(badge.count).toBe(1)
    expect(badge.severity).toBe('critical')
    expect(badge.findings[0].id).toBe('crashloop')
  })

  it('counts a finding once for a set, however many members it names, worst first', () => {
    const badge = badgeFor(index, [
      { apiKind: 'Pod', namespace: 'shop', name: 'web-1' },
      { apiKind: 'Pod', namespace: 'shop', name: 'web-2' },
      { apiKind: 'Deployment', namespace: 'shop', name: 'web' },
    ])!
    expect(badge.count).toBe(2)
    expect(badge.findings.map((f) => f.id)).toEqual(['crashloop', 'replicas'])
  })

  it('matches on kind, namespace and name together', () => {
    expect(badgeFor(index, [{ apiKind: 'Pod', namespace: 'other', name: 'web-1' }])).toBeNull()
    expect(badgeFor(index, [{ apiKind: 'Service', namespace: 'shop', name: 'web' }])).toBeNull()
  })

  it('is empty without findings', () => {
    expect(badgeFor(indexFindings(null), [{ apiKind: 'Pod', namespace: 'shop', name: 'web-1' }])).toBeNull()
  })
})

describe('findings on a backend summary', () => {
  const crash = [
    {
      id: 'crash',
      severity: 'critical',
      title: 'Pods crash-looping',
      subjects: [
        { kind: 'Pod', namespace: 'shop', name: 'web-api-5f9-x1' },
        // A pod NOT named after its owner — created by an operator with a
        // name of its own. Membership finds it; a name prefix would not.
        { kind: 'Pod', namespace: 'shop', name: 'standalone-worker' },
      ],
    },
  ]
  const summaries = [
    { id: 'fold/rs-web/Pod', namespace: 'shop', members: ['web-7d4-a2', 'standalone-worker'] },
    { id: 'fold/rs-web-api/Pod', namespace: 'shop', members: ['web-api-5f9-x1'] },
    // Same pod name, another namespace: not the same pod.
    { id: 'fold/rs-other/Pod', namespace: 'other', members: ['web-api-5f9-x1'] },
  ]
  const member = (id: string, namespace = 'shop') => [{ id, apiKind: '', namespace, name: '' }]

  it('lands a finding about a summarised pod on the box that folded it, by membership', () => {
    const index = indexFindings(crash, summaries)
    expect(badgeFor(index, member('fold/rs-web/Pod'))?.count).toBe(1)
    expect(badgeFor(index, member('fold/rs-web-api/Pod'))?.count).toBe(1)
    expect(badgeFor(index, member('fold/rs-other/Pod', 'other'))).toBeNull()
  })

  it('does not guess from names: a pod that is not a member does not land', () => {
    // web-7d4-zz looks like one of fold/rs-web's pods, and is not listed as one.
    const index = indexFindings(
      [{ id: 'x', severity: 'warning', title: 't', subjects: [{ kind: 'Pod', namespace: 'shop', name: 'web-7d4-zz' }] }],
      summaries,
    )
    expect(badgeFor(index, member('fold/rs-web/Pod'))).toBeNull()
  })
})
