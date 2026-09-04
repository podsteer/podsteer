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
} from '$lib/wailsjs/go/wails/ManagementAPI'
import { GetOverview as bindGetOverview } from '$lib/wailsjs/go/wails/OverviewAPI'
import {
  GetSettings as bindGetHistorySettings,
  GetSeries as bindGetSeries,
  SetRetention as bindSetRetention,
  SetSamplingInterval as bindSetSamplingInterval,
} from '$lib/wailsjs/go/wails/HistoryAPI'
import {
  Credits as bindCredits,
  Info as bindInfo,
  LicenceText as bindLicenceText,
  OpenURL as bindOpenURL,
  SaveTextFile as bindSaveTextFile,
} from '$lib/wailsjs/go/wails/SystemAPI'
import { EventsOn } from '$lib/wailsjs/runtime/runtime'
import type { wails } from '$lib/wailsjs/go/models'
import { toApiError } from './errors'

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
/** A cluster node. */
export type Node = wails.Node
/** A pod-managing controller. */
export type Workload = wails.Workload

export type Consumption = wails.Consumption

export type ApplicationInventory = wails.ApplicationInventory
export type Application = wails.Application
/** A Kubernetes Event. */
export type K8sEvent = wails.Event
/** A browsable kind, as shown in the navigator. */
export type ResourceKind = wails.ResourceKind
/** A generically browsed kind, with server-printed columns. */
export type ResourceTable = wails.ResourceTable

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
/** One thing worth knowing about an inspected certificate chain. */
export type CertificateInsight = wails.CertificateInsight
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
 */
export function listNamespaceSummaries(clusterId: string): Promise<NamespaceSummary[]> {
  return call(() => bindListNamespaceSummaries(clusterId))
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

/** Lists the nodes of a connected cluster, with usage where available. */
export function listNodes(clusterId: string): Promise<Node[]> {
  return call(() => bindListNodes(clusterId))
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

/** Lists pods. An empty namespace means every namespace. */
export function listPods(clusterId: string, namespace: string): Promise<Pod[]> {
  return call(() => bindListPods(clusterId, namespace))
}

/** Lists controllers of one kind, named as "Deployment", "StatefulSet", etc. */
export function listWorkloads(
  clusterId: string,
  kind: string,
  namespace: string,
): Promise<Workload[]> {
  return call(() => bindListWorkloads(clusterId, kind, namespace))
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

// --- Events -----------------------------------------------------------------

/** Lists events, warnings first and most recent first. */
export function listEvents(clusterId: string, namespace: string): Promise<K8sEvent[]> {
  return call(() => bindListEvents(clusterId, namespace))
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

/** Lists any kind as a table with the columns the API server prints. */
export function listTable(
  clusterId: string,
  kindId: string,
  namespace: string,
): Promise<ResourceTable> {
  return call(() => bindListTable(clusterId, kindId, namespace))
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

// --- Management -------------------------------------------------------------

/** Scales a workload to the specified number of replicas. */
export function scaleWorkload(
  clusterId: string,
  kind: string,
  namespace: string,
  name: string,
  replicas: number,
): Promise<void> {
  return call(() => bindScaleWorkload(clusterId, kind, namespace, name, replicas))
}

/**
 * Applies a manifest of any kind to the cluster — the generic path, not a
 * fixed set of typed kinds. A manifest carrying `metadata.resourceVersion`
 * is sent as a PUT the server optimistic-locks against; a stale one comes
 * back as an ApiError with code `conflict` (see api/errors.ts). One without
 * it is created, replacing any existing object of the same name.
 */
export function updateResource(clusterId: string, manifest: string): Promise<ApplyOutcome> {
  return call(() => bindUpdateResource(clusterId, manifest))
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
  return call(() => bindDeleteResource(clusterId, kindGroup, kindVersion, kindKind, namespace, name))
}

/** Restarts a rollout (deployment/statefulset/daemonset). */
export function restartRollout(
  clusterId: string,
  kind: string,
  namespace: string,
  name: string,
): Promise<void> {
  return call(() => bindRestartRollout(clusterId, kind, namespace, name))
}

/**
 * Creates a Job from a CronJob's template right now, outside its schedule.
 * Resolves to the created Job's name.
 */
export function triggerCronJob(clusterId: string, namespace: string, name: string): Promise<string> {
  return call(() => bindTriggerCronJob(clusterId, namespace, name))
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
  return call(() => bindSetSecretKey(clusterId, namespace, name, key, value))
}

/** Writes one key of one ConfigMap. */
export function setConfigMapKey(
  clusterId: string,
  namespace: string,
  name: string,
  key: string,
  value: string,
): Promise<void> {
  return call(() => bindSetConfigMapKey(clusterId, namespace, name, key, value))
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
  return call(() => bindSetImage(clusterId, kind, namespace, name, container, image, initContainer))
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
  return call(() => bindRollbackWorkload(clusterId, kind, namespace, name, toRevision, dryRun))
}

/** Suspends or resumes a CronJob's schedule, or a Job's pods. */
export function suspendWorkload(
  clusterId: string,
  kind: string,
  namespace: string,
  name: string,
  suspend: boolean,
): Promise<void> {
  return call(() => bindSuspendWorkload(clusterId, kind, namespace, name, suspend))
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
  return call(() => bindCordonNode(clusterId, name, cordon))
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
  return call(() => bindEvictPod(clusterId, namespace, name, gracePeriodSeconds))
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
  return call(() =>
    bindDrainNode(clusterId, name, force, deleteEmptyDirData, gracePeriodSeconds, timeoutSeconds),
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
