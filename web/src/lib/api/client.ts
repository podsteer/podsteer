/**
 * The single seam between the PodSteer UI and the Go backend.
 *
 * Nothing outside this module imports from `$lib/wailsjs` — those files are
 * regenerated on every build and their shape is dictated by the Go binder, so
 * keeping the rest of the app one step removed means a change over there lands
 * in one place. This module also normalises the two things the raw bindings
 * get wrong for our purposes: rejection values arrive as bare strings (see
 * errors.ts), and Go's positional `arg1, arg2` signatures say nothing about
 * what the arguments mean.
 */

import {
  AddKubeconfig as bindAddKubeconfig,
  Connect as bindConnect,
  Connections as bindConnections,
  Disconnect as bindDisconnect,
  ListClusters as bindListClusters,
  ListNamespaces as bindListNamespaces,
  ListNamespaceSummaries as bindListNamespaceSummaries,
  ListNodes as bindListNodes,
  PreviewKubeconfig as bindPreviewKubeconfig,
  ReadKubeconfigFile as bindReadKubeconfigFile,
  SetReadOnly as bindSetReadOnly,
} from '$lib/wailsjs/go/wails/ClusterAPI'
import {
  GetManifest as bindGetManifest,
  RevealSecretKey as bindRevealSecretKey,
  InspectTLSSecret as bindInspectTLSSecret,
  ListEvents as bindListEvents,
  ListEventsForResource as bindListEventsForResource,
  ListKinds as bindListKinds,
  ListTable as bindListTable,
  NamespaceInventory as bindNamespaceInventory,
  ClassifyConditions as bindClassifyConditions,
  AssessCertificateRenewal as bindAssessCertificateRenewal,
  VulnerabilitySummaries as bindVulnerabilitySummaries,
  ObjectGraph as bindObjectGraph,
} from '$lib/wailsjs/go/wails/BrowseAPI'
import {
  ListPods as bindListPods,
  ListWorkloads as bindListWorkloads,
  ListPodsForWorkload as bindListPodsForWorkload,
  WorkloadUsage as bindWorkloadUsage,
  WorkloadConsumption as bindWorkloadConsumption,
  ListApplications as bindListApplications,
  ListPodsOnNode as bindListPodsOnNode,
  PodGraph as bindPodGraph,
  WorkloadGraph as bindWorkloadGraph,
  RolloutHistory as bindRolloutHistory,
} from '$lib/wailsjs/go/wails/WorkloadAPI'
import {
  ListEvents as bindListFleetEvents,
  ListPods as bindListFleetPods,
  ListWorkloads as bindListFleetWorkloads,
} from '$lib/wailsjs/go/wails/FleetAPI'
import {
  CanI as bindCanI,
  InspectRole as bindInspectRole,
  SubjectRules as bindSubjectRules,
} from '$lib/wailsjs/go/wails/RBACAPI'
import {
  ScaleWorkload as bindScaleWorkload,
  UpdateResource as bindUpdateResource,
  ValidateResource as bindValidateResource,
  DeleteResource as bindDeleteResource,
  RestartRollout as bindRestartRollout,
  TriggerCronJob as bindTriggerCronJob,
  SuspendWorkload as bindSuspendWorkload,
  SetImage as bindSetImage,
  SetSecretKey as bindSetSecretKey,
  SetConfigMapKey as bindSetConfigMapKey,
  StreamLogs as bindStreamLogs,
  StopLogStream as bindStopLogStream,
  StartPortForward as bindStartPortForward,
  StopPortForward as bindStopPortForward,
  ListPortForwards as bindListPortForwards,
  CordonNode as bindCordonNode,
  EvictPod as bindEvictPod,
  PlanDrain as bindPlanDrain,
  DrainNode as bindDrainNode,
  StopAllPortForwards as bindStopAllPortForwards,
  ProbeLocalPort as bindProbeLocalPort,
  FreeLocalPort as bindFreeLocalPort,
  RollbackWorkload as bindRollbackWorkload,
  ListNodeShells as bindListNodeShells,
  StopNodeShell as bindStopNodeShell,
  StopAllNodeShells as bindStopAllNodeShells,
  PlanBulk as bindPlanBulk,
  BulkDelete as bindBulkDelete,
  BulkRestart as bindBulkRestart,
  BulkScale as bindBulkScale,
  BulkCordon as bindBulkCordon,
  PromoteRollout as bindPromoteRollout,
  AbortRollout as bindAbortRollout,
} from '$lib/wailsjs/go/wails/ManagementAPI'
import {
  GetOverview as bindGetOverview,
  GetOverviewForTarget as bindGetOverviewForTarget,
} from '$lib/wailsjs/go/wails/OverviewAPI'
import {
  GetSettings as bindGetHistorySettings,
  GetSeries as bindGetSeries,
  SetRetention as bindSetRetention,
  SetSamplingInterval as bindSetSamplingInterval,
} from '$lib/wailsjs/go/wails/HistoryAPI'
import {
  ChooseDirectory as bindChooseDirectory,
  ChooseFile as bindChooseFile,
  Credits as bindCredits,
  Info as bindInfo,
  LicenceText as bindLicenceText,
  OpenURL as bindOpenURL,
  ReadTextFile as bindReadTextFile,
  SaveTextFile as bindSaveTextFile,
} from '$lib/wailsjs/go/wails/SystemAPI'
import {
  Cancel as bindCancelFileCopy,
  StartDownload as bindStartDownload,
  StartUpload as bindStartUpload,
} from '$lib/wailsjs/go/wails/FileCopyAPI'
import {
  ImageReport as bindImageReport,
  ProbeFromHere as bindProbeFromHere,
  ProbeFromPod as bindProbeFromPod,
} from '$lib/wailsjs/go/wails/InspectAPI'
import { EventsOn } from '$lib/wailsjs/runtime/runtime'
import type { wails } from '$lib/wailsjs/go/models'
import { toApiError } from './errors'
// The session timeline records every write made through this module — see
// `writing` below. Type-only in the other direction, so the two modules
// referring to each other costs nothing at runtime.
import { timeline } from '$stores/timeline.svelte'
import type { TimelineTarget, WriteRecord } from '$lib/timeline'

/** A cluster described by the local kubeconfig. */
export type Cluster = wails.Cluster
/** What adding a kubeconfig would change, or did. */
export type KubeconfigMerge = wails.KubeconfigMerge
/** A namespace in a connected cluster. */
export type Namespace = wails.Namespace

export type NamespaceSummary = wails.NamespaceSummary
/** A pod, with derived status and measured usage. */
export type Pod = wails.Pod
/** One container within a pod. */
export type Container = wails.Container
/** One live port-forward, as the backend is actually holding it. */
export type PortForward = wails.PortForward
/** One live node shell, as the backend is actually holding it. */
export type NodeShell = wails.NodeShell
/** A cluster node. */
export type Node = wails.Node
/** A pod-managing controller. */
export type Workload = wails.Workload

export type Consumption = wails.Consumption

export type ApplicationInventory = wails.ApplicationInventory
export type Application = wails.Application
/** A Kubernetes Event. */
export type K8sEvent = wails.Event
/** One cluster's share of a cross-cluster list, with its own verdict — see
    app/domain/fleet.go for the statuses. */
export type ClusterPods = wails.ClusterPods
export type ClusterWorkloads = wails.ClusterWorkloads
export type ClusterEvents = wails.ClusterEvents
/** A browsable kind, as shown in the navigator. */
export type ResourceKind = wails.ResourceKind
/** A generically browsed kind, with server-printed columns. */
export type ResourceTable = wails.ResourceTable

