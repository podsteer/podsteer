/**
 * What a session timeline is made of, and the two rules that shape it.
 *
 * The timeline answers "what happened while I was watching this" — the
 * Kubernetes Events an object produced, the findings that appeared and
 * cleared underneath it, and the writes PodSteer itself made. Everything here
 * is pure: the diffing, the grouping and the keys. The store that holds the
 * entries is `$stores/timeline`, and it is deliberately in memory only.
 *
 * TWO RULES LIVE IN THIS FILE, and both are the kind that is easy to get
 * subtly wrong:
 *
 * - **A finding appearing or clearing is DERIVED, not read.** Nothing in the
 *   assessment says "this is new"; it is new because the previous assessment
 *   did not carry it. `diffFindings` is that comparison, and its whole
 *   subtlety is that "the finding is gone" and "no assessment arrived" are
 *   different facts — see its own comment.
 * - **Grouping is a VIEW decision, and the entries are always complete.** The
 *   same rule `graphFold.ts` holds to for the dependency map: a row may stand
 *   for forty occurrences, but every occurrence is in exactly one row and the
 *   count is on the row, so a collapsed set can never under-report.
 */

/** Which of the three things an entry records. */
export type TimelineEntryKind = 'event' | 'finding' | 'write'

/**
 * The least a Kubernetes Event has to carry to be recorded.
 *
 * TWO SHAPES ARRIVE HERE and this is what they have in common: the assessment
 * carries `Overview.events`, deliberately narrowed to these eight fields
 * because it crosses the bridge on every tick whatever view is on screen, and
 * the Events page carries the full `K8sEvent` rows it is rendering anyway.
 * Structural rather than a union so the recorder never has to ask which one it
 * was handed — and so a field added to one of them cannot quietly become
 * something the recorder depends on without appearing here first.
 *
 * `namespace` and `name` are the EVENT OBJECT'S identity, not the involved
 * object's: Kubernetes gives each event a unique name in its namespace and
 * folds repeats into it by raising `count`, which is what lets one entry stand
 * for however many occurrences the cluster actually saw.
 */
export interface RecordedEvent {
  namespace: string
  name: string
  reason: string
  message: string
  involvedKind: string
  involvedName: string
  isWarning: boolean
  count: number
}

/** How loudly an entry should read. The overview's own vocabulary. */
export type TimelineSeverity = 'info' | 'warning' | 'critical'

/**
 * The object an entry is about.
 *
 * `kind` is the Kubernetes Kind verbatim — `Pod`, `Deployment` — because the
 * drawer resolves a reference against the navigator's catalogue, which is
 * keyed by Kind. A lowercased plural matches nothing and the row click
 * silently does nothing at all, which is what every followable node in the
 * dependency map used to do.
 *
 * A cluster-wide entry (a capacity finding, say) carries an empty kind and
 * name: there is no object to open, and inventing one would draw a row that
 * navigates somewhere unrelated.
 */
export interface TimelineTarget {
  kind: string
  namespace: string
  name: string
}

/** One thing that happened, as the timeline records it. */
export interface TimelineEntry {
  /** Unique within one cluster's timeline. */
  id: string
  /** When it was first observed, epoch milliseconds on the local clock. */
  at: number
  /**
   * When it was last observed.
   *
   * Equal to `at` for a finding and a write, which happen once. A Kubernetes
   * Event is re-read on every refresh and carries its own last-seen, so this
   * moves while the event keeps recurring.
   */
  lastAt: number
  /**
   * How many times it happened.
   *
   * One for a finding and a write. For an event this is the API server's OWN
   * count — the field it increments instead of writing the event again — so
   * an event seen on ten refreshes still contributes what the cluster says
   * happened, not ten.
   */
  count: number
  kind: TimelineEntryKind
  severity: TimelineSeverity
  /** The headline, in the vocabulary of whatever produced it. */
  title: string
  /** What was observed, with the numbers in it. May be empty. */
  detail: string
  target: TimelineTarget
  /** For a finding: whether this row is it arriving or it going away. */
  state?: 'appeared' | 'cleared'
  /** For a write: whether the cluster accepted it. */
  outcome?: 'ok' | 'failed'
}

