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
  getOverviewForTarget,
  listEvents,
  listKinds,
  listNamespaces,
  listApplications,
  listNamespaceSummaries,
  workloadConsumption,
  listNodes,
  listPods,
  listTable,
  listWorkloads,
  scaleWorkload,
  updateResource,
  validateResource,
  type ApplyOutcome,
  type Cluster,
  type Finding,
  type K8sEvent,
  type Namespace,
  type NamespaceSummary,
  type Application,
  type ApplicationInventory,
  type Consumption,
  type Node,
  type Overview,
  type Pod,
  type ResourceKind,
  type ResourceTable,
  type TableRow,
  type Workload,
} from '$lib/api/client'
import { ApiError, toApiError } from '$lib/api/errors'
import { findAutoscalers, type AutoscalerCheck } from '$lib/autoscalers'
import { RowSelection } from '$lib/selection.svelte'
import { nodeItem, podItem, rowKey, tableRowItem, workloadItem, type BulkItem } from '$lib/bulk'
import { podStatusLabel } from '$lib/format'
import { matchesPodStatusChips } from '$lib/podStatusFilters'
import { EVENT_CHIPS, WORKLOAD_CHIPS, matchesChips, type FleetRow, type FleetTab } from '$lib/fleet'
import { describeQuery, matches, parseQuery, type Query, type Row } from '$lib/query'
import {
  annotationKeysOf,
  customSearchText,
  customSortAccessor,
  keysOnScreen,
  type MetadataKeys,
  type MetadataRow,
} from '$lib/customColumns'
import {
  parseAgeSeconds,
  parseQuantity,
  sortRows,
  type SortAccessors,
  type SortState,
} from '$lib/sort'
import { alertPlayer } from './alerts.svelte'
import { forgetConfigMaps } from './configMaps.svelte'
import { forgetVulnerabilities } from './vulnerabilities.svelte'
import { timeline } from './timeline.svelte'
import { notifications } from './notifications.svelte'
// The ONE finding diff, shared with the session timeline — see #adopt.
import { diffFindings } from '$lib/timeline'
import { sourcesAreComparable } from '$lib/notify'
import { usageHistory, usageKey } from './usageHistory.svelte'
import { preferences } from './preferences.svelte'
import { fleet } from './fleet.svelte'

/** Lifecycle of an asynchronous read. */

/**
 * How many ticks a request may miss before it is written off as wedged.
 *
 * Four, which is generous enough that an ordinary slow cluster is never
 * interrupted and short enough that nobody stares at a frozen screen for
 * long. The replaced request is not cancelled — nothing here can cancel one —
 * it is simply superseded, and refresh()'s generation guard stops it landing.
 */
const MISSED_TICKS_BEFORE_RETRY = 4

export type LoadStatus = 'idle' | 'loading' | 'ready' | 'error'

/** One measurement of the open pod, taken from a refresh that already happened. */
export interface UsageSample {
  /** Epoch milliseconds, from the client — these are points on a local clock,
      spaced by the refresh interval, not timestamps the cluster asserted. */
  at: number
  /**
   * CPU in CORES and memory in BYTES, parsed back out of the strings Go
   * formatted. Both are only ever used to draw a shape scaled to its own
   * peak, so the units matter less than their consistency across samples —
   * which is why they are parsed from one formatter's output rather than
   * mixed with a second source.
   */
  cpuCores: number
  memoryBytes: number
}

/**
 * How many samples the open drawer keeps.
 *
 * Two hundred is a little over half an hour at the default ten-second
 * refresh, and about forty kilobytes. Long enough to show a shape; short
 * enough that nobody has to think about it.
 */
const MAX_USAGE_SAMPLES = 200

/** How often the current view re-fetches while auto-refresh is on. */
export const DEFAULT_REFRESH_INTERVAL_MS = 10_000

/** One object recently opened in this cluster's detail drawer. */
export interface RecentObject {
  kindId: string
  name: string
  namespace: string
}

/**
 * How many recently opened objects the Recent section keeps.
 *
 * Twelve, the same order of magnitude as the pinned-kinds star affordance it
 * sits beside in the navigator — enough to cover a working session's worth of
 * "what was I just looking at" without becoming a second, unbounded list.
 */
const MAX_RECENT_OBJECTS = 12

/**
 * How stale the browsable-kind list may get before it is re-read.
 *
 * Its own cadence rather than the row poll's, because the two cost wildly
 * different amounts. Listing pods is one request; enumerating kinds is a
 * discovery walk — ServerPreferredResources issues a request per API group,
 * which is dozens on a cluster carrying a few operators. Running that every
 * couple of seconds to catch a CRD installed once a month is not a trade
 * worth making.
 *
 * Five minutes is chosen against what it is watching for: a CRD appears when
 * somebody installs an operator, and somebody who has just done that will
 * wait a few minutes for it to show up far more readily than they would
 * reconnect a tab to find out it worked.
 */
const KINDS_REFRESH_INTERVAL_MS = 5 * 60_000

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
/**
 * The applications view, pinned beside the overview.
 *
 * A PSEUDO-ENTRY, NOT A KIND, for the same reason the overview is one: there
 * is no object to GET called an application. It is a grouping of objects by
 * the labels Kubernetes recommends they carry, and putting it in the catalog
 * would offer it to every consumer that expects to be able to fetch what it
 * names.
 */
export const APPLICATIONS_KIND_ID = 'podsteer/applications'

/**
 * The merged cross-cluster view, pinned beside the other two.
 *
 * THE THIRD PSEUDO-ENTRY, AND FOR THE SAME REASON: there is no object to GET
 * called "all clusters". It is the pods, workloads or events of every open
 * tab in one table — an aggregation, not a kind — and a catalog entry would
 * offer it to every consumer that expects to fetch what it names, from a
 * cluster that by definition knows nothing of the other tabs. Its rows live
 * in $stores/fleet rather than on any one session, because they are nobody's
 * tab's; what stays here is this tab's own view of them — search, sort,
 * page, chips.
 */
export const FLEET_KIND_ID = 'podsteer/fleet'

/**
 * The RBAC explorer, pinned beside the other three.
 *
 * THE FOURTH PSEUDO-ENTRY, AND FOR THE SAME REASON: there is no object to
 * GET called "my permissions". It is three questions asked of the
 * authorization review APIs and one reverse lookup over the binding graph —
 * an interrogation, not a list — so it is deliberately absent from
 * `domain/catalog.go`, which is offered to every consumer that expects to be
 * able to fetch what it names. The Roles and ClusterRoles it inspects ARE
 * catalog entries, and browsing them stays exactly where it was.
 *
 * It also polls nothing. The panel's reads happen when somebody presses
 * something, which is why `#fetch` has a case for it that fetches nothing at
 * all: an allow or deny decision shown from a previous tick could report a
 * permission that has since been revoked as still granted.
 */
export const RBAC_KIND_ID = 'podsteer/rbac'

/**
 * The column-preference and sort key of one of the merged tables.
 *
 * Per table rather than one for the view: the three hold different columns,
 * so a sort set on one must not leak into another — the same rule `sorts`
 * already applies between kinds.
 */
export function fleetTableId(tab: FleetTab): string {
  return `${FLEET_KIND_ID}/${tab}`
}

/**
 * The session timeline's navigation id, the fourth pinned pseudo-entry.
 *
 * NOT A KIND, for the reason the other three are not: there is no object to
 * GET called a timeline. It is the record this tab kept of what it saw while
 * it was open — events, findings appearing and clearing, and the writes
 * PodSteer made — so a catalogue entry would offer it to every consumer that
 * expects to be able to fetch what it names. It is also the only view that
 * fetches nothing at all: everything in it already crossed the bridge for
 * some other reason. See $stores/timeline.
 */
export const TIMELINE_KIND_ID = 'podsteer/timeline'

export const DEFAULT_KIND_ID = OVERVIEW_KIND_ID

/** Kind ids PodSteer renders with purpose-built columns rather than generically. */
export const RICH_KIND_IDS = {
  pods: 'core/v1/pods',
  nodes: 'core/v1/nodes',
  events: 'core/v1/events',
  namespaces: 'core/v1/namespaces',
} as const

/** Maps a rich workload kind id onto the controller name the backend expects. */
/**
 * How long the filter waits behind the keyboard.
 *
 * Long enough that a burst of typing is one pass rather than one per letter,
 * short enough to read as instant — the threshold where a delay starts being
 * felt is around a fifth of a second.
 */
const SEARCH_DEBOUNCE_MS = 120

export const WORKLOAD_KIND_BY_ID: Record<string, string> = {
  'apps/v1/deployments': 'Deployment',
  'apps/v1/statefulsets': 'StatefulSet',
  'apps/v1/daemonsets': 'DaemonSet',
  'apps/v1/replicasets': 'ReplicaSet',
  'batch/v1/jobs': 'Job',
  'batch/v1/cronjobs': 'CronJob',
}

/** The kind id behind a controller name — `WORKLOAD_KIND_BY_ID` read the
    other way, for a row of a merged table that names its kind and needs the
    navigator's id to open in. */
export function workloadKindId(kind: string): string | undefined {
  return Object.keys(WORKLOAD_KIND_BY_ID).find((id) => WORKLOAD_KIND_BY_ID[id] === kind)
}

/**
 * The HorizontalPodAutoscaler kind's id.
 *
 * Stable, unlike a KEDA ScaledObject's: HPA is a built-in kind — see
 * `domain/catalog.go` — present in every cluster's catalog whether or not the
 * API server actually serves `autoscaling/v2`. A ScaledObject has no such
 * fixed id because it is discovered per cluster, so it is looked up in
 * `session.kinds` instead. See `ClusterSession.autoscalersFor`.
 */
const HPA_KIND_ID = 'autoscaling/v2/horizontalpodautoscalers'

