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
  ListNodes as bindListNodes,
  PreviewKubeconfig as bindPreviewKubeconfig,
  ReadKubeconfigFile as bindReadKubeconfigFile,
} from '$lib/wailsjs/go/wails/ClusterAPI'
import {
  GetManifest as bindGetManifest,
  RevealSecretKey as bindRevealSecretKey,
  ListEvents as bindListEvents,
  ListEventsForResource as bindListEventsForResource,
  ListKinds as bindListKinds,
  ListTable as bindListTable,
} from '$lib/wailsjs/go/wails/BrowseAPI'
import {
  ListPods as bindListPods,
  ListWorkloads as bindListWorkloads,
  ListPodsForWorkload as bindListPodsForWorkload,
  ListPodsOnNode as bindListPodsOnNode,
  PodGraph as bindPodGraph,
} from '$lib/wailsjs/go/wails/WorkloadAPI'
import {
  ScaleWorkload as bindScaleWorkload,
  UpdateResource as bindUpdateResource,
  DeleteResource as bindDeleteResource,
  RestartRollout as bindRestartRollout,
  StreamLogs as bindStreamLogs,
  StopLogStream as bindStopLogStream,
  StartPortForward as bindStartPortForward,
  StopPortForward as bindStopPortForward,
  ListPortForwards as bindListPortForwards,
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
/** A Kubernetes Event. */
export type K8sEvent = wails.Event
/** A browsable kind, as shown in the navigator. */
export type ResourceKind = wails.ResourceKind
/** A generically browsed kind, with server-printed columns. */
export type ResourceTable = wails.ResourceTable
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

/** Returns the open clusters, in the order they were opened. */
export function connections(): Promise<Cluster[]> {
  return call(() => bindConnections())
}

/** Lists the namespaces of a connected cluster. */
export function listNamespaces(clusterId: string): Promise<Namespace[]> {
  return call(() => bindListNamespaces(clusterId))
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

/** Lists the pods the scheduler has placed on one node, across namespaces. */
export function listPodsOnNode(clusterId: string, nodeName: string): Promise<Pod[]> {
  return call(() => bindListPodsOnNode(clusterId, nodeName))
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

/** Updates a resource with the provided YAML manifest. */
export function updateResource(clusterId: string, manifest: string): Promise<void> {
  return call(() => bindUpdateResource(clusterId, manifest))
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

/** Streams logs from a pod container. Returns a stream ID for stopping. */
export function streamLogs(
  clusterId: string,
  namespace: string,
  podName: string,
  containerName: string,
  follow: boolean,
  tailLines: number,
): Promise<string> {
  return call(() => bindStreamLogs(clusterId, namespace, podName, containerName, follow, tailLines))
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