/** What one kubeconfig may do in one namespace, as the API server enumerated
    it — see app/domain/rbac.go. */
export type SubjectRules = wails.SubjectRules
/** One RBAC rule, as the cluster stated it. */
export type PolicyRule = wails.PolicyRule
/** One account a binding names, or one being asked about. */
export type RBACSubject = wails.RBACSubject
/** One "can I" question. Blank subject fields ask about the current account. */
export type AccessRequest = wails.AccessRequest
/** The API server's own answer to one question, carried verbatim. */
export type AccessDecision = wails.AccessDecision
/** One RoleBinding or ClusterRoleBinding, with the subjects it carries. */
export type RoleBindingRef = wails.RoleBindingRef
/** One blast-radius flag — a verdict, and the only one in this feature. */
export type RBACFinding = wails.RBACFinding
/** One role, what references it, and what its rules permit. */
export type RoleInspection = wails.RoleInspection

/** What the frontend read off an object it is offering to probe. Every field
    is a quotation of the manifest already on screen, so planning a probe
    costs no read at all. */
export type ProbeSubjectInput = wails.ProbeSubjectInput
/** One step of a probe — resolution, connection, request — and what it
    produced. The three stay apart because a name that did not resolve and a
    port that refused need opposite next steps. */
export type ProbeStep = wails.ProbeStep
/** A finished reachability probe: what was tried, from where, what happened. */
export type ProbeResult = wails.ProbeResult
/** What Kubernetes reports about one container's image. Read `sizeStatus`
    before `sizeBytes`, and always show `bounded`. */
export type ImageReport = wails.ImageReport

export type NamespaceInventory = wails.NamespaceInventory
export type ConditionRef = wails.ConditionRef
export type ResourceCount = wails.ResourceCount
/** One column of a generic table. */
export type TableColumn = wails.TableColumn
/** One row of a generic table. */
export type TableRow = wails.TableRow
/** The running application's identity. */
export type AppInfo = wails.AppInfo
/** One shipped dependency and the licence it is distributed under. */
export type Credit = wails.Credit
/** One recorded measurement of a cluster. */
export type Sample = wails.Sample
/** A cluster's recorded history, with an account of its extent. */
export type SeriesResult = wails.SeriesResult
/** What PodSteer records locally, and how often. */
export type HistorySettings = wails.HistorySettings
/** An assessed cluster: what is wrong, what is left, what is running. */
export type Overview = wails.Overview
/** One X.509 certificate, as shown by a TLS Secret's certificate inspection. */
export type Certificate = wails.Certificate
/** One thing worth knowing about an inspected certificate chain, or about a
 * cert-manager Certificate that is running out. */
export type CertificateInsight = wails.CertificateInsight
/** What a cert-manager Certificate's status says about its own expiry, on the
 * way to the one verdict the operator panels draw. See assessCertificateRenewal. */
export type CertificateRenewalRef = wails.CertificateRenewalRef
/** What a scanner already running in the cluster recorded about one workload. */
export type VulnerabilitySummary = wails.VulnerabilitySummary
/** A TLS Secret's parsed certificate material — the leaf, its issuers, and
 * what is worth knowing about them. Never fetched except by inspectTLSSecret. */
export type CertificateChain = wails.CertificateChainDTO
export type MetricsBackend = wails.MetricsBackend
export type PodGraph = wails.PodGraph
/** One problem, aggregated across the objects it affects. */
export type Finding = wails.Finding

/** One node's share of what the cluster has been asked to run. */
export type NodeLoad = wails.NodeLoad

/** Pod slots: how many the cluster runs against how many it can. */
export type PodCapacity = wails.PodCapacity
/** One object a finding is about. */
export type Subject = wails.Subject
/** One dimension of cluster capacity. */
export type ResourceUsage = wails.ResourceUsage
/** One namespace's share of the cluster. */
export type NamespaceLoad = wails.NamespaceLoad
/** A pod worth looking at because it keeps restarting. */
export type RestartHotspot = wails.RestartHotspot
/** Counts for one controller kind. */
export type WorkloadKindSummary = wails.WorkloadKindSummary

/** One pod a drain plan or report leaves alone, or refuses to touch, and why. */
export type DrainSkip = wails.DrainSkip
/** One pod a drain attempted and could not evict, or could not confirm gone. */
export type DrainFailure = wails.DrainFailure
/** What a drain would do, previewed before it runs. */
export type DrainPlan = wails.DrainPlanDTO
/** One selected row, as a bulk action's plan and run take it. See $lib/bulk. */
export type BulkItemDTO = wails.BulkItemDTO
export type BulkLine = wails.BulkLineDTO
export type BulkPlan = wails.BulkPlanDTO
export type BulkResult = wails.BulkResultDTO
/** What happened when a drain ran. */
export type DrainReport = wails.DrainReportDTO
/** What an apply — real or a dry-run Validate — actually did. */
export type ApplyOutcome = wails.ApplyOutcomeDTO
/** One recorded revision of a Deployment, StatefulSet or DaemonSet's pod
 * template — what the History tab lists and a rollback picks a target
 * from. */
export type Revision = wails.RevisionDTO
/** What a rollback — real or a dry-run Preview — actually did. */
export type RollbackOutcome = wails.RollbackOutcomeDTO

/** Selects every namespace. Matches the backend's empty-string convention. */
export const ALL_NAMESPACES = ''

/** Payload of the `cluster:connected` event. */
export interface ClusterConnectedEvent {
  cluster: Cluster
  at: string
}

/** Payload of the `cluster:unreachable` event. */
export interface ClusterUnreachableEvent {
  clusterId: string
  reason: string
  at: string
}

/** Payload of the `filecopy:progress` event: bytes moved so far. */
export interface FileCopyProgressEvent {
  transferId: string
  bytes: number
}

/**
 * Payload of the `filecopy:done` event, sent once per transfer however it
 * ended. `error` is the same `[code] message` envelope a rejected call
 * carries, so `toApiError` parses it; `cancelled` is the operator's own
 * Cancel and carries no error.
 */
export interface FileCopyDoneEvent {
  transferId: string
  direction: 'download' | 'upload'
  files: number
  entries: number
  bytes: number
  durationMs: number
  notes: string[]
  localPath: string
  cancelled: boolean
  error: string
}

/** Removes an event subscription. */
export type Unsubscribe = () => void

/**
 * Runs a binding call, converting any rejection into an ApiError.
 *
 * Every exported function goes through here so callers can rely on
 * `toApiError(err).code` never seeing a raw string.
 */
async function call<T>(operation: () => Promise<T>): Promise<T> {
  try {
    return await operation()
  } catch (cause) {
    throw toApiError(cause)
  }
}

/**
 * Runs a write and records it on the session timeline, however it ended.
 *
 * HERE, AND ONLY HERE, for the same reason `ClusterSession.#recordRecent` is
 * the single place an object becomes recently opened: every control that
 * writes to a cluster goes through one of the wrappers below, so a dialog
 * added later cannot forget to be recorded, and there is exactly one
 * definition of what a recorded write looks like.
 *
 * A FAILURE IS RECORDED TOO. "I pressed delete and nothing happened" is
 * precisely the question a timeline answers, and one that showed only what
 * succeeded could not answer it. Nothing that does not change the cluster
 * goes through this: `validateResource`, `planDrain`, `planBulk` and a
 * dry-run rollback are all reads dressed as writes, and a timeline claiming
 * a rollback that was only previewed would be worse than no timeline.
 *
 * `describe` is handed the result on success so an entry can quote what the
 * cluster actually did — the kind and name an applied manifest turned out to
 * name, say — and `undefined` when there is none.
 */
