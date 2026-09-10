/**
 * The application's top-level state: which clusters exist, which are open, and
 * which tab is in front.
 *
 * The split from ClusterSession is deliberate. This store owns the tab *bar*;
 * each session owns the contents of one tab. Keeping them apart is what lets a
 * tab be closed without any other tab noticing, and what stops per-cluster
 * state leaking between clusters — the bug that makes multi-cluster tools show
 * you production's pod list under staging's heading.
 */

import {
  cancelConnect,
  connect,
  connections,
  disconnect,
  listClusters,
  onClusterUnreachable,
  onKubeconfigChanged,
  pingCluster,
  setReadOnly,
  type Cluster,
  type Unsubscribe,
} from '$lib/api/client'
import { ApiError, toApiError } from '$lib/api/errors'
import type { FleetTarget } from '$lib/fleet'
import { clusterActivity } from './activity.svelte'
import { fleet } from './fleet.svelte'
import { notifications } from './notifications.svelte'
import { organisation } from './organisation.svelte'
import { preferences } from './preferences.svelte'
import {
  ClusterSession,
  RICH_KIND_IDS,
  workloadKindId,
  type LoadStatus,
} from './session.svelte'

/**
 * The navigator id of a kind a merged table can show.
 *
 * The built-ins' own constants rather than a catalogue lookup: every kind
 * the All-clusters view lists is a built-in with a fixed id, present in
 * every cluster's catalogue, so there is nothing to discover — and the
 * target tab may not have loaded its catalogue yet when the click lands.
 */
function kindIdFor(kind: string): string | undefined {
  if (kind === 'Pod') return RICH_KIND_IDS.pods
  if (kind === 'Event') return RICH_KIND_IDS.events
  return workloadKindId(kind)
}

class Workspace {
  /** Every cluster in the kubeconfig, for the picker. */
  clusters = $state.raw<Cluster[]>([])
  clustersStatus = $state<LoadStatus>('idle')

  /** Open tabs, in the order they were opened. */
  sessions = $state<ClusterSession[]>([])

  /** The cluster id of the tab in front, or null when the picker is showing. */
  activeClusterId = $state<string | null>(null)

  /**
   * The clusters with a connect attempt in the air, in the order they were
   * started.
   *
   * A LIST RATHER THAN THE SINGLE ID THIS WAS. `connectingTo` held one id and
   * `open` began by returning early if it was set, so connecting to one
   * cluster disabled the control on every other — and a cluster behind a link
   * that drops packets rather than refusing them holds that lock for the whole
   * request timeout. An operator with five clusters and two of them down
   * waited for both to fail before they could open the three that were fine.
   *
   * Nothing about connecting was ever serial: the Go side runs each call on
   * its own goroutine against its own client. The queue was here.
   */
  connecting = $state<string[]>([])

  /**
   * Attempts the operator stopped, so their rejection is not reported as a
   * failure. A cancellation is an answer, not a fault, and the banner is for
   * faults.
   */
  #cancelled = new Set<string>()

  /** Whether this cluster has a connect attempt in the air. */
  isConnecting = (clusterId: string): boolean => this.connecting.includes(clusterId)

  /** A failure not owned by any one tab — connecting, or reading kubeconfig. */
  error = $state<ApiError | null>(null)

  #unsubscribers: Unsubscribe[] = []

  /** The session in front, or undefined when the picker is showing. */
  readonly active = $derived(
    this.sessions.find((session) => session.cluster.id === this.activeClusterId),
  )

  /** Whether any cluster is open. */
  readonly hasSessions = $derived(this.sessions.length > 0)

  /** Cluster ids currently open, for marking them in the picker. */
  readonly openIds = $derived(new Set(this.sessions.map((session) => session.cluster.id)))

