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
  connect,
  connections,
  disconnect,
  listClusters,
  onClusterUnreachable,
  setReadOnly,
  type Cluster,
  type Unsubscribe,
} from '$lib/api/client'
import { ApiError, toApiError } from '$lib/api/errors'
import { clusterActivity } from './activity.svelte'
import { organisation } from './organisation.svelte'
import { ClusterSession, type LoadStatus } from './session.svelte'

class Workspace {
  /** Every cluster in the kubeconfig, for the picker. */
  clusters = $state.raw<Cluster[]>([])
  clustersStatus = $state<LoadStatus>('idle')

  /** Open tabs, in the order they were opened. */
  sessions = $state<ClusterSession[]>([])

  /** The cluster id of the tab in front, or null when the picker is showing. */
  activeClusterId = $state<string | null>(null)

  /** The id a connection attempt is in flight for. */
  connectingTo = $state<string | null>(null)

  /** A failure not owned by any one tab — connecting, or reading kubeconfig. */
  error = $state<ApiError | null>(null)

  #unsubscribe: Unsubscribe | null = null

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
   */
  initialise = async (): Promise<void> => {
    this.#subscribe()
    await this.loadClusters()

    try {
      const open = await connections()
      for (const cluster of open) {
        this.#adopt(cluster)
        // Reassert rather than trust the backend's own memory. This path is
        // taken during a `wails dev` hot reload, where the Go process (and
        // therefore its Registry) outlives the page — see this function's
        // own doc comment — and the organisation may have changed on disk
        // since that connection's flag was last set.
        void this.syncReadOnly(cluster.id)
      }
      if (!this.activeClusterId && this.sessions.length > 0) {
        this.activeClusterId = this.sessions[0].cluster.id
      }
      await this.active?.initialise()
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
    if (this.connectingTo) return

    const existing = this.sessions.find((session) => session.cluster.id === clusterId)
    if (existing) {
      if (focus) this.activeClusterId = clusterId
      return
    }

    this.connectingTo = clusterId
    try {
      const cluster = await connect(clusterId)
      this.error = null

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
      this.error = toApiError(cause)
    } finally {
      this.connectingTo = null
    }
  }

  /** Closes a tab and disconnects its cluster. */
  close = async (clusterId: string): Promise<void> => {
    const session = this.sessions.find((entry) => entry.cluster.id === clusterId)
    session?.dispose()

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
    this.#unsubscribe?.()
    this.#unsubscribe = null
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
   * Listens for connections the backend notices have failed.
   *
   * These arrive without a call having been made, which makes them the one
   * path by which a tab learns its cluster went away.
   */
  #subscribe(): void {
    this.#unsubscribe?.()
    this.#unsubscribe = onClusterUnreachable((event) => {
      const session = this.sessions.find((entry) => entry.cluster.id === event.clusterId)
      if (session) {
        session.error = new ApiError('unreachable', event.reason)
      } else {
        this.error = new ApiError('unreachable', event.reason)
      }
    })
  }
}

/**
 * The application-wide workspace.
 *
 * A module singleton: the desktop app has one window, and per-component
 * instances would only invite them to disagree about which tab is in front.
 */
export const workspace = new Workspace()