/**
 * Identifies one object within a cluster.
 *
 * Namespace AND name, never name alone: two namespaces routinely hold pods
 * with the same name, and collapsing those would file one pod's events under
 * another's. The kind is in it for the same reason a ConfigMap and a Secret
 * can share a name in one namespace.
 */
/**
 * Whether a timeline entry matches what somebody typed into the page's search
 * box.
 *
 * A PLAIN CASE-INSENSITIVE SUBSTRING, and not the filter language the object
 * lists use. That grammar selects on labels, on `key=value` and on a cluster
 * name — fields a timeline entry does not have — so offering it here would be
 * offering syntax that silently matches nothing and reads as a broken search.
 *
 * What it searches is exactly the text a row PUTS ON SCREEN: the title, the
 * detail line beneath it, and the target's kind, name and namespace. That
 * correspondence is the point rather than an implementation detail — a search
 * that finds nothing then means the words are not on the page, instead of
 * meaning they were looked for somewhere the reader cannot see.
 */
export function matchesTimelineSearch(entry: TimelineEntry, search: string): boolean {
  const needle = search.trim().toLowerCase()
  if (needle === '') return true

  return (
    entry.title.toLowerCase().includes(needle) ||
    (entry.detail ?? '').toLowerCase().includes(needle) ||
    entry.target.kind.toLowerCase().includes(needle) ||
    entry.target.name.toLowerCase().includes(needle) ||
    (entry.target.namespace ?? '').toLowerCase().includes(needle)
  )
}

export function objectKey(kind: string, namespace: string, name: string): string {
  return `${kind}|${namespace}/${name}`
}

/** The key of the object an entry is about. */
export function targetKey(target: TimelineTarget): string {
  return objectKey(target.kind, target.namespace, target.name)
}

// --- Diffing ---------------------------------------------------------------

/** What one assessment changed against the one before it. */
export interface FindingDiff<T> {
  /** Findings in `current` that `previous` did not carry. */
  appeared: T[]
  /** Findings `previous` carried that `current` does not. */
  cleared: T[]
  /**
   * The baseline to carry into the next diff.
   *
   * Null only while no assessment has ever landed. A refresh that observed
   * nothing hands back the baseline it was given, unchanged — see below.
   */
  next: Map<string, T> | null
}

/**
 * Reports what appeared and what cleared between two assessments.
 *
 * THE WHOLE SUBTLETY IS THE DIFFERENCE BETWEEN "GONE" AND "NOT LOOKED AT",
 * and getting it wrong is spectacular rather than quiet: a refresh that
 * failed carries no findings, so a naive comparison reports every outstanding
 * problem in the cluster clearing at the same instant. An operator reads that
 * as thirty things fixing themselves while they watched.
 *
 * So a null `current` means "no assessment arrived" and is not evidence about
 * anything: nothing appeared, nothing cleared, and the baseline is kept so
 * the NEXT successful assessment is compared against the last one that was
 * real rather than against an empty set.
 *
 * A null `previous` is the other end of the same idea. The first assessment
 * of a session only establishes the baseline — a cluster that has been broken
 * since Tuesday is not thirty things happening now, which is the same reason
 * `ClusterSession.#adopt` stays silent on its first assessment rather than
 * sounding a chord of every finding at once.
 *
 * An id that clears and comes back appears AGAIN, because it is absent from
 * the baseline by the time it returns. That is what somebody watching a
 * flapping workload needs to see.
 */
export function diffFindings<T>(
  previous: Map<string, T> | null,
  current: Map<string, T> | null,
): FindingDiff<T> {
  if (current === null) return { appeared: [], cleared: [], next: previous }
  if (previous === null) return { appeared: [], cleared: [], next: current }

  const appeared: T[] = []
  for (const [id, finding] of current) {
    if (!previous.has(id)) appeared.push(finding)
  }

  const cleared: T[] = []
  for (const [id, finding] of previous) {
    if (!current.has(id)) cleared.push(finding)
  }

  return { appeared, cleared, next: current }
}

