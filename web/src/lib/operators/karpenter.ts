/**
 * What Karpenter's NodePool and NodeClaim say about themselves, in
 * Karpenter's own words.
 *
 * THE STRAGGLER OF ITEM 20. Every other operator the gap list named shipped in
 * wave 5; this one did not, and it is the one whose absence is most visible on
 * a cluster that runs it — Karpenter's whole job is that nodes appear and
 * disappear without anybody asking, so "why did that node go away" is the
 * question its objects exist to answer and the generic table cannot.
 *
 * QUOTATION, NOT VERDICT, as everything in this directory is. The disruption
 * policy, the requirements and the limits are lifted out of the manifest the
 * drawer already fetched. Karpenter's own conditions are shown with its
 * status, reason and message; this file never decides what `Drifted=True`
 * means, never lists the nodes a pool created, and never asks a cloud provider
 * anything.
 *
 * WHAT IS DELIBERATELY NOT CLAIMED: `EC2NodeClass` and the other providers'
 * node classes (`karpenter.k8s.aws`, `karpenter.azure.com`). They are provider
 * configuration — AMI selectors, subnets, security groups — with a different
 * shape per cloud and no status worth a panel, and a panel that claimed the
 * kind and then rendered a column of empty rows is worse than the server's own
 * table. A NodePool NAMES its node class, which is the part an operator
 * follows.
 *
 * Field names follow karpenter.sh/v1:
 * https://karpenter.sh/docs/concepts/nodepools/ and .../nodeclaims/
 */

import { conditionOf } from './panel'
import type { OperatorCondition } from './panel'

/** One scheduling requirement, as Karpenter wrote it. */
export interface KarpenterRequirement {
  key: string
  /** In, NotIn, Exists, DoesNotExist, Gt, Lt — verbatim. */
  operator: string
  values: string[]
  /** minValues, when the pool insists on a spread. Null when unset. */
  minValues: number | null
}

/** A taint, in the shape both kinds carry. */
export interface KarpenterTaint {
  key: string
  value: string
  effect: string
}

/** What a pool or claim points at for provider configuration. */
export interface KarpenterNodeClassRef {
  group: string
  kind: string
  name: string
}

/** A disruption budget: how much churn Karpenter may cause at once. */
export interface KarpenterBudget {
  /** A count or a percentage, verbatim — "10%" and "3" are both valid. */
  nodes: string
  /** An RFC 5545 schedule, empty when the budget is unconditional. */
  schedule: string
  /** How long the window lasts, empty when unset. */
  duration: string
  /** Which disruption reasons it bounds; empty means all of them. */
  reasons: string[]
}

export interface KarpenterNodePool {
  /** status.conditions[type=Ready] — the pool's own summary of itself. */
  ready: OperatorCondition | null
  /** NodeClassReady: whether the provider configuration it names resolved. */
  nodeClassReady: OperatorCondition | null
  /** ValidationSucceeded: whether the template it would create is admissible. */
  validated: OperatorCondition | null

  nodeClassRef: KarpenterNodeClassRef | null
  requirements: KarpenterRequirement[]
  taints: KarpenterTaint[]
  startupTaints: KarpenterTaint[]

  /**
   * spec.disruption.consolidationPolicy — "WhenEmpty" or
   * "WhenEmptyOrUnderutilized", verbatim. This is the single field that
   * decides whether Karpenter will move a running workload to save money, and
   * it is the first thing anybody wants to know when a node vanished.
   */
  consolidationPolicy: string
  /** spec.disruption.consolidateAfter, e.g. "30s" or "Never". Empty when unset. */
  consolidateAfter: string
  budgets: KarpenterBudget[]

  /** spec.template.spec.expireAfter — when a node is replaced regardless. */
  expireAfter: string
  /** spec.template.spec.terminationGracePeriod, empty when unset. */
  terminationGracePeriod: string

  /** spec.limits, as written: resource name to quantity. */
  limits: Array<{ resource: string; quantity: string }>
  /** status.resources — what the pool's nodes currently add up to. */
  allocated: Array<{ resource: string; quantity: string }>

  /** spec.weight, null when unset. Higher wins when several pools fit. */
  weight: number | null
}

export interface KarpenterNodeClaim {
  ready: OperatorCondition | null
  launched: OperatorCondition | null
  registered: OperatorCondition | null
  initialized: OperatorCondition | null
  /**
   * Drifted and Expired: the two conditions that explain a node being
   * replaced, and the reason this panel is worth having. Null when the
   * condition is absent, which is the ordinary state.
   */
  drifted: OperatorCondition | null
  expired: OperatorCondition | null

  /** status.nodeName — the Node object, once the claim has registered. */
  nodeName: string
  /** status.providerID, which names the instance at the cloud provider. */
  providerID: string
  /** status.imageID — the AMI or image the instance booted. */
  imageID: string

  nodeClassRef: KarpenterNodeClassRef | null
  requirements: KarpenterRequirement[]
  taints: KarpenterTaint[]

  /** status.capacity and status.allocatable, as the claim recorded them. */
  capacity: Array<{ resource: string; quantity: string }>
  allocatable: Array<{ resource: string; quantity: string }>

