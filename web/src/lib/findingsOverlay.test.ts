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
        { kind: 'Pod', namespace: 'shop', name: 'web-7d4-a2' },
      ],
    },
  ]
  const summaries = [
    { id: 'fold/rs-web/Pod', namespace: 'shop', ownerName: 'web-7d4' },
    { id: 'fold/rs-web-api/Pod', namespace: 'shop', ownerName: 'web-api-5f9' },
    { id: 'fold/rs-other/Pod', namespace: 'other', ownerName: 'web-7d4' },
  ]

  it('lands a finding about a summarised pod on the box standing for it', () => {
    const index = indexFindings(crash, summaries)
    const member = (id: string) => [{ id, apiKind: '', namespace: 'shop', name: '' }]
    expect(badgeFor(index, member('fold/rs-web/Pod'))?.count).toBe(1)
    expect(badgeFor(index, member('fold/rs-web-api/Pod'))?.count).toBe(1)
    // Same owner name, other namespace: not the same pods.
    expect(badgeFor(index, [{ id: 'fold/rs-other/Pod', apiKind: '', namespace: 'other', name: '' }])).toBeNull()
  })
})