  /**
   * Loads the cluster list and re-adopts anything the backend already has open.
   *
   * The re-adoption matters during development, where the Go process outlives
   * the page across a hot reload and is still connected to everything.
   *
   * NOTHING HERE WAITS ON A CLUSTER, and that is the point rather than an
   * optimisation. This function is what the splash screen waits for, and it
   * used to end by awaiting the restored tab's own initialise — which lists
   * kinds, lists namespaces and reads a view, all of them round trips to an
   * API server. An operator whose VPN was not up yet, or whose cluster was
   * simply slow that morning, got a splash screen for as long as the network
   * took to answer, with no cluster list, no settings and no way to reach any
   * of the other clusters that were perfectly reachable. Measured at 42
   * seconds on a warm tunnel, and unbounded on a black-holed one.
   *
   * The tab still loads; it loads BESIDE the application instead of in front
   * of it, and reports its own failure in its own surface, which is where the
   * operator can act on it.
   */
  initialise = async (): Promise<void> => {
    this.#subscribe()
    await this.loadClusters()

    try {
      const open = await connections()
      for (const cluster of open) {
        this.#adopt(cluster)
        // Reassert rather than trust the backend's own memory. This path is
        // taken during a `make dev` hot reload, where the Go process (and
        // therefore its Registry) outlives the page — see this function's
        // own doc comment — and the organisation may have changed on disk
        // since that connection's flag was last set.
        void this.syncReadOnly(cluster.id)
      }
      if (!this.activeClusterId && this.sessions.length > 0) {
        this.activeClusterId = this.sessions[0].cluster.id
      }
      // NOT AWAITED — see this function's own note. The session reports its
      // own failure through its own status, and a rejection here would have
      // nowhere to go anyway: this catch belongs to reading the connection
      // list, not to whatever one cluster is doing.
      void this.active?.initialise()
    } catch (cause) {
      this.error = toApiError(cause)
    }
  }

  loadClusters = async (): Promise<void> => {
    this.clustersStatus = 'loading'
    try {
      this.clusters = await listClusters()
      this.clustersStatus = 'ready'
      this.error = null
    } catch (cause) {
      this.clusters = []
      this.clustersStatus = 'error'
      this.error = toApiError(cause)
    }
  }

