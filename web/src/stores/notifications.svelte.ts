/**
 * Desktop notifications for a new critical finding.
 *
 * THE PLUMBING ONLY. Every rule about whether one is raised lives in
 * `$lib/notify`, as a pure function with a test each; this holds the two
 * pieces of state that cannot be pure — what the platform says it will do,
 * and when each cluster last interrupted somebody — and makes the one call
 * across the bridge.
 *
 * IT SITS BESIDE THE SOUND, NOT INSTEAD OF IT. `$stores/alerts` plays a motif
 * for a new warning or critical while the window is in front; this is for
 * when it is not. They are raised from the same place, from the same diff, so
 * the two can never disagree about what is new — see `ClusterSession.#adopt`.
 *
 * OFF UNTIL SOMEBODY ASKS. The preference defaults to off, and permission is
 * requested at the moment they turn it on rather than at startup: on macOS
 * that request is a visible system prompt, and one that arrives unbidden is
 * one people deny for ever.
 */

import { Capability, Notify, Request } from '$lib/wailsjs/go/wails/NotificationAPI'
import { decideNotification, type NotifiableFinding, type NotificationPlan } from '$lib/notify'

/** What the platform says it will do. */
export interface NotificationCapability {
  /** Whether this build and platform can deliver at all. */
  supported: boolean
  /** Whether the operating system will let PodSteer post one. */
  authorised: boolean
}

class DesktopNotifications {
  /**
   * What the platform reported, or null before it has been asked.
   *
   * Null rather than a hopeful default: the Settings pane renders a different
   * sentence for "cannot" and for "not asked yet", and a default of true
   * would have it claim the switch works on a machine where it does not.
   */
  capability = $state.raw<NotificationCapability | null>(null)

  /**
   * When each cluster last posted one, keyed by kubeconfig context name.
   *
   * PER CLUSTER, so one noisy staging cluster cannot use up the whole
   * window's budget and leave production silent. Not reactive: nothing
   * renders it, and it exists only to decide.
   *
   * In memory and gone with the process, which is the right lifetime for it —
   * carrying a cooldown across a restart would mean writing when somebody was
   * last interrupted to disk, and it would be wrong anyway: an application
   * that has just started has nothing to compare against yet.
   */
  #lastAt = new Map<string, number>()

  /** Whether anything at all would be delivered if it were posted. */
  readonly deliverable = $derived(
    this.capability !== null && this.capability.supported && this.capability.authorised,
  )

  /**
   * Asks the platform what it will do.
   *
   * Called when the Settings pane opens, so the switch can explain itself,
   * and once at startup so the first finding of a session does not have to
   * wait on a round trip to find out. Failures leave the capability
   * unsupported rather than throwing: a machine that cannot answer is a
   * machine that shows nothing, which is what the pane then says.
   */
  probe = async (): Promise<void> => {
    try {
      const reported = await Capability()
      this.capability = { supported: reported.supported, authorised: reported.authorised }
    } catch {
      this.capability = { supported: false, authorised: false }
    }
  }

  /**
   * Asks the operating system for permission, and reports whether it was
   * given.
   *
   * Called from the Settings switch as it is turned ON — never from startup
   * and never on the path of an actual finding. On macOS this is the system
   * prompt; everywhere else it answers true without showing anything.
   */
  authorise = async (): Promise<boolean> => {
    try {
      const granted = await Request()
      this.capability = { supported: true, authorised: granted }
      return granted
    } catch {
      // Refused, or a platform that cannot. Either way nothing is posted, and
      // the pane reads the capability rather than an error nobody can act on.
      this.capability = { supported: false, authorised: false }
      return false
    }
  }

  /**
   * Raises one notification for what an assessment just added, if the rules
   * allow it.
   *
   * Returns the plan that was posted, or null when nothing was — which is the
   * ordinary outcome and is what the tests assert against. `enabled` is passed
   * in rather than read from preferences here so the decision has one place it
   * can be argued with; the caller is `ClusterSession.#adopt`.
   *
   * FAILURES ARE SWALLOWED, exactly as the alert sound's are: a notification
   * that could not be posted must never become an error on the assessment it
   * came from.
   */
  raise = async <T extends NotifiableFinding>(input: {
    clusterId: string
    enabled: boolean
    appeared: readonly T[]
    comparable: boolean
    isSnoozed: (finding: T) => boolean
  }): Promise<NotificationPlan | null> => {
    const now = Date.now()
    const plan = decideNotification({
      clusterId: input.clusterId,
      enabled: input.enabled,
      deliverable: this.deliverable,
      appeared: input.appeared,
      comparable: input.comparable,
      isSnoozed: input.isSnoozed,
      now,
      lastNotifiedAt: this.#lastAt.get(input.clusterId) ?? 0,
    })
    if (!plan) return null

    // STAMPED BEFORE THE CALL, not after it. A post that is slow to resolve
    // must not let the next refresh's batch through underneath it — the same
    // reason `#refreshKinds` stamps before its own call rather than on
    // success.
    this.#lastAt.set(input.clusterId, now)

    try {
      await Notify({
        id: plan.id,
        title: plan.title,
        body: plan.body,
        clusterId: plan.clusterId,
      })
    } catch {
      // A platform that refused, or a window on its way out. Nothing to
      // report: the finding is already in the overview and the timeline.
      return null
    }

    return plan
  }

  /** Drops a closed cluster's cooldown, so reopening it starts clean. */
  forget = (clusterId: string): void => {
    this.#lastAt.delete(clusterId)
  }
}

export const notifications = new DesktopNotifications()
