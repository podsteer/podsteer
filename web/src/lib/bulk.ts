/**
 * Bulk actions: what a selection of one kind can be put through at once, and
 * the shape each row is handed to the backend in.
 *
 * WHICH BUTTONS TO SHOW is decided here; WHAT HAPPENS TO EACH OBJECT is not.
 * `domain.PlanBulk` (app/domain/bulk.go) decides per object, from facts, and
 * the review dialog shows its verdicts verbatim — so a Job selected alongside
 * three Deployments is offered Restart (the list is one kind, and that kind
 * restarts) and told in the review why it sits out. This table only keeps a
 * Scale button off the Pods page, where no row could ever take it.
 */

import type { Pod, Workload, Node, TableRow, ResourceKind } from './api/client'
import { cordon, delMany, resourceArgForKind, rolloutRestartMany, scaleMany } from './kubectl'

export type BulkActionId = 'delete' | 'restart' | 'scale' | 'cordon' | 'uncordon'

/** How an action reads on a button, in a heading, and in a result. */
export interface BulkActionCopy {
  /** The button and the dialog heading: "Restart". */
  label: string
  /** What a done result says: "restarted". */
  done: string
  /** Whether the action removes something — what marks the button as the
      dangerous one, and what earns a type-the-name gate on production. */
  destructive: boolean
}

export const BULK_ACTIONS: Record<BulkActionId, BulkActionCopy> = {
  delete: { label: 'Delete', done: 'deleted', destructive: true },
  restart: { label: 'Restart', done: 'restarted', destructive: false },
  scale: { label: 'Scale', done: 'scaled', destructive: false },
  cordon: { label: 'Cordon', done: 'cordoned', destructive: false },
  uncordon: { label: 'Uncordon', done: 'uncordoned', destructive: false },
}

/**
 * The actions a list of `kind` offers, in toolbar order.
 *
 * Mirrors the kind rules in `domain/bulk.go`'s `bulkUnsupported`, at the
 * granularity of a list rather than an object: every kind can be deleted;
 * the three controllers with a rollout restart; the three with a replica
 * count scale; nodes cordon. Anything else — a ConfigMap, a CRD — offers
 * delete alone, which is the one verb the generic table's rows support.
 */
export function bulkActionsFor(kind: string): BulkActionId[] {
  switch (kind) {
    case 'Deployment':
    case 'StatefulSet':
      return ['restart', 'scale', 'delete']
    case 'DaemonSet':
      return ['restart', 'delete']
    case 'ReplicaSet':
      return ['scale', 'delete']
    case 'Node':
      return ['cordon', 'uncordon', 'delete']
    default:
      return ['delete']
  }
}

/**
 * One selected row, as the backend's plan needs it. Mirrors
 * `wails.BulkItemDTO` field for field; kept as its own interface so the views
 * can build one without importing a generated class.
 */
export interface BulkItem {
  group: string
  version: string
  kind: string
  namespace: string
  name: string
  controllerKind: string
  controllerName: string
  replicas: number
  unschedulable: boolean
}

/**
 * Splits a row's "Controlled By" — `ownerLabel` in app/adapters/wails, which
 * renders the controlling ownerReference as "Kind/name" — back into its two
 * parts. A quotation of a field already on screen, never a lookup: the kind
 * is a Kubernetes identifier and can never contain a slash, so the first one
 * is the separator whatever the name holds. Empty in, empty out.
 */
export function controllerOf(controlledBy: string): { kind: string; name: string } {
  const slash = controlledBy.indexOf('/')
  if (slash === -1) return { kind: '', name: '' }
  return { kind: controlledBy.slice(0, slash), name: controlledBy.slice(slash + 1) }
}

/** The selection key of a row: "namespace/name", or the bare name for a
    cluster-scoped object, so two namespaces' identically named pods stay
    two rows. */
export function rowKey(namespace: string, name: string): string {
  return namespace ? `${namespace}/${name}` : name
}

/**
 * The kubectl equivalent of a bulk action over `targets` — the objects the
 * plan will actually act on, not the whole selection, so the hint matches
 * what is about to happen rather than what was ticked. See $lib/kubectl for
 * how each verb is composed and why a selection spanning namespaces is one
 * line per namespace.
 */
export function bulkCommand(
  ctx: string,
  action: BulkActionId,
  kind: ResourceKind,
  targets: { name: string; namespace: string }[],
  replicas: number,
): string {
  const scoped = targets.map((target) => ({
    name: target.name,
    ns: kind.namespaced && target.namespace ? target.namespace : undefined,
  }))
  switch (action) {
    case 'delete':
      return delMany(ctx, resourceArgForKind(kind), scoped)
    case 'restart':
      return rolloutRestartMany(ctx, kind.kind, scoped)
    case 'scale':
      return scaleMany(ctx, kind.kind, scoped, replicas)
    case 'cordon':
      return cordon(
        ctx,
        targets.map((target) => target.name),
        true,
      )
    case 'uncordon':
      return cordon(
        ctx,
        targets.map((target) => target.name),
        false,
      )
  }
}

/** A kind's API coordinates, as every item carries them. */
function coordinates(kind: ResourceKind): Pick<BulkItem, 'group' | 'version' | 'kind'> {
  return { group: kind.group, version: kind.version, kind: kind.kind }
}

export function podItem(kind: ResourceKind, pod: Pod): BulkItem {
  const controller = controllerOf(pod.controlledBy)
  return {
    ...coordinates(kind),
    namespace: pod.namespace,
    name: pod.name,
    controllerKind: controller.kind,
    controllerName: controller.name,
    replicas: 0,
    unschedulable: false,
  }
}

export function workloadItem(kind: ResourceKind, workload: Workload): BulkItem {
  const controller = controllerOf(workload.controlledBy)
  return {
    ...coordinates(kind),
    namespace: workload.namespace,
    name: workload.name,
    controllerKind: controller.kind,
    controllerName: controller.name,
    replicas: workload.desired,
    unschedulable: false,
  }
}

export function nodeItem(kind: ResourceKind, node: Node): BulkItem {
  return {
    ...coordinates(kind),
    namespace: '',
    name: node.name,
    controllerKind: '',
    controllerName: '',
    replicas: 0,
    unschedulable: node.unschedulable,
  }
}

/**
 * A generic table row carries no owner, count or cordon fact — the server's
 * printer prints columns, not ownerReferences — so its item carries none,
 * and the plan says nothing it was not told.
 */
export function tableRowItem(kind: ResourceKind, row: TableRow): BulkItem {
  return {
    ...coordinates(kind),
    namespace: kind.namespaced ? row.namespace : '',
    name: row.name,
    controllerKind: '',
    controllerName: '',
    replicas: 0,
    unschedulable: false,
  }
}
