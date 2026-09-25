/**
 * Whether a desktop notification is raised, and what it says.
 *
 * EVERY RULE IS HERE AND NOTHING HERE TOUCHES THE BRIDGE, because each of
 * these rules is the kind that is easy to get subtly wrong and impossible to
 * notice going wrong: a notification that fires once too often is one people
 * switch off within a day, and one that fires for something that was already
 * there is one nobody believes afterwards. So the decision is a pure function
 * with a test per rule, and `$stores/notifications` is only the plumbing that
 * asks it and posts the answer.
 *
 * THE DIFF IS NOT COMPUTED HERE. `appeared` is what `diffFindings` in
 * `$lib/timeline` reported — the same comparison the session timeline is
 * built on, held once. A second differ is a second baseline, and two baselines
 * drift: the timeline would record a finding appearing on one refresh and the
 * notification would announce it on another.
 *
 * AND A NOTIFICATION IS A WRITE. macOS keeps delivered notifications in
 * Notification Centre, which is a database on disk, and a Linux notification
 * daemon may log what it showed. So the no-object-names commitment SECURITY.md
 * makes about files holds here in full: what is composed below is a COUNT and
 * a finding's TITLE — a rule's own name, written in this repository — and
 * never a subject, which is a namespace and an object name. `Finding.summary`
 * is deliberately not read for the same reason.
 */

/** The part of a finding this decision reads. Deliberately not the subjects. */
export interface NotifiableFinding {
  id: string
  severity: string
  title: string
}

/** One notification, composed and ready to post. */
export interface NotificationPlan {
  /**
   * The id the operating system replaces on.
   *
   * Per CLUSTER, so a second notification about the same cluster supersedes
   * the first rather than stacking beside it. Somebody who has been away from
   * their desk should come back to one line per cluster, not a column of
   * them.
   */
  id: string
  title: string
  body: string
  clusterId: string
  /** How many new critical findings it stands for. */
  count: number
}

/**
 * Everything the decision needs, and nothing it could reach for itself.
 *
 * Generic over the finding type for the same reason `diffFindings` is: the
 * caller holds whole `Finding` values and its snooze rule reads their
 * subjects, while everything decided here needs three fields. Narrowing the
 * array would mean the predicate could no longer see what it judges.
 */
export interface NotifyInput<T extends NotifiableFinding = NotifiableFinding> {
  /** The kubeconfig context name. It is the click target and the body's "where". */
  clusterId: string
  /** Whether the operator turned this on. Off is the default — see below. */
  enabled: boolean
  /** Whether the platform will actually deliver one. */
  deliverable: boolean
  /** What `diffFindings` reported appearing. Never a whole assessment. */
  appeared: readonly T[]
  /**
   * Whether this assessment can be compared with the one before it at all.
   *
   * See `sourcesAreComparable`. A refresh that read a source the last one
   * could not makes every finding from that source look new.
   */
  comparable: boolean
  /**
   * Whether the operator has snoozed every object a finding names.
   *
   * APPLIED HERE rather than by the caller handing over a pre-filtered list,
   * so the rule has a test of its own on this side. A snooze is the operator
   * saying they already know; announcing one on the desktop would be the
   * loudest available way of ignoring that.
   */
  isSnoozed: (finding: T) => boolean
  /** Epoch milliseconds. Passed rather than read, so the rules are testable. */
  now: number
  /** When this cluster last notified, or 0 for never. */
  lastNotifiedAt: number
}

/**
 * The shortest gap between two notifications about one cluster.
 *
 * A BURST IS ALREADY ONE NOTIFICATION — a whole assessment's new findings are
 * one call naming the count, so twenty pods failing together never becomes
 * twenty of these. This is the other axis: a cluster that flaps produces a new
 * batch on every refresh, and the fastest refresh PodSteer offers is five
 * seconds. Without a floor, a workload in CrashLoopBackOff would post twelve
 * notifications a minute for as long as nobody fixed it.
 *
 * What a suppressed notification costs is the interruption and nothing else:
 * the finding is in the overview, in the navigator's count and in the session
 * timeline the whole time, and it is the interruption that people mute at the
 * operating system — taking with it the alarm they did want.
 */
export const NOTIFY_COOLDOWN_MS = 60_000

/**
 * How many rules a body names before it stops listing and counts instead.
 *
 * Three fits a notification on every platform without being truncated by the
 * OS, and a list long enough to be cut off is one that reads as though
 * something is missing — which it would be.
 */
const MAX_NAMED_RULES = 3

/**
 * The most a body may be, matching NotificationAPI's own cap.
 *
 * Kept on this side as well as in Go so the composition never produces
 * something the bridge then refuses: a notification that failed to post
 * because it was too long is one nobody ever sees or hears about.
 *
 * IN BYTES, because that is what Go counts. A cluster name in a script other
 * than Latin, or the em dash this very body uses, is more bytes than
 * characters, so a `.length` check here would agree with Go on English and
 * disagree with it on the cases nobody tests by hand.
 */
const MAX_BODY_BYTES = 240

const encoder = new TextEncoder()