  /** spec.expireAfter, empty when unset. */
  expireAfter: string
  /** status.lastPodEventTime — when a pod last started or stopped here. */
  lastPodEvent: string
}

interface RawRequirement {
  key?: string
  operator?: string
  values?: unknown
  minValues?: number
}

interface RawTaint {
  key?: string
  value?: string
  effect?: string
}

interface RawNodeClassRef {
  group?: string
  kind?: string
  name?: string
}

/** A resource map as Kubernetes writes it, sorted so the panel does not shuffle. */
function quantities(source: unknown): Array<{ resource: string; quantity: string }> {
  if (!source || typeof source !== 'object') return []
  const entries = source as Record<string, unknown>
  return Object.keys(entries)
    .sort()
    .map((resource) => ({ resource, quantity: String(entries[resource] ?? '') }))
}

function requirements(source: unknown): KarpenterRequirement[] {
  if (!Array.isArray(source)) return []
  return source.map((raw) => {
    const item = (raw ?? {}) as RawRequirement
    return {
      key: item.key ?? '',
      operator: item.operator ?? '',
      values: Array.isArray(item.values) ? item.values.map((value) => String(value)) : [],
      minValues: typeof item.minValues === 'number' ? item.minValues : null,
    }
  })
}

function taints(source: unknown): KarpenterTaint[] {
  if (!Array.isArray(source)) return []
  return source.map((raw) => {
    const item = (raw ?? {}) as RawTaint
    return { key: item.key ?? '', value: item.value ?? '', effect: item.effect ?? '' }
  })
}

function nodeClassRef(source: unknown): KarpenterNodeClassRef | null {
  if (!source || typeof source !== 'object') return null
  const ref = source as RawNodeClassRef
  if (!ref.name) return null
  return { group: ref.group ?? '', kind: ref.kind ?? '', name: ref.name }
}

/**
 * Reads a NodePool, or null when there is no manifest.
 *
 * A pool Karpenter has not reached yet comes back with its spec and empty
 * conditions rather than null: what somebody wrote is worth showing before the
 * controller has agreed to it.
 */
export function karpenterNodePool(manifest: unknown): KarpenterNodePool | null {
  if (!manifest || typeof manifest !== 'object') return null
  const { spec = {}, status = {} } = manifest as {
    spec?: Record<string, unknown>
    status?: Record<string, unknown>
  }

  const template = (spec.template ?? {}) as Record<string, unknown>
  const templateSpec = (template.spec ?? {}) as Record<string, unknown>
  const disruption = (spec.disruption ?? {}) as Record<string, unknown>

  return {
    ready: conditionOf(status.conditions, 'Ready'),
    nodeClassReady: conditionOf(status.conditions, 'NodeClassReady'),
    validated: conditionOf(status.conditions, 'ValidationSucceeded'),

    nodeClassRef: nodeClassRef(templateSpec.nodeClassRef),
    requirements: requirements(templateSpec.requirements),
    taints: taints(templateSpec.taints),
    startupTaints: taints(templateSpec.startupTaints),

    consolidationPolicy: String(disruption.consolidationPolicy ?? ''),
    consolidateAfter: String(disruption.consolidateAfter ?? ''),
    budgets: Array.isArray(disruption.budgets)
      ? disruption.budgets.map((raw) => {
          const budget = (raw ?? {}) as Record<string, unknown>
          return {
            nodes: String(budget.nodes ?? ''),
            schedule: String(budget.schedule ?? ''),
            duration: String(budget.duration ?? ''),
            reasons: Array.isArray(budget.reasons)
              ? budget.reasons.map((reason) => String(reason))
              : [],
          }
        })
      : [],

    expireAfter: String(templateSpec.expireAfter ?? ''),
    terminationGracePeriod: String(templateSpec.terminationGracePeriod ?? ''),

    limits: quantities(spec.limits),
    allocated: quantities(status.resources),

    weight: typeof spec.weight === 'number' ? spec.weight : null,
  }
}

/** Reads a NodeClaim, or null when there is no manifest. */
export function karpenterNodeClaim(manifest: unknown): KarpenterNodeClaim | null {
  if (!manifest || typeof manifest !== 'object') return null
  const { spec = {}, status = {} } = manifest as {
    spec?: Record<string, unknown>
    status?: Record<string, unknown>
  }

  return {
    ready: conditionOf(status.conditions, 'Ready'),
    launched: conditionOf(status.conditions, 'Launched'),
    registered: conditionOf(status.conditions, 'Registered'),
    initialized: conditionOf(status.conditions, 'Initialized'),
    drifted: conditionOf(status.conditions, 'Drifted'),
    expired: conditionOf(status.conditions, 'Expired'),

    nodeName: String(status.nodeName ?? ''),
    providerID: String(status.providerID ?? ''),
    imageID: String(status.imageID ?? ''),

    nodeClassRef: nodeClassRef(spec.nodeClassRef),
    requirements: requirements(spec.requirements),
    taints: taints(spec.taints),

    capacity: quantities(status.capacity),
    allocatable: quantities(status.allocatable),

    expireAfter: String(spec.expireAfter ?? ''),
    lastPodEvent: String(status.lastPodEventTime ?? ''),
  }
}