  /**
   * Opens a cluster in a new tab and brings it to the front.
   *
   * A cluster already open is simply focused rather than opened twice — the
   * operator clicking it in the picker means "show me that", not "connect
   * again".
   */
  open = async (clusterId: string, focus = true): Promise<void> => {
    // Only THIS cluster's own attempt is a reason not to start another. A
    // second click on the same card is a duplicate; a click on a different one
    // is a second cluster, and the whole point is that it does not queue.
    if (this.isConnecting(clusterId)) return

    const existing = this.sessions.find((session) => session.cluster.id === clusterId)
    if (existing) {
      if (focus) this.activeClusterId = clusterId
      return
    }

    this.connecting = [...this.connecting, clusterId]
    this.#cancelled.delete(clusterId)
    try {
      const cluster = await connect(clusterId)
      this.error = null

      // WHAT IT TURNED OUT TO BE, KEPT. A connected cluster identifies itself
      // from its version string, which is the strongest evidence there is and
      // exists only while it is open. Remembering it against the context name
      // is what makes the Home list right on the next launch, before anything
      // is connected. Nothing is stored when nothing was identified — see
      // preferences.rememberDistribution.
      preferences.rememberDistribution(clusterId, cluster.distributionId)

      const session = this.#adopt(cluster)
      // Not focused when the picker asked for a connection rather than for a
      // view: connecting several clusters in a row should not throw the
      // operator into the first one.
      if (focus) this.activeClusterId = cluster.id
      // Recorded here rather than in the picker: this is the moment a
      // connection is actually made, and it is the only one.
      clusterActivity.markConnected(cluster.id)

      // Mark it open in the picker without re-reading the kubeconfig.
      this.clusters = this.clusters.map((entry) => (entry.id === cluster.id ? cluster : entry))

      // The backend's read-only policy starts empty on every connect — see
      // application.Registry.Close — so the client re-asserts whatever the
      // cluster's CURRENT group says right away, rather than leaving a
      // production cluster whose group is marked read-only briefly
      // unguarded server-side between Connect returning and this call
      // landing.
      void this.syncReadOnly(cluster.id)

      await session.initialise()
    } catch (cause) {
      // Silent for an attempt the operator stopped: they know, they asked, and
      // a banner reporting it back to them is the application arguing with a
      // decision it was told about.
      if (!this.#cancelled.has(clusterId)) this.error = toApiError(cause)
    } finally {
      this.connecting = this.connecting.filter((id) => id !== clusterId)
      this.#cancelled.delete(clusterId)
    }
  }

  /**
   * Stops a connect attempt that has not answered yet.
   *
   * The rejection lands in `open`'s catch a moment later; the flag set here is
   * what tells that catch this was asked for rather than suffered.
   */
  stopConnecting = async (clusterId: string): Promise<void> => {
    if (!this.isConnecting(clusterId)) return
    this.#cancelled.add(clusterId)
    try {
      await cancelConnect(clusterId)
    } catch {
      // Nothing useful to say: the attempt either stopped or had already
      // finished, and both leave the operator where they wanted to be.
    }
  }

  /** Closes a tab and disconnects its cluster. */
  close = async (clusterId: string): Promise<void> => {
    const session = this.sessions.find((entry) => entry.cluster.id === clusterId)
    session?.dispose()
    // The notification cooldown goes with the tab. A closed cluster has no
    // baseline any more — reopening it establishes a fresh one and announces
    // nothing until something actually changes — so holding "this cluster was
    // interrupted forty seconds ago" would only mute the first real change
    // after a reconnect.
    notifications.forget(clusterId)

    const index = this.sessions.findIndex((entry) => entry.cluster.id === clusterId)
    this.sessions = this.sessions.filter((entry) => entry.cluster.id !== clusterId)

    // Focus the neighbour rather than falling back to the picker, which is
    // what every tabbed interface does and what muscle memory expects.
    if (this.activeClusterId === clusterId) {
      const neighbour = this.sessions[Math.min(index, this.sessions.length - 1)]
      this.activeClusterId = neighbour?.cluster.id ?? null
    }

    try {
      await disconnect(clusterId)
    } catch (cause) {
      this.error = toApiError(cause)
    }
  }

  /** Brings a tab to the front, loading it on first focus. */
  focus = async (clusterId: string): Promise<void> => {
    this.activeClusterId = clusterId

    const session = this.sessions.find((entry) => entry.cluster.id === clusterId)
    if (session && session.status === 'idle') {
      await session.initialise()
    }
  }

  /**
   * Opens one object of one cluster from a merged table — a row of the
   * All-clusters view, or a palette hit made from it.
   *
   * The tab first, then the object. `focus` initialises a tab that was
   * opened without ever being shown; `openObject` on that tab's session then
   * switches it to the object's kind, moves its namespace filter only if
   * that filter would hide the object, and opens the drawer — the same path
   * following a reference takes, so the panel arrives with its live
   * sections rather than the manifest alone. Nothing here reads any cluster
   * the row did not come from.
   */
  openInCluster = async (target: FleetTarget): Promise<void> => {
    const session = this.sessions.find((entry) => entry.cluster.id === target.cluster)
    const kindId = kindIdFor(target.kind)
    if (!session || !kindId) return

    await this.focus(target.cluster)
    await session.openObject(kindId, target.name, target.namespace, true)
  }

  /** Returns to the cluster picker without closing anything. */
  showPicker = (): void => {
    this.activeClusterId = null
  }

  /**
   * Pushes one open cluster's CURRENT read-only setting to the backend.
   *
   * This is the client re-asserting its own local guard (CLAUDE.md's
   * read-only section) — never a permission — so a failure here is logged
   * and swallowed rather than surfaced: the frontend's own disabling of
   * write controls, which reads `organisation` directly and does not depend
   * on this call succeeding, is what actually protects an operator in the
   * moment. A cluster that is not open is skipped rather than erroring, so a
   * stale call racing a closed tab does nothing rather than reopening one.
   */
  syncReadOnly = async (clusterId: string): Promise<void> => {
    if (!this.openIds.has(clusterId)) return

    const { project, group } = organisation.placementOf(clusterId)
    const { readOnly } = organisation.settingsFor(project, group)

    try {
      await setReadOnly(clusterId, readOnly)
    } catch (cause) {
      console.error(`podsteer: could not sync the read-only policy for ${clusterId}`, cause)
    }
  }

  /**
   * Re-syncs every open cluster's read-only setting.
   *
   * Called after anything in `organisation` that could have changed what any
   * open cluster's group says — a group's own settings, which cluster is in
   * which group, or a group moving to another project — rather than after
   * each specific mutation individually. Every one of those is an
   * infrequent, operator-driven edit in OrganiseDialog or the picker, so
   * resyncing the whole (typically small) set of open tabs costs nothing
   * worth optimising and cannot miss a case the way tracking each mutation
   * by hand could.
   */
  syncAllReadOnly = (): void => {
    for (const session of this.sessions) {
      void this.syncReadOnly(session.cluster.id)
    }
  }

  /**
   * Moves one tab left or right, wrapping.
   *
   * The picker counts as the first tab rather than as a separate place,
   * because that is how it behaves: it sits in the same strip, at the head,
   * and cycling that skipped it would make it unreachable from the keyboard
   * while every other tab was not.
   */
  cycleTab = (delta: -1 | 1): void => {
    // null for the picker, then one entry per open cluster, in tab order.
    const order: Array<string | null> = [null, ...this.sessions.map((s) => s.cluster.id)]
    if (order.length < 2) return

    const from = order.indexOf(this.activeClusterId)
    const to = (from + delta + order.length) % order.length
    const target = order[to]

    if (target === null) this.showPicker()
    else void this.focus(target)
  }

  /** Releases every tab's timer and the event subscription. */
  dispose = (): void => {
    for (const session of this.sessions) session.dispose()
    for (const stop of this.#unsubscribers) stop()
    this.#unsubscribers = []
  }

  /** Adds a session for a cluster, or returns the existing one. */
  #adopt(cluster: Cluster): ClusterSession {
    const existing = this.sessions.find((session) => session.cluster.id === cluster.id)
    if (existing) return existing

    const session = new ClusterSession(cluster)

    // A cluster deleted from the kubeconfig cannot be refreshed, retried or
    // reconnected — it is gone. Closing the tab is the honest response, and it
    // returns the operator to the cluster list where they can see what IS
    // there. Leaving it open would show stale data from a cluster that no
    // longer exists.
    session.onVanished = (reason) => {
      void this.close(cluster.id)
      // Raised at the workspace rather than on the session, because the
      // session is about to stop existing and its error would go with it.
      this.error = reason
    }
    this.sessions = [...this.sessions, session]
    return session
  }

  /**
   * Listens for what the backend notices without being asked.
   *
   * Two things arrive this way, and they are the only two: a connection that
   * failed on its own, and the kubeconfig changing under the application.
   * Both are events nobody's click caused, which is what makes them events
   * rather than answers.
   */
  #subscribe(): void {
    for (const stop of this.#unsubscribers) stop()
    this.#unsubscribers = [
      onClusterUnreachable((event) => {
        const session = this.sessions.find((entry) => entry.cluster.id === event.clusterId)
        if (session) {
          session.error = new ApiError('unreachable', event.reason)
        } else {
          this.error = new ApiError('unreachable', event.reason)
        }
      }),
      /**
       * THE LIST IS RE-READ AND NOTHING ELSE HAPPENS.
       *
       * A kubeconfig changing is somebody running `kubectl config
       * use-context` in another window, or a colleague's file landing in a
       * synced folder. What it must NOT do is touch the tabs: an open cluster
       * is a connection this operator made, and re-reading a file is not a
       * reason to disturb it — not to reconnect it, not to close it, and
       * certainly not to follow a `current-context` that moved, which is a
       * decision about somebody else's terminal.
       *
       * So the picker learns about a new context, a removed one leaves the
       * list, and every open tab carries on exactly as it was. A cluster
       * whose entry has gone keeps working until its credentials expire,
       * which is what the connection actually depends on.
       */
      onKubeconfigChanged(() => {
        void this.loadClusters()
      }),
    ]
  }