// --- Grouping --------------------------------------------------------------

/** A run of identical entries, rendered as one row. */
export interface TimelineGroup {
  /** What the row renders from: the most recent member. */
  head: TimelineEntry
  /** Every entry the row stands for, newest first, INCLUDING the head. */
  members: TimelineEntry[]
  /**
   * Total occurrences. The SUM of the members' counts rather than the number
   * of members, so a Kubernetes Event's own count reaches the row: forty
   * BackOff events the API server folded into one object with count 40 must
   * read as forty, not as one.
   */
  count: number
  /** Oldest and newest occurrence, epoch milliseconds — the row's span. */
  firstAt: number
  lastAt: number
}

/**
 * What makes two entries the same line.
 *
 * The object, what kind of thing it was, and what it said. Deliberately NOT
 * the timestamp or the entry id: two identical BackOff events a minute apart
 * are one thing recurring, which is the case this exists for.
 */
function groupKey(entry: TimelineEntry): string {
  return [
    entry.kind,
    entry.state ?? '',
    entry.outcome ?? '',
    targetKey(entry.target),
    entry.title,
    entry.detail,
  ].join(' ')
}

/**
 * Collapses repeats into one row each, newest group first.
 *
 * Grouped across the whole list rather than only between neighbours — unlike
 * `groupLogLines`, where a stack trace's frames genuinely are adjacent. A
 * recurring event interleaved with other entries is still one recurring
 * event, and a row that says "40 times over 12 minutes" is the only form of
 * it anybody reads.
 *
 * NOTHING IS INVENTED AND NOTHING DISAPPEARS: every input entry is in exactly
 * one group, the count is the members' own counts summed, and the span is
 * their own timestamps. That is the same completeness rule `graphFold.ts`
 * holds the folded dependency map to, and for the same reason — a view that
 * quietly dropped an occurrence is a view nobody can reason from.
 */
export function groupTimeline(entries: TimelineEntry[]): TimelineGroup[] {
  const groups = new Map<string, TimelineGroup>()

  for (const entry of entries) {
    const key = groupKey(entry)
    const existing = groups.get(key)
    if (!existing) {
      groups.set(key, {
        head: entry,
        members: [entry],
        count: entry.count,
        firstAt: entry.at,
        lastAt: entry.lastAt,
      })
      continue
    }

    existing.members.push(entry)
    existing.count += entry.count
    existing.firstAt = Math.min(existing.firstAt, entry.at)
    existing.lastAt = Math.max(existing.lastAt, entry.lastAt)
    // The head is whichever member is newest, so the row shows the most
    // recent occurrence's wording rather than the first one's — an event's
    // message can change while its reason does not.
    if (entry.lastAt >= existing.head.lastAt) existing.head = entry
  }

  const ordered = [...groups.values()]
  for (const group of ordered) {
    group.members.sort((a, b) => b.lastAt - a.lastAt)
  }
  ordered.sort((a, b) => b.lastAt - a.lastAt)
  return ordered
}

// --- Writes ----------------------------------------------------------------

/**
 * A write PodSteer made, as the timeline records it.
 *
 * NO VALUE EVER REACHES THIS. Writing one key of a Secret or a ConfigMap
 * records the cluster, namespace, name and KEY — never what was written —
 * which is exactly the audit line `ManagementService` already writes on the
 * Go side. A timeline holding the plaintext somebody typed into a Secret
 * would be a second copy of it, in the webview, for the life of the tab.
 */
export interface WriteRecord {
  /** The verb, in the past tense the row reads in: `Scaled`, `Deleted`. */
  action: string
  target: TimelineTarget
  /** What the write asked for — `to 3 replicas`, `web to nginx:1.27`. */
  detail: string
  outcome: 'ok' | 'failed'
  /** Why it was refused, when it was. The classified error's own message. */
  failure?: string
}
