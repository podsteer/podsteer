/**
 * Per-cluster workspace state.
 *
 * One ClusterSession backs one tab. Everything an operator has set up while
 * looking at a cluster — which kind is selected, which namespace, what they
 * typed in the search box, the rows themselves — lives here rather than in a
 * global store, because switching tabs must not disturb any of it. That is the
 * whole promise of tabs: come back to a cluster and find it as you left it.
 */

import {
  ALL_NAMESPACES,
  getManifest,
  getOverview,
  listEvents,
  listKinds,
  listNamespaces,
  listNodes,
  listPods,
  listTable,
  listWorkloads,
  scaleWorkload,
  updateResource,
  type Cluster,
  type Finding,
  type K8sEvent,
  type Namespace,
  type Node,
  type Overview,
  type Pod,
  type ResourceKind,
  type ResourceTable,
  type TableRow,
  type Workload,
} from '$lib/api/client'
import { ApiError, toApiError } from '$lib/api/errors'
import { podStatusLabel } from '$lib/format'
import {
  parseAgeSeconds,
  parseQuantity,
  sortRows,
  type SortAccessors,
  type SortState,
} from '$lib/sort'
import { preferences } from './preferences.svelte'

/** Lifecycle of an asynchronous read. */
export type LoadStatus = 'idle' | 'loading' | 'ready' | 'error'

/** How often the current view re-fetches while auto-refresh is on. */
export const DEFAULT_REFRESH_INTERVAL_MS = 10_000

/**
 * The cluster dashboard's navigation id.
 *
 * Not a Kubernetes kind and deliberately not in the backend catalog: the
 * overview is a computed assessment, not a list of objects, and adding it to
 * the catalog would put it in front of every consumer that expects to be able
 * to GET what it names. The `podsteer` prefix cannot collide with a real API
 * group, which always contains a dot.
 */
export const OVERVIEW_KIND_ID = 'podsteer/overview'

/**
 * The view the navigator selects by default.
 *
 * The dashboard rather than the pod list: an operator opening a cluster is
 * asking "is anything wrong", and a pod list makes them work that out for
 * themselves by reading it.
 */
export const DEFAULT_KIND_ID = OVERVIEW_KIND_ID

/** Kind ids PodSteer renders with purpose-built columns rather than generically. */
export const RICH_KIND_IDS = {
  pods: 'core/v1/pods',
  nodes: 'core/v1/nodes',
  events: 'core/v1/events',
  namespaces: 'core/v1/namespaces',
} as const

/** Maps a rich workload kind id onto the controller name the backend expects. */
const WORKLOAD_KIND_BY_ID: Record<string, string> = {
  'apps/v1/deployments': 'Deployment',
  'apps/v1/statefulsets': 'StatefulSet',
  'apps/v1/daemonsets': 'DaemonSet',
  'apps/v1/replicasets': 'ReplicaSet',
  'batch/v1/jobs': 'Job',
  'batch/v1/cronjobs': 'CronJob',
}

/** What the content pane should render for the selected kind. */
export type ViewMode = 'overview' | 'pods' | 'nodes' | 'events' | 'workloads' | 'table'

/*
 * Sort accessors per view, keyed by the column ids the views declare. Values
 * are compared as numbers when both sides are numeric, otherwise as strings
 * with natural ordering; nulls (an unmeasured CPU, a CronJob that never ran)
 * always sort last.
 */
const POD_SORT: SortAccessors<Pod> = {
  status: (pod) => podStatusLabel(pod),
  name: (pod) => pod.name,
  namespace: (pod) => pod.namespace,
  cpu: (pod) => parseQuantity(pod.cpu),
  memory: (pod) => parseQuantity(pod.memory),
  ready: (pod) => pod.readyContainers,
  restarts: (pod) => pod.restarts,
  controlledBy: (pod) => pod.controlledBy,
  node: (pod) => pod.nodeName,
  qos: (pod) => pod.qosClass,
  ip: (pod) => pod.podIp,
  age: (pod) => pod.ageSeconds,
}