async function writing<T>(
  clusterId: string,
  operation: () => Promise<T>,
  describe: (result: T | undefined) => Omit<WriteRecord, 'outcome' | 'failure'>,
): Promise<T> {
  try {
    const result = await call(operation)
    timeline.recordWrite(clusterId, { ...describe(result), outcome: 'ok' })
    return result
  } catch (cause) {
    const error = toApiError(cause)
    timeline.recordWrite(clusterId, {
      ...describe(undefined),
      outcome: 'failed',
      failure: error.message,
    })
    throw error
  }
}

/**
 * The object a recorded write was made on.
 *
 * The Kubernetes Kind verbatim, because that is what the timeline's row click
 * resolves against the navigator's catalogue — a lowercased plural matches
 * nothing and the row silently does nothing at all.
 */
function at(kind: string, namespace: string, name: string): TimelineTarget {
  return { kind, namespace, name }
}

// --- Clusters ---------------------------------------------------------------

/** Lists every cluster in the local kubeconfig. Works before connecting. */
export function listClusters(): Promise<Cluster[]> {
  return call(() => bindListClusters())
}

/** Reports what adding a kubeconfig would change, without touching the file. */
export function previewKubeconfig(raw: string): Promise<KubeconfigMerge> {
  return call(() => bindPreviewKubeconfig(raw))
}

/** Adds a kubeconfig to the local one and reports what changed. */
export function addKubeconfig(raw: string): Promise<KubeconfigMerge> {
  return call(() => bindAddKubeconfig(raw))
}

/**
 * Opens a native file picker and returns the chosen file's contents.
 *
 * An empty string means the operator cancelled, which is not an error. The
 * file is read on the Go side because the webview cannot open files — and
 * should not be able to.
 */
export function readKubeconfigFile(): Promise<string> {
  return call(() => bindReadKubeconfigFile())
}

/**
 * Opens a cluster and returns it with its server version.
 *
 * Resolving is proof the API server answered with the configured credentials.
 * Opening an already open cluster refreshes it rather than failing.
 */
export function connect(clusterId: string): Promise<Cluster> {
  return call(() => bindConnect(clusterId))
}

/** Closes a cluster, for when its tab is closed. */
export function disconnect(clusterId: string): Promise<void> {
  return call(() => bindDisconnect(clusterId))
}

/**
 * Marks a connected cluster read-only in PodSteer, or lifts the mark.
 *
 * A LOCAL GUARD, NOT A PERMISSION — the flag lives in this process's memory
 * and every write PodSteer makes checks it, but RBAC is what actually decides
 * what the underlying credentials may do. Call this right after a successful
 * {@link connect}, and again whenever the group setting or the cluster's
 * group changes (see stores/organisation.svelte.ts and workspace.svelte.ts).
 */
export function setReadOnly(clusterId: string, readOnly: boolean): Promise<void> {
  return call(() => bindSetReadOnly(clusterId, readOnly))
}

/** Returns the open clusters, in the order they were opened. */
export function connections(): Promise<Cluster[]> {
  return call(() => bindConnections())
}

/** Lists the namespaces of a connected cluster. */
export function listNamespaces(clusterId: string): Promise<Namespace[]> {
  return call(() => bindListNamespaces(clusterId))
}

/**
 * Lists every namespace with what is running in it.
 *
 * Separate from listNamespaces, which feeds the namespace filter and stays a
 * cheap read of names: this one counts pods, which means listing them.
 *
 * `annotationKeys` names the annotations each row should carry — the ones on
 * the kind's custom columns (see $lib/customColumns). Every list call takes
 * the same parameter, for the same reason: nothing else of an object's
 * annotations crosses the bridge, because kubectl's last-applied manifest
 * alone is tens of kilobytes per row. Labels always come along.
 */
export function listNamespaceSummaries(
  clusterId: string,
  annotationKeys: string[] = [],
): Promise<NamespaceSummary[]> {
  return call(() => bindListNamespaceSummaries(clusterId, annotationKeys))
}

/**
 * Says whether a cert-manager Certificate is running out without being
 * renewed.
 *
 * A pure call that reaches no cluster, exactly like `classifyConditions`. The
 * cert-manager panel quotes the manifest the drawer already holds, and
 * quoting needs no round trip — but `status.notAfter` and
 * `status.renewalTime` are dates, and comparing them to the clock and to the
 * Ready condition is a verdict. Verdicts live in the Go domain, where
 * `domain.AssessCertificateRenewal` can be argued with in a test.
 *
 * An empty list means there is nothing to say, which is the answer for almost
 * every certificate.
 */
export function assessCertificateRenewal(
  certificate: CertificateRenewalRef,
): Promise<CertificateInsight[]> {
  return call(() => bindAssessCertificateRenewal(certificate))
}

/**
 * What a vulnerability scanner already running in the cluster has recorded
 * about one namespace's workloads, keyed by the `Kind/name` a pod row's
 * `controlledBy` already carries.
 *
 * ON ITS OWN, NEVER FROM A LIST, and never on a refresh tick. The pod list is
 * drawn without it and the counts fill in when this answers; a cluster with
 * no scanner returns an empty array and the list is exactly what it was
 * before this existed. The read is bounded and cached in Go — see
 * `app/adapters/k8s/trivy.go`.
 */
export function vulnerabilitySummaries(
  clusterId: string,
  namespace: string,
): Promise<VulnerabilitySummary[]> {
  return call(() => bindVulnerabilitySummaries(clusterId, namespace))
}

/**
 * Says which status conditions report a problem, in order.
 *
 * A pure call that reaches no cluster: the polarity of a condition is a
 * verdict, verdicts live in the Go domain, and getting one backwards is
 * invisible until somebody reads the wrong colour during an incident.
 */
export function classifyConditions(conditions: ConditionRef[]): Promise<string[]> {
  return call(() => bindClassifyConditions(conditions))
}

/** Lists the nodes of a connected cluster, with usage where available.
    `annotationKeys` is the projection listNamespaceSummaries describes. */
export function listNodes(clusterId: string, annotationKeys: string[] = []): Promise<Node[]> {
  return call(() => bindListNodes(clusterId, annotationKeys))
}

// --- Overview ---------------------------------------------------------------

/**
 * Assesses a connected cluster.
 *
 * One call rather than a dozen, because the assessment has to be of a cluster
 * seen at one moment — and because the analysis belongs in Go, where it is
 * tested, rather than in the browser.
 *
 * A rejection means no assessment could be made at all. Individual sources
 * that could not be read are named in `overview.unavailable` instead.
 */
export function getOverview(clusterId: string): Promise<Overview> {
  return call(() => bindGetOverview(clusterId))
}

/**
 * Assesses a connected cluster the way `getOverview` does, but scores the
 * upgrade-impact findings against `targetMinor` (e.g. `"1.33"`) instead of
 * the default of the next minor after the cluster's current version — what
 * the overview header's "check against" selector calls.
 */
export function getOverviewForTarget(clusterId: string, targetMinor: string): Promise<Overview> {
  return call(() => bindGetOverviewForTarget(clusterId, targetMinor))
}

// --- History ----------------------------------------------------------------

/**
 * Returns a cluster's recorded history over the last `windowMinutes`.
 *
 * The history covers the window the application has been open — PodSteer
 * samples while it runs and stores nothing anywhere else — so the result also
 * reports the span it actually holds. The UI must say which, rather than
 * implying the completeness a monitoring stack would have.
 */
export function getSeries(
  clusterId: string,
  windowMinutes: number,
  maxPoints: number,
): Promise<SeriesResult> {
  return call(() => bindGetSeries(clusterId, windowMinutes, maxPoints))
}

