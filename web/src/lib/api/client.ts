/**
 * The single seam between the K8Sense UI and the Go backend.
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
  Connect as bindConnect,
  Connections as bindConnections,
  Disconnect as bindDisconnect,
  ListClusters as bindListClusters,
  ListNamespaces as bindListNamespaces,
  ListNodes as bindListNodes,
} from '$lib/wailsjs/go/wails/ClusterAPI'
import {
  GetManifest as bindGetManifest,
  ListEvents as bindListEvents,
  ListEventsForResource as bindListEventsForResource,
  ListKinds as bindListKinds,
  ListTable as bindListTable,
} from '$lib/wailsjs/go/wails/BrowseAPI'
import {
  ListPods as bindListPods,
  ListWorkloads as bindListWorkloads,
  ListPodsForWorkload as bindListPodsForWorkload,
} from '$lib/wailsjs/go/wails/WorkloadAPI'
import {
  ScaleWorkload as bindScaleWorkload,
  UpdateResource as bindUpdateResource,
  DeleteResource as bindDeleteResource,
  RestartRollout as bindRestartRollout,
  StreamLogs as bindStreamLogs,
  StopLogStream as bindStopLogStream,
} from '$lib/wailsjs/go/wails/ManagementAPI'
import { Info as bindInfo, OpenURL as bindOpenURL } from '$lib/wailsjs/go/wails/SystemAPI'
import { EventsOn } from '$lib/wailsjs/runtime/runtime'
import type { wails } from '$lib/wailsjs/go/models'
import { toApiError } from './errors'

/** A cluster described by the local kubeconfig. */
export type Cluster = wails.Cluster
/** A namespace in a connected cluster. */
export type Namespace = wails.Namespace
/** A pod, with derived status and measured usage. */
export type Pod = wails.Pod
/** One container within a pod. */
export type Container = wails.Container
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

/** Returns one object as YAML, for the detail view. */
export function getManifest(
  clusterId: string,
  kindId: string,
  namespace: string,
  name: string,
): Promise<string> {
  return call(() => bindGetManifest(clusterId, kindId, namespace, name))
}

// --- System -----------------------------------------------------------------

/** Returns the running application's name, version and platform. */
export function appInfo(): Promise<AppInfo> {
  return call(() => bindInfo())
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