  /**
   * Asks every cluster whose tab is NOT in front whether it still answers.
   *
   * THE TAB IN FRONT IS SKIPPED because it is already being polled: its
   * workspace is mounted and its own refresh is recording contact on every
   * tick. Asking it again would be a second request per interval for an
   * answer it already has.
   *
   * WHY THIS EXISTS AT ALL. One workspace is mounted at a time — see
   * App.svelte, which keys it on the cluster id so the refresh timer moves
   * with the tab — so a background tab polls nothing. Its dot therefore said
   * what was true when somebody last looked at it, which on a laptop that
   * changes network is a green dot on a cluster that has been gone for an
   * hour.
   *
   * SILENT ON EVERY OTHER FAILURE. A ping that comes back forbidden or
   * refused is not this function's business — it hands the outcome to the
   * session, which counts only a transport failure. Nothing here raises an
   * error banner: an operator reading one cluster must not be interrupted by
   * a background question about another.
   */
  beat = async (): Promise<void> => {
    const active = this.activeClusterId
    await Promise.all(
      this.sessions
        .filter((session) => session.cluster.id !== active)
        .map(async (session) => {
          try {
            await pingCluster(session.cluster.id)
            session.noteLiveness(null)
          } catch (cause) {
            session.noteLiveness(toApiError(cause))
          }
        }),
    )
  }