/** Reports what is recorded on this machine, and how often. */
export function getHistorySettings(): Promise<HistorySettings> {
  return call(() => bindGetHistorySettings())
}

/** Changes how long samples are kept. Zero stops recording and erases what exists. */
export function setRetention(days: number): Promise<void> {
  return call(() => bindSetRetention(days))
}

/**
 * Changes how often each open cluster is sampled.
 *
 * Every sample costs a full cluster assessment, so on a large cluster this is
 * a load control as much as a chart-resolution preference.
 */
export function setSamplingInterval(seconds: number): Promise<void> {
  return call(() => bindSetSamplingInterval(seconds))
}

// --- Navigation -------------------------------------------------------------

/**
 * Lists every browsable kind in a connected cluster.
 *
 * The navigator is built from this rather than hard-coded, so a cluster's own
 * operators appear in the tree with no frontend change.
 */
export function listKinds(clusterId: string): Promise<ResourceKind[]> {
  return call(() => bindListKinds(clusterId))
}

// --- Workloads --------------------------------------------------------------

/** Lists pods. An empty namespace means every namespace. `annotationKeys` is
    the projection listNamespaceSummaries describes. */
export function listPods(
  clusterId: string,
  namespace: string,
  annotationKeys: string[] = [],
): Promise<Pod[]> {
  return call(() => bindListPods(clusterId, namespace, annotationKeys))
}

/** Lists controllers of one kind, named as "Deployment", "StatefulSet", etc.
    `annotationKeys` is the projection listNamespaceSummaries describes. */
export function listWorkloads(
  clusterId: string,
  kind: string,
  namespace: string,
  annotationKeys: string[] = [],
): Promise<Workload[]> {
  return call(() => bindListWorkloads(clusterId, kind, namespace, annotationKeys))
}

/** The dependency chain around one pod, from what routes to it to what it needs. */
export function podGraph(clusterId: string, namespace: string, podName: string): Promise<PodGraph> {
  return call(() => bindPodGraph(clusterId, namespace, podName))
}

/** The dependency chain around one workload: what routes to it, and its pods. */
export function workloadGraph(
  clusterId: string,
  namespace: string,
  kind: string,
  name: string,
): Promise<PodGraph> {
  return call(() => bindWorkloadGraph(clusterId, namespace, kind, name))
}

/**
 * The neighbourhood of one object of any kind: what its spec names below it,
 * what owns it above.
 *
 * The third map shape, for everything the generic table lists — a Service, a
 * ConfigMap, a PVC, a CRD instance. Keyed by the navigator catalogue id rather
 * than by a kind name, because that is what the drawer already holds and what
 * the backend needs to know which API path to read.
 */
export function objectGraph(
  clusterId: string,
  kindId: string,
  namespace: string,
  name: string,
): Promise<PodGraph> {
  return call(() => bindObjectGraph(clusterId, kindId, namespace, name))
}

/** Lists the pods the scheduler has placed on one node, across namespaces. */
export function listPodsOnNode(clusterId: string, nodeName: string): Promise<Pod[]> {
  return call(() => bindListPodsOnNode(clusterId, nodeName))
}

/**
 * Sums what a controller's pods are consuming, against what they reserved.
 *
 * Read while a panel is open rather than alongside the list: a controller has
 * no usage of its own, so this costs that controller's pods and the
 * namespace's metrics, and the figure is only looked at one at a time.
 */
export function workloadUsage(
  clusterId: string,
  namespace: string,
  kind: string,
  name: string,
): Promise<Consumption> {
  return call(() => bindWorkloadUsage(clusterId, namespace, kind, name))
}

/**
 * Sums what every controller in a list is using, keyed by "namespace/name".
 *
 * Fetched beside the list rather than as part of it: the controllers are one
 * cheap read and this is the namespace's pods and their metrics, so a cluster
 * without a metrics API still gets its list.
 */
export function workloadConsumption(
  clusterId: string,
  kind: string,
  namespace: string,
): Promise<Record<string, Consumption>> {
  return call(() => bindWorkloadConsumption(clusterId, kind, namespace))
}

/**
 * Groups a cluster's workloads by the application they belong to.
 *
 * From Kubernetes' own recommended labels, which is the only thing that
 * standardises this — and a convention rather than a guarantee, so the answer
 * carries a count of what did not say which application it belongs to.
 */
export function listApplications(
  clusterId: string,
  namespace: string,
): Promise<ApplicationInventory> {
  return call(() => bindListApplications(clusterId, namespace))
}

/**
 * Returns the recorded revisions of a Deployment, StatefulSet or DaemonSet's
 * pod template, newest first — the drawer's History tab, and what
 * RollbackDialog picks a target revision from.
 */
export function rolloutHistory(
  clusterId: string,
  kind: string,
  namespace: string,
  name: string,
): Promise<Revision[]> {
  return call(() => bindRolloutHistory(clusterId, kind, namespace, name))
}

/** Lists all pods owned by a specific workload. */
export function listPodsForWorkload(
  clusterId: string,
  namespace: string,
  kind: string,
  name: string,
): Promise<Pod[]> {
  return call(() => bindListPodsForWorkload(clusterId, namespace, kind, name))
}

// --- Fleet ------------------------------------------------------------------
//
// One call per tick however many clusters are open. The fan-out is in Go —
// application.FleetService — and every cluster comes back with its own
// verdict, so a refused or unreachable cluster is a row in the answer, never
// a rejection of it. The rejection cases are the caller's own: naming a
// cluster that is not open, or an unusable namespace.

/** Lists pods across the named open clusters, grouped per cluster in tab order. */
export function listFleetPods(clusterIds: string[], namespace: string): Promise<ClusterPods[]> {
  return call(() => bindListFleetPods(clusterIds, namespace))
}

/** Lists every controller kind but ReplicaSet across the named open clusters. */
export function listFleetWorkloads(
  clusterIds: string[],
  namespace: string,
): Promise<ClusterWorkloads[]> {
  return call(() => bindListFleetWorkloads(clusterIds, namespace))
}

/** Lists events across the named open clusters. */
export function listFleetEvents(clusterIds: string[], namespace: string): Promise<ClusterEvents[]> {
  return call(() => bindListFleetEvents(clusterIds, namespace))
}

// --- RBAC explorer ----------------------------------------------------------
//
// Three reads, every one of them made because somebody pressed something.
// Nothing here is on the refresh tick and nothing here is cached, in Go or
// on this side: a decision held over from an earlier instant could report a
// permission that has since been revoked as still granted.
//
// Being refused is an ordinary answer rather than a rejection — each result
// carries its own `status`, so a pane says which of "you may not ask", "the
// cluster does not offer it" and "it failed, try again" happened.

/** Asks what the current credentials may do in one namespace. One request. */
export function subjectRules(clusterId: string, namespace: string): Promise<SubjectRules> {
  return call(() => bindSubjectRules(clusterId, namespace))
}

/**
 * Asks whether one action is permitted — for the current account when the
 * subject fields are blank, or for a named user, group or service account.
 *
 * The API server decides. What comes back is its own allowed, denied and
 * reason; nothing on this side evaluates a rule to reach a verdict.
 */
export function canI(clusterId: string, request: AccessRequest): Promise<AccessDecision> {
  return call(() => bindCanI(clusterId, request))
}

/**
 * Reads one Role or ClusterRole, the bindings that reference it, and the
 * blast-radius flags its rules raise.
 *
 * `scope` is 'cluster' or 'namespace'. Three requests, two of them
 * cluster-wide lists, so it is called when the panel is opened and never on
 * a tick.
 */
