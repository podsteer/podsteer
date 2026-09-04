import { describe, expect, it } from 'vitest'

import { diffFindings, groupTimeline, objectKey, type TimelineEntry } from './timeline'

/** A finding as the diff sees it: an id and whatever hangs off it. */
const finding = (id: string) => ({ id })

const set = (...ids: string[]) => new Map(ids.map((id) => [id, finding(id)]))

const entry = (over: Partial<TimelineEntry> & { id: string }): TimelineEntry => ({
  at: 1_000,
  lastAt: 1_000,
  count: 1,
  kind: 'event',
  severity: 'info',
  title: 'BackOff',
  detail: 'Back-off restarting failed container',
  target: { kind: 'Pod', namespace: 'shop', name: 'web-1' },
  ...over,
})

describe('diffFindings', () => {
  it('reports a finding that appeared', () => {
    const diff = diffFindings(set('capacity:waste'), set('capacity:waste', 'pods:crashloop'))

    expect(diff.appeared.map((held) => held.id)).toEqual(['pods:crashloop'])
    expect(diff.cleared).toEqual([])
  })

  it('reports a finding that cleared', () => {
    const diff = diffFindings(set('capacity:waste', 'pods:crashloop'), set('capacity:waste'))

    expect(diff.appeared).toEqual([])
    expect(diff.cleared.map((held) => held.id)).toEqual(['pods:crashloop'])
  })

  it('says nothing about a finding that is simply still there', () => {
    const diff = diffFindings(set('capacity:waste'), set('capacity:waste'))

    expect(diff.appeared).toEqual([])
    expect(diff.cleared).toEqual([])
  })

  it('establishes a baseline on the first assessment without announcing it', () => {
    // A cluster that has been broken since Tuesday is not thirty things
    // happening now — the same rule ClusterSession.#adopt follows before it
    // is willing to sound an alert.
    const diff = diffFindings(null, set('capacity:waste', 'pods:crashloop'))

    expect(diff.appeared).toEqual([])
    expect(diff.cleared).toEqual([])
    expect([...(diff.next?.keys() ?? [])]).toEqual(['capacity:waste', 'pods:crashloop'])
  })

  it('does not read a failed refresh as everything clearing at once', () => {
    // THE BUG THIS GUARDS. A refresh that failed carries no findings, and
    // read as an assessment it reports every outstanding problem in the
    // cluster clearing in the same instant — which an operator reads as
    // thirty things fixing themselves while they watched.
    const previous = set('capacity:waste', 'pods:crashloop')

    const diff = diffFindings(previous, null)

    expect(diff.appeared).toEqual([])
    expect(diff.cleared).toEqual([])
    // And the baseline survives, so the NEXT successful assessment is
    // compared against the last one that was real rather than against
    // nothing.
    expect(diff.next).toBe(previous)
  })

  it('leaves the baseline unset when the very first refresh fails', () => {
    const diff = diffFindings(null, null)

    expect(diff.next).toBeNull()
  })

  it('announces an id that cleared and came back', () => {
    // What somebody watching a flapping workload needs: the second arrival is
    // news, not a repeat of the first.
    const first = diffFindings(set('capacity:waste'), set('capacity:waste', 'pods:crashloop'))
    expect(first.appeared.map((held) => held.id)).toEqual(['pods:crashloop'])

    const gone = diffFindings(first.next, set('capacity:waste'))
    expect(gone.cleared.map((held) => held.id)).toEqual(['pods:crashloop'])

    const again = diffFindings(gone.next, set('capacity:waste', 'pods:crashloop'))
    expect(again.appeared.map((held) => held.id)).toEqual(['pods:crashloop'])
  })
})

