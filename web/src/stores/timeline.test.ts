import { beforeEach, describe, expect, it } from 'vitest'

import type { Finding, K8sEvent, Pod } from '$lib/api/client'
import { timeline } from './timeline.svelte'

// Only the fields the store reads. Cast through unknown because each DTO is
// a generated class with a dozen more, none of which this touches.
const event = (over: Partial<K8sEvent> & { name: string }): K8sEvent =>
  ({
    namespace: 'shop',
    involvedKind: 'Pod',
    involvedName: 'web-1',
    reason: 'BackOff',
    message: 'Back-off restarting failed container',
    isWarning: true,
    count: 1,
    ...over,
  }) as unknown as K8sEvent

const finding = (over: Partial<Finding> & { id: string }): Finding =>
  ({
    severity: 'warning',
    title: 'Pods are crash-looping',
    summary: 'two pods have restarted repeatedly',
    subjects: [],
    ...over,
  }) as unknown as Finding

const pod = (name: string, findings: { title: string; severity: string; detail: string }[]): Pod =>
  ({ namespace: 'shop', name, findings }) as unknown as Pod

describe('the session timeline', () => {
  beforeEach(() => {
    timeline.forget('dev')
    timeline.forget('prod')
  })

  describe('Kubernetes events', () => {
    it('records one entry per event however often it is re-read', () => {
      // A poll re-reads the same event for as long as it survives, and one
      // entry per refresh is exactly the wall of identical lines this
      // feature exists to avoid.
      timeline.recordEvents('dev', [event({ name: 'web-1.a1' })])
      timeline.recordEvents('dev', [event({ name: 'web-1.a1' })])
      timeline.recordEvents('dev', [event({ name: 'web-1.a1' })])

      expect(timeline.forCluster('dev')).toHaveLength(1)
    })

    it('carries the API server’s own count rather than counting refreshes', () => {
      timeline.recordEvents('dev', [event({ name: 'web-1.a1', count: 3 })])
      timeline.recordEvents('dev', [event({ name: 'web-1.a1', count: 12 })])

      const [entry] = timeline.forCluster('dev')
      expect(entry.count).toBe(12)
    })

    it('files an event under the object it is about', () => {
      timeline.recordEvents('dev', [event({ name: 'web-1.a1' })])

      expect(timeline.forObject('dev', 'Pod', 'shop', 'web-1')).toHaveLength(1)
      expect(timeline.forObject('dev', 'Pod', 'shop', 'web-2')).toHaveLength(0)
    })

    it('finds a cluster-scoped object’s events whatever namespace they were filed in', () => {
      // Kubernetes files a core/v1 Event about a Node in `default` rather
      // than in no namespace at all, so a strict comparison would leave a
      // node's own events out of its own timeline. A cluster-scoped name is
      // unique, so there is nothing to confuse it with.
      timeline.recordEvents('dev', [
        event({
          name: 'node-a.b2',
          namespace: 'default',
          involvedKind: 'Node',
          involvedName: 'node-a',
        }),
      ])

      expect(timeline.forObject('dev', 'Node', '', 'node-a')).toHaveLength(1)
    })

    it('keeps two clusters’ timelines apart', () => {
      // The same identity hazard usageHistory guards: two open tabs
      // routinely hold identically named pods in identically named
      // namespaces.
      timeline.recordEvents('dev', [event({ name: 'web-1.a1' })])
      timeline.recordEvents('prod', [event({ name: 'web-1.a1' })])

      timeline.forget('dev')

      expect(timeline.forCluster('dev')).toHaveLength(0)
      expect(timeline.forCluster('prod')).toHaveLength(1)
    })
  })

  describe('cluster findings', () => {
    it('says nothing about the first assessment', () => {
      timeline.recordFindings('dev', [finding({ id: 'pods:crashloop' })])

      expect(timeline.forCluster('dev')).toHaveLength(0)
    })

    it('records a finding appearing and then clearing', () => {
      timeline.recordFindings('dev', [])
      timeline.recordFindings('dev', [finding({ id: 'pods:crashloop' })])
      timeline.recordFindings('dev', [])

      const entries = timeline.forCluster('dev')
      expect(entries.map((entry) => entry.state)).toEqual(['cleared', 'appeared'])
    })

    it('does not read a failed refresh as everything clearing at once', () => {
      // THE BUG THIS GUARDS. Passing null says "no assessment arrived",
      // which is not evidence that anything went away.
      timeline.recordFindings('dev', [])
      timeline.recordFindings('dev', [finding({ id: 'a' }), finding({ id: 'b' })])
      const afterAppearing = timeline.forCluster('dev').length

      timeline.recordFindings('dev', null)

      expect(timeline.forCluster('dev')).toHaveLength(afterAppearing)
      // And the baseline survived, so the next real assessment is compared
      // against the last one that was real.
      timeline.recordFindings('dev', [finding({ id: 'a' }), finding({ id: 'b' })])
      expect(timeline.forCluster('dev')).toHaveLength(afterAppearing)
    })

    it('files a finding naming one object against that object', () => {
      timeline.recordFindings('dev', [])
      timeline.recordFindings('dev', [
        finding({
          id: 'pods:crashloop',
          subjects: [{ kind: 'Pod', namespace: 'shop', name: 'web-1', detail: '' }],
        }),
      ])

      expect(timeline.forObject('dev', 'Pod', 'shop', 'web-1')).toHaveLength(1)
    })

    it('leaves a finding naming several objects cluster-wide', () => {
      // It is a statement about the group; hanging it on one of its subjects
      // would put a row on a pod that is not what the finding is about.
      timeline.recordFindings('dev', [])
      timeline.recordFindings('dev', [
        finding({
          id: 'pods:crashloop',
          subjects: [
            { kind: 'Pod', namespace: 'shop', name: 'web-1', detail: '' },
            { kind: 'Pod', namespace: 'shop', name: 'web-2', detail: '' },
          ],
        }),
      ])

      expect(timeline.forCluster('dev')).toHaveLength(1)
      expect(timeline.forObject('dev', 'Pod', 'shop', 'web-1')).toHaveLength(0)
    })
  })

  describe('pod findings', () => {
    const burstable = { title: 'Burstable, not Guaranteed', severity: 'info', detail: 'no limit' }
    const crashing = { title: 'Nothing will recreate this pod', severity: 'warning', detail: '' }

    it('records a pod finding appearing and clearing', () => {
      timeline.recordPodFindings('dev', [pod('web-1', [])])
      timeline.recordPodFindings('dev', [pod('web-1', [burstable])])
      timeline.recordPodFindings('dev', [pod('web-1', [])])

      const entries = timeline.forObject('dev', 'Pod', 'shop', 'web-1')
      expect(entries.map((entry) => entry.state)).toEqual(['cleared', 'appeared'])
    })

    it('says nothing when a pod’s findings are unchanged', () => {
      timeline.recordPodFindings('dev', [pod('web-1', [burstable])])
      timeline.recordPodFindings('dev', [pod('web-1', [burstable])])
      timeline.recordPodFindings('dev', [pod('web-1', [burstable])])

      expect(timeline.forCluster('dev')).toHaveLength(0)
    })

    it('does not read a refresh that carried no pods as everything clearing', () => {
      // The row buffers are mutually exclusive: a poll on the Nodes page
      // leaves the pod list empty. Read as an assessment that would announce
      // every pod in the cluster recovering the moment somebody changed
      // view.
      timeline.recordPodFindings('dev', [pod('web-1', [burstable]), pod('web-2', [crashing])])
      timeline.recordPodFindings('dev', null)

      expect(timeline.forCluster('dev')).toHaveLength(0)
    })

    it('does not clear a pod a PARTIAL refresh did not carry', () => {
      // A namespace filter narrows the list, so a pod missing from it has
      // not been looked at rather than recovered.
      timeline.recordPodFindings('dev', [pod('web-1', [burstable]), pod('web-2', [crashing])])
      timeline.recordPodFindings('dev', [pod('web-1', [burstable])])

      expect(timeline.forObject('dev', 'Pod', 'shop', 'web-2')).toHaveLength(0)

      // And its baseline survived: web-2 coming back unchanged is still not
      // news.
      timeline.recordPodFindings('dev', [pod('web-1', [burstable]), pod('web-2', [crashing])])
      expect(timeline.forObject('dev', 'Pod', 'shop', 'web-2')).toHaveLength(0)
    })

    it('does not re-raise a finding whose detail changed', () => {
      // The title is the identity; the detail carries numbers that move.
      timeline.recordPodFindings('dev', [pod('web-1', [burstable])])
      timeline.recordPodFindings('dev', [
        pod('web-1', [{ ...burstable, detail: 'still no limit, now on two containers' }]),
      ])

      expect(timeline.forCluster('dev')).toHaveLength(0)
    })
  })

  describe('a recorded write', () => {
    it('carries the action, the object and the outcome', () => {
      timeline.recordWrite('dev', {
        action: 'Scaled',
        target: { kind: 'Deployment', namespace: 'shop', name: 'web' },
        detail: 'to 3 replicas',
        outcome: 'ok',
      })

      const [entry] = timeline.forCluster('dev')
      expect(entry.kind).toBe('write')
      expect(entry.title).toBe('Scaled')
      expect(entry.detail).toBe('to 3 replicas')
      expect(entry.outcome).toBe('ok')
      expect(entry.severity).toBe('info')
      expect(entry.target).toEqual({ kind: 'Deployment', namespace: 'shop', name: 'web' })
    })

    it('records a refusal, with the reason', () => {
      // "I pressed delete and nothing happened" is exactly the question a
      // timeline answers, and one showing only what succeeded cannot.
      timeline.recordWrite('dev', {
        action: 'Deleted',
        target: { kind: 'Pod', namespace: 'shop', name: 'web-1' },
        detail: '',
        outcome: 'failed',
        failure: 'pods "web-1" is forbidden',
      })

      const [entry] = timeline.forCluster('dev')
      expect(entry.outcome).toBe('failed')
      expect(entry.severity).toBe('warning')
      expect(entry.detail).toContain('is forbidden')
    })

    it('files a write against the object it was made on', () => {
      timeline.recordWrite('dev', {
        action: 'Evicted',
        target: { kind: 'Pod', namespace: 'shop', name: 'web-1' },
        detail: '',
        outcome: 'ok',
      })

      expect(timeline.forObject('dev', 'Pod', 'shop', 'web-1')).toHaveLength(1)
    })
  })

  describe('the caps', () => {
    it('drops one object’s oldest entries past its own cap', () => {
      // A pod stuck in CrashLoopBackOff produces an event every few seconds,
      // and without a per-object cap it would fill the cluster's whole
      // budget on its own.
      for (let i = 0; i < 260; i++) {
        timeline.recordEvents('dev', [event({ name: `web-1.${i}`, message: `attempt ${i}` })])
      }

      const entries = timeline.forObject('dev', 'Pod', 'shop', 'web-1')
      expect(entries).toHaveLength(200)
      // Newest first, and the oldest sixty went.
      expect(entries[0].detail).toBe('attempt 259')
      expect(entries.some((entry) => entry.detail === 'attempt 0')).toBe(false)
    })

    it('does not let one noisy object evict another object’s history', () => {
      timeline.recordEvents('dev', [
        event({ name: 'quiet.1', involvedName: 'db-0', message: 'the only one' }),
      ])
      for (let i = 0; i < 260; i++) {
        timeline.recordEvents('dev', [event({ name: `web-1.${i}`, message: `attempt ${i}` })])
      }

      expect(timeline.forObject('dev', 'Pod', 'shop', 'db-0')).toHaveLength(1)
    })

    it('drops the cluster’s oldest entries past the overall cap', () => {
      // Spread over enough objects that the per-object cap never bites, so
      // this is the cluster cap and nothing else.
      for (let i = 0; i < 2_100; i++) {
        timeline.recordEvents('dev', [
          event({ name: `e.${i}`, involvedName: `web-${i}`, message: `attempt ${i}` }),
        ])
      }

      const entries = timeline.forCluster('dev')
      expect(entries).toHaveLength(2_000)
      expect(entries[0].detail).toBe('attempt 2099')
      expect(entries[entries.length - 1].detail).toBe('attempt 100')
    })

    it('lets an evicted event be recorded again', () => {
      // The observation identity has to go with the entry, or an evicted
      // event could never come back: the next read would update a record no
      // longer in the array and the panel would never show it again.
      for (let i = 0; i < 260; i++) {
        timeline.recordEvents('dev', [event({ name: `web-1.${i}`, message: `attempt ${i}` })])
      }
      expect(timeline.forObject('dev', 'Pod', 'shop', 'web-1')).toHaveLength(200)

      timeline.recordEvents('dev', [event({ name: 'web-1.0', message: 'attempt 0' })])

      const entries = timeline.forObject('dev', 'Pod', 'shop', 'web-1')
      expect(entries[0].detail).toBe('attempt 0')
    })
  })

  it('reports when a cluster’s timeline started, and forgets it with the tab', () => {
    expect(timeline.startedAt('dev')).toBeNull()

    timeline.recordEvents('dev', [event({ name: 'web-1.a1' })])
    expect(timeline.startedAt('dev')).not.toBeNull()

    timeline.forget('dev')
    expect(timeline.startedAt('dev')).toBeNull()
  })
})
