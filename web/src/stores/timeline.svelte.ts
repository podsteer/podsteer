/**
 * What happened while the application was open, per cluster and per object.
 *
 * IN MEMORY, FOR THE LIFE OF THE TAB, AND NOWHERE ELSE. That is the whole
 * shape of this feature rather than a limitation of it. A timeline is made
 * almost entirely of object names — which pod restarted, which Deployment was
 * scaled — and object names are not on the list of things SECURITY.md says
 * PodSteer writes to disk. `ClusterSession.recentObjects` makes the same
 * commitment for the navigator's Recent section, and the sampled capacity
 * history keeps it by carrying no names at all. A file of every event and
 * every write against every object somebody looked at would reverse all
 * three, so there is deliberately no preference that would persist this.
 *
 * The durable version is the planned paid tier, server-side, for the reason
 * `HistoryPort` records: the obvious next implementation records outside the
 * application entirely, and the seam belongs at a port rather than at a file
 * this process writes.
 *
 * NOTHING HERE COSTS A REQUEST. Every source already crosses the Wails
 * bridge: the assessment is fetched on every refresh whatever view is open,
 * a pod's findings ride every row of the pod list, the event lists are read
 * by the views that show them, and a write's outcome is resolved in
 * `$lib/api/client` before this is told about it. The same argument
 * `usageHistory` makes — the measurements were already on the wire.
 */

import type { Finding, K8sEvent, Pod } from '$lib/api/client'
import {
  diffFindings,
  objectKey,
  targetKey,
  type TimelineEntry,
  type TimelineSeverity,
  type TimelineTarget,
  type WriteRecord,
} from '$lib/timeline'

/**
 * The most entries kept about one object.
 *
 * A ceiling on what one noisy object can crowd out. A pod stuck in
 * CrashLoopBackOff produces a new event every few seconds for as long as
 * nobody fixes it, and without this it would fill the cluster's whole budget
 * on its own — so the two hundredth entry about that pod evicts its own
 * oldest rather than somebody else's.
 */
const MAX_ENTRIES_PER_OBJECT = 200

/**
 * The most entries kept for one cluster, across every object.
 *
 * Two thousand is a few hours of a busy cluster and roughly a quarter of a
 * megabyte. The bound exists because a session is open-ended: an application
 * left running over a weekend must not accumulate without limit, and a cap
 * measured in entries is one that holds whatever the cluster is doing.
 */
const MAX_ENTRIES_PER_CLUSTER = 2000

/** One cluster's timeline, oldest entry first. */
interface ClusterTimeline {
  entries: TimelineEntry[]
  /** When this cluster's timeline started — what the panel says it covers. */
  startedAt: number
}

/**
 * One pod finding as the baseline remembers it.
 *
 * The title is the identity and is therefore the map key too; it is repeated
 * here so a CLEARED finding still has something to render, which a key alone
 * would not give once the finding is gone from the pod.
 */
interface RememberedPodFinding {
  title: string
  severity: string
  detail: string
}

/** How a Kubernetes Event reads as a severity. */
function eventSeverity(event: K8sEvent): TimelineSeverity {
  return event.isWarning ? 'warning' : 'info'
}

/** A finding's severity, narrowed to what the timeline renders. */
function findingSeverity(severity: string): TimelineSeverity {
  return severity === 'critical' ? 'critical' : severity === 'warning' ? 'warning' : 'info'
}

class SessionTimeline {
  /**
   * Entries per cluster.
   *
   * `$state.raw` and replaced wholesale, not mutated: the panel renders from
   * this directly, so it has to be reactive, and deep proxying an array that
   * every refresh appends to would create a signal per field of every entry.
   * The same trade `ClusterSession`'s row buffers make, for the same reason.
   *
   * Replaced once per batch rather than once per entry — a refresh records a
   * whole event list in one call — so a tick costs a handful of array copies
   * however many rows it carried.
   */
  #timelines = $state.raw<Record<string, ClusterTimeline>>({})

  /**
   * Which entry each observation already produced, per cluster.
   *
   * Only events need this. A finding appearing and a write happening are
   * point-in-time occurrences recorded once; a Kubernetes Event is re-read on
   * every refresh for as long as it survives, and without an identity to
   * upsert against, one event would become one entry per refresh — the exact
   * wall of identical lines this feature exists to avoid.
   *
   * Not reactive: nothing renders it.
   */
  #observed = new Map<string, Map<string, TimelineEntry>>()