describe('groupTimeline', () => {
  it('collapses repeats into one row carrying the count and the span', () => {
    const groups = groupTimeline([
      entry({ id: 'c', at: 3_000, lastAt: 3_000, count: 2 }),
      entry({ id: 'b', at: 2_000, lastAt: 2_000, count: 5 }),
      entry({ id: 'a', at: 1_000, lastAt: 1_000, count: 3 }),
    ])

    expect(groups).toHaveLength(1)
    expect(groups[0].count).toBe(10)
    expect(groups[0].firstAt).toBe(1_000)
    expect(groups[0].lastAt).toBe(3_000)
  })

  it('keeps entries about different objects apart', () => {
    const groups = groupTimeline([
      entry({ id: 'a' }),
      entry({ id: 'b', target: { kind: 'Pod', namespace: 'billing', name: 'web-1' } }),
    ])

    expect(groups).toHaveLength(2)
  })

  it('keeps a finding raised apart from the same finding cleared', () => {
    const groups = groupTimeline([
      entry({ id: 'a', kind: 'finding', title: 'Burstable', detail: '', state: 'appeared' }),
      entry({ id: 'b', kind: 'finding', title: 'Burstable', detail: '', state: 'cleared' }),
    ])

    expect(groups).toHaveLength(2)
  })

  it('keeps a refused write apart from the same write succeeding', () => {
    const raised = entry({ id: 'a', kind: 'write', title: 'Deleted', detail: '', outcome: 'ok' })
    const refused = entry({
      id: 'b',
      kind: 'write',
      title: 'Deleted',
      detail: '',
      outcome: 'failed',
    })

    expect(groupTimeline([raised, refused])).toHaveLength(2)
  })

  it('groups repeats that are not adjacent', () => {
    // Unlike groupLogLines, where a stack trace's frames genuinely are next
    // to one another. A recurring event interleaved with other entries is
    // still one recurring event.
    const groups = groupTimeline([
      entry({ id: 'a', at: 3_000, lastAt: 3_000 }),
      entry({ id: 'b', at: 2_000, lastAt: 2_000, kind: 'write', title: 'Scaled', detail: '' }),
      entry({ id: 'c', at: 1_000, lastAt: 1_000 }),
    ])

    expect(groups).toHaveLength(2)
    expect(groups[0].members.map((held) => held.id)).toEqual(['a', 'c'])
  })

  it('puts every entry in exactly one group', () => {
    // The completeness rule graphFold.ts holds the folded dependency map to:
    // a view that quietly dropped an occurrence is one nobody can reason
    // from.
    const entries = [
      entry({ id: 'a', at: 1_000, lastAt: 1_000 }),
      entry({ id: 'b', at: 2_000, lastAt: 2_000 }),
      entry({ id: 'c', at: 3_000, lastAt: 3_000, title: 'Killing' }),
      entry({ id: 'd', at: 4_000, lastAt: 4_000, kind: 'write', title: 'Evicted', detail: '' }),
    ]

    const seen = groupTimeline(entries).flatMap((group) => group.members.map((held) => held.id))

    expect(seen.sort()).toEqual(['a', 'b', 'c', 'd'])
  })

  it('orders the newest group first and renders from the newest member', () => {
    const groups = groupTimeline([
      entry({ id: 'old', at: 1_000, lastAt: 1_000, title: 'Killing' }),
      // Two occurrences of one thing, handed over oldest first so the head
      // cannot be the one that merely arrived first.
      entry({ id: 'earlier', at: 5_000, lastAt: 5_000 }),
      entry({ id: 'latest', at: 6_000, lastAt: 6_000 }),
    ])

    expect(groups).toHaveLength(2)
    expect(groups[0].head.id).toBe('latest')
    expect(groups[1].head.id).toBe('old')
  })
})

describe('objectKey', () => {
  it('keeps two namespaces’ identically named pods apart', () => {
    expect(objectKey('Pod', 'shop', 'web')).not.toBe(objectKey('Pod', 'billing', 'web'))
  })

  it('keeps two kinds sharing a name apart', () => {
    expect(objectKey('Secret', 'shop', 'api')).not.toBe(objectKey('ConfigMap', 'shop', 'api'))
  })
})