const NODE_SORT: SortAccessors<Node> = {
  status: (node) => node.status,
  name: (node) => node.name,
  roles: (node) => (node.roles.length ? node.roles.join(', ') : 'worker'),
  cpu: (node) => (node.hasMetrics ? node.cpuPercent : null),
  memory: (node) => (node.hasMetrics ? node.memoryPercent : null),
  version: (node) => node.version,
  ip: (node) => node.internalIp,
  os: (node) => node.osImage,
  pods: (node) => node.maxPods,
  taints: (node) => node.taints,
  age: (node) => node.ageSeconds,
}

const WORKLOAD_SORT: SortAccessors<Workload> = {
  status: (workload) => workload.status,
  name: (workload) => workload.name,
  namespace: (workload) => workload.namespace,
  schedule: (workload) => workload.schedule,
  lastRun: (workload) => workload.lastScheduled || null,
  ready: (workload) => workload.readyCount,
  updated: (workload) => workload.updated,
  available: (workload) => workload.available,
  images: (workload) => workload.images.join(', '),
  controlledBy: (workload) => workload.controlledBy,
  age: (workload) => workload.ageSeconds,
}

const EVENT_SORT: SortAccessors<K8sEvent> = {
  type: (event) => event.type,
  reason: (event) => event.reason,
  object: (event) => event.involvedObject,
  namespace: (event) => event.namespace,
  message: (event) => event.message,
  source: (event) => event.source,
  count: (event) => event.count,
  age: (event) => event.ageSeconds,
}

export class ClusterSession {
  /** The connected cluster this tab shows. */
  readonly cluster: Cluster

  /** Kinds the navigator can offer, from the backend's per-cluster catalog. */
  kinds = $state<ResourceKind[]>([])
  /** Namespaces, for the filter. */
  namespaces = $state<Namespace[]>([])

  /** The kind currently selected in the navigator. */
  selectedKindId = $state<string>(DEFAULT_KIND_ID)
  /** The namespace filter. ALL_NAMESPACES means every namespace. */
  namespace = $state<string>(ALL_NAMESPACES)
  /** The client-side search term. */
  search = $state<string>('')
  /** The 1-based page currently shown. */
  page = $state<number>(1)

  /** Active sort per kind id. Kinds hold different columns, so a sort set on
      one must not leak into another. */
  sorts = $state<Record<string, SortState>>({})

  /** Rows for whichever view is active. Only one is populated at a time. */
  pods = $state<Pod[]>([])
  nodes = $state<Node[]>([])
  workloads = $state<Workload[]>([])
  events = $state<K8sEvent[]>([])
  table = $state<ResourceTable | null>(null)
  overview = $state<Overview | null>(null)

  status = $state<LoadStatus>('idle')
  error = $state<ApiError | null>(null)
  lastRefreshedAt = $state<Date | null>(null)

  /** The selected row, shown in the detail drawer. */
  selectedName = $state<string | null>(null)
  selectedNamespace = $state<string>('')
  selectedPod = $state<Pod | null>(null)
  selectedWorkload = $state<Workload | null>(null)
  manifest = $state<string | null>(null)
  manifestStatus = $state<LoadStatus>('idle')

  /**
   * Monotonic request counter.
   *
   * A slow response must never overwrite a newer one. Clicking through three
   * kinds in quick succession leaves three requests in flight, and without
   * this the first to *return* wins rather than the last to be *asked* — which
   * shows the operator a list they already navigated away from.
   */
  #request = 0
  #timer: ReturnType<typeof setInterval> | null = null

  constructor(cluster: Cluster) {
    this.cluster = cluster
    // The operator's own last choice for this cluster wins over kubeconfig's
    // context default — that default is a fallback for kubectl, not a
    // statement about which namespace matters to whoever is looking at
    // PodSteer, and reconnecting to a cluster that was left on "billing"
    // should not silently snap back to "default".
    this.namespace = preferences.getClusterNamespace(cluster.id) ?? (cluster.defaultNamespace || ALL_NAMESPACES)
  }

  /** The kind currently selected, or undefined before kinds have loaded. */
  readonly selectedKind = $derived(this.kinds.find((kind) => kind.id === this.selectedKindId))