  /**
   * The last assessment's findings, per cluster, to diff the next against.
   *
   * Null for a cluster no assessment has landed for yet, which is what makes
   * the first one establish a baseline rather than announce everything the
   * cluster has been living with since Tuesday.
   */
  #clusterFindings = new Map<string, Map<string, Finding> | null>()

  /**
   * The last pod assessment, per cluster and then per pod.
   *
   * KEYED BY POD BECAUSE THE SCOPE IS THE POD. The cluster assessment covers
   * the whole cluster, so a finding missing from it has genuinely cleared; a
   * pod list covers whichever namespace is filtered and only while the pod
   * list is the view on screen, so a pod missing from it has not been LOOKED
   * AT. Diffing per pod is what keeps those apart — see `recordPodFindings`.
   */
  #podFindings = new Map<string, Map<string, Map<string, RememberedPodFinding>>>()

  /** Makes entry ids unique without a clock, which repeats within a tick. */
  #sequence = 0

  /** One cluster's entries, newest first. Empty for a cluster with none. */
  forCluster = (clusterId: string): TimelineEntry[] => {
    const held = this.#timelines[clusterId]
    if (!held) return []
    return [...held.entries].reverse()
  }

  /**
   * One object's entries, newest first.
   *
   * AN EMPTY NAMESPACE MATCHES ANY, and only then. A cluster-scoped object's
   * name is unique across the cluster, so there is nothing to confuse it
   * with — and Kubernetes files a core/v1 Event about a Node in the `default`
   * namespace rather than in none, so a strict comparison would leave a
   * node's own events out of its timeline. A namespaced object still matches
   * on its namespace, which is what keeps two namespaces' identically named
   * pods apart.
   */
  forObject = (
    clusterId: string,
    kind: string,
    namespace: string,
    name: string,
  ): TimelineEntry[] => {
    // An unnamed object is the cluster itself, which no panel opens on.
    if (name === '') return []

    if (namespace === '') {
      return this.forCluster(clusterId).filter(
        (entry) => entry.target.kind === kind && entry.target.name === name,
      )
    }
    const key = objectKey(kind, namespace, name)
    return this.forCluster(clusterId).filter((entry) => targetKey(entry.target) === key)
  }

  /**
   * When this cluster's timeline started, or null before anything landed.
   *
   * What the panel's one honest line is drawn from, the way
   * `SeriesResult.spanSeconds` lets the sampled charts say "the last 40
   * minutes" instead of implying more.
   */
  startedAt = (clusterId: string): number | null => this.#timelines[clusterId]?.startedAt ?? null

  /**
   * Records the Kubernetes Events one read returned.
   *
   * Idempotent: re-reading an event that is still there updates the entry it
   * already produced rather than adding another. The COUNT is the API
   * server's own — the field it increments instead of writing the event again
   * — so an event observed on ten refreshes contributes what the cluster says
   * happened rather than ten.
   *
   * The timestamps are LOCAL OBSERVATIONS, not the event's `firstSeen`. Every
   * other entry here is stamped when PodSteer saw it, and a timeline sorted
   * on two clocks at once orders a write made a second ago below an event the
   * cluster dated an hour before the tab opened. The panel says which it is.
   */
  recordEvents = (clusterId: string, events: K8sEvent[]): void => {
    if (events.length === 0) return

    const at = Date.now()
    const seen = this.#seenFor(clusterId)
    const appended: TimelineEntry[] = []
    let changed = false

    for (const event of events) {
      // The Event object's own identity. Kubernetes gives each one a unique
      // name in its namespace and folds repeats into it by raising `count`,
      // so this is one entry per thing that actually happened.
      const key = `event|${event.namespace}/${event.name}`
      const existing = seen.get(key)
      if (existing) {
        if (existing.count !== event.count) {
          existing.count = event.count
          existing.lastAt = at
          changed = true
        }
        continue
      }

      const entry: TimelineEntry = {
        id: `e${++this.#sequence}`,
        at,
        lastAt: at,
        count: Math.max(1, event.count),
        kind: 'event',
        severity: eventSeverity(event),
        title: event.reason,
        detail: event.message,
        target: {
          kind: event.involvedKind,
          namespace: event.namespace,
          name: event.involvedName,
        },
      }
      seen.set(key, entry)
      appended.push(entry)
    }

    if (appended.length > 0) this.#append(clusterId, appended)
    // An event whose count moved is already in the array — it is the same
    // object — so the entries need no splice, only a new array identity for
    // the panel to notice. Skipped entirely when nothing moved, which is the
    // ordinary case on a quiet cluster.
    else if (changed) this.#touch(clusterId)
  }