export function inspectRole(
  clusterId: string,
  scope: 'cluster' | 'namespace',
  namespace: string,
  name: string,
): Promise<RoleInspection> {
  return call(() => bindInspectRole(clusterId, scope, namespace, name))
}

// --- Events -----------------------------------------------------------------

/** Lists events, warnings first and most recent first. `annotationKeys` is
    the projection listNamespaceSummaries describes. */
export function listEvents(
  clusterId: string,
  namespace: string,
  annotationKeys: string[] = [],
): Promise<K8sEvent[]> {
  return call(() => bindListEvents(clusterId, namespace, annotationKeys))
}

/** Lists events for one specific object — what the detail drawer's Events tab shows. */
export function listEventsForResource(
  clusterId: string,
  namespace: string,
  kind: string,
  name: string,
): Promise<K8sEvent[]> {
  return call(() => bindListEventsForResource(clusterId, namespace, kind, name))
}

// --- Generic browsing -------------------------------------------------------

/** Lists any kind as a table with the columns the API server prints. Every
    row also carries its labels and the `annotationKeys` asked for — the
    projection listNamespaceSummaries describes — read from the metadata the
    server attaches to the row, never from a request per object. */
export function listTable(
  clusterId: string,
  kindId: string,
  namespace: string,
  annotationKeys: string[] = [],
): Promise<ResourceTable> {
  return call(() => bindListTable(clusterId, kindId, namespace, annotationKeys))
}

/**
 * Counts what one namespace holds, kind by kind.
 *
 * One request per built-in namespaced kind on the Go side, so this is asked
 * when a panel section is opened rather than on every refresh.
 */
export function namespaceInventory(
  clusterId: string,
  namespace: string,
): Promise<NamespaceInventory> {
  return call(() => bindNamespaceInventory(clusterId, namespace))
}

/**
 * Returns one object as YAML, for the detail view.
 *
 * `revealSecrets` applies to core/v1 Secrets and nothing else. Left false,
 * their values arrive as their decoded SIZE — the form `kubectl describe
 * secret` prints — and the material never crosses the bridge at all. Pass
 * true only from a deliberate click: reading a Secret is an audited action,
 * and the YAML tab would otherwise perform one every time somebody browsed
 * past a Secret in a list.
 */
export function getManifest(
  clusterId: string,
  kindId: string,
  namespace: string,
  name: string,
  revealSecrets = false,
): Promise<string> {
  return call(() => bindGetManifest(clusterId, kindId, namespace, name, revealSecrets))
}

/**
 * Returns one decoded Secret value, for a reveal the operator asked for.
 *
 * NEVER call this speculatively — not on mount, not on a timer, not to warm
 * anything. Reading a Secret is an audited action, and Kubernetes' own
 * good-practices page tells cluster operators to alert on several being read
 * at once; a pane that resolved every reference on open would produce that
 * signature on somebody else's security dashboard. One call, one key, one
 * deliberate click.
 *
 * The result is a plaintext credential. Hold it in component-local state that
 * is cleared, never in a store, and never write it anywhere that persists.
 */
export function revealSecretKey(
  clusterId: string,
  namespace: string,
  name: string,
  key: string,
): Promise<string> {
  return call(() => bindRevealSecretKey(clusterId, namespace, name, key))
}

/**
 * Parses a TLS Secret's certificate material, for a deliberate inspection.
 *
 * The certificate equivalent of revealSecretKey, and NEVER call this
 * speculatively for the same reason: the certificate is public material, but
 * it lives inside the same Secret object as the private key, and a read of
 * that object is a read of that object regardless of which half somebody
 * wants. One call, one press of "Inspect certificate".
 *
 * The private key itself never crosses this boundary at all — the backend
 * only ever hands back whether it matched, as `keyMatches` on the result.
 */
export function inspectTLSSecret(
  clusterId: string,
  namespace: string,
  name: string,
): Promise<CertificateChain> {
  return call(() => bindInspectTLSSecret(clusterId, namespace, name))
}

// --- On-request inspections -------------------------------------------------
//
// A reachability probe and an image report. NEITHER IS EVER CALLED BY THE
// REFRESH TICK, and that is the one rule this whole group shares: a probe
// opens a socket or runs a command in somebody's container, and repeating
// that every ten seconds would fill an audit log with execs nobody asked for.
// The panels above them keep the answer with the time it was taken and wait
// to be asked again — the same discipline `objectGraph` follows.

/**
 * Probes a target from THIS MACHINE, through the API server named in the
 * kubeconfig and through nothing else.
 *
 * A Service goes through the API server's own service proxy; a pod through an
 * ephemeral port-forward the backend opens and tears down again. An Ingress
 * host is refused outright, because reaching it would mean opening a
 * connection to a host that is not an API server — the in-cluster vantage is
 * what answers for one.
 *
 * A target that refuses a connection is an ordinary RESULT, not a rejection:
 * the promise resolves with an outcome of "refused". A rejection means the
 * probe could not be performed at all.
 */
export function probeFromHere(
  clusterId: string,
  subject: wails.ProbeSubjectInput,
): Promise<wails.ProbeResult> {
  return call(() => bindProbeFromHere(clusterId, subject))
}

/**
 * Probes a target from inside a container the operator chose.
 *
 * A WRITE-SHAPED ACT: it runs a command in somebody else's container, so the
 * backend refuses it on a cluster marked read-only and leaves one audit line
 * naming the cluster, namespace, pod, container and target. Like the reveal of
 * a Secret key, never call it speculatively — one call, one deliberate press.
 *
 * A container with no nc, curl or wget rejects with the `probe_tool_missing`
 * code, which says nothing about the target and everything about the image.
 */
export function probeFromPod(
  clusterId: string,
  namespace: string,
  podName: string,
  containerName: string,
  subject: wails.ProbeSubjectInput,
): Promise<wails.ProbeResult> {
  return call(() => bindProbeFromPod(clusterId, namespace, podName, containerName, subject))
}

/**
 * Describes one container's image using only what Kubernetes reports.
 *
 * Two GETs — the pod, and the node that pulled it. No registry is contacted
 * and no pull Secret is read; the report carries the line saying so, and the
 * panel always shows it, because empty space where layers would be is a claim
 * nothing checked.
 */
export function imageReport(
  clusterId: string,
  namespace: string,
  podName: string,
  containerName: string,
): Promise<wails.ImageReport> {
  return call(() => bindImageReport(clusterId, namespace, podName, containerName))
}

// --- Port forwards ----------------------------------------------------------

/**
 * Opens a local port onto a container port.
 *
 * `localPort` of 0 lets the operating system choose, and the returned forward
 * carries whichever port it actually bound — which is the only truthful
 * answer, since there is an unavoidable race between finding a free port and
 * binding it.
 */
export function startPortForward(
  clusterId: string,
  namespace: string,
  pod: string,
  podUID: string,
  localPort: number,
  remotePort: number,
  portName: string,
  protocol: string,
  selector: Record<string, string>,
): Promise<PortForward> {
  return call(() =>
    bindStartPortForward(
      clusterId,
      namespace,
      pod,
      podUID,
      localPort,
      remotePort,
      portName,
      protocol,
      selector,
    ),
  )
}

/** Closes one forward, waiting for its local port to be released. */
export function stopPortForward(forwardId: string): Promise<void> {
  return call(() => bindStopPortForward(forwardId))
}

/** Reports what is forwarded right now — the live registry, not intent. */
export function listPortForwards(): Promise<PortForward[]> {
  return call(() => bindListPortForwards())
}

/** Closes every running forward, across every cluster. */
export function stopAllPortForwards(): Promise<void> {
  return call(() => bindStopAllPortForwards())
}

