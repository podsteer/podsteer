/**
 * The single seam between the K8Sense UI and the Go backend.
 *
 * Nothing outside this module imports from `$lib/wailsjs` — those files are
 * regenerated on every build and their shape is dictated by the Go binder, so
 * keeping the rest of the app one step removed means a change over there
 * lands in one place. This module also normalises the two things the raw
 * bindings get wrong for our purposes: rejection values arrive as bare strings
 * (see errors.ts), and a Go pointer return is typed non-nullable even though
 * it is genuinely null at runtime.
 */

import {
  ActiveCluster as bindActiveCluster,
  Connect as bindConnect,
  ListClusters as bindListClusters,
  ListNamespaces as bindListNamespaces,
} from '$lib/wailsjs/go/wails/ClusterAPI'
import { ListPods as bindListPods } from '$lib/wailsjs/go/wails/WorkloadAPI'
import { EventsOn } from '$lib/wailsjs/runtime/runtime'
import type { wails } from '$lib/wailsjs/go/models'
import { toApiError } from './errors'

/** A cluster described by the local kubeconfig. */
export type Cluster = wails.Cluster
/** A namespace in the connected cluster. */
export type Namespace = wails.Namespace
/** A pod in the connected cluster. */
export type Pod = wails.Pod
/** One container within a pod. */
export type Container = wails.Container

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
 * Every exported function goes through here so that callers can rely on
 * `catch (err) { toApiError(err).code }` never seeing a raw string.
 */
async function call<T>(operation: () => Promise<T>): Promise<T> {
  try {
    return await operation()
  } catch (cause) {
    throw toApiError(cause)
  }
}

/** Lists every cluster in the local kubeconfig. Works before connecting. */
export function listClusters(): Promise<Cluster[]> {
  return call(() => bindListClusters())
}

/**
 * Connects to a cluster and makes it active.
 *
 * Resolves only once the API server has answered, so a resolved promise is
 * proof the cluster is reachable with the configured credentials.
 */
export function connect(clusterId: string): Promise<Cluster> {
  return call(() => bindConnect(clusterId))
}

/**
 * Returns the connected cluster, or null when there is none.
 *
 * The cast is deliberate: the Go method returns *Cluster and genuinely sends
 * null before anything is connected, but the Wails TypeScript generator has no
 * way to express a nullable return and declares it as Cluster.
 */
export function activeCluster(): Promise<Cluster | null> {
  return call(() => bindActiveCluster() as Promise<Cluster | null>)
}

/** Lists the namespaces of the connected cluster. */
export function listNamespaces(): Promise<Namespace[]> {
  return call(() => bindListNamespaces())
}

/**
 * Lists pods in the connected cluster.
 *
 * An empty namespace lists across all of them, mirroring
 * `kubectl get pods --all-namespaces`.
 */
export function listPods(namespace: string): Promise<Pod[]> {
  return call(() => bindListPods(namespace))
}

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