  /**
   * Records what the cluster assessment changed.
   *
   * `null` means no assessment arrived — a refresh that failed, or one that
   * was never made because the view does not fetch one. It is NOT an empty
   * assessment: read as one it reports every outstanding finding in the
   * cluster clearing at the same instant, which an operator reads as thirty
   * problems fixing themselves while they watched. See `diffFindings`.
   */
  recordFindings = (clusterId: string, findings: Finding[] | null): void => {
    const current =
      findings === null ? null : new Map(findings.map((finding) => [finding.id, finding]))
    const previous = this.#clusterFindings.get(clusterId) ?? null

    const diff = diffFindings(previous, current)
    this.#clusterFindings.set(clusterId, diff.next)
    if (diff.appeared.length === 0 && diff.cleared.length === 0) return

    const at = Date.now()
    const entries: TimelineEntry[] = []

    for (const finding of diff.appeared) {
      entries.push(this.#findingEntry(finding, 'appeared', at))
    }
    for (const finding of diff.cleared) {
      entries.push(this.#findingEntry(finding, 'cleared', at))
    }

    this.#append(clusterId, entries)
  }

  /**
   * Records what the pod assessment changed, per pod.
   *
   * `null` means no pod list arrived, and a pod ABSENT from a list that did
   * arrive means the same thing about that pod. The row buffers are mutually
   * exclusive and the namespace filter narrows them, so a poll on the Nodes
   * page carries no pods at all and a poll filtered to `billing` carries none
   * from `shop` — treating either as "the findings cleared" would announce
   * every pod in the cluster recovering the moment somebody changed view.
   * Only a pod the list actually carried is diffed.
   *
   * A pod's finding has no id of its own (`domain.PodFinding` carries no
   * subjects and nothing to navigate to), so the TITLE is the identity. That
   * holds because the per-container rules put the container's name in their
   * title — `Liveness probe is close to killing sidecar` — so two findings on
   * one pod cannot collide, while the detail underneath is free to change its
   * numbers without the finding reading as cleared and raised again.
   */
  recordPodFindings = (clusterId: string, pods: Pod[] | null): void => {
    if (pods === null) return

    const held =
      this.#podFindings.get(clusterId) ?? new Map<string, Map<string, RememberedPodFinding>>()
    this.#podFindings.set(clusterId, held)

    const at = Date.now()
    const entries: TimelineEntry[] = []

    for (const pod of pods) {
      const key = objectKey('Pod', pod.namespace, pod.name)
      const current = new Map<string, RememberedPodFinding>(
        pod.findings.map((finding) => [
          finding.title,
          { title: finding.title, severity: finding.severity, detail: finding.detail },
        ]),
      )
      const diff = diffFindings(held.get(key) ?? null, current)
      held.set(key, diff.next ?? current)

      const target: TimelineTarget = { kind: 'Pod', namespace: pod.namespace, name: pod.name }
      for (const finding of diff.appeared) {
        entries.push({
          id: `f${++this.#sequence}`,
          at,
          lastAt: at,
          count: 1,
          kind: 'finding',
          severity: findingSeverity(finding.severity),
          title: finding.title,
          detail: finding.detail,
          target,
          state: 'appeared',
        })
      }
      for (const finding of diff.cleared) {
        // No detail on a clearing: the detail said what was observed, and
        // what was observed is exactly what is no longer true.
        entries.push({
          id: `f${++this.#sequence}`,
          at,
          lastAt: at,
          count: 1,
          kind: 'finding',
          severity: findingSeverity(finding.severity),
          title: finding.title,
          detail: '',
          target,
          state: 'cleared',
        })
      }
    }

    if (entries.length > 0) this.#append(clusterId, entries)
  }

  /**
   * Records a write PodSteer made, however it ended.
   *
   * A refused write is recorded too. "I pressed delete and nothing happened"
   * is exactly the question a timeline answers, and a row that only ever
   * shows what succeeded cannot answer it.
   */
  recordWrite = (clusterId: string, record: WriteRecord): void => {
    const at = Date.now()
    this.#append(clusterId, [
      {
        id: `w${++this.#sequence}`,
        at,
        lastAt: at,
        count: 1,
        kind: 'write',
        severity: record.outcome === 'failed' ? 'warning' : 'info',
        title: record.action,
        detail: record.failure ? `${record.detail} — ${record.failure}`.trim() : record.detail,
        target: record.target,
        outcome: record.outcome,
      },
    ])
  }