/** Reports the node shells running right now — the live registry, not intent. */
export function listNodeShells(): Promise<NodeShell[]> {
  return call(() => bindListNodeShells())
}

/** Deletes the pod behind one node shell. The attach session ending does this
 * too, so both reaching the same shell is not an error. */
export function stopNodeShell(shellId: string): Promise<void> {
  return call(() => bindStopNodeShell(shellId))
}

/** Deletes every node-shell pod, across every cluster. */
export function stopAllNodeShells(): Promise<void> {
  return call(() => bindStopAllNodeShells())
}

/**
 * Reports whether a TCP port on THIS machine is free to bind.
 *
 * Never the cluster — the local-port picker in the forward-start dialog is
 * asking about a collision on the operator's own machine, which is the one
 * kind of collision Kubernetes cannot warn about.
 */
export function probeLocalPort(port: number): Promise<boolean> {
  return call(() => bindProbeLocalPort(port))
}

/** Asks the operating system for a local TCP port nothing is using. */
export function freeLocalPort(): Promise<number> {
  return call(() => bindFreeLocalPort())
}

/**
 * Where "Open" should send the operator for a running forward.
 *
 * 127.0.0.1 rather than the `localhost` in PortForward.address: that field is
 * meant for DISPLAY, and this is meant for OpenURL, which the CSP-tightened
 * webview never touches — only the OS's own resolver does, and 127.0.0.1
 * skips it needing to resolve anything at all.
 */
export function forwardBrowserURL(forward: PortForward): string {
  return `${forward.scheme}://127.0.0.1:${forward.localPort}/`
}

// --- System -----------------------------------------------------------------

/** Returns the running application's name, version and platform. */
export function appInfo(): Promise<AppInfo> {
  return call(() => bindInfo())
}

/**
 * Lists every dependency PodSteer ships, with its licence.
 *
 * Not decoration: MIT, BSD, ISC and Apache-2.0 all require the licence and its
 * copyright notice to travel with the binary, and a desktop application has
 * nowhere to put them except its own Credits pane.
 */
export function listCredits(): Promise<Credit[]> {
  return call(() => bindCredits())
}

/** Fetches one licence's full text, on demand. */
export function licenceText(textId: string): Promise<string> {
  return call(() => bindLicenceText(textId))
}

/**
 * Opens an address in the operator's default browser.
 *
 * Never navigate the webview itself to an external site: it has no address
 * bar, so there would be no way back to the application.
 */
export function openURL(url: string): Promise<void> {
  return call(() => bindOpenURL(url))
}

/**
 * Opens a native save dialog seeded with `name` and writes `content` to
 * wherever the operator chose.
 *
 * An empty returned path means the operator cancelled, which is not an
 * error — the same convention as readKubeconfigFile. The write happens on
 * the Go side because the webview cannot touch the filesystem and should
 * not be able to.
 */
export function saveTextFile(name: string, content: string): Promise<string> {
  return call(() => bindSaveTextFile(name, content))
}

/**
 * Opens the native folder picker and returns the chosen path, or "" if the
 * operator cancelled — the same convention as readKubeconfigFile.
 *
 * Only ever handed back to startDownload or startUpload, which check it
 * again in Go; the webview itself can do nothing with a path.
 */
export function chooseDirectory(title: string): Promise<string> {
  return call(() => bindChooseDirectory(title))
}

/** Opens the native file picker; "" means cancelled. See chooseDirectory. */
export function chooseFile(title: string): Promise<string> {
  return call(() => bindChooseFile(title))
}

/**
 * Opens the native file picker and returns what the chosen file CONTAINS.
 *
 * Distinct from chooseFile, which returns a path: a path is only useful to a
 * Go method that will act on it, and the webview cannot open a file itself.
 * The settings import is the caller. An empty string means cancelled, as
 * everywhere else here; an empty or oversized file is an error instead, so
 * the two cannot be confused.
 */
export function readTextFile(title: string): Promise<string> {
  return call(() => bindReadTextFile(title))
}

// --- File copy --------------------------------------------------------------

/**
 * Starts copying `remotePath` out of a container into `localDir`, a folder
 * from chooseDirectory. Returns the transfer id; progress and completion
 * arrive as events (onFileCopyProgress, onFileCopyDone).
 *
 * The download lands under localDir by the remote entry's own name, exactly
 * as `kubectl cp pod:/etc/nginx localDir/nginx` would.
 */
export function startDownload(
  clusterId: string,
  namespace: string,
  podName: string,
  containerName: string,
  remotePath: string,
  localDir: string,
): Promise<string> {
  return call(() =>
    bindStartDownload(clusterId, namespace, podName, containerName, remotePath, localDir),
  )
}

/**
 * Starts copying `localPath` — a file or folder from the pickers — into
 * `remoteDir` inside a container. A write, refused on a read-only cluster.
 */
export function startUpload(
  clusterId: string,
  namespace: string,
  podName: string,
  containerName: string,
  localPath: string,
  remoteDir: string,
): Promise<string> {
  return call(() =>
    bindStartUpload(clusterId, namespace, podName, containerName, localPath, remoteDir),
  )
}

/** Stops a running transfer. A no-op for one that has already finished. */
export function cancelFileCopy(transferId: string): Promise<void> {
  return call(() => bindCancelFileCopy(transferId))
}

// --- Management -------------------------------------------------------------

/** Scales a workload to the specified number of replicas. */
export function scaleWorkload(
  clusterId: string,
  kind: string,
  namespace: string,
  name: string,
  replicas: number,
): Promise<void> {
  return writing(
    clusterId,
    () => bindScaleWorkload(clusterId, kind, namespace, name, replicas),
    () => ({
      action: 'Scaled',
      target: at(kind, namespace, name),
      detail: `to ${replicas} replica${replicas === 1 ? '' : 's'}`,
    }),
  )
}

/**
 * Applies a manifest of any kind to the cluster — the generic path, not a
 * fixed set of typed kinds. A manifest carrying `metadata.resourceVersion`
 * is sent as a PUT the server optimistic-locks against; a stale one comes
 * back as an ApiError with code `conflict` (see api/errors.ts). One without
 * it is created, replacing any existing object of the same name.
 */
export function updateResource(clusterId: string, manifest: string): Promise<ApplyOutcome> {
  // The manifest itself is never recorded — only what the cluster says it
  // named. It is an arbitrary document that may carry a Secret's data, and a
  // timeline holding a copy of one would be a second copy of it.
  return writing(
    clusterId,
    () => bindUpdateResource(clusterId, manifest),
    // A REFUSAL NAMES NOTHING, and must not claim to: the outcome is where
    // the kind and name come from, and a rejected manifest never produced
    // one. The row then reads as an apply that was refused, filed
    // cluster-wide rather than against an object nothing confirmed.
    (outcome) => ({
      action: outcome === undefined ? 'Apply' : outcome.created ? 'Created' : 'Applied',
      target: at(outcome?.kind ?? '', outcome?.namespace ?? '', outcome?.name ?? ''),
      detail: '',
    }),
  )
}

/**
 * Validates a manifest without applying it — the same generic path as
 * updateResource, but with the API server's dry run: every admission check
 * (schema validation, webhooks) runs, and nothing is persisted. Allowed on a
 * read-only cluster, since nothing here writes anything.
 */
export function validateResource(clusterId: string, manifest: string): Promise<ApplyOutcome> {
  return call(() => bindValidateResource(clusterId, manifest))
}

