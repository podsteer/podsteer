/**
 * The application's Kubernetes state.
 *
 * A single runes-backed store class rather than a set of loose writables: the
 * pieces of this state are strongly coupled — connecting a cluster invalidates
 * the namespace list, which invalidates the pod list — and keeping the
 * transitions in one object is what stops those invalidations from being
 * forgotten at some call site.
 *
 * The file is named `.svelte.ts` because Svelte 5 only compiles runes in files
 * carrying that extension. It is ordinary TypeScript otherwise.
 */

import {
  activeCluster as fetchActiveCluster,
  connect as requestConnect,
  listClusters,
  listNamespaces,
  listPods,
  onClusterUnreachable,
  type Cluster,
  type Namespace,
  type Pod,
  type Unsubscribe,
} from '$lib/api/client'
import { ApiError, toApiError } from '$lib/api/errors'

/** Lifecycle of an asynchronous read. */
export type LoadStatus = 'idle' | 'loading' | 'ready' | 'error'

/** How often pods are re-fetched while auto-refresh is on. */
export const DEFAULT_REFRESH_INTERVAL_MS = 10_000

/** Sentinel for "every namespace", matching the backend's empty-string convention. */
export const ALL_NAMESPACES = ''

class K8sStore {
  // ---- Clusters --------------------------------------------------------

  /** Clusters found in the local kubeconfig. */
  clusters = $state<Cluster[]>([])
  /** Status of the cluster list. */
  clustersStatus = $state<LoadStatus>('idle')
  /** The connected cluster, or null. */
  activeCluster = $state<Cluster | null>(null)
  /** Id of the cluster a connection attempt is in flight for. */
  connectingTo = $state<string | null>(null)

  // ---- Namespaces ------------------------------------------------------

  /** Namespaces of the connected cluster. */
  namespaces = $state<Namespace[]>([])
  /** The namespace being browsed; ALL_NAMESPACES means every namespace. */
  selectedNamespace = $state<string>(ALL_NAMESPACES)

  // ---- Pods ------------------------------------------------------------

  /** Pods of the selected namespace. */
  pods = $state<Pod[]>([])
  /** Status of the pod list. */
  podsStatus = $state<LoadStatus>('idle')
  /** When the pod list last completed a refresh. */
  lastRefreshedAt = $state<Date | null>(null)

  // ---- Diagnostics -----------------------------------------------------

  /** The most recent failure, or null. Cleared by the next successful read. */
  error = $state<ApiError | null>(null)

  // ---- Derived ---------------------------------------------------------

  /** Whether a cluster is connected. */
  readonly isConnected = $derived(this.activeCluster !== null)

  /** Whether the pod list has never loaded. */
  readonly isInitialPodLoad = $derived(this.podsStatus === 'loading' && this.pods.length === 0)

  /** Pods that are not doing what they should — the ones worth looking at. */
  readonly unhealthyPods = $derived(this.pods.filter((pod) => !pod.isHealthy))

  /** Pod counts for the dashboard summary. */
  readonly podSummary = $derived({
    total: this.pods.length,
    healthy: this.pods.length - this.unhealthyPods.length,
    unhealthy: this.unhealthyPods.length,
    restarts: this.pods.reduce((sum, pod) => sum + pod.restarts, 0),
  })

  // ---- Internals -------------------------------------------------------

  /**
   * Monotonic request counters.
   *
   * A slow response must never overwrite a newer one. Switching namespace
   * twice in quick succession leaves two list calls in flight, and without
   * this the first to *return* wins rather than the last to be *asked* —
   * which shows the operator a namespace they already navigated away from.
   */
  #podsRequest = 0
  #namespacesRequest = 0

  #refreshTimer: ReturnType<typeof setInterval> | null = null
  #unsubscribeUnreachable: Unsubscribe | null = null

  // ---- Actions ---------------------------------------------------------

  /**
   * Loads the cluster list and adopts whatever cluster the backend already
   * considers active.
   *
   * Called once at startup. The active-cluster query matters on a hot reload
   * during development, where the Go process outlives the page and is still
   * connected.
   */
  initialise = async (): Promise<void> => {
    this.#subscribeToEvents()
    await this.loadClusters()

    try {
      const active = await fetchActiveCluster()
      if (active) {
        await this.#adoptCluster(active)
      }
    } catch (cause) {
      // A missing active cluster is the normal startup state and is reported
      // as null, not an error — so anything landing here is worth surfacing.
      this.error = toApiError(cause)
    }
  }

  /** Reloads the cluster list from the kubeconfig. */
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
   * Connects to a cluster, then loads its namespaces and pods.
   *
   * The namespace preselected is the one pinned by the kubeconfig context,
   * which is almost always the namespace the operator meant.
   */
  connect = async (clusterId: string): Promise<void> => {
    if (this.connectingTo) return

    this.connectingTo = clusterId
    try {
      const cluster = await requestConnect(clusterId)
      this.error = null
      await this.#adoptCluster(cluster)
    } catch (cause) {
      this.error = toApiError(cause)
    } finally {
      this.connectingTo = null
    }
  }

