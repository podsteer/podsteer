/**
 * Which GitOps controller's detail panel an open object gets, if any.
 *
 * The drawer already picks purpose-built sections by the opened object's
 * Kind — "Ingress", "CronJob". A Kind alone is not enough here: "Application"
 * is also a kind in app.k8s.io and in core.oam.dev, and "Kustomization" is the
 * name of kustomize's own config file kind (kustomize.config.k8s.io). Both
 * halves of the API coordinate have to agree before a panel that reads
 * `status.resources` is shown, or it would open on an object that has no such
 * field and render three empty sections.
 *
 * WHY MEMBERSHIP IS QUOTED FROM THE CONTROLLER AND NEVER INFERRED. A
 * label-based "applications" view was considered and rejected (decision of
 * 2026-09-02): a label is weaker than a selector, overlapping labels
 * double-count, an unlabelled workload vanishes, and every line it drew
 * would be a relationship Kubernetes does not have. The controller's own
 * status — Argo CD's `status.resources`, Flux's `status.inventory` — is the
 * membership the controller itself acts on, it costs the one GET the drawer
 * already makes, and it reads no Secret. This is the top-down complement of
 * `$lib/gitops`, which answers the bottom-up question "who manages this".
 */

import { ARGO_GROUP } from './argo'
import { FLUX_HELM_GROUP, FLUX_KUSTOMIZE_GROUP } from './flux'

/** The panels this module can select. */
export type GitOpsPanel = 'argo-application' | 'flux-kustomization' | 'flux-helmrelease'

/**
 * Selects a panel from the opened object's API group and Kind, or null.
 *
 * Null is the ordinary answer for every other kind, and for a cluster where
 * neither controller is installed nothing here is ever reached: the kinds
 * only exist in the navigator when discovery found their groups.
 */
export function gitOpsPanelFor(
  group: string | undefined,
  kind: string | undefined,
): GitOpsPanel | null {
  if (group === ARGO_GROUP && kind === 'Application') return 'argo-application'
  if (group === FLUX_KUSTOMIZE_GROUP && kind === 'Kustomization') return 'flux-kustomization'
  if (group === FLUX_HELM_GROUP && kind === 'HelmRelease') return 'flux-helmrelease'
  return null
}

/**
 * One object a controller says it manages, as the controller recorded it.
 *
 * The same shape for both controllers so one list renders either; the
 * per-member state is Argo CD's alone, because Flux's inventory records
 * identity and nothing else.
 */
export interface GitOpsMember {
  /** The API group, empty for the core group. */
  group: string
  /** The API version, when the controller recorded one. */
  version: string
  /** The Kubernetes Kind, VERBATIM — it is what the drawer resolves a click by. */
  kind: string
  /** Empty for a cluster-scoped object. */
  namespace: string
  name: string
  /** Argo CD's `status` for the member: Synced or OutOfSync. Empty for Flux. */
  sync: string
  /** Argo CD's `health.status` for the member. Empty for Flux. */
  health: string
  /** Argo CD's health message, when it wrote one. */
  healthMessage: string
  /**
   * Argo CD's own flag for a member that is live in the cluster and gone from
   * Git — what a sync with pruning would delete.
   */
  requiresPruning: boolean
}

/**
 * Seconds elapsed since an RFC 3339 timestamp, for `formatAge`.
 *
 * NaN for anything that does not parse, which `formatAge` renders as an em
 * dash — the same answer as for an absent field, and the right one: a
 * timestamp this cannot read is not an age of zero.
 */
export function secondsSince(timestamp: string, now: number = Date.now()): number {
  if (!timestamp) return Number.NaN
  const then = new Date(timestamp).getTime()
  if (!Number.isFinite(then)) return Number.NaN
  return Math.max(0, Math.floor((now - then) / 1000))
}