/** Deletes a resource. */
export function deleteResource(
  clusterId: string,
  kindGroup: string,
  kindVersion: string,
  kindKind: string,
  namespace: string,
  name: string,
): Promise<void> {
  return writing(
    clusterId,
    () => bindDeleteResource(clusterId, kindGroup, kindVersion, kindKind, namespace, name),
    () => ({ action: 'Deleted', target: at(kindKind, namespace, name), detail: '' }),
  )
}

/** Restarts a rollout (deployment/statefulset/daemonset). */
export function restartRollout(
  clusterId: string,
  kind: string,
  namespace: string,
  name: string,
): Promise<void> {
  return writing(
    clusterId,
    () => bindRestartRollout(clusterId, kind, namespace, name),
    () => ({ action: 'Restarted', target: at(kind, namespace, name), detail: 'rolling restart' }),
  )
}

/**
 * Creates a Job from a CronJob's template right now, outside its schedule.
 * Resolves to the created Job's name.
 */
export function triggerCronJob(clusterId: string, namespace: string, name: string): Promise<string> {
  return writing(
    clusterId,
    () => bindTriggerCronJob(clusterId, namespace, name),
    (job) => ({
      action: 'Triggered',
      target: at('CronJob', namespace, name),
      detail: job ? `created ${job}` : '',
    }),
  )
}

/**
 * Writes one key of one Secret, decoded — the plaintext an operator typed,
 * not base64. The backend converts it to bytes; nothing here encodes
 * anything.
 *
 * The same deliberate act as revealSecretKey, in the other direction: this
 * is only ever called from a Save on a key that has already been revealed
 * (see $lib/components/ContainerDetail.svelte), which is what keeps a
 * cluster's audit log meaningful — one entry per click, never a side effect
 * of anything else.
 */
export function setSecretKey(
  clusterId: string,
  namespace: string,
  name: string,
  key: string,
  value: string,
): Promise<void> {
  // THE KEY, NEVER THE VALUE — the same line ManagementService writes to the
  // log, and for the same reason. A timeline carrying the plaintext somebody
  // typed into a Secret would keep a second copy of it in the webview for as
  // long as the tab stayed open.
  return writing(
    clusterId,
    () => bindSetSecretKey(clusterId, namespace, name, key, value),
    () => ({ action: 'Wrote key', target: at('Secret', namespace, name), detail: key }),
  )
}

/** Writes one key of one ConfigMap. */
export function setConfigMapKey(
  clusterId: string,
  namespace: string,
  name: string,
  key: string,
  value: string,
): Promise<void> {
  return writing(
    clusterId,
    () => bindSetConfigMapKey(clusterId, namespace, name, key, value),
    () => ({ action: 'Wrote key', target: at('ConfigMap', namespace, name), detail: key }),
  )
}

/**
 * Sets one container's (or, with initContainer true, one init container's)
 * image on a Deployment, StatefulSet or DaemonSet.
 *
 * Applies to exactly one container per call — SetImageDialog calls this once
 * per changed row rather than sending every container in one request, so a
 * refusal partway through names exactly which containers already changed.
 */
export function setImage(
  clusterId: string,
  kind: string,
  namespace: string,
  name: string,
  container: string,
  image: string,
  initContainer: boolean,
): Promise<void> {
  return writing(
    clusterId,
    () => bindSetImage(clusterId, kind, namespace, name, container, image, initContainer),
    () => ({
      action: 'Set image',
      target: at(kind, namespace, name),
      detail: `${container} to ${image}`,
    }),
  )
}

/**
 * Rolls a Deployment, StatefulSet or DaemonSet back to a previously recorded
 * revision, the way `kubectl rollout undo --to-revision` does.
 *
 * dryRun asks the API server to validate the request without persisting
 * anything — RollbackDialog's Preview button — and is allowed on a
 * read-only cluster for the same reason validateResource is.
 */
export function rollbackWorkload(
  clusterId: string,
  kind: string,
  namespace: string,
  name: string,
  toRevision: number,
  dryRun: boolean,
): Promise<RollbackOutcome> {
  // A dry run persists nothing, so it is not a write and is not recorded.
  // Preview is a read that happens to travel down the write's own path.
  if (dryRun) return call(() => bindRollbackWorkload(clusterId, kind, namespace, name, toRevision, true))

  return writing(
    clusterId,
    () => bindRollbackWorkload(clusterId, kind, namespace, name, toRevision, false),
    () => ({
      action: 'Rolled back',
      target: at(kind, namespace, name),
      detail: `to revision ${toRevision}`,
    }),
  )
}

/** Suspends or resumes a CronJob's schedule, or a Job's pods. */
/**
 * Advances a paused Argo Rollouts Rollout by one step — what
 * `kubectl argo rollouts promote NAME` does.
 *
 * A write like any other: refused on a read-only cluster in Go, audited
 * there, and planned there too. Which patch it sends depends on what is
 * holding the Rollout, and the adapter reads the live object immediately
 * beforehand so the plan is made from the step it is on now.
 */
export function promoteRollout(
  clusterId: string,
  namespace: string,
  name: string,
): Promise<void> {
  return call(() => bindPromoteRollout(clusterId, namespace, name))
}

/**
 * Abandons the update a Rollout is part way through — what
 * `kubectl argo rollouts abort NAME` does.
 *
 * Not the reverse of a promote: traffic returns to the stable ReplicaSet, but
 * the spec is untouched, so the Rollout stays Degraded against the revision
 * that was being deployed until something changes its template.
 */
export function abortRollout(
  clusterId: string,
  namespace: string,
  name: string,
): Promise<void> {
  return call(() => bindAbortRollout(clusterId, namespace, name))
}

export function suspendWorkload(
  clusterId: string,
  kind: string,
  namespace: string,
  name: string,
  suspend: boolean,
): Promise<void> {
  return writing(
    clusterId,
    () => bindSuspendWorkload(clusterId, kind, namespace, name, suspend),
    () => ({
      action: suspend ? 'Suspended' : 'Resumed',
      target: at(kind, namespace, name),
      detail: '',
    }),
  )
}

/**
 * Marks a node schedulable or unschedulable.
 *
 * Cordoning only affects NEW pods — nothing already running on the node is
 * touched. Uncordoning is immediate; the caller is expected to confirm
 * cordoning first, since it changes where the scheduler is willing to place
 * work.
 */
export function cordonNode(clusterId: string, name: string, cordon: boolean): Promise<void> {
  return writing(
    clusterId,
    () => bindCordonNode(clusterId, name, cordon),
    () => ({
      action: cordon ? 'Cordoned' : 'Uncordoned',
      target: at('Node', '', name),
      detail: '',
    }),
  )
}

/**
 * Evicts one pod through the eviction subresource, distinct from deleting it:
 * a PodDisruptionBudget may refuse this and not a delete, which is what makes
 * it the respectful way to remove a pod from a running workload.
 *
 * gracePeriodSeconds negative means "use the pod's own
 * terminationGracePeriodSeconds".
 */
export function evictPod(
  clusterId: string,
  namespace: string,
  name: string,
  gracePeriodSeconds: number,
): Promise<void> {
  return writing(
    clusterId,
    () => bindEvictPod(clusterId, namespace, name, gracePeriodSeconds),
    () => ({ action: 'Evicted', target: at('Pod', namespace, name), detail: '' }),
  )
}

/**
 * Previews what draining a node would do, without touching the cluster.
 *
 * Call this when a drain dialog opens and again whenever force or
 * deleteEmptyDirData changes — it is cheap (one field-selected pod list) and
 * is what the confirm button's enabled state is built from.
 */
export function planDrain(
  clusterId: string,
  name: string,
  force: boolean,
  deleteEmptyDirData: boolean,
): Promise<DrainPlan> {
  return call(() => bindPlanDrain(clusterId, name, force, deleteEmptyDirData))
}