/**
 * Whether two assessments read the same set of sources.
 *
 * THIS IS THE "PARTIAL REFRESH" RULE, and it is the same trap `diffFindings`
 * exists for, pointed the other way. A failed refresh carries no findings, so
 * reading it as an assessment reports everything clearing at once — that is
 * handled by passing `null` rather than an empty set. A PARTIAL one is
 * subtler: `Overview.unavailable` names the sources that could not be read,
 * and a source that was missing last refresh and answered this one hands over
 * every finding it produces in the same instant. Not one of them is new; they
 * were simply not being looked at, exactly as an unlisted pod's findings are
 * not cleared.
 *
 * So the sets have to match for the two assessments to be comparable at all.
 * Deliberately not "the current one is clean": a cluster with no
 * metrics-server reports that source unavailable on every refresh for ever,
 * and refusing to notify there would permanently mute the clusters most
 * likely to need it. What matters is that the source set did not MOVE.
 *
 * A null previous — no assessment yet — is not comparable with anything, which
 * is the same thing `diffFindings` says by returning nothing appeared.
 */
export function sourcesAreComparable(
  previous: readonly string[] | null,
  current: readonly string[],
): boolean {
  if (previous === null) return false
  if (previous.length !== current.length) return false

  const held = new Set(previous)
  return current.every((source) => held.has(source))
}

/**
 * Decides whether to raise a desktop notification, and composes it.
 *
 * The rules, in the order they are applied and each with a test:
 *
 * - **OFF BY DEFAULT, and the preference is checked first.** Off is the same
 *   choice `alertSoundsEnabled` makes and for the same reason: an application
 *   that starts interrupting somebody who never asked is one they silence at
 *   the operating system, which takes the alarm they DID want with it. There
 *   is a second reason here that the sound does not have — on macOS the first
 *   notification triggers a system permission prompt, and a prompt nobody
 *   asked for is one people deny permanently.
 * - **The platform has to be willing.** Not deliverable is not an error; it
 *   is a machine that shows no notifications, and the Settings pane says so.
 * - **Only a comparable assessment.** See `sourcesAreComparable`.
 * - **CRITICAL ONLY.** The sound offers a warning motif because a sound is
 *   over in half a second; a notification persists in a tray until it is
 *   dismissed, so the bar for one is higher. Warnings stay where warnings
 *   already are.
 * - **Snoozed findings are silent.** A snooze is the operator saying they
 *   know; announcing it on the desktop would be the loudest possible way of
 *   ignoring that.
 * - **One notification per batch, naming the count**, and at most one per
 *   cluster per NOTIFY_COOLDOWN_MS.
 *
 * A finding that was already there when PodSteer opened never reaches this at
 * all: `diffFindings` reports nothing appeared against a null baseline, so
 * the first assessment of a session establishes it and announces nothing.
 */
export function decideNotification<T extends NotifiableFinding>(
  input: NotifyInput<T>,
): NotificationPlan | null {
  if (!input.enabled) return null
  if (!input.deliverable) return null
  if (!input.comparable) return null

  const raised = input.appeared.filter(
    (finding) => finding.severity === 'critical' && !input.isSnoozed(finding),
  )
  if (raised.length === 0) return null

  if (input.lastNotifiedAt > 0 && input.now - input.lastNotifiedAt < NOTIFY_COOLDOWN_MS) {
    return null
  }

  return {
    id: `podsteer-findings-${input.clusterId}`,
    title:
      raised.length === 1 ? 'New critical finding' : `${raised.length} new critical findings`,
    body: composeBody(input.clusterId, raised),
    clusterId: input.clusterId,
    count: raised.length,
  }
}

/**
 * The sentence under the headline: where, and which rules.
 *
 * TITLES AND NOTHING ELSE. A finding's title is a rule's own name —
 * "CrashLoopBackOff", "Pods cannot be scheduled" — written here rather than
 * read off a cluster, so it names no object. The cluster is a kubeconfig
 * CONTEXT NAME, which travels on the same terms the settings file lets one
 * travel: a handle the operator's own machine already gives them.
 *
 * Distinct titles, because one rule firing on six pods is one line to read
 * and the count in the headline already says six.
 */
function composeBody(clusterId: string, raised: readonly NotifiableFinding[]): string {
  const titles = [...new Set(raised.map((finding) => finding.title))]

  const named = titles.slice(0, MAX_NAMED_RULES)
  const rest = titles.length - named.length
  const list = rest > 0 ? `${named.join(', ')} and ${rest} more` : named.join(', ')

  const full = clusterId === '' ? list : `${clusterId} — ${list}`
  if (encoder.encode(full).length <= MAX_BODY_BYTES) return full

  // A title long enough to overflow is a rule name somebody wrote at length,
  // not a leak — but a body the bridge would refuse is a notification nobody
  // sees, so it falls back to the half that is always short. The headline
  // still carries the count.
  //
  // Sixty CHARACTERS, which is at most 240 bytes however they are encoded.
  // Cutting on a byte boundary instead would risk splitting a character in
  // half; a lone surrogate here becomes one replacement character, which is
  // still inside the bound.
  return clusterId.slice(0, MAX_BODY_BYTES / 4)
}