  /**
   * Starts the heartbeat, and stops any previous one.
   *
   * NOT STARTED WHEN AUTO-REFRESH IS OFF. Somebody who set refresh to manual
   * chose to stop talking to their clusters, and a heartbeat they did not ask
   * for would be this application deciding otherwise — the same rule the
   * watch manager states when it reaps a watch nobody is reading.
   */
  startHeartbeat = (intervalMs: number): void => {
    this.stopHeartbeat()
    if (intervalMs <= 0) return
    this.#heartbeat = setInterval(() => void this.beat(), intervalMs)
  }

  stopHeartbeat = (): void => {
    if (this.#heartbeat === null) return
    clearInterval(this.#heartbeat)
    this.#heartbeat = null
  }

  #heartbeat: ReturnType<typeof setInterval> | null = null
}

/**
 * How often a tab that is not in front is asked whether its cluster answers.
 *
 * Thirty seconds, and deliberately slower than any refresh interval offered.
 * This is not a refresh — it reads /version and displays nothing — it is the
 * question "is that cluster still there", asked so a dot can stop claiming
 * something nobody has checked in an hour. One tiny request per background
 * cluster per half-minute is a cost worth paying for a tab bar that is true;
 * anything faster would be a poll of every open cluster wearing a disguise.
 */
export const HEARTBEAT_INTERVAL_MS = 30_000

/**
 * The application-wide workspace.
 *
 * A module singleton: the desktop app has one window, and per-component
 * instances would only invite them to disagree about which tab is in front.
 */
export const workspace = new Workspace()

// The merged tables read whatever tabs are open, and this is the mirror of
// the registry that says which — handed over as a function rather than
// imported by $stores/fleet, which would close a circle through
// $stores/session. Read at each fleet refresh, so a tab opened or closed a
// moment ago is in or out of the next read without anything being told.
fleet.openClusters = () => workspace.sessions.map((session) => session.cluster.id)

// And which of them have stopped answering, which a fleet read cannot find out
// for itself: on a watched kind the backend answers from an in-memory store
// without touching the network, so a cluster that has gone away keeps
// returning a confident count. See stripModel in $lib/fleet.
fleet.silentClusters = () =>
  workspace.sessions.filter((session) => !session.answering).map((session) => session.cluster.id)