  /** Disconnects locally, returning the UI to the cluster picker. */
  disconnect = (): void => {
    this.stopAutoRefresh()
    this.activeCluster = null
    this.namespaces = []
    this.pods = []
    this.podsStatus = 'idle'
    this.selectedNamespace = ALL_NAMESPACES
    this.lastRefreshedAt = null
    this.error = null
  }

  /** Switches the namespace being browsed and reloads its pods. */
  selectNamespace = async (namespace: string): Promise<void> => {
    if (namespace === this.selectedNamespace) return

    this.selectedNamespace = namespace
    await this.loadPods()
  }

  /** Reloads the namespace list of the connected cluster. */
  loadNamespaces = async (): Promise<void> => {
    if (!this.activeCluster) return

    const request = ++this.#namespacesRequest
    try {
      const namespaces = await listNamespaces()
      if (request !== this.#namespacesRequest) return
      this.namespaces = namespaces
    } catch (cause) {
      if (request !== this.#namespacesRequest) return

      // A cluster whose RBAC forbids listing namespaces is still perfectly
      // usable for a namespace the operator names directly, so this failure
      // empties the picker rather than blocking the whole view.
      this.namespaces = []
      const error = toApiError(cause)
      if (error.code !== 'forbidden') {
        this.error = error
      }
    }
  }

  /** Reloads the pods of the selected namespace. */
  loadPods = async (): Promise<void> => {
    if (!this.activeCluster) return

    const request = ++this.#podsRequest
    this.podsStatus = 'loading'

    try {
      const pods = await listPods(this.selectedNamespace)
      if (request !== this.#podsRequest) return

      this.pods = pods
      this.podsStatus = 'ready'
      this.lastRefreshedAt = new Date()
      this.error = null
    } catch (cause) {
      if (request !== this.#podsRequest) return

      this.podsStatus = 'error'
      this.error = toApiError(cause)
    }
  }

  /** Reloads namespaces and pods together. */
  refresh = async (): Promise<void> => {
    await Promise.all([this.loadNamespaces(), this.loadPods()])
  }

  // ---- Auto-refresh ----------------------------------------------------

  /**
   * Starts polling the pod list.
   *
   * Polling rather than a watch stream, deliberately: this is the foundation,
   * and a poll is correct — if occasionally late — under every condition a
   * watch has to handle (reconnects, resource-version expiry, compaction).
   * Swapping in a watch later changes this method and nothing else.
   *
   * A refresh is skipped while one is still in flight, so a slow cluster
   * cannot accumulate a backlog of overlapping requests.
   */
  startAutoRefresh = (intervalMs: number = DEFAULT_REFRESH_INTERVAL_MS): void => {
    this.stopAutoRefresh()
    this.#refreshTimer = setInterval(() => {
      if (this.podsStatus === 'loading' || !this.activeCluster) return
      void this.loadPods()
    }, intervalMs)
  }

  /** Stops polling. */
  stopAutoRefresh = (): void => {
    if (this.#refreshTimer !== null) {
      clearInterval(this.#refreshTimer)
      this.#refreshTimer = null
    }
  }

  /** Releases the timer and event subscriptions. */
  dispose = (): void => {
    this.stopAutoRefresh()
    this.#unsubscribeUnreachable?.()
    this.#unsubscribeUnreachable = null
  }

  // ---- Private ---------------------------------------------------------

  /** Adopts a connected cluster and loads everything scoped to it. */
  async #adoptCluster(cluster: Cluster): Promise<void> {
    this.activeCluster = cluster
    this.selectedNamespace = cluster.defaultNamespace || ALL_NAMESPACES
    this.pods = []

    // Update the entry in the picker so it shows as reachable, with its
    // version, without re-reading the whole kubeconfig.
    this.clusters = this.clusters.map((entry) => (entry.id === cluster.id ? cluster : entry))

    await this.refresh()
  }

  /**
   * Listens for connection failures the backend detects on its own.
   *
   * These arrive without a call having been made — the backend can notice a
   * cluster went away — so they are the one path by which the UI learns
   * something it did not ask about.
   */
  #subscribeToEvents(): void {
    this.#unsubscribeUnreachable?.()
    this.#unsubscribeUnreachable = onClusterUnreachable((event) => {
      if (this.activeCluster && this.activeCluster.id !== event.clusterId) return
      this.error = new ApiError('unreachable', event.reason)
    })
  }
}

/**
 * The application-wide Kubernetes store.
 *
 * A module singleton: the desktop app has exactly one window and one connected
 * cluster, so per-component instances would only invite them to disagree.
 */
export const k8s = new K8sStore()