  /**
   * Forgets one cluster's timeline, for a tab being closed.
   *
   * Per cluster rather than wholesale, the way `usageHistory.forget` is:
   * closing one tab must not empty the timeline in another. Everything a
   * closed tab accumulated goes with it, which is the same trade the Recent
   * section makes and the point of holding this in memory at all.
   */
  forget = (clusterId: string): void => {
    if (!(clusterId in this.#timelines)) {
      this.#observed.delete(clusterId)
      this.#clusterFindings.delete(clusterId)
      this.#podFindings.delete(clusterId)
      return
    }
    const next = { ...this.#timelines }
    delete next[clusterId]
    this.#timelines = next
    this.#observed.delete(clusterId)
    this.#clusterFindings.delete(clusterId)
    this.#podFindings.delete(clusterId)
  }

  #seenFor(clusterId: string): Map<string, TimelineEntry> {
    const held = this.#observed.get(clusterId)
    if (held) return held
    const fresh = new Map<string, TimelineEntry>()
    this.#observed.set(clusterId, fresh)
    return fresh
  }

  #findingEntry(finding: Finding, state: 'appeared' | 'cleared', at: number): TimelineEntry {
    // A finding naming exactly one object is filed against that object, so it
    // shows in that object's own timeline and its row opens it. One naming
    // several is filed cluster-wide: it is a statement about the group, and
    // picking one of its subjects to hang it on would put a row on a pod that
    // is not what the finding is about.
    const subject = finding.subjects.length === 1 ? finding.subjects[0] : null

    return {
      id: `f${++this.#sequence}`,
      at,
      lastAt: at,
      count: 1,
      kind: 'finding',
      severity: findingSeverity(finding.severity),
      title: finding.title,
      detail: state === 'appeared' ? finding.summary : '',
      target: subject
        ? { kind: subject.kind, namespace: subject.namespace, name: subject.name }
        : { kind: '', namespace: '', name: '' },
      state,
    }
  }

  /**
   * Appends entries and enforces both caps, oldest dropped first.
   *
   * The per-object cap runs before the cluster one so a single noisy object
   * cannot evict every other object's history before its own.
   */
  #append(clusterId: string, entries: TimelineEntry[]): void {
    if (entries.length === 0) return

    const held = this.#timelines[clusterId]
    const startedAt = held?.startedAt ?? entries[0].at
    let next = held ? [...held.entries, ...entries] : [...entries]

    const overfull = new Set<string>()
    const counts = new Map<string, number>()
    for (const entry of next) {
      const key = targetKey(entry.target)
      const count = (counts.get(key) ?? 0) + 1
      counts.set(key, count)
      if (count > MAX_ENTRIES_PER_OBJECT) overfull.add(key)
    }

    for (const key of overfull) {
      // Oldest first, which is what a forward scan drops: the array is in
      // observation order, so the first entries matching are the ones that
      // have been on screen longest.
      let excess = (counts.get(key) ?? 0) - MAX_ENTRIES_PER_OBJECT
      next = next.filter((entry) => {
        if (excess === 0 || targetKey(entry.target) !== key) return true
        excess--
        this.#drop(clusterId, entry)
        return false
      })
    }

    if (next.length > MAX_ENTRIES_PER_CLUSTER) {
      for (const entry of next.slice(0, next.length - MAX_ENTRIES_PER_CLUSTER)) {
        this.#drop(clusterId, entry)
      }
      next = next.slice(next.length - MAX_ENTRIES_PER_CLUSTER)
    }

    this.#timelines = { ...this.#timelines, [clusterId]: { entries: next, startedAt } }
  }

  /**
   * Forgets an evicted entry's observation identity.
   *
   * Without this an evicted event could never come back: its key would still
   * be in `#observed`, so the next read of the same event would update an
   * entry no longer in the array and the panel would never show it again.
   */
  #drop(clusterId: string, entry: TimelineEntry): void {
    if (entry.kind !== 'event') return
    const seen = this.#observed.get(clusterId)
    if (!seen) return
    for (const [key, held] of seen) {
      if (held.id === entry.id) {
        seen.delete(key)
        return
      }
    }
  }

  /** Gives the panel a new array identity when an entry changed in place. */
  #touch(clusterId: string): void {
    const held = this.#timelines[clusterId]
    if (!held) return
    this.#timelines = {
      ...this.#timelines,
      [clusterId]: { entries: [...held.entries], startedAt: held.startedAt },
    }
  }
}

/** The session timeline, shared by every tab and keyed by cluster. */
export const timeline = new SessionTimeline()
