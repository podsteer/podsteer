/**
 * What a Workload or PodGroup declares: Kubernetes' gang scheduling.
 *
 * Workload-aware scheduling went beta in Kubernetes 1.37 (scheduling.k8s.io/v1beta1,
 * behind the GenericWorkload feature gate). A Workload is the template — a list
 * of named pod group templates, each with a scheduling policy — and a PodGroup
 * is the runtime instance a workload controller stamps out from one. A gang
 * policy says the group is scheduled all-or-nothing and names the minimum
 * number of pods (`minCount`) that must be placeable together.
 *
 * Field names follow the v1beta1 reference
 * (kubernetes.io/docs/reference/kubernetes-api/scheduling/workload-v1beta1 and
 * pod-group-v1beta1). Only what those pages state is read: the PodGroup's
 * `spec.workloadRef` is `{name, namespace}` and says WHICH Workload, and this
 * module does not invent a template-name field the reference does not list.
 *
 * QUOTATION, NOT VERDICT, like the rest of this directory. A condition's
 * status and reason are the scheduler's own words; "stuck" is not decided here.
 *
 * THE LINK FROM A POD IS THE API'S OWN: `spec.schedulingGroup.podGroupName`
 * names the PodGroup in the pod's namespace. `podGroupNameOf` reads exactly
 * that field and nothing is inferred from labels or owners.
 */

import { conditionsOf, namedCondition, numberOr, type StandardCondition } from './panel'

/** The condition the scheduler sets when a PodGroup's scheduling requirement is met. */
export const INITIALLY_SCHEDULED = 'PodGroupInitiallyScheduled'

/** The condition set when a PodGroup is about to be terminated by a disruption. */
export const DISRUPTION_TARGET = 'DisruptionTarget'

/** A scheduling policy: which one (`gang`, `basic`, or a future key), and gang's minimum. */
export interface SchedulingPolicyView {
  /** The one key set under `schedulingPolicy`, verbatim; empty when none is. */
  policy: string
  /** `gang.minCount`; null when absent or not a gang. */
  minCount: number | null
}

/** One template of a Workload. */
export interface PodGroupTemplateView extends SchedulingPolicyView {
  name: string
  priorityClassName: string
  /** The one key set under `disruptionMode` (`single` or `all`); empty when unset. */
  disruptionMode: string
}

export interface WorkloadView {
  /** `spec.controllerRef`; the API records a group and kind, not a namespace. */
  controller: { apiGroup: string; kind: string; name: string } | null
  templates: PodGroupTemplateView[]
  /** How many `compositePodGroupTemplates` are declared (alpha); they are counted, not rendered. */
  compositeTemplates: number
}

export interface PodGroupView extends SchedulingPolicyView {
  priorityClassName: string
  priority: number | null
  preemptionPolicy: string
  disruptionMode: string
  /** `spec.workloadRef`; namespace falls back to the PodGroup's own. */
  workload: { name: string; namespace: string } | null
  parentCompositePodGroup: string
  resourceClaims: string[]
  scheduled: StandardCondition | null
  disruptionTarget: StandardCondition | null
  /** Every condition, including types this module has never heard of. */
  conditions: StandardCondition[]
}

interface RawPolicy {
  [key: string]: { minCount?: number } | undefined
}

function policyOf(raw: unknown): SchedulingPolicyView {
  if (!raw || typeof raw !== 'object') return { policy: '', minCount: null }
  const policy = raw as RawPolicy
  const key = Object.keys(policy).find((candidate) => policy[candidate] !== undefined) ?? ''
  const gang = key === 'gang' ? policy.gang : undefined
  return { policy: key, minCount: numberOr(gang?.minCount) }
}

/** The one key of a union-shaped object (`disruptionMode`), or empty. */
function unionKey(raw: unknown): string {
  if (!raw || typeof raw !== 'object') return ''
  return Object.keys(raw as object)[0] ?? ''
}

function text(value: unknown): string {
  return typeof value === 'string' ? value : ''
}

/** Reads a Workload, or null when there is no manifest at all. */
export function workload(manifest: unknown): WorkloadView | null {
  if (!manifest || typeof manifest !== 'object') return null
  const { spec = {} } = manifest as {
    spec?: {
      controllerRef?: { apiGroup?: string; kind?: string; name?: string }
      podGroupTemplates?: {
        name?: string
        schedulingPolicy?: unknown
        priorityClassName?: string
        disruptionMode?: unknown
      }[]
      compositePodGroupTemplates?: unknown[]
    }
  }

  const ref = spec.controllerRef
  return {
    controller:
      ref && typeof ref === 'object'
        ? { apiGroup: text(ref.apiGroup), kind: text(ref.kind), name: text(ref.name) }
        : null,
    templates: (Array.isArray(spec.podGroupTemplates) ? spec.podGroupTemplates : [])
      .filter((template) => template && typeof template === 'object')
      .map((template) => ({
        name: text(template.name),
        ...policyOf(template.schedulingPolicy),
        priorityClassName: text(template.priorityClassName),
        disruptionMode: unionKey(template.disruptionMode),
      })),
    compositeTemplates: Array.isArray(spec.compositePodGroupTemplates)
      ? spec.compositePodGroupTemplates.length
      : 0,
  }
}

/** Reads a PodGroup, or null when there is no manifest at all. */
export function podGroup(manifest: unknown): PodGroupView | null {
  if (!manifest || typeof manifest !== 'object') return null
  const { metadata = {}, spec = {}, status = {} } = manifest as {
    metadata?: { namespace?: string }
    spec?: {
      schedulingPolicy?: unknown
      priorityClassName?: string
      priority?: number
      preemptionPolicy?: string
      disruptionMode?: unknown
      workloadRef?: { name?: string; namespace?: string }
      parentCompositePodGroupName?: string
      resourceClaims?: { name?: string }[]
    }
    status?: { conditions?: unknown }
  }

  const conditions = conditionsOf(status.conditions)
  const ref = spec.workloadRef

  return {
    ...policyOf(spec.schedulingPolicy),
    priorityClassName: text(spec.priorityClassName),
    priority: numberOr(spec.priority),
    preemptionPolicy: text(spec.preemptionPolicy),
    disruptionMode: unionKey(spec.disruptionMode),
    workload:
      ref && typeof ref === 'object' && text(ref.name)
        ? { name: text(ref.name), namespace: text(ref.namespace) || text(metadata.namespace) }
        : null,
    parentCompositePodGroup: text(spec.parentCompositePodGroupName),
    resourceClaims: (Array.isArray(spec.resourceClaims) ? spec.resourceClaims : [])
      .map((claim) => text(claim?.name))
      .filter(Boolean),
    scheduled: namedCondition(conditions, INITIALLY_SCHEDULED),
    disruptionTarget: namedCondition(conditions, DISRUPTION_TARGET),
    conditions,
  }
}

/**
 * The PodGroup a pod names, or empty.
 *
 * `spec.schedulingGroup.podGroupName` — the one real link between the two
 * objects. The PodGroup is in the pod's own namespace.
 */
export function podGroupNameOf(pod: unknown): string {
  if (!pod || typeof pod !== 'object') return ''
  const { spec } = pod as { spec?: { schedulingGroup?: { podGroupName?: string } } }
  return text(spec?.schedulingGroup?.podGroupName)
}