/**
 * Cordons a node and evicts every pod the drain plan allows.
 *
 * timeoutSeconds of 0 means the backend's own default (5 minutes).
 * gracePeriodSeconds negative means "use each pod's own
 * terminationGracePeriodSeconds".
 */
export function drainNode(
  clusterId: string,
  name: string,
  force: boolean,
  deleteEmptyDirData: boolean,
  gracePeriodSeconds: number,
  timeoutSeconds: number,
): Promise<DrainReport> {
  return writing(
    clusterId,
    () =>
      bindDrainNode(clusterId, name, force, deleteEmptyDirData, gracePeriodSeconds, timeoutSeconds),
    (report) => ({
      action: 'Drained',
      target: at('Node', '', name),
      // What the drain DID, from its own report, rather than what it was
      // asked to do: a drain that timed out having evicted three of nine is
      // not the same event as one that finished, and the row has to say so.
      detail: report
        ? `${report.evicted.length} evicted, ${report.failed.length} failed` +
          (report.timedOut ? ', timed out' : '')
        : '',
    }),
  )
}

// --- Bulk actions -----------------------------------------------------------

/**
 * Previews what a bulk action would do to each selected row, without
 * touching the cluster.
 *
 * The same domain function the run itself goes through — see
 * app/domain/bulk.go — so the review dialog and the outcome can never
 * disagree. Call it when the dialog opens and again whenever the target
 * replica count changes; it costs no cluster read at all, since every fact
 * it needs is on the rows already on screen. `replicas` is read for the
 * scale action only.
 */
export function planBulk(
  clusterId: string,
  action: string,
  items: BulkItemDTO[],
  replicas = 0,
): Promise<BulkPlan> {
  return call(() => bindPlanBulk(clusterId, action, items, replicas))
}

/**
 * Deletes every selected row the plan allows. Resolves to one result per
 * row whether it was skipped, deleted or failed — a per-object failure is a
 * result, never a rejected promise; only what stops the whole action (a
 * read-only cluster, an unusable argument) rejects.
 */

/**
 * Records one entry per row a bulk action actually acted on.
 *
 * PER ROW, NOT PER RUN, because that is what the action itself is: a bulk
 * delete is fifty deletes whose failures are per-object results rather than
 * one outcome (see app/domain/bulk.go), so a single "deleted 50" row could
 * not say which of them the cluster refused. A SKIPPED row is not recorded at
 * all — the plan declined to act on it, and nothing reached the cluster.
 *
 * `failure` is what stopped the whole run, when something did: a read-only
 * cluster or an unusable argument rejects before any row is attempted, and
 * every row is then recorded as refused for that one reason.
 */
function recordBulk(
  clusterId: string,
  action: string,
  detail: string,
  items: BulkItemDTO[],
  results: BulkResult[] | null,
  failure?: string,
): void {
  if (results === null) {
    for (const item of items) {
      timeline.recordWrite(clusterId, {
        action,
        target: at(item.kind, item.namespace, item.name),
        detail,
        outcome: 'failed',
        failure,
      })
    }
    return
  }

  for (const result of results) {
    if (result.skipped) continue
    timeline.recordWrite(clusterId, {
      action,
      target: at(result.kind, result.namespace, result.name),
      detail,
      outcome: result.done ? 'ok' : 'failed',
      failure: result.done ? undefined : result.reason,
    })
  }
}

/** Runs one bulk action and records what it did to each row. */
async function bulkWriting(
  clusterId: string,
  action: string,
  detail: string,
  items: BulkItemDTO[],
  operation: () => Promise<BulkResult[]>,
): Promise<BulkResult[]> {
  try {
    const results = await call(operation)
    recordBulk(clusterId, action, detail, items, results)
    return results
  } catch (cause) {
    const error = toApiError(cause)
    recordBulk(clusterId, action, detail, items, null, error.message)
    throw error
  }
}

export function bulkDelete(clusterId: string, items: BulkItemDTO[]): Promise<BulkResult[]> {
  return bulkWriting(clusterId, 'Deleted', '', items, () => bindBulkDelete(clusterId, items))
}

/** Rolling-restarts every selected Deployment, StatefulSet and DaemonSet. Resolves like bulkDelete. */
export function bulkRestart(clusterId: string, items: BulkItemDTO[]): Promise<BulkResult[]> {
  return bulkWriting(clusterId, 'Restarted', 'rolling restart', items, () =>
    bindBulkRestart(clusterId, items),
  )
}

/** Scales every selected Deployment, StatefulSet and ReplicaSet to `replicas`. Resolves like bulkDelete. */
export function bulkScale(
  clusterId: string,
  items: BulkItemDTO[],
  replicas: number,
): Promise<BulkResult[]> {
  return bulkWriting(
    clusterId,
    'Scaled',
    `to ${replicas} replica${replicas === 1 ? '' : 's'}`,
    items,
    () => bindBulkScale(clusterId, items, replicas),
  )
}

/** Cordons (`cordon` true) or uncordons every selected node. Resolves like bulkDelete. */
export function bulkCordon(
  clusterId: string,
  items: BulkItemDTO[],
  cordon: boolean,
): Promise<BulkResult[]> {
  return bulkWriting(clusterId, cordon ? 'Cordoned' : 'Uncordoned', '', items, () =>
    bindBulkCordon(clusterId, items, cordon),
  )
}

/**
 * Streams logs from a pod container. Returns a stream ID for stopping.
 *
 * sinceSeconds and limitBytes of 0 mean unset — see domain.LogOptions on the
 * Go side. timestamps is always sent true by every caller today: the
 * frontend decides whether to DISPLAY a timestamp at render time rather than
 * by re-opening the stream — see LogViewer.svelte, which is why it never
 * varies this parameter.
 */
export function streamLogs(
  clusterId: string,
  namespace: string,
  podName: string,
  containerName: string,
  follow: boolean,
  tailLines: number,
  sinceSeconds: number,
  previous: boolean,
  timestamps: boolean,
  limitBytes: number,
): Promise<string> {
  return call(() =>
    bindStreamLogs(
      clusterId,
      namespace,
      podName,
      containerName,
      follow,
      tailLines,
      sinceSeconds,
      previous,
      timestamps,
      limitBytes,
    ),
  )
}

/** Stops a log stream. */
export function stopLogStream(streamId: string): Promise<void> {
  return call(() => bindStopLogStream(streamId))
}

// --- Events from the backend ------------------------------------------------

/** Subscribes to successful cluster connections. Returns an unsubscribe fn. */
export function onClusterConnected(handler: (event: ClusterConnectedEvent) => void): Unsubscribe {
  return EventsOn('cluster:connected', (event: ClusterConnectedEvent) => handler(event))
}

/** Subscribes to failed connection attempts. Returns an unsubscribe fn. */
export function onClusterUnreachable(
  handler: (event: ClusterUnreachableEvent) => void,
): Unsubscribe {
  return EventsOn('cluster:unreachable', (event: ClusterUnreachableEvent) => handler(event))
}

/** Subscribes to a file copy's byte count, throttled by the backend. */
export function onFileCopyProgress(
  handler: (event: FileCopyProgressEvent) => void,
): Unsubscribe {
  return EventsOn('filecopy:progress', (event: FileCopyProgressEvent) => handler(event))
}

/** Subscribes to file copies ending, however they end. */
export function onFileCopyDone(handler: (event: FileCopyDoneEvent) => void): Unsubscribe {
  return EventsOn('filecopy:done', (event: FileCopyDoneEvent) => handler(event))
}
