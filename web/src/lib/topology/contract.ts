/**
 * The topology and traffic contract between the Go backend and the interface.
 *
 * Written by hand ahead of the backend so the two halves could be built at
 * the same time. The Go DTOs (`app/adapters/wails/dto_topology.go`,
 * `dto_traffic.go`) carry exactly these JSON names. Where a generated type
 * matches the contract it is re-exported from the bindings (PodSummary,
 * TrafficEdge, TrafficEndpoint); where it is WIDER — a union typed `string`,
 * a slice or map typed `| null` — the narrow type stays here and the api
 * modules convert (topology/api.ts `normaliseGraph`, trafficApi.ts). This
 * file stays the one place the interface imports these types from.
 */

import type { TopologyPodSummary, TrafficEdge, TrafficEndpoint } from '$bindings/models'

/** How healthy a box is. `neutral` means nothing was checked, not that it is fine. */
export type NodeState = 'ok' | 'warn' | 'bad' | 'neutral'

/** Every relationship the topology draws. Each is one Kubernetes really has. */
export type TopologyEdgeKind =
  | 'owns'
  | 'selects'
  | 'routes'
  | 'scales'
  | 'protects'
  | 'policy-selects'
  | 'attaches'
  | 'runs-as'

/**
 * Pods folded by the backend above the summary cap. The counts are complete.
 * Re-exported from the bindings: the generated type matches exactly.
 */
export type PodSummary = TopologyPodSummary

export interface TopologyNode {
  id: string
  /** The graph kind: pod, workload, replicaset, service, ingress, gateway, route, scaler, budget, policy, config, secret, claim, serviceaccount, object. */
  kind: string
  /** The Kubernetes Kind, verbatim, for navigation. */
  apiKind: string
  name: string
  namespace: string
  state: NodeState
  detail: string
  /** Sibling set for folding, as in the other map shapes. */
  group: string
  /** Labels of top-level objects only, for grouping by app or by label. */
  labels?: Record<string, string>
  /** Set only on a backend-folded pod set. */
  podSummary?: PodSummary
}

export interface TopologyEdge {
  from: string
  to: string
  kind: TopologyEdgeKind
  label: string
}

export interface TopologyGraph {
  nodes: TopologyNode[]
  edges: TopologyEdge[]
  /** Per Kubernetes Kind, COMPLETE even when pods are summarised. */
  counts: Record<string, number>
  unreadable: string[]
  bounded: string
  /** True when pods were folded in the backend because there were too many to draw. */
  summarised: boolean
  generatedAt: string
}

/** Emitted as `topology:changed` when something in a drawn scope changed. */
export interface TopologyChanged {
  clusterId: string
  namespaces: string[]
}

export type TrafficSourceName = 'istio' | 'linkerd' | 'beyla' | 'caretta' | 'hubble'
export type TrafficWindow = '5m' | '15m' | '1h'

export interface TrafficSourceStatus {
  source: TrafficSourceName
  available: boolean
  /** What was found, or what would be needed. */
  detail: string
}

export interface TrafficSources {
  /** The Prometheus the queries go to, as the metrics query feature names it. */
  backend: string
  sources: TrafficSourceStatus[]
  /** The metrics query backend status: enabled, not enabled, unreachable… */
  status: string
  message: string
}

/**
 * An endpoint and an edge of the traffic layer, re-exported from the
 * generated bindings because they match this contract exactly. The other
 * traffic types stay hand-written: the generated ones widen the source and
 * window unions to `string`, type `provenance` as SeriesProvenance and mark
 * slices `| null` (the backend never sends null for them).
 *
 * TrafficEdge carries `latencyBeyondBuckets`: true when a percentile fell in
 * the histogram's +Inf bucket. That percentile is still -1, and the flag is
 * what tells "slower than every bucket" from "not exposed".
 */
export type { TrafficEdge, TrafficEndpoint }

export interface TrafficLayer {
  source: TrafficSourceName
  window: TrafficWindow
  edges: TrafficEdge[]
  unmapped: TrafficEndpoint[]
  status: string
  message: string
  /** Which backend answered and when, as the metrics query feature reports it. */
  provenance: unknown
  /** The PromQL that was sent, shown to the operator. */
  expressions: string[]
}