  /** What the content pane should render. */
  readonly viewMode = $derived.by<ViewMode>(() => {
    const id = this.selectedKindId
    if (id === OVERVIEW_KIND_ID) return 'overview'
    if (id === RICH_KIND_IDS.pods) return 'pods'
    if (id === RICH_KIND_IDS.nodes) return 'nodes'
    if (id === RICH_KIND_IDS.events) return 'events'
    if (id in WORKLOAD_KIND_BY_ID) return 'workloads'
    return 'table'
  })

  /** Whether the selected kind carries namespaces. */
  readonly isNamespaced = $derived(
    this.viewMode === 'overview' ? false : (this.selectedKind?.namespaced ?? true),
  )

  /**
   * Whether the current view is a list.
   *
   * The overview is an assessment of the whole cluster, so the search box,
   * the pagination and the row count in the toolbar have nothing to act on.
   */
  readonly isList = $derived(this.viewMode !== 'overview')

  /** Rows after the search filter, for whichever view is active. */
  readonly visiblePods = $derived(
    filterRows(this.pods, this.search, (pod) => [pod.name, pod.namespace, pod.nodeName, pod.phase]),
  )
  readonly visibleNodes = $derived(
    filterRows(this.nodes, this.search, (node) => [node.name, node.status, ...node.roles]),
  )
  readonly visibleWorkloads = $derived(
    filterRows(this.workloads, this.search, (workload) => [
      workload.name,
      workload.namespace,
      workload.status,
    ]),
  )
  readonly visibleEvents = $derived(
    filterRows(this.events, this.search, (event) => [
      event.reason,
      event.message,
      event.involvedObject,
      event.namespace,
    ]),
  )
  readonly visibleTableRows = $derived(
    filterRows(this.table?.rows ?? [], this.search, (row) => [row.name, row.namespace, ...row.cells]),
  )

  /** Total rows after filtering, before pagination. */
  readonly visibleCount = $derived.by(() => {
    switch (this.viewMode) {
      case 'overview':
        return 0
      case 'pods':
        return this.visiblePods.length
      case 'nodes':
        return this.visibleNodes.length
      case 'workloads':
        return this.visibleWorkloads.length
      case 'events':
        return this.visibleEvents.length
      default:
        return this.visibleTableRows.length
    }
  })

  /** How many pages the filtered rows fill. */
  readonly pageCount = $derived(
    Math.max(1, Math.ceil(this.visibleCount / preferences.pageSize)),
  )

  /**
   * The page actually rendered.
   *
   * Clamped rather than stored blindly: deleting enough rows, or typing a
   * narrower search, can leave the stored page beyond the end — and a table
   * that renders nothing while claiming "page 7 of 3" looks broken.
   */
  readonly currentPage = $derived(Math.min(Math.max(1, this.page), this.pageCount))

  /** Index of the first row on the current page, for the range readout. */
  readonly pageStart = $derived((this.currentPage - 1) * preferences.pageSize)

  /** The sort applied to the current kind, or null for server order. */
  readonly sort = $derived(this.sorts[this.selectedKindId] ?? null)

  /** Filtered rows after sorting, per view. */
  readonly sortedPods = $derived(sortRows(this.visiblePods, this.sort, POD_SORT))
  readonly sortedNodes = $derived(sortRows(this.visibleNodes, this.sort, NODE_SORT))
  readonly sortedWorkloads = $derived(sortRows(this.visibleWorkloads, this.sort, WORKLOAD_SORT))
  readonly sortedEvents = $derived(sortRows(this.visibleEvents, this.sort, EVENT_SORT))