/** What the content pane should render for the selected kind. */
export type ViewMode =
  | 'overview'
  | 'applications'
  | 'fleet'
  | 'rbac'
  | 'timeline'
  | 'pods'
  | 'nodes'
  | 'events'
  | 'namespaces'
  | 'workloads'
  | 'table'

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
  // Sorted by how FULL it is, not by bytes used. A 900GiB disk with 100GiB
  // free sorts above a 20GiB disk with 1GiB free by volume and below it by
  // urgency, and urgency is what somebody scanning this column wants.
  disk: (node) => (node.hasDisk ? node.diskPercent : null),
  version: (node) => node.version,
  ip: (node) => node.internalIp,
  os: (node) => node.osImage,
  pods: (node) => node.maxPods,
  taints: (node) => node.taints,
  age: (node) => node.ageSeconds,
}

const APPLICATION_SORT: SortAccessors<Application> = {
  instance: (application) => application.instance,
  namespace: (application) => application.namespace,
  partOf: (application) => application.partOf || null,
  managedBy: (application) => application.managedBy || null,
  version: (application) => application.version || null,
  objects: (application) => application.objects,
}

const NAMESPACE_SORT: SortAccessors<NamespaceSummary> = {
  status: (namespace) => namespace.phase,
  name: (namespace) => namespace.name,
  pods: (namespace) => namespace.pods,
  notReady: (namespace) => namespace.notReady,
  // Sorted on the numbers rather than on the formatted strings beside them:
  // "1.5" and "900m" compare as text in the wrong order entirely.
  // Sorted on how FULL the reservation is rather than on bytes: a namespace
  // using 8GiB of 16 and one using 1GiB of 1 are ordered by volume one way
  // and by urgency the other, and urgency is what this column is scanned for.
  cpu: (namespace) => (namespace.hasMetrics && namespace.hasCpuRequest ? namespace.cpuPercent : null),
  memory: (namespace) =>
    namespace.hasMetrics && namespace.hasMemoryRequest ? namespace.memoryPercent : null,
  cpuRequests: (namespace) => namespace.requestCores,
  memoryRequests: (namespace) => namespace.requestBytes,
  age: (namespace) => namespace.ageSeconds,
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

/*
 * The merged tables sort by the same accessors as their single-cluster twins,
 * plus the columns they add. Spread rather than re-declared, so a column's
 * ordering rule cannot differ between "this cluster's pods" and "every
 * cluster's pods".
 */
const FLEET_POD_SORT: SortAccessors<FleetRow<Pod>> = {
  ...POD_SORT,
  cluster: (pod) => pod.cluster,
}

const FLEET_WORKLOAD_SORT: SortAccessors<FleetRow<Workload>> = {
  ...WORKLOAD_SORT,
  cluster: (workload) => workload.cluster,
  kind: (workload) => workload.kind,
}

const FLEET_EVENT_SORT: SortAccessors<FleetRow<K8sEvent>> = {
  ...EVENT_SORT,
  cluster: (event) => event.cluster,
}

export class ClusterSession {
  /** The connected cluster this tab shows. */
  readonly cluster: Cluster

  /** Kinds the navigator can offer, from the backend's per-cluster catalog. */
  kinds = $state.raw<ResourceKind[]>([])
  /** Namespaces, for the filter. */
  namespaces = $state.raw<Namespace[]>([])

  /**
   * Objects recently opened in this cluster's detail drawer, most recent
   * first.
   *
   * IN MEMORY ONLY, NEVER PERSISTED — and deliberately not alongside
   * pinnedKinds in preferences.svelte.ts, even though both live in the
   * navigator. A kind id says "this operator watches Deployments here"; an
   * object name says which Deployment, and SECURITY.md enumerates exactly
   * what PodSteer writes to disk on this operator's behalf. Object names are
   * not on that list — the recorded capacity history holds no object names as
   * a product commitment (see CLAUDE.md, "History is sampled, and says so"),
   * and a localStorage entry naming every pod somebody opened would quietly
   * reverse it. This is per tab, like `usage` below, and gone the moment the
   * tab closes.
   */
  recentObjects = $state.raw<RecentObject[]>([])

  /** The kind currently selected in the navigator. */
  selectedKindId = $state<string>(DEFAULT_KIND_ID)
  /** The namespace filter. ALL_NAMESPACES means every namespace. */
  namespace = $state<string>(ALL_NAMESPACES)
  /** The client-side search term. */
  /** The term the lists are filtered by. Trails `typedSearch` by a beat. */
  search = $state<string>('')
  /** What is in the box right now, applied immediately so typing feels live. */
  typedSearch = $state<string>('')
  #searchTimer: ReturnType<typeof setTimeout> | null = null
  /** The 1-based page currently shown. */
  page = $state<number>(1)

  /** Active sort per kind id. Kinds hold different columns, so a sort set on
      one must not leak into another. */
  sorts = $state<Record<string, SortState>>({})

  /**
   * Active status quick-filter ids on the Pods page — see
   * `$lib/podStatusFilters`. Pod-only rather than a per-kind record like
   * `sorts`, because no other view has quick-filter chips; if one grows some
   * this should become one.
   */
  podStatusFilters = $state<string[]>([])

  /**
   * Active quick-filter chips on each of the merged tables — the pod chips
   * again for Pods, `WORKLOAD_CHIPS` and `EVENT_CHIPS` for the other two.
   * Kept apart from `podStatusFilters`: a chip pressed while reading every
   * cluster's pods is not a chip pressed on this cluster's, and the two
   * views must not surprise each other.
   */
  fleetChips = $state<Record<FleetTab, string[]>>({ pods: [], workloads: [], events: [] })

  /**
   * Rows for whichever view is active. Only one is populated at a time.
   *
   * `$state.raw`, not `$state`, and the difference is the cost of a refresh.
   * A plain `$state` array is deeply proxied: every property read of every
   * row goes through a proxy trap and CREATES A SIGNAL for that field. The
   * filter touches four fields per row and the sort accessors touch more, so
   * a single keystroke over five thousand pods materialised tens of thousands
   * of signals inside a derived and subscribed it to all of them — then the
   * next refresh replaced the array and rebuilt the lot.
   *
   * Raw state is correct here precisely because nothing ever mutates these in
   * place: every assignment replaces the whole array with what the backend
   * just returned. Deep proxying was paying for a capability nothing uses.
   */
  pods = $state.raw<Pod[]>([])
  nodes = $state.raw<Node[]>([])
  workloads = $state.raw<Workload[]>([])
  events = $state.raw<K8sEvent[]>([])

  /**
   * The namespace list view's rows.
   *
   * Distinct from `namespaces`, which is the filter's list of names and is
   * read on connect. These carry what is IN each namespace and cost a
   * cluster-wide pod list to produce, so they are fetched only while this
   * view is the one on screen.
   */
  namespaceRows = $state.raw<NamespaceSummary[]>([])

  /** The applications view's rows, and what carried no label to group by. */
  applications = $state.raw<Application[]>([])
  unlabelled = $state(0)

  /**
   * What each controller in the open list is consuming, keyed by
   * "namespace/name".
   *
   * Fetched ALONGSIDE the list rather than as part of it, and allowed to
   * fail: the controllers are one cheap read and this is the namespace's pods
   * and their metrics, so a cluster with no metrics API — or an account that
   * may list Deployments and not pods — still gets its list, with the meters
   * reading "not measured" rather than nothing at all.
   */
  workloadUsage = $state.raw<Record<string, Consumption>>({})

  /**
   * Which request for those figures is the current one.
   *
   * Incremented per fetch, so only the newest response may land. Comparing
   * what was asked for — the kind, the namespace — cannot do this: two
   * refreshes of the SAME list can resolve out of order, and the older one
   * would win.
   */
  #usageGeneration = 0
  table = $state.raw<ResourceTable | null>(null)

  /**
   * Autoscaler table reads for the Scale dialog, keyed by `"namespace/kindId"`.
   *
   * An HPA or ScaledObject LIST already carries every autoscaler in a
   * namespace, so a dialog opened on three workloads in the same namespace —
   * one after another, or reopened while this tab stays open — costs one HPA
   * request and one ScaledObject request, not three of each. `findAutoscalers`
   * does the per-workload filtering against whichever table this returns.
   *
   * FAILURES ARE NOT CACHED. The same reasoning `readcache.go` applies to the
   * backend's poll cache holds here: handing the same refusal to every caller
   * would leave the dialog reporting "could not check" for a namespace whose
   * permission was granted a moment ago.
   */
  #autoscalerTables = new Map<string, Promise<ResourceTable>>()
  overview = $state.raw<Overview | null>(null)

  /**
   * The Kubernetes minor the overview's "check against" selector chose, or
   * null for the default (the next minor after the cluster's current
   * version, decided in Go). Session-scoped rather than persisted: asking
   * what a specific future upgrade breaks is a question about one visit, not
   * a standing preference like a page size or a sort order.
   */
  upgradeTarget = $state<string | null>(null)

  /**
   * The previous assessment's non-info findings, keyed by id, or null before
   * the first one has landed. Not reactive: nothing renders it, and it exists
   * only to decide what is new.
   *
   * A MAP RATHER THAN A SET OF IDS, because `diffFindings` is what compares
   * them — the same function the session timeline is built from, held once.
   * There used to be a second differ here, hand-written over a Set, and two
   * differs mean two baselines: one could report a finding appearing on a
   * refresh the other did not, so the sound, the notification and the
   * timeline row would describe different instants.
   */
  #lastFindings: Map<string, Finding> | null = null

  /**
   * The sources the previous assessment could not read.
   *
   * Kept beside #lastFindings and assigned in the same place, because it is
   * half of the same baseline: a source that was missing last refresh and
   * answered this one hands over every finding it produces at once, and not
   * one of them is new. See `sourcesAreComparable`.
   */
  #lastUnavailable: string[] | null = null

  status = $state<LoadStatus>('idle')
  error = $state<ApiError | null>(null)
  lastRefreshedAt = $state<Date | null>(null)

  /** The selected row, shown in the detail drawer. */
  selectedName = $state<string | null>(null)
  selectedNamespace = $state<string>('')
  selectedPod = $state<Pod | null>(null)
  /** The node the drawer is open on, when it is a node. */
  selectedNode = $state<Node | null>(null)
  selectedWorkload = $state<Workload | null>(null)
  /** The open namespace's row, which is where its usage figures live. */
  selectedNamespaceRow = $state<NamespaceSummary | null>(null)
  /** The open application, which is not a Kubernetes object at all. */
  selectedApplication = $state<Application | null>(null)
  manifest = $state<string | null>(null)
  manifestStatus = $state<LoadStatus>('idle')

  /**
   * The rows ticked for a bulk action — a different thing from the row the
   * drawer is open on above: that is one object being read, this is a set
   * about to be acted on together, and ticking five pods then opening a
   * sixth to check something must leave the five ticked. Cleared whenever
   * the list underneath changes kind or namespace, because a tick made on
   * one list means nothing on another. See $lib/selection.
   */
  readonly selection = new RowSelection()

  /**
   * Monotonic request counter.
   *
   * A slow response must never overwrite a newer one. Clicking through three
   * kinds in quick succession leaves three requests in flight, and without
   * this the first to *return* wins rather than the last to be *asked* — which
   * shows the operator a list they already navigated away from.
   */
  #request = 0
  /** When the newest request left, so a wedged one can be written off. */
  #requestedAt = 0
  #timer: ReturnType<typeof setInterval> | null = null

  /**
   * When the kind list was last read, as a timestamp.
   *
   * Zero means "never successfully read", which is what makes a discovery
   * that failed at connect retry on the first poll instead of leaving the
   * navigator empty for five minutes.
   */
  #kindsReadAt = 0

  /**
   * Invoked when this cluster turns out to be gone from the kubeconfig.
   *
   * A callback rather than the session reaching for the workspace, which would
   * make the two mutually dependent for one edge case. The workspace owns tab
   * lifecycle; the session only reports what it observed.
   */
  onVanished?: (reason: ApiError) => void

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
    if (id === APPLICATIONS_KIND_ID) return 'applications'
    if (id === FLEET_KIND_ID) return 'fleet'
    if (id === RBAC_KIND_ID) return 'rbac'
    if (id === TIMELINE_KIND_ID) return 'timeline'
    if (id === RICH_KIND_IDS.pods) return 'pods'
    if (id === RICH_KIND_IDS.nodes) return 'nodes'
    if (id === RICH_KIND_IDS.events) return 'events'
    if (id === RICH_KIND_IDS.namespaces) return 'namespaces'
    if (id in WORKLOAD_KIND_BY_ID) return 'workloads'
    return 'table'
  })

  /** Whether the selected kind carries namespaces. */
  readonly isNamespaced = $derived(
    this.viewMode === 'overview' || this.viewMode === 'timeline'
      ? false
      : (this.selectedKind?.namespaced ?? true),
  )

  /**
   * Whether the current view is a list.
   *
   * The overview is an assessment of the whole cluster, so the search box,
   * the pagination and the row count in the toolbar have nothing to act on.
   * The RBAC explorer is the same case for the same reason — it is a set of
   * questions and their answers, not rows — and it additionally must not
   * offer the bulk action bar, which acts on a selection no pane here has.
   */
  readonly isList = $derived(
    this.viewMode !== 'overview' && this.viewMode !== 'rbac' && this.viewMode !== 'timeline',
  )

  /**
   * The search term parsed into a filter language query — regex, negation and
   * label selectors alongside the plain substring `search` always supported.
   * See `$lib/query`.
   *
   * Parsed here, ONCE per settled search term, rather than inside
   * `filterRows` per row: a regex compile and a tokenise pass are cheap once
   * and expensive five thousand times over.
   */
  readonly query = $derived(parseQuery(this.search))

  /**
   * The query for what is CURRENTLY in the box, parsed live rather than
   * after the debounce `query` waits for.
   *
   * Parsing itself is cheap — it is FILTERING the rows that is worth
   * debouncing — so the field's error state (an unclosed regex, mid-type)
   * can appear immediately instead of a beat behind the keystroke that
   * caused it.
   */
  readonly typedQuery = $derived(parseQuery(this.typedSearch))

  /** The invalid-regex message for `typedQuery`, or undefined when it parses
      cleanly. Drives the search field's error styling and accessible
      description. */
  readonly searchError = $derived(this.typedQuery.error)

  /** A one-line summary of the syntax currently in the box, for the field's
      tooltip — see `describeQuery`. */
  readonly searchDescription = $derived(describeQuery(this.typedQuery))

  /**
   * The operator's own columns for the selected kind — see $lib/customColumns.
   *
   * Read from preferences HERE, once, so the filter, the sort, the fetch and
   * the views all see one list: a column added while a refresh is in flight
   * must not leave the search knowing about it and the request not.
   */
  readonly customColumns = $derived(preferences.customColumnsFor(this.selectedKindId))

  /**
   * The annotation keys the current view's list is asked for — the
   * projection every list call takes. Label columns cost nothing here: every
   * row carries its labels already.
   */
  readonly annotationKeys = $derived(annotationKeysOf(this.customColumns))

  /**
   * Pods after the search filter alone, BEFORE the status quick-filter chips.
   *
   * Kept separate from `visiblePods` so the chip row can count how many of
   * what a search already narrowed down each chip would ADD — including a
   * chip that is not currently selected. Counting against the
   * already-chip-filtered list would make every unselected chip's count
   * collapse towards zero the moment any chip was active.
   */
  readonly searchedPods = $derived(
    filterRows(
      this.pods,
      this.query,
      (pod) => [pod.name, pod.namespace, pod.nodeName, pod.phase, ...this.#customText(pod)],
      (pod) => pod.labels,
      () => this.cluster.id,
    ),
  )

  /**
   * Rows after the search filter, for whichever view is active.
   *
   * Pods additionally pass through the status quick-filter chips — ANDed
   * with the text query, since a search term and a chip both narrow the
   * same list rather than answering different questions.
   */
  readonly visiblePods = $derived(
    this.searchedPods.filter((pod) => matchesPodStatusChips(pod, this.podStatusFilters)),
  )
  // Every kind's rows carry labels now, so `label:key` and `key=value` in
  // the search box mean the same thing on the node list as on the pod list
  // — and a custom column's value is searchable the way a built-in one's is.
  readonly visibleNodes = $derived(
    filterRows(
      this.nodes,
      this.query,
      (node) => [node.name, node.status, ...node.roles, ...this.#customText(node)],
      (node) => node.labels,
      () => this.cluster.id,
    ),
  )
  readonly visibleWorkloads = $derived(
    filterRows(
      this.workloads,
      this.query,
      (workload) => [workload.name, workload.namespace, workload.status, ...this.#customText(workload)],
      (workload) => workload.labels,
      () => this.cluster.id,
    ),
  )
  readonly visibleApplications = $derived(
    filterRows(
      this.applications,
      this.query,
      (application) => [
        application.instance,
        application.namespace,
        application.partOf,
        application.name,
      ],
      undefined,
      () => this.cluster.id,
    ),
  )
  readonly visibleNamespaces = $derived(
    filterRows(
      this.namespaceRows,
      this.query,
      (namespace) => [namespace.name, namespace.phase, ...this.#customText(namespace)],
      (namespace) => namespace.labels,
      () => this.cluster.id,
    ),
  )
  readonly visibleEvents = $derived(
    filterRows(
      this.events,
      this.query,
      (event) => [
        event.reason,
        event.message,
        event.involvedObject,
        event.namespace,
        ...this.#customText(event),
      ],
      (event) => event.labels,
      () => this.cluster.id,
    ),
  )
  readonly visibleTableRows = $derived(
    filterRows(
      this.table?.rows ?? [],
      this.query,
      (row) => [row.name, row.namespace, ...row.cells, ...this.#customText(row)],
      (row) => row.labels,
      () => this.cluster.id,
    ),
  )

  /**
   * The merged tables' rows, filtered the same way — search, then chips —
   * over what $stores/fleet holds for every open cluster. The cluster each
   * row carries is what a `cluster:` term selects on; the rest of the text
   * fields are exactly the single-cluster list's, so a search that finds a
   * pod in one tab finds it here too.
   */
  readonly searchedFleetPods = $derived(
    filterRows(
      fleet.podRows,
      this.query,
      (pod) => [pod.name, pod.namespace, pod.nodeName, pod.phase],
      (pod) => pod.labels,
      (pod) => pod.cluster,
    ),
  )
  readonly visibleFleetPods = $derived(
    this.searchedFleetPods.filter((pod) => matchesPodStatusChips(pod, this.fleetChips.pods)),
  )
  readonly searchedFleetWorkloads = $derived(
    filterRows(
      fleet.workloadRows,
      this.query,
      (workload) => [workload.name, workload.namespace, workload.kind, workload.status],
      (workload) => workload.labels,
      (workload) => workload.cluster,
    ),
  )
  readonly visibleFleetWorkloads = $derived(
    this.searchedFleetWorkloads.filter((workload) =>
      matchesChips(workload, WORKLOAD_CHIPS, this.fleetChips.workloads),
    ),
  )
  readonly searchedFleetEvents = $derived(
    filterRows(
      fleet.eventRows,
      this.query,
      (event) => [event.reason, event.message, event.involvedObject, event.namespace],
      undefined,
      (event) => event.cluster,
    ),
  )
  readonly visibleFleetEvents = $derived(
    this.searchedFleetEvents.filter((event) =>
      matchesChips(event, EVENT_CHIPS, this.fleetChips.events),
    ),
  )

  /** Total rows of the merged table showing, after filtering. */
  readonly visibleFleetCount = $derived.by(() => {
    switch (fleet.tab) {
      case 'pods':
        return this.visibleFleetPods.length
      case 'workloads':
        return this.visibleFleetWorkloads.length
      case 'events':
        return this.visibleFleetEvents.length
    }
  })

  /** What a row's custom columns add to the searchable text. */
  #customText(row: MetadataRow): string[] {
    return customSearchText(row, this.customColumns)
  }

  /** Total rows after filtering, before pagination. */
  readonly visibleCount = $derived.by(() => {
    switch (this.viewMode) {
      case 'overview':
      case 'rbac':
      case 'timeline':
        return 0
      case 'pods':
        return this.visiblePods.length
      case 'nodes':
        return this.visibleNodes.length
      case 'workloads':
        return this.visibleWorkloads.length
      case 'events':
        return this.visibleEvents.length
      case 'namespaces':
        return this.visibleNamespaces.length
      case 'applications':
        return this.visibleApplications.length
      case 'fleet':
        return this.visibleFleetCount
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

  /** What `sorts` is keyed by: the kind id, or for the merged view the
      table showing — see `fleetTableId`. */
  readonly sortKey = $derived(
    this.viewMode === 'fleet' ? fleetTableId(fleet.tab) : this.selectedKindId,
  )

  /** The sort applied to the current kind, or null for server order. */
  readonly sort = $derived(this.sorts[this.sortKey] ?? null)

  /** Filtered rows after sorting, per view. */
  readonly sortedPods = $derived(sortRows(this.visiblePods, this.sort, this.#accessors(POD_SORT)))
  readonly sortedNodes = $derived(sortRows(this.visibleNodes, this.sort, this.#accessors(NODE_SORT)))
  readonly sortedWorkloads = $derived(
    sortRows(this.visibleWorkloads, this.sort, this.#accessors(WORKLOAD_SORT)),
  )
  readonly sortedEvents = $derived(sortRows(this.visibleEvents, this.sort, this.#accessors(EVENT_SORT)))
  readonly sortedNamespaces = $derived(
    sortRows(this.visibleNamespaces, this.sort, this.#accessors(NAMESPACE_SORT)),
  )

  /**
   * A view's sort accessors, with the custom column's added when that is
   * what the sort names. Built per sort rather than per view: the custom
   * columns are the operator's and the built-in tables above are not, so
   * the two are joined only at the one id being sorted on.
   */
  #accessors<T extends MetadataRow>(base: SortAccessors<T>): SortAccessors<T> {
    const sort = this.sort
    const custom = sort ? customSortAccessor<T>(sort.columnId) : null
    return sort && custom ? { ...base, [sort.columnId]: custom } : base
  }

  /**
   * Generic table rows after sorting. The column ids are positional ("c0"),
   * so the accessor is built from the table's own column definitions: numeric
   * columns compare as numbers, date columns as parsed ages, everything else
   * as text. A custom column is neither: it reads the row's own metadata.
   */
  readonly sortedTableRows = $derived.by(() => {
    const state = this.sort
    const table = this.table
    const custom = state ? customSortAccessor<TableRow>(state.columnId) : null
    if (state && custom) {
      return sortRows(this.visibleTableRows, state, { [state.columnId]: custom })
    }
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
  readonly pagedNamespaces = $derived(this.#slice(this.sortedNamespaces))

  /** The merged tables, sorted and paged like any other. */
  readonly sortedFleetPods = $derived(sortRows(this.visibleFleetPods, this.sort, FLEET_POD_SORT))
  readonly sortedFleetWorkloads = $derived(
    sortRows(this.visibleFleetWorkloads, this.sort, FLEET_WORKLOAD_SORT),
  )
  readonly sortedFleetEvents = $derived(
    sortRows(this.visibleFleetEvents, this.sort, FLEET_EVENT_SORT),
  )
  readonly pagedFleetPods = $derived(this.#slice(this.sortedFleetPods))
  readonly pagedFleetWorkloads = $derived(this.#slice(this.sortedFleetWorkloads))
  readonly pagedFleetEvents = $derived(this.#slice(this.sortedFleetEvents))
  readonly sortedApplications = $derived(
    sortRows(this.visibleApplications, this.sort, APPLICATION_SORT),
  )
  readonly pagedApplications = $derived(this.#slice(this.sortedApplications))
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
  readonly health = $derived.by((): 'healthy' | 'degraded' | 'critical' | 'unknown' => {
    if (this.activeIssues.some((finding) => finding.severity === 'critical')) return 'critical'
    if (this.activeIssues.length > 0) return 'degraded'

    // SNOOZING CANNOT TURN "NOT READ" INTO "NOTHING WRONG". Re-grading over the
    // outstanding findings is right for everything else, but the backend
    // reports `unknown` when a source the verdict depends on could not be read
    // — and no amount of quietening findings makes unread data read. Falling
    // through to 'healthy' here is how this view kept saying "No problems
    // found" over a cluster it could not reach.
    if (this.overview?.health === 'unknown') return 'unknown'

    return 'healthy'
  })

  /** Counts for the header summary, meaningful only for pod views. */
  readonly podSummary = $derived({
    total: this.pods.length,
    unhealthy: this.pods.filter((pod) => !pod.isHealthy).length,
    restarts: this.pods.reduce((sum, pod) => sum + pod.restarts, 0),
  })

  /**
   * The ticked rows as a bulk action's plan needs them.
   *
   * Resolved against the rows held NOW rather than remembered at tick time:
   * a row deleted by somebody else between the tick and the action drops
   * out here instead of being sent to the cluster by name alone, and the
   * count the action bar shows is of objects that still exist. Every fact
   * on an item is a quotation of a field the row already carries — the
   * controller a pod's "Controlled By" names, a workload's desired count, a
   * node's cordoned flag — so planning costs no read at all. See $lib/bulk.
   */
  readonly bulkItems = $derived.by((): BulkItem[] => {
    const kind = this.selectedKind
    const keys = this.selection.keys
    if (!kind || keys.size === 0) return []

    switch (this.viewMode) {
      case 'pods':
        return this.pods
          .filter((pod) => keys.has(rowKey(pod.namespace, pod.name)))
          .map((pod) => podItem(kind, pod))
      case 'workloads':
        return this.workloads
          .filter((workload) => keys.has(rowKey(workload.namespace, workload.name)))
          .map((workload) => workloadItem(kind, workload))
      case 'nodes':
        return this.nodes.filter((node) => keys.has(node.name)).map((node) => nodeItem(kind, node))
      case 'table':
        return (this.table?.rows ?? [])
          .filter(
            (row) => row.name && keys.has(rowKey(kind.namespaced ? row.namespace : '', row.name)),
          )
          .map((row) => tableRowItem(kind, row))
      default:
        return []
    }
  })

  /** Loads the navigator tree and namespace list, then the default view. */
  initialise = async (): Promise<void> => {
    await Promise.all([this.loadKinds(), this.loadNamespaces()])
    await this.refresh()
  }

  loadKinds = async (): Promise<void> => {
    try {
      this.kinds = await listKinds(this.cluster.id)
      this.#kindsReadAt = Date.now()
    } catch (cause) {
      this.#fail(cause)
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

  /**
   * Re-reads the namespace list, for the moment somebody opens the filter.
   *
   * Namespaces are created and deleted while a tab sits open, and this list
   * was read once at connect and never again — so a namespace created a
   * minute ago stayed missing from the filter until the cluster was
   * reconnected, on a screen that was meanwhile listing that namespace's own
   * objects quite happily.
   *
   * Refreshed on OPEN rather than folded into the poll, because the filter is
   * the only thing that reads this list. Polling it would re-list namespaces
   * every few seconds on every connected cluster to keep fresh a list nobody
   * is looking at; opening the dropdown is the one moment its freshness can
   * possibly matter, and it costs a single request.
   *
   * Failure is silent and leaves the list alone — the difference from
   * loadNamespaces, which is populating an empty filter and for which empty
   * is the honest answer. Here there is a working list on screen under the
   * operator's cursor, and a momentary blip must neither empty it nor throw a
   * banner over a control that is still perfectly usable.
   */
  refreshNamespaces = async (): Promise<void> => {
    try {
      this.namespaces = await listNamespaces(this.cluster.id)
    } catch {
      // Keep what is displayed. The next open tries again.
    }
  }

  /** Selects a kind and loads it. */
  selectKind = async (kindId: string): Promise<void> => {
    if (kindId === this.selectedKindId) return
    this.selectedKindId = kindId
    this.page = 1
    this.closeDetail()
    this.selection.clear()
    await this.refresh()
  }

  /**
   * Finds the row object for an object being opened, when one was not handed
   * in. Null when the list holds no such row, which is not an error: the
   * panel falls back to what the manifest alone can show.
   */
  #findPod(name: string, namespace: string): Pod | null {
    if (this.viewMode !== 'pods') return null
    return this.pods.find((pod) => pod.name === name && pod.namespace === namespace) ?? null
  }

  #findNamespace(name: string): NamespaceSummary | null {
    if (this.viewMode !== 'namespaces') return null
    return this.namespaceRows.find((row) => row.name === name) ?? null
  }

  #findNode(name: string): Node | null {
    if (this.viewMode !== 'nodes') return null
    return this.nodes.find((node) => node.name === name) ?? null
  }

  #findWorkload(name: string, namespace: string): Workload | null {
    if (this.viewMode !== 'workloads') return null
    return (
      this.workloads.find(
        (workload) => workload.name === name && workload.namespace === namespace,
      ) ?? null
    )
  }

  /**
   * Opens one object, bringing the list behind it to where that object is.
   *
   * What following a reference has to do, and in ONE refresh. Selecting the
   * kind and then opening the object separately loads the list twice — and
   * loads it the first time under whatever namespace filter was already set,
   * which for a pod on a node in another namespace is a list that does not
   * contain the pod being opened. The panel is then left with no row object
   * to read its live sections from.
   *
   * The namespace filter is moved ONLY when it would otherwise hide the
   * target: a filter set to one namespace, and an object in another. "All
   * namespaces" already shows it and is left alone.
   */
  openObject = async (
    kindId: string,
    name: string,
    namespace: string,
    namespaced: boolean,
  ): Promise<void> => {
    const needsNamespace =
      namespaced &&
      namespace !== '' &&
      this.namespace !== ALL_NAMESPACES &&
      this.namespace !== namespace

    if (kindId !== this.selectedKindId || needsNamespace) {
      this.selectedKindId = kindId
      if (needsNamespace) {
        this.namespace = namespace
        preferences.setClusterNamespace(this.cluster.id, namespace)
      }
      this.page = 1
      this.closeDetail()
      this.selection.clear()
      await this.refresh()
    }

    await this.openDetail(name, namespace)
  }

  /**
   * Opens an application's panel.
   *
   * ITS OWN PATH, because an application is not an object: there is no
   * manifest to fetch and nothing to GET by that name. Everything its panel
   * shows is already in the row, which is why this takes the row.
   */
  openApplication = (application: Application): void => {
    this.selectedApplication = application
    this.selectedName = application.instance
    this.selectedNamespace = application.namespace
    this.selectedPod = null
    this.selectedNode = null
    this.selectedWorkload = null
    this.selectedNamespaceRow = null
    this.manifest = null
    this.manifestStatus = 'ready'
    // Seeded from what the list has been recording since the tab opened, the
    // same way a pod's and a node's are.
    this.usage = usageHistory.since(
      usageKey(this.cluster.id, 'application', application.namespace, application.instance),
    )
  }

  /**
   * Opens a kind's list, filtered to one namespace.
   *
   * Both at once and ONE reload. Calling selectKind and selectNamespace in
   * turn does the same thing in two refreshes, the first of which loads the
   * new kind across whatever namespace was previously selected — a flash of
   * the wrong list, and on a large cluster an expensive one.
   */
  browseKind = async (kindId: string, namespace: string): Promise<void> => {
    const changed = kindId !== this.selectedKindId || namespace !== this.namespace

    this.selectedKindId = kindId
    this.namespace = namespace
    preferences.setClusterNamespace(this.cluster.id, namespace)
    this.page = 1
    // Closed either way: the drawer is open on the namespace that was just
    // navigated away from, and leaving it there over a list of something else
    // is a panel describing an object nothing on screen refers to.
    this.closeDetail()
    if (changed) this.selection.clear()

    if (changed) await this.refresh()
  }

  /** Changes the namespace filter, remembers it for this cluster, and reloads. */
  selectNamespace = async (namespace: string): Promise<void> => {
    if (namespace === this.namespace) return
    this.namespace = namespace
    preferences.setClusterNamespace(this.cluster.id, namespace)
    this.page = 1
    this.selection.clear()
    await this.refresh()
  }

  /**
   * Changes what the overview's upgrade-impact findings are checked against,
   * and reloads. `null` returns to the default (the next minor).
   *
   * Not remembered across tabs or sessions like the namespace filter is:
   * comparing against a specific future version is a question about this
   * visit, and a choice made checking one cluster has no bearing on another.
   */
  setUpgradeTarget = async (minor: string | null): Promise<void> => {
    if (minor === this.upgradeTarget) return
    this.upgradeTarget = minor
    await this.refresh()
  }

  /**
   * Sets the search term.
   *
   * Resets to the first page: narrowing a search while on page 4 otherwise
   * lands the operator on an empty page of a shorter list.
   *
   * The TYPED text is applied at once so the field never lags the keyboard;
   * the term the lists filter and sort by follows a beat later. On a few
   * thousand rows every keystroke otherwise re-filtered, re-sorted, re-paged
   * and re-rendered the whole table — work that is thrown away by the next
   * character. A short delay collapses a burst of typing into one pass and is
   * imperceptible on a word typed at speed.
   */
  setSearch = (search: string): void => {
    this.typedSearch = search
    this.page = 1

    if (this.#searchTimer) clearTimeout(this.#searchTimer)
    if (search === '') {
      // Clearing is immediate. It is usually a deliberate "show me everything
      // again", and waiting for a timer to restore the full list feels broken.
      this.search = ''
      return
    }
    this.#searchTimer = setTimeout(() => {
      this.#searchTimer = null
      this.search = this.typedSearch
    }, SEARCH_DEBOUNCE_MS)
  }

  /**
   * Toggles one status quick-filter chip on the Pods page.
   *
   * Resets to the first page, for the same reason `setSearch` does: the
   * visible rows just changed, so wherever the operator was pointing may no
   * longer exist. Not persisted anywhere — see `preferences.svelte.ts` for
   * what IS — because a chip is a "right now" question about what is broken,
   * not a standing preference about how to view Pods.
   */
  togglePodStatusFilter = (id: string): void => {
    this.podStatusFilters = this.podStatusFilters.includes(id)
      ? this.podStatusFilters.filter((existing) => existing !== id)
      : [...this.podStatusFilters, id]
    this.page = 1
  }

  /** Toggles one quick-filter chip on a merged table. Same page reset, same
      reasons, as the pod chips. */
  toggleFleetChip = (tab: FleetTab, id: string): void => {
    const active = this.fleetChips[tab]
    this.fleetChips = {
      ...this.fleetChips,
      [tab]: active.includes(id) ? active.filter((existing) => existing !== id) : [...active, id],
    }
    this.page = 1
  }

  /**
   * Switches which merged table is showing, and reads it.
   *
   * Through this session's own refresh rather than the fleet store's, so
   * the read lands under this tab's generation guard and reports into its
   * error banner — the same path the poll takes, which is the only other
   * caller.
   */
  selectFleetTab = async (tab: FleetTab): Promise<void> => {
    if (tab === fleet.tab) return
    fleet.tab = tab
    this.page = 1
    await this.refresh()
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
    const current = this.sorts[this.sortKey]
    const next: SortState | null =
      !current || current.columnId !== columnId
        ? { columnId, direction: 'asc' }
        : current.direction === 'asc'
          ? { columnId, direction: 'desc' }
          : null

    const sorts = { ...this.sorts }
    if (next) {
      sorts[this.sortKey] = next
    } else {
      delete sorts[this.sortKey]
    }
    this.sorts = sorts
    this.page = 1
  }

  /** Slices a filtered list down to the current page. */
  #slice<T>(rows: T[]): T[] {
    const start = (Math.min(Math.max(1, this.page), Math.max(1, Math.ceil(rows.length / preferences.pageSize))) - 1) * preferences.pageSize
    return rows.slice(start, start + preferences.pageSize)
  }

  /**
   * Records an error, and reports the one that means this tab should not exist.
   *
   * A cluster removed from the kubeconfig is not a failure to retry — it is
   * gone, and every subsequent refresh will fail identically. Leaving the tab
   * open shows an operator stale data from a cluster that no longer exists,
   * which is how a rebuilt cluster came to be displayed as healthy with zero
   * nodes.
   */
  #fail(cause: unknown): ApiError {
    const error = toApiError(cause)
    this.error = error
    if (error.code === 'cluster_not_found') this.onVanished?.(error)
    return error
  }

  /** Reloads whichever view is active. */
  refresh = async (): Promise<void> => {
    const request = ++this.#request
    this.#requestedAt = Date.now()
    this.status = 'loading'

    // Hung off refresh rather than off the poll timer, so it also happens for
    // somebody who turned auto-refresh off in Settings and drives the app with
    // the refresh button. It rate-limits itself, so the mutation handlers that
    // call refresh() to reload a list tick past it for free.
    void this.#refreshKinds()

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
      this.#fail(cause)
    }
  }

  /**
   * Re-reads the navigator's kinds once they are old enough to be worth it.
   *
   * The same staleness the namespace filter had, in the place it cannot be
   * fixed the same way: the tree is on screen the whole time, so there is no
   * "moment of use" to hang a refresh on the way the dropdown has. It is
   * therefore polled, but on its own slow clock — see
   * KINDS_REFRESH_INTERVAL_MS for what discovery costs.
   *
   * Without this, a CRD installed while a tab was open never appeared in it,
   * which quietly contradicted the thing the tree exists to do: render
   * whatever the API server serves rather than a list compiled into the
   * frontend.
   */
  async #refreshKinds(): Promise<void> {
    if (Date.now() - this.#kindsReadAt < KINDS_REFRESH_INTERVAL_MS) return

    // Stamped BEFORE the call, not after it. Stamping on success alone means
    // a cluster whose discovery is failing gets retried on every single poll
    // — every two seconds, at the fastest interval — instead of once per
    // window, which is the opposite of what a slow clock is for.
    this.#kindsReadAt = Date.now()

    try {
      this.kinds = await listKinds(this.cluster.id)
    } catch {
      // Keep the tree that is on screen. This is a background top-up of
      // something already displayed and usable, so it must not blank the
      // navigator or raise an error over the list the operator is reading —
      // the same reasoning as #refreshAssessment below.
    }
  }

  /**
   * Fetches the assessment on its own, for the views that are not it.
   *
   * Errors are swallowed. This is a background refresh of a badge and an
   * alarm; failing it must never surface as the error state of the list the
   * operator is looking at, which is fetched separately and has its own.
   */
  async #refreshAssessment(): Promise<void> {
    try {
      this.#adopt(await getOverview(this.cluster.id))
    } catch {
      // The next cycle tries again. A missed assessment is a stale badge for
      // one interval, not something to interrupt anyone over.
      //
      // The timeline is told so EXPLICITLY rather than by silence: a refresh
      // that failed carries no findings, and anything reading that as an
      // assessment would report every outstanding problem in the cluster
      // clearing in the same instant. Passing null keeps the baseline the
      // next successful assessment is compared against — see diffFindings.
      timeline.recordFindings(this.cluster.id, null)
    }
  }

  /**
   * Takes a new assessment, raising anything it added.
   *
   * "New" is measured against the previous assessment rather than against
   * everything ever seen, so a problem that clears and comes back is
   * announced again — which is what somebody watching a flapping workload
   * needs — while one that simply persists is announced once.
   *
   * The first assessment of a session only establishes the baseline. Opening
   * a cluster that has been broken since Tuesday is not news happening now,
   * and greeting an operator with a chord of every finding at once is how a
   * feature like this gets switched off in its first minute.
   *
   * ONE DIFF FEEDS ALL THREE — the sound, the desktop notification and the
   * timeline. `diffFindings` ($lib/timeline) is that diff, and it is the only
   * one: this method used to hand-roll a second comparison over a Set of ids,
   * which meant two baselines that could drift and three surfaces that could
   * disagree about the instant a finding arrived. It also carries the rule
   * this is easiest to get wrong about, so it is worth not re-deriving: a
   * null baseline announces nothing, and a refresh that produced no
   * assessment is passed null rather than an empty set.
   */
  #adopt(overview: Overview): void {
    const previous = this.#lastFindings
    const previousUnavailable = this.#lastUnavailable

    // Info findings are outside this entirely. They are worth reading and are
    // never worth interrupting anybody over, so keeping them out of the
    // baseline keeps the diff about the things that can raise something.
    const current = new Map(
      overview.findings
        .filter((finding) => finding.severity !== 'info')
        .map((finding) => [finding.id, finding]),
    )
    const diff = diffFindings(previous, current)
    this.#lastFindings = diff.next
    this.#lastUnavailable = [...overview.unavailable]

    this.overview = overview
    this.#retainNodeUsage(overview)
    // The timeline is handed the WHOLE assessment rather than this diff: it
    // records what cleared as well, and it renders info findings that never
    // reach the baseline above. It runs the same `diffFindings` against its
    // own copy of the same data, which is why the two cannot disagree.
    timeline.recordFindings(this.cluster.id, overview.findings)

    // Snoozed findings are silent by definition, and so is un-snoozing one:
    // the id was in the baseline throughout, because the baseline is not
    // filtered by snoozing.
    const raised = diff.appeared.filter((finding) => !this.isFullySnoozed(finding))
    if (raised.length === 0) return

    // One sound for the batch, at the worst severity in it. Six pods failing
    // at once is one event to an operator, and six overlapping chimes is
    // noise they cannot count anyway. The worst severity wins because a
    // critical arriving alongside a warning is a critical arriving.
    if (preferences.alertSoundsEnabled) {
      const worst = raised.some((finding) => finding.severity === 'critical')
        ? 'critical'
        : 'warning'
      void alertPlayer.play(preferences.alertSoundFor(worst))
    }

    // And the desktop, for the operator who is not looking at the window.
    // Every rule about whether one is posted — critical only, snoozed
    // excluded, one per batch, at most one a minute — is in $lib/notify, and
    // the two arguments it cannot work out for itself are handed over here:
    // whether the operator asked for this, and whether this assessment read
    // the same sources as the one it is being compared against.
    void notifications.raise({
      clusterId: this.cluster.id,
      enabled: preferences.desktopNotificationsEnabled,
      // The UNFILTERED diff, with the snooze rule handed over rather than
      // applied first: the sound and the notification snooze on the same
      // fact but are separate decisions, and each keeps its own rule where
      // it can be argued with.
      appeared: diff.appeared,
      comparable: sourcesAreComparable(previousUnavailable, overview.unavailable),
      isSnoozed: (finding) => this.isFullySnoozed(finding),
    })
  }

  /** Issues the call the active view needs. */
  async #fetch(): Promise<unknown> {
    const { id } = this.cluster
    const namespace = this.isNamespaced ? this.namespace : ALL_NAMESPACES

    // The assessment is refreshed whatever is on screen. It used to be
    // fetched only while the overview was open, which left two things wrong:
    // the navigator badge kept asserting a count from whenever that page was
    // last visited — an hour ago, on a cluster that had since broken — and
    // nothing could raise an alert about a finding while somebody was reading
    // a pod list. It runs alongside, so a slow assessment never delays the
    // rows the operator is actually waiting for.
    if (this.viewMode !== 'overview') void this.#refreshAssessment()

    switch (this.viewMode) {
      case 'timeline':
        // NOTHING IS FETCHED. The timeline is a record of what other reads
        // already carried, so a poll landing on this view costs exactly the
        // assessment above — which runs whatever is on screen anyway.
        return null
      case 'overview':
        // The explicit target only applies to the view an operator is
        // actually looking at. #refreshAssessment (below) always asks for
        // the default, so a comparison chosen here never changes the
        // navigator badge or the alert a different tab's poll would raise.
        return this.upgradeTarget
          ? getOverviewForTarget(id, this.upgradeTarget)
          : getOverview(id)
      case 'fleet':
        // Every open cluster, one call, at this tab's cadence — and only
        // while this view is the one on screen, because this switch is the
        // only thing that ever calls it. See $stores/fleet.
        return fleet.refresh(namespace)
      case 'rbac':
        // NOTHING, DELIBERATELY. The RBAC explorer's reads are made by the
        // panel when somebody presses something, never by this tick: a
        // decision re-fetched on a timer would still be a decision shown
        // from an earlier instant, and a permission revoked between two
        // ticks would keep reading as granted until the next one. The
        // assessment above still runs, so the navigator badge stays current
        // while this view is open.
        return Promise.resolve(null)
      // Every list carries the kind's annotation projection — the keys on
      // its custom columns — and nothing else of the annotation map. See
      // $lib/customColumns and the client's listNamespaceSummaries note.
      case 'pods':
        return listPods(id, namespace, this.annotationKeys)
      case 'nodes':
        return listNodes(id, this.annotationKeys)
      case 'events':
        return listEvents(id, namespace, this.annotationKeys)
      case 'namespaces':
        return listNamespaceSummaries(id, this.annotationKeys)
      case 'applications':
        return listApplications(id, namespace)
      case 'workloads': {
        const kind = WORKLOAD_KIND_BY_ID[this.selectedKindId]
        // Not awaited, so a slow pod list never delays the rows themselves.
        //
        // GUARDED BY A GENERATION, not by comparing the kind. The kind alone
        // let three things through: a namespace change with the kind
        // unchanged, two refreshes of the same list resolving out of order
        // with the older winning, and a failure clearing figures a later
        // success had already installed. One counter closes all three.
        const generation = ++this.#usageGeneration
        void workloadConsumption(id, kind, namespace)
          .then((usage) => {
            if (generation === this.#usageGeneration) this.workloadUsage = usage
          })
          .catch(() => {
            if (generation === this.#usageGeneration) this.workloadUsage = {}
          })
        return listWorkloads(id, kind, namespace, this.annotationKeys)
      }
      default:
        return listTable(id, this.selectedKindId, namespace, this.annotationKeys)
    }
  }

  /**
   * The label and annotation keys the current view's rows carry — what the
   * column picker offers, so a column is chosen from keys this cluster
   * actually uses rather than typed from memory.
   *
   * A method rather than a derived: it walks every row's metadata, and the
   * menu is opened far less often than the list refreshes. Over the filtered
   * rows, not the page, so a key on row 40 of 25-per-page is still offered.
   */
  metadataKeysOnScreen = (): MetadataKeys => {
    switch (this.viewMode) {
      case 'pods':
        return keysOnScreen(this.visiblePods)
      case 'nodes':
        return keysOnScreen(this.visibleNodes)
      case 'workloads':
        return keysOnScreen(this.visibleWorkloads)
      case 'events':
        return keysOnScreen(this.visibleEvents)
      case 'namespaces':
        return keysOnScreen(this.visibleNamespaces)
      case 'table':
        return keysOnScreen(this.visibleTableRows)
      default:
        return { labels: [], annotations: [] }
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
    this.namespaceRows = []
    this.applications = []
    this.table = null
    // NOT cleared: it arrives a beat after the rows it belongs to, and
    // clearing it here would blank every meter for one frame on each refresh.
    // A response for another list is turned away where it lands instead.
    // The overview is deliberately NOT cleared here. It is a cached assessment
    // rather than one of the mutually exclusive row buffers, and the navigator
    // badge reads from it: clearing it would blank the "3 issues" badge the
    // moment an operator clicked through to look at those three issues.

    switch (this.viewMode) {
      case 'overview':
        this.#adopt(rows as Overview)
        break
      case 'timeline':
        // Nothing to hold: the entries live in $stores/timeline, written by
        // whatever produced them rather than by this refresh.
        break
      case 'fleet':
        // Nothing to hold: the merged rows are the workspace's, in
        // $stores/fleet, and the fetch folded them in itself.
        break
      case 'rbac':
        // Nothing to hold either, and for a different reason: the panel owns
        // its own answers, because it is the thing that asked for them.
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
      case 'namespaces':
        this.namespaceRows = rows as NamespaceSummary[]
        break
      case 'applications': {
        const inventory = rows as ApplicationInventory
        this.applications = inventory.applications
        this.unlabelled = inventory.unlabelled
        break
      }
      case 'workloads':
        this.workloads = rows as Workload[]
        break
      default:
        this.table = rows as ResourceTable
    }

    this.#retainUsage()
    this.#recordTimeline()
    this.#refreshSelection()
  }

  /**
   * Files what this refresh carried on the session timeline.
   *
   * COSTS NOTHING ON THE WIRE, which is the whole reason the timeline is
   * built here rather than in Go: the pod assessment rides every row of the
   * pod list and the events are the rows of the event list, so both were
   * already fetched and both were already discarded. Same trade as
   * `#retainUsage` above.
   *
   * A view that did not fetch pods passes null rather than an empty list.
   * The row buffers are mutually exclusive — a poll on the Nodes page leaves
   * `pods` empty — and an empty list read as an assessment would announce
   * every pod in the cluster recovering the moment somebody changed view.
   */
  #recordTimeline(): void {
    timeline.recordPodFindings(this.cluster.id, this.viewMode === 'pods' ? this.pods : null)
    if (this.viewMode === 'events') timeline.recordEvents(this.cluster.id, this.events)
  }

  /**
   * Keeps the usage every refresh already fetched.
   *
   * The list response carries a measurement for EVERY row, and until now all
   * of it except the one being drawn was discarded — so a drawer opened after
   * five minutes of browsing started from nothing, having thrown away the
   * five minutes it had been handed. metrics-server cannot be asked for that
   * history afterwards: a PodMetrics is one point and the API has no range.
   *
   * Costs one array push per row per refresh, and nothing on the wire.
   */
  #retainUsage(): void {
    const at = Date.now()

    // Applications, on the same terms: the row carries a measurement for
    // every application on screen, so a panel opened after a few refreshes
    // has a line in it rather than an empty frame. Without this the charts
    // never filled at all — the panel was being handed a series nothing had
    // ever written to.
    for (const application of this.applications) {
      if (!application.hasMetrics) continue
      usageHistory.record(usageKey(this.cluster.id, 'application', application.namespace, application.instance), {
        at,
        cpuCores: application.cpuCores,
        memoryBytes: application.memoryBytes,
      })
    }

    // Namespaces, on the same terms and for the same reason: the row already
    // carries a measurement for every namespace on screen, so a panel opened
    // after a few refreshes has a line in it rather than an empty frame.
    // Raw numbers rather than the formatted strings the pods loop parses —
    // the row carries both, and the formatted CPU is rounded to two decimals.
    for (const row of this.namespaceRows) {
      if (!row.hasMetrics) continue
      usageHistory.record(usageKey(this.cluster.id, 'namespace', '', row.name), {
        at,
        cpuCores: row.cpuCores,
        memoryBytes: row.memoryBytes,
      })
    }

    for (const pod of this.pods) {
      if (!pod.hasMetrics) continue
      usageHistory.record(usageKey(this.cluster.id, 'pod', pod.namespace, pod.name), {
        at,
        cpuCores: parseQuantity(pod.cpu) ?? 0,
        memoryBytes: parseQuantity(pod.memory) ?? 0,
      })
    }

    // Nodes are NOT recorded here. #assign clears every row buffer each poll
    // and fills only the one the open view reads, so this loop saw nothing
    // whenever the operator was anywhere but the node list — which is exactly
    // the case the retention exists for. They come from the assessment
    // instead, which runs on every poll whatever is on screen.
  }

  /**
   * Keeps each node's usage from the assessment, which never stops running.
   *
   * WHY NOT FROM THE NODE LIST. The row buffers are mutually exclusive: a
   * poll on the Pods page fetches pods and leaves `this.nodes` empty, so a
   * minute spent reading pods put a minute-wide hole in every node's chart
   * and, past the window, erased it. The assessment is fetched alongside
   * whatever view is open — it already feeds the navigator badge — so it is
   * the one source that is always current.
   *
   * It carries `usageCpuMilli` rather than the `cpuPercent` beside it because
   * those are DIFFERENT MEASUREMENTS: the shares on a NodeLoad are requests
   * against allocatable, what the scheduler decides on, while the chart plots
   * what metrics-server measured. Feeding the chart the share would draw a
   * plausible line of the wrong quantity.
   */
  #retainNodeUsage(overview: Overview): void {
    const at = Date.now()

    for (const load of overview.nodeLoads) {
      // An unmeasured node is skipped rather than recorded as zero: a cluster
      // with no metrics-server would otherwise accumulate a confident flat
      // line along the axis.
      if (!load.usageMeasured) continue
      usageHistory.record(usageKey(this.cluster.id, 'node', '', load.name), {
        at,
        cpuCores: load.usageCpuMilli / 1000,
        memoryBytes: load.usageMemoryBytes,
      })
    }
  }

  /**
   * Re-points the open drawer at the freshly fetched row.
   *
   * The drawer used to hold the object it was opened with and nothing ever
   * replaced it, so a pane left open showed the pod as it was at the moment
   * of the click: the CPU from then, the restart count from then, the
   * container states from then. Watching a pod crash-loop in it showed a
   * frozen healthy pod, which is worse than showing nothing.
   *
   * Matched by name and namespace rather than by identity, because every
   * refresh builds new objects. A pod that has GONE — deleted, or evicted —
   * leaves the last known copy on screen rather than blanking the pane
   * underneath somebody: the detail is stale but it is what they were
   * reading, and the list behind them already shows it is no longer there.
   */
  #refreshSelection(): void {
    if (!this.selectedName) return

    // An open application, refreshed from the list behind it and appended to
    // its chart. Without this the panel showed whatever had been recorded
    // when it opened and never moved again — the series is written by the
    // list, and the open panel has to keep taking from it.
    if (this.selectedApplication) {
      const fresh = this.applications.find(
        (application) =>
          application.instance === this.selectedName &&
          application.namespace === this.selectedNamespace,
      )
      if (fresh) {
        this.selectedApplication = fresh
        if (fresh.hasMetrics) {
          this.#append({
            at: Date.now(),
            cpuCores: fresh.cpuCores,
            memoryBytes: fresh.memoryBytes,
          })
        }
      }
      return
    }

    if (this.selectedPod) {
      const fresh = this.pods.find(
        (pod) => pod.name === this.selectedName && pod.namespace === this.selectedNamespace,
      )
      if (fresh) {
        this.selectedPod = fresh
        this.#recordUsage(fresh)
      }
      return
    }

    if (this.selectedNode) {
      const fresh = this.nodes.find((node) => node.name === this.selectedName)
      if (fresh) {
        this.selectedNode = fresh
        this.#recordNodeUsage(fresh)
      }
      return
    }

    if (this.selectedWorkload) {
      const fresh = this.workloads.find(
        (workload) =>
          workload.name === this.selectedName && workload.namespace === this.selectedNamespace,
      )
      if (fresh) this.selectedWorkload = fresh
    }
  }

  /**
   * The open pod's recent usage, kept only while the drawer is open.
   *
   * IN MEMORY, FOR THIS ONE POD, AND NOT ON DISK. The history subsystem
   * samples whole clusters and its samples deliberately carry no object names
   * at all; retaining per-pod series would reverse that privacy property and
   * cost roughly a gigabyte per cluster per week to do it. What a chart in a
   * drawer actually needs is the last few minutes of the pod in front of you,
   * which the poll already fetches — so this costs no extra request, no
   * goroutine and no file.
   *
   * Closing the drawer discards it. That is the honest trade: the chart
   * starts empty and fills as you watch, rather than pretending to a history
   * nothing recorded.
   */
  usage = $state.raw<UsageSample[]>([])

  /** The open node's usage, on the same terms as a pod's. */
  #recordNodeUsage(node: Node): void {
    if (!node.hasMetrics) return
    this.#append({
      at: Date.now(),
      cpuCores: parseQuantity(node.cpu) ?? 0,
      memoryBytes: parseQuantity(node.memory) ?? 0,
    })
  }

  #recordUsage(pod: Pod): void {
    if (!pod.hasMetrics) return

    this.#append({
      at: Date.now(),
      cpuCores: parseQuantity(pod.cpu) ?? 0,
      memoryBytes: parseQuantity(pod.memory) ?? 0,
    })
  }

  #append(sample: UsageSample): void {
    // Replaced rather than pushed: `$state.raw` does not track mutation, and
    // a chart that never redrew would be a subtle and very confusing bug.
    const next = [...this.usage, sample]
    this.usage = next.length > MAX_USAGE_SAMPLES ? next.slice(-MAX_USAGE_SAMPLES) : next
  }

  // --- Detail drawer --------------------------------------------------------

  /**
   * Whether the manifest on screen has had a Secret's values hidden.
   *
   * Tracked rather than inferred from the text, because the pane has to do
   * two things with it: say so, and refuse to let the manifest be saved.
   * Editing a masked Secret would write the placeholders back over the real
   * values, which is data loss dressed up as an edit.
   */
  secretsRevealed = $state(false)

  /**
   * Opens the detail drawer for one object and loads its manifest.
   *
   * The row that was clicked hands its own object in, because it has one and
   * a lookup would be wasted. NOTHING ELSE DOES — a reference followed from
   * another panel knows a kind, a name and a namespace and no more — so when
   * one is not supplied it is found in the list this session has loaded.
   *
   * That lookup is not a nicety. A panel's live sections come from the row
   * object rather than from the manifest: a node's usage charts, a pod's
   * findings and its containers' current state, a workload's replica figures.
   * Without it, following a link opened a panel missing exactly the parts the
   * manifest cannot supply — and closing it and clicking the row fixed it,
   * which is how the difference was noticed.
   */
  openDetail = async (
    name: string,
    namespace: string,
    pod?: Pod,
    workload?: Workload,
    node?: Node,
  ): Promise<void> => {
    // Recorded here, and only here, so a click, a followed reference (via
    // openObject, which sets selectedKindId and then calls this) and a click
    // from the Recent section itself all count as "opened" the same way —
    // there is exactly one place an object becomes recently opened.
    this.#recordRecent(this.selectedKindId, name, namespace)

    this.selectedName = name
    this.selectedNamespace = namespace
    this.selectedPod = pod ?? this.#findPod(name, namespace)
    this.selectedNode = node ?? this.#findNode(name)
    this.selectedWorkload = workload ?? this.#findWorkload(name, namespace)
    this.selectedNamespaceRow = this.#findNamespace(name)
    this.selectedApplication = null
    this.manifest = null
    this.manifestStatus = 'loading'
    // SEEDED FROM WHAT WAS ALREADY WATCHED, rather than starting empty. The
    // list has been refreshing since the tab opened and every one of those
    // responses carried this object's usage; the chart may as well open with
    // it. Empty when nothing was retained — a window of zero, or an object
    // whose list has not been visited.
    // For whichever of the two this turned out to be, INCLUDING when it was
    // resolved above rather than handed in — which is what makes a followed
    // link open with the same history a clicked row does.
    this.usage = this.selectedPod
      ? usageHistory.since(usageKey(this.cluster.id, 'pod', namespace, name))
      : this.selectedNode
        ? usageHistory.since(usageKey(this.cluster.id, 'node', '', name))
        : this.selectedNamespaceRow
          ? usageHistory.since(usageKey(this.cluster.id, 'namespace', '', name))
          : []
    // Every open starts hidden. A reveal is a decision about one object, and
    // carrying it to the next one is how Freelens ends up showing a value
    // somebody unmasked in private on the pod they open in a meeting.
    this.secretsRevealed = false

    await this.#loadManifest(name, namespace)
  }

  /**
   * Records an object as opened, most recent first, deduplicated by identity.
   *
   * Identity is kind + namespace + name, not name alone: a ConfigMap and a
   * Secret can share a name in the same namespace, and two namespaces
   * routinely hold pods with the same name — collapsing those would reopen
   * the wrong object from Recent.
   */
  #recordRecent(kindId: string, name: string, namespace: string): void {
    const withoutExisting = this.recentObjects.filter(
      (entry) => !(entry.kindId === kindId && entry.name === name && entry.namespace === namespace),
    )
    this.recentObjects = [{ kindId, name, namespace }, ...withoutExisting].slice(
      0,
      MAX_RECENT_OBJECTS,
    )
  }

  /** Empties the Recent section. Nothing else can un-forget an object once
      this runs — that is the same trade Data → local history's "Don't
      record" makes, and it is the point of a Clear control. */
  clearRecents = (): void => {
    this.recentObjects = []
  }

  /**
   * Re-reads the manifest with the Secret values in it.
   *
   * A separate call rather than a flag on the first one: this is the audited
   * read, and it happens because somebody asked for it.
   */
  revealManifestSecrets = async (): Promise<void> => {
    if (!this.selectedName) return
    this.secretsRevealed = true
    await this.#loadManifest(this.selectedName, this.selectedNamespace)
  }

  /**
   * Puts the values back behind their placeholders.
   *
   * THERE WAS NO WAY BACK, which was an oversight rather than a policy:
   * revealing swapped the Reveal control for nothing, so the only way to
   * re-mask a Secret was to close the panel and open it again. Everything
   * else about revealing a value here says it should be re-hideable —
   * including the reveal on an environment variable, which hides itself.
   *
   * Not an audited read: masking asks the API server for the same object with
   * the values replaced by their sizes, which is what an unprivileged read
   * looks like anyway.
   */
  hideManifestSecrets = async (): Promise<void> => {
    if (!this.selectedName || !this.secretsRevealed) return
    this.secretsRevealed = false
    await this.#loadManifest(this.selectedName, this.selectedNamespace)
  }

  /**
   * The manifest reads issued so far, so a stale one cannot land.
   *
   * WITHOUT THIS A SECRET STAYS ON SCREEN WITH ITS AUTO-MASK DISARMED, which
   * is the one outcome the whole reveal design exists to prevent. Revealing
   * sets `secretsRevealed` synchronously, so the toolbar button swaps to
   * "Hide" in the same position before the larger read returns. On a slow
   * cluster the operator does the universal did-that-work gesture and clicks
   * again — now two reads are in flight, and if the reveal lands last the
   * manifest holds decoded values while `secretsRevealed` is false. The
   * window-blur mask is gated on that flag, so alt-tabbing to start a screen
   * share no longer hides anything.
   *
   * It also stops pod A's YAML appearing under pod B's header, and stops a
   * stale failure closing a tab for an object already navigated away from.
   */
  #manifestRequest = 0

  async #loadManifest(name: string, namespace: string): Promise<void> {
    const request = ++this.#manifestRequest
    const revealed = this.secretsRevealed
    this.manifestStatus = 'loading'
    try {
      const manifest = await getManifest(
        this.cluster.id,
        this.selectedKindId,
        namespace,
        name,
        revealed,
      )
      if (request !== this.#manifestRequest) return
      this.manifest = manifest
      this.manifestStatus = 'ready'
    } catch (cause) {
      if (request !== this.#manifestRequest) return
      this.manifestStatus = 'error'
      this.#fail(cause)
    }
  }

  /** Closes the detail drawer. */
  closeDetail = (): void => {
    this.selectedName = null
    this.selectedNamespace = ''
    this.selectedPod = null
    this.selectedNode = null
    this.selectedWorkload = null
    this.manifest = null
    this.manifestStatus = 'idle'
    this.usage = []
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
      // a backlog of overlapping refreshes — BUT NOT FOR EVER.
      //
      // `status` is only ever cleared inside refresh(), so a request that
      // never settles left it reading 'loading' and this check skipping every
      // tick from then on. Auto-refresh stopped, permanently and silently,
      // with the screen showing whatever it had last managed to read. Nothing
      // said so; the numbers simply stopped moving.
      //
      // After a few missed ticks the request is written off and a new one
      // starts. Nothing stale can land on top of it — refresh() guards every
      // assignment on its own generation.
      const stalled = Date.now() - this.#requestedAt >= intervalMs * MISSED_TICKS_BEFORE_RETRY
      if (this.status === 'loading' && !stalled) return
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
    // Everything this tab accumulated goes with it. Per-cluster in both
    // cases, so closing one tab does not blank the charts in another or make
    // it re-read ConfigMaps it already has.
    usageHistory.forget(this.cluster.id)
    forgetConfigMaps(this.cluster.id)
    forgetVulnerabilities(this.cluster.id)
    // The timeline goes with the tab, like the Recent section below and for
    // the same reason: it is made of object names, it was never written
    // anywhere, and a reconnect starts a fresh record rather than resuming
    // one from a session that ended.
    timeline.forget(this.cluster.id)
    // Recent objects are in-memory only and scoped to this connection — see
    // recentObjects above. A reconnect to the same cluster starts the list
    // over rather than resurrecting names from a session that ended.
    this.recentObjects = []
  }

  /** Scales a workload to the specified number of replicas. */
  scaleWorkload = async (kind: string, name: string, namespace: string, replicas: number): Promise<void> => {
    try {
      await scaleWorkload(this.cluster.id, kind, namespace, name, replicas)
    } catch (cause) {
      this.#fail(cause)
    }
  }

  /**
   * Reads one autoscaler-serving kind's table for a namespace, sharing the
   * read across every call this session makes for that namespace and kind —
   * see `#autoscalerTables`.
   */
  #autoscalerTable(namespace: string, kindId: string): Promise<ResourceTable> {
    const key = `${namespace}/${kindId}`
    const held = this.#autoscalerTables.get(key)
    if (held) return held

    const read = listTable(this.cluster.id, kindId, namespace)
    this.#autoscalerTables.set(key, read)
    // A refused or failed read must not be handed to the next caller — the
    // account may be granted the permission it lacked a moment later, or the
    // cluster blip may already be over. Dropping the entry is enough: nothing
    // here waits on `read` before returning it to its own caller.
    read.catch(() => this.#autoscalerTables.delete(key))
    return read
  }

  /**
   * Whether an autoscaler manages a workload's replica count — asked by the
   * Scale dialog when it opens, never on a keystroke in the field it warns
   * above.
   *
   * Reads the HorizontalPodAutoscaler table always, and the ScaledObject
   * table only when this cluster's catalog carries a `keda.sh` one. A cluster
   * with no KEDA installed is not asked for it at all: that is not "no KEDA
   * autoscaler", it is a request for a kind the cluster does not serve, and
   * making it would turn an ordinary cluster into a failed check.
   *
   * A FAILED READ IS `'unknown'`, NEVER `'known'` WITH AN EMPTY LIST — the
   * same distinction `domain.MetricsStatus` draws for the overview (see
   * CLAUDE.md). Telling an operator nothing manages their workload when the
   * honest answer is "could not check" would let them scale over an
   * autoscaler they were never told about.
   */
  autoscalersFor = async (
    kind: string,
    namespace: string,
    name: string,
  ): Promise<AutoscalerCheck> => {
    const keda = this.kinds.find((entry) => entry.group === 'keda.sh' && entry.kind === 'ScaledObject')
    const sources: { kindId: string; hint: 'hpa' | 'keda' }[] = [{ kindId: HPA_KIND_ID, hint: 'hpa' }]
    if (keda) sources.push({ kindId: keda.id, hint: 'keda' })

    const reads = await Promise.all(
      sources.map(async (source) => {
        try {
          const table = await this.#autoscalerTable(namespace, source.kindId)
          return { ok: true as const, autoscalers: findAutoscalers(table, source.hint, { kind, name }) }
        } catch (cause) {
          return { ok: false as const, reason: toApiError(cause).message }
        }
      }),
    )

    const failure = reads.find((read) => !read.ok)
    if (failure && !failure.ok) return { status: 'unknown', reason: failure.reason }

    return {
      status: 'known',
      autoscalers: reads.flatMap((read) => (read.ok ? read.autoscalers : [])),
    }
  }

  /**
   * Applies manifest to the cluster — the generic path, any kind — creating
   * or replacing the object it names.
   *
   * RETHROWS on failure, unlike most write methods here (scaleWorkload,
   * say): the YAML editor's footer needs the actual ApiError to tell a
   * conflict — the object changed on the cluster since the manifest was
   * read — from every other failure, and needs the outcome itself to say
   * "Applied" or "Created". session.error is set first regardless, so
   * anything reading it generically still sees the same failure.
   */
  updateResource = async (manifest: string): Promise<ApplyOutcome> => {
    try {
      return await updateResource(this.cluster.id, manifest)
    } catch (cause) {
      throw this.#fail(cause)
    }
  }

  /**
   * Validates manifest against the cluster without applying it — the same
   * generic path as updateResource, with the API server's dry run. Rethrows
   * for the same reason updateResource does.
   */
  validateResource = async (manifest: string): Promise<ApplyOutcome> => {
    try {
      return await validateResource(this.cluster.id, manifest)
    } catch (cause) {
      throw this.#fail(cause)
    }
  }

  /**
   * Re-reads the currently open object's manifest, unconditionally.
   *
   * Used after a successful apply — so the NEXT apply carries the
   * resourceVersion this one just produced, rather than the one the draft
   * was opened with, which would otherwise fail as a conflict for a reason
   * nothing on screen explains — and by the conflict banner's Reload
   * action, where discarding what is on screen for what the cluster has now
   * is the entire point.
   */
  reloadManifest = async (): Promise<void> => {
    if (!this.selectedName) return
    await this.#loadManifest(this.selectedName, this.selectedNamespace)
  }
}

/**
 * Filters rows by a parsed query — see `$lib/query` for the grammar
 * (substring by default, plus regex, negation and label selectors).
 *
 * `text` projects the fields a plain substring search always compared
 * against, joined into the one string `matches` tests a substring or regex
 * term against — the concatenation IS the "row.text" the query language
 * matches over. `labels` is omitted for every kind whose DTO does not carry
 * one (Nodes, Namespaces, Events, Applications, every generic table row);
 * `matches` already treats an absent label map as "this row has no labels"
 * rather than as a reason to special-case the call site.
 *
 * The query is parsed once by the caller (`session.query`) and passed in
 * already-parsed, not re-parsed per row.
 *
 * `cluster` is which cluster the row came from, for a `cluster:` term. Every
 * row in PodSteer belongs to one — the tab's own for its lists, the row's
 * own for a merged table — so it is always supplied: typing `cluster:prod`
 * over prod's own pod list shows the list, not an empty table.
 */
function filterRows<T>(
  rows: T[],
  query: Query,
  text: (row: T) => (string | undefined)[],
  labels?: (row: T) => Record<string, string> | undefined,
  cluster?: (row: T) => string | undefined,
): T[] {
  if (query.terms.length === 0) return rows

  return rows.filter((row) =>
    matches(query, {
      text: text(row).filter((field): field is string => Boolean(field)).join(' '),
      labels: labels?.(row),
      cluster: cluster?.(row),
    } satisfies Row),
  )
}