  /**
   * Generic table rows after sorting. The column ids are positional ("c0"),
   * so the accessor is built from the table's own column definitions: numeric
   * columns compare as numbers, date columns as parsed ages, everything else
   * as text.
   */
  readonly sortedTableRows = $derived.by(() => {
    const state = this.sort
    const table = this.table
    const index = state ? /^c(\d+)$/.exec(state.columnId)?.[1] : undefined
    if (!state || !table || index === undefined) return this.visibleTableRows

    const column = table.columns[Number(index)]
    if (!column) return this.visibleTableRows

    const cell = (row: TableRow): string => row.cells[Number(index)] ?? ''
    let accessor: (row: TableRow) => string | number | null
    if (column.type === 'integer' || column.type === 'number') {
      accessor = (row) => {
        const parsed = Number.parseFloat(cell(row))
        return Number.isNaN(parsed) ? null : parsed
      }
    } else if (column.type === 'date') {
      accessor = (row) => parseAgeSeconds(cell(row))
    } else {
      accessor = cell
    }
    return sortRows(this.visibleTableRows, state, { [state.columnId]: accessor })
  })

  /** Rows of the current page, per view. */
  readonly pagedPods = $derived(this.#slice(this.sortedPods))
  readonly pagedNodes = $derived(this.#slice(this.sortedNodes))
  readonly pagedWorkloads = $derived(this.#slice(this.sortedWorkloads))
  readonly pagedEvents = $derived(this.#slice(this.sortedEvents))
  readonly pagedTableRows = $derived(this.#slice(this.sortedTableRows))

  /**
   * Findings that need acting on, minus anything the operator has snoozed.
   *
   * The counts the backend reports are deliberately not used for this: it
   * assesses the cluster and has no idea what somebody chose to live with
   * until Tuesday. Snoozing is applied here, once, so the verdict, this count
   * and the navigator badge cannot disagree about what is outstanding.
   */
  readonly activeIssues = $derived(
    (this.overview?.findings ?? []).filter(
      (finding) => finding.severity !== 'info' && !this.isFullySnoozed(finding),
    ),
  )

  /** Findings every object of which is currently quietened. */
  readonly snoozedIssues = $derived(
    (this.overview?.findings ?? []).filter(
      (finding) => finding.severity !== 'info' && this.isFullySnoozed(finding),
    ),
  )

  /** How many of a finding's listed objects are snoozed. */
  snoozedSubjectCount = (finding: Finding): number =>
    finding.subjects.filter(
      (subject) =>
        preferences.snoozedUntil(this.cluster.id, finding.id, subject.namespace, subject.name) > 0,
    ).length

  /**
   * Whether a finding has nothing left to say.
   *
   * Every object it names has to be snoozed, and it must not be truncated:
   * quietening the twenty-five rows of a capped list says nothing about the
   * fifteen it never showed, and treating that as silence would drop a
   * finding whose greater part was never seen.
   */
  isFullySnoozed = (finding: Finding): boolean =>
    finding.subjects.length > 0 &&
    !finding.truncated &&
    this.snoozedSubjectCount(finding) === finding.subjects.length

  /**
   * How many findings need attention, from the last assessment.
   *
   * Info findings are excluded: they are worth reading but do not mean
   * anything is wrong, and a badge that is permanently lit stops being read.
   */
  readonly issueCount = $derived(this.activeIssues.length)

  /** Whether any finding is critical, which decides the badge's colour. */
  readonly hasCriticalIssues = $derived(
    this.activeIssues.some((finding) => finding.severity === 'critical'),
  )

  /**
   * The verdict, re-graded over what is actually outstanding.
   *
   * The same rule the backend applies (domain/overview.go grade) — worst
   * severity wins — but over the findings left after snoozing. Re-applying it
   * here rather than reading `overview.health` is what makes a snooze mean
   * something: quietening the only warning on a cluster should leave it
   * reading as healthy, not as degraded by something deliberately deferred.
   */
  readonly health = $derived.by((): 'healthy' | 'degraded' | 'critical' => {
    if (this.activeIssues.some((finding) => finding.severity === 'critical')) return 'critical'
    if (this.activeIssues.length > 0) return 'degraded'
    return 'healthy'
  })

  /** Counts for the header summary, meaningful only for pod views. */
  readonly podSummary = $derived({
    total: this.pods.length,
    unhealthy: this.pods.filter((pod) => !pod.isHealthy).length,
    restarts: this.pods.reduce((sum, pod) => sum + pod.restarts, 0),
  })

  /** Loads the navigator tree and namespace list, then the default view. */
  initialise = async (): Promise<void> => {
    await Promise.all([this.loadKinds(), this.loadNamespaces()])
    await this.refresh()
  }

  loadKinds = async (): Promise<void> => {
    try {
      this.kinds = await listKinds(this.cluster.id)
    } catch (cause) {
      this.error = toApiError(cause)
    }
  }

  loadNamespaces = async (): Promise<void> => {
    try {
      this.namespaces = await listNamespaces(this.cluster.id)
    } catch (cause) {
      // A cluster whose RBAC forbids listing namespaces is still usable for a
      // namespace named directly, so this empties the filter rather than
      // blocking the whole tab.
      const error = toApiError(cause)
      this.namespaces = []
      if (error.code !== 'forbidden') this.error = error
    }
  }

  /** Selects a kind and loads it. */
  selectKind = async (kindId: string): Promise<void> => {
    if (kindId === this.selectedKindId) return
    this.selectedKindId = kindId
    this.page = 1
    this.closeDetail()
    await this.refresh()
  }

  /** Changes the namespace filter, remembers it for this cluster, and reloads. */
  selectNamespace = async (namespace: string): Promise<void> => {
    if (namespace === this.namespace) return
    this.namespace = namespace
    preferences.setClusterNamespace(this.cluster.id, namespace)
    this.page = 1
    await this.refresh()
  }

  /**
   * Sets the search term.
   *
   * Resets to the first page: narrowing a search while on page 4 otherwise
   * lands the operator on an empty page of a shorter list.
   */
  setSearch = (search: string): void => {
    this.search = search
    this.page = 1
  }

  /** Moves to a page, clamped to the range that exists. */
  goToPage = (page: number): void => {
    this.page = Math.min(Math.max(1, page), this.pageCount)
  }

  /**
   * Cycles a column's sort: ascending, descending, then back to server order.
   *
   * Resorting lands on the first page, for the same reason searching does —
   * the rows have been reshuffled, so wherever the operator was pointing no
   * longer exists.
   */
  toggleSort = (columnId: string): void => {
    const current = this.sorts[this.selectedKindId]
    const next: SortState | null =
      !current || current.columnId !== columnId
        ? { columnId, direction: 'asc' }
        : current.direction === 'asc'
          ? { columnId, direction: 'desc' }
          : null

    const sorts = { ...this.sorts }
    if (next) {
      sorts[this.selectedKindId] = next
    } else {
      delete sorts[this.selectedKindId]
    }
    this.sorts = sorts
    this.page = 1
  }

  /** Slices a filtered list down to the current page. */
  #slice<T>(rows: T[]): T[] {
    const start = (Math.min(Math.max(1, this.page), Math.max(1, Math.ceil(rows.length / preferences.pageSize))) - 1) * preferences.pageSize
    return rows.slice(start, start + preferences.pageSize)
  }

  /** Reloads whichever view is active. */
  refresh = async (): Promise<void> => {
    const request = ++this.#request
    this.status = 'loading'

    try {
      const rows = await this.#fetch()
      if (request !== this.#request) return

      this.#assign(rows)
      this.status = 'ready'
      this.lastRefreshedAt = new Date()
      this.error = null
    } catch (cause) {
      if (request !== this.#request) return
      this.status = 'error'
      this.error = toApiError(cause)
    }
  }

  /** Issues the call the active view needs. */
  async #fetch(): Promise<unknown> {
    const { id } = this.cluster
    const namespace = this.isNamespaced ? this.namespace : ALL_NAMESPACES

    switch (this.viewMode) {
      case 'overview':
        return getOverview(id)
      case 'pods':
        return listPods(id, namespace)
      case 'nodes':
        return listNodes(id)
      case 'events':
        return listEvents(id, namespace)
      case 'workloads':
        return listWorkloads(id, WORKLOAD_KIND_BY_ID[this.selectedKindId], namespace)
      default:
        return listTable(id, this.selectedKindId, namespace)
    }
  }

  /** Stores a fetch result in the field its view reads. */
  #assign(rows: unknown): void {
    // Clearing the others matters: a stale pod list left behind would flash
    // back into view for a frame when the operator returns to Pods.
    this.pods = []
    this.nodes = []
    this.workloads = []
    this.events = []
    this.table = null
    // The overview is deliberately NOT cleared here. It is a cached assessment
    // rather than one of the mutually exclusive row buffers, and the navigator
    // badge reads from it: clearing it would blank the "3 issues" badge the
    // moment an operator clicked through to look at those three issues.

    switch (this.viewMode) {
      case 'overview':
        this.overview = rows as Overview
        break
      case 'pods':
        this.pods = rows as Pod[]
        break
      case 'nodes':
        this.nodes = rows as Node[]
        break
      case 'events':
        this.events = rows as K8sEvent[]
        break
      case 'workloads':
        this.workloads = rows as Workload[]
        break
      default:
        this.table = rows as ResourceTable
    }
  }

  // --- Detail drawer --------------------------------------------------------

  /** Opens the detail drawer for one object and loads its manifest. */
  openDetail = async (name: string, namespace: string, pod?: Pod, workload?: Workload): Promise<void> => {
    this.selectedName = name
    this.selectedNamespace = namespace
    this.selectedPod = pod ?? null
    this.selectedWorkload = workload ?? null
    this.manifest = null
    this.manifestStatus = 'loading'

    try {
      this.manifest = await getManifest(
        this.cluster.id,
        this.selectedKindId,
        namespace,
        name,
      )
      this.manifestStatus = 'ready'
    } catch (cause) {
      this.manifestStatus = 'error'
      this.error = toApiError(cause)
    }
  }

  /** Closes the detail drawer. */
  closeDetail = (): void => {
    this.selectedName = null
    this.selectedNamespace = ''
    this.selectedPod = null
    this.selectedWorkload = null
    this.manifest = null
    this.manifestStatus = 'idle'
  }

  // --- Auto-refresh ---------------------------------------------------------

  /**
   * Starts polling the active view.
   *
   * Polling rather than a watch stream, deliberately: this is the foundation,
   * and a poll is correct — if occasionally late — under every condition a
   * watch has to handle. Swapping in a watch changes this method and nothing
   * else.
   */
  startAutoRefresh = (intervalMs: number = preferences.effectiveIntervalMs): void => {
    this.stopAutoRefresh()

    // Zero means the operator chose manual refreshing in Settings. Starting a
    // zero-delay interval would spin the CPU rather than doing nothing.
    if (intervalMs <= 0) return

    this.#timer = setInterval(() => {
      // Skip while a request is in flight, so a slow cluster cannot accumulate
      // a backlog of overlapping refreshes.
      if (this.status === 'loading') return
      void this.refresh()
    }, intervalMs)
  }

  stopAutoRefresh = (): void => {
    if (this.#timer !== null) {
      clearInterval(this.#timer)
      this.#timer = null
    }
  }

  /** Releases the timer, for when the tab closes. */
  dispose = (): void => {
    this.stopAutoRefresh()
  }

  /** Scales a workload to the specified number of replicas. */
  scaleWorkload = async (kind: string, name: string, namespace: string, replicas: number): Promise<void> => {
    try {
      await scaleWorkload(this.cluster.id, kind, namespace, name, replicas)
    } catch (cause) {
      this.error = toApiError(cause)
    }
  }

  /** Updates a resource with the provided YAML manifest. */
  updateResource = async (manifest: string): Promise<void> => {
    try {
      await updateResource(this.cluster.id, manifest)
    } catch (cause) {
      this.error = toApiError(cause)
    }
  }
}

/**
 * Filters rows by a search term across the fields a projector exposes.
 *
 * Case-insensitive substring matching rather than fuzzy: an operator searching
 * a pod list is usually pasting part of a name they already have, and fuzzy
 * matching would bury the exact hit among approximations.
 */
function filterRows<T>(rows: T[], search: string, project: (row: T) => (string | undefined)[]): T[] {
  const term = search.trim().toLowerCase()
  if (!term) return rows

  return rows.filter((row) =>
    project(row).some((field) => field?.toLowerCase().includes(term)),
  )
}
