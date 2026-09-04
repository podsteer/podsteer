/**
 * What a Flux Kustomization or HelmRelease says about itself, in Flux's words.
 *
 * QUOTATION, NOT VERDICT — the same rule as argo.ts. The Ready condition is
 * Flux's own conclusion and is shown with Flux's own status and reason
 * (True/False/Unknown; ReconciliationSucceeded, BuildFailed, …); its colour
 * is asked of the Go domain through the same path every other condition
 * takes, so this file never decides what Ready=False means.
 *
 * The member list is `status.inventory.entries`, which is Flux's own record
 * of what it applied last — what it will prune, what it health-checks. It is
 * the membership the controller acts on, read from the one manifest the
 * drawer already fetched: no LIST of child kinds, no Secret.
 *
 * Field names follow the CRDs:
 *   Kustomization (kustomize.toolkit.fluxcd.io/v1)
 *     https://fluxcd.io/flux/components/kustomize/kustomizations/
 *   HelmRelease (helm.toolkit.fluxcd.io/v2)
 *     https://fluxcd.io/flux/components/helm/helmreleases/
 */

import type { GitOpsMember } from './panel'

/** The API groups of the two Flux kinds this reads. */
export const FLUX_KUSTOMIZE_GROUP = 'kustomize.toolkit.fluxcd.io'
export const FLUX_HELM_GROUP = 'helm.toolkit.fluxcd.io'

/** The Ready condition, as Flux wrote it. */
export interface FluxReady {
  /** "True", "False" or "Unknown". */
  status: string
  /** Flux's machine-readable reason: ReconciliationSucceeded, BuildFailed, … */
  reason: string
  message: string
  /**
   * When Ready last changed status. NOT the last reconcile: Flux does not
   * record one, and a condition's transition time moves only when its status
   * does — a Kustomization applying a new revision every ten minutes keeps
   * the same Ready=True transition time throughout.
   */
  since: string
}

/** A reference to the source a Flux object renders from. */
export interface FluxSourceRef {
  /** GitRepository, OCIRepository, Bucket, HelmRepository or HelmChart. */
  kind: string
  name: string
  /** Empty when the source is in the object's own namespace. */
  namespace: string
}

export interface FluxKustomization {
  /** Null when Flux has not yet written a Ready condition. */
  ready: FluxReady | null
  source: FluxSourceRef | null
  path: string
  interval: string
  prune: boolean
  suspended: boolean
  targetNamespace: string
  /** What is live, e.g. "main@sha1:…". */
  lastAppliedRevision: string
  /** What Flux last tried; differs from applied when the last attempt failed. */
  lastAttemptedRevision: string
  /** Set only when a reconcile was requested by hand (`flux reconcile`). */
  lastHandledReconcileAt: string
  /**
   * The inventory, or NULL when the status carries none — which is different
   * from an empty one. A Kustomization that has never applied has no
   * inventory; one that applied a directory of nothing has an empty one.
   */
  inventory: GitOpsMember[] | null
}

export interface FluxHelmRelease {
  ready: FluxReady | null
  /** The chart's source: `spec.chart.spec.sourceRef`, or `spec.chartRef` in v2. */
  source: FluxSourceRef | null
  /** The chart name, or for a chartRef the referenced object's kind/name. */
  chart: string
  /** The version constraint from the spec, e.g. ">=6.0.0". */
  version: string
  interval: string
  suspended: boolean
  releaseName: string
  targetNamespace: string
  /**
   * The chart version deployed. v2 records it in `status.history`, newest
   * first; the v2beta APIs wrote `status.lastAppliedRevision`, which is read
   * when present so an older controller's objects still say something.
   */
  lastAppliedRevision: string
  lastAttemptedRevision: string
  /** The latest release as Helm reported it: "deployed", "failed", … */
  releaseStatus: string
  /** The application version the deployed chart declares. */
  appVersion: string
  lastDeployed: string
  lastHandledReconcileAt: string
  /**
   * Always null today: helm-controller keeps no inventory in the HelmRelease
   * status, because the Helm release itself records the manifest. Read all
   * the same, so a controller that starts writing one is shown rather than
   * silently ignored.
   */
  inventory: GitOpsMember[] | null
}

interface Condition {
  type?: string
  status?: string
  reason?: string
  message?: string
  lastTransitionTime?: string
}

interface RawSourceRef {
  kind?: string
  name?: string
  namespace?: string
}

interface InventoryEntry {
  id?: string
  v?: string
}

interface KustomizationManifest {
  spec?: {
    interval?: string
    path?: string
    prune?: boolean
    suspend?: boolean
    targetNamespace?: string
    sourceRef?: RawSourceRef
  }
  status?: {
    conditions?: Condition[]
    lastAppliedRevision?: string
    lastAttemptedRevision?: string
    lastHandledReconcileAt?: string
    inventory?: { entries?: InventoryEntry[] }
  }
}

interface HelmReleaseManifest {
  spec?: {
    interval?: string
    suspend?: boolean
    releaseName?: string
    targetNamespace?: string
    chart?: { spec?: { chart?: string; version?: string; sourceRef?: RawSourceRef } }
    chartRef?: RawSourceRef
  }
  status?: {
    conditions?: Condition[]
    lastAppliedRevision?: string
    lastAttemptedRevision?: string
    lastHandledReconcileAt?: string
    history?: {
      chartVersion?: string
      appVersion?: string
      status?: string
      lastDeployed?: string
    }[]
    inventory?: { entries?: InventoryEntry[] }
  }
}

/** Reads a Kustomization, or null when there is no manifest at all. */
export function fluxKustomization(manifest: unknown): FluxKustomization | null {
  if (!manifest || typeof manifest !== 'object') return null
  const { spec = {}, status = {} } = manifest as KustomizationManifest

  return {
    ready: readyCondition(status.conditions),
    source: sourceRef(spec.sourceRef),
    path: spec.path ?? '',
    interval: spec.interval ?? '',
    prune: spec.prune ?? false,
    suspended: spec.suspend ?? false,
    targetNamespace: spec.targetNamespace ?? '',
    lastAppliedRevision: status.lastAppliedRevision ?? '',
    lastAttemptedRevision: status.lastAttemptedRevision ?? '',
    lastHandledReconcileAt: status.lastHandledReconcileAt ?? '',
    inventory: inventoryOf(status.inventory),
  }
}

/** Reads a HelmRelease, or null when there is no manifest at all. */
export function fluxHelmRelease(manifest: unknown): FluxHelmRelease | null {
  if (!manifest || typeof manifest !== 'object') return null
  const { spec = {}, status = {} } = manifest as HelmReleaseManifest

  const chart = spec.chart?.spec
  const chartRef = sourceRef(spec.chartRef)
  // `history` is newest first: helm-controller prepends each release and
  // truncates the tail, so the first entry is the one that is deployed.
  const latest = status.history?.[0]

  return {
    ready: readyCondition(status.conditions),
    source: chartRef ?? sourceRef(chart?.sourceRef),
    chart: chart?.chart ?? (chartRef ? `${chartRef.kind}/${chartRef.name}` : ''),
    version: chart?.version ?? '',
    interval: spec.interval ?? '',
    suspended: spec.suspend ?? false,
    releaseName: spec.releaseName ?? '',
    targetNamespace: spec.targetNamespace ?? '',
    lastAppliedRevision: status.lastAppliedRevision || latest?.chartVersion || '',
    lastAttemptedRevision: status.lastAttemptedRevision ?? '',
    releaseStatus: latest?.status ?? '',
    appVersion: latest?.appVersion ?? '',
    lastDeployed: latest?.lastDeployed ?? '',
    lastHandledReconcileAt: status.lastHandledReconcileAt ?? '',
    inventory: inventoryOf(status.inventory),
  }
}

function readyCondition(conditions: Condition[] | undefined): FluxReady | null {
  const ready = (conditions ?? []).find((condition) => condition.type === 'Ready')
  if (!ready) return null
  return {
    status: ready.status ?? '',
    reason: ready.reason ?? '',
    message: ready.message ?? '',
    since: ready.lastTransitionTime ?? '',
  }
}

function sourceRef(raw: RawSourceRef | undefined): FluxSourceRef | null {
  if (!raw?.kind || !raw.name) return null
  return { kind: raw.kind, name: raw.name, namespace: raw.namespace ?? '' }
}

/**
 * The inventory as members, null when the status has none, and an entry
 * this cannot parse is dropped rather than shown as a half-read object.
 */
function inventoryOf(inventory: { entries?: InventoryEntry[] } | undefined): GitOpsMember[] | null {
  if (!inventory) return null

  const members: GitOpsMember[] = []
  for (const entry of inventory.entries ?? []) {
    const parsed = parseInventoryId(entry.id ?? '')
    if (!parsed) continue
    members.push({
      ...parsed,
      version: entry.v ?? '',
      sync: '',
      health: '',
      healthMessage: '',
      requiresPruning: false,
    })
  }
  return members
}

/**
 * Splits an inventory entry's id into the object it names.
 *
 * THE FORMAT IS `<namespace>_<name>_<group>_<kind>`, documented on the
 * Kustomization's `status.inventory` in the Flux API reference
 * (https://fluxcd.io/flux/components/kustomize/kustomizations/#resource-inventory)
 * and produced by `ObjMetadata.String()` in `sigs.k8s.io/cli-utils/pkg/object`,
 * whose `ParseObjMetadata` this mirrors. Two details of that parser are
 * load-bearing and easy to get wrong with a plain split on "_":
 *
 *  - A CORE-GROUP KIND HAS AN EMPTY GROUP SEGMENT, so a Service in "default"
 *    is `default_web__Service` with two underscores in a row; and a
 *    cluster-scoped object has an empty namespace, so a Namespace is
 *    `_monitoring__Namespace`. Empty segments are meaningful, not junk.
 *  - A NAME MAY CONTAIN A COLON, which RBAC names routinely do
 *    (`system:controller`), and cli-utils transcodes it as `__` because a
 *    colon is not allowed in the ConfigMap key this id was designed to be.
 *    So the fields are found from the OUTSIDE IN — namespace up to the first
 *    separator, kind after the last, group after the second-to-last — and
 *    whatever is left in the middle is the name, with `__` turned back into
 *    the colon it stood for. Underscores themselves cannot appear in a
 *    Kubernetes name, which is what makes this unambiguous.
 */
export function parseInventoryId(
  id: string,
): { namespace: string; name: string; group: string; kind: string } | null {
  const first = id.indexOf('_')
  if (first === -1) return null
  const namespace = id.slice(0, first)
  let rest = id.slice(first + 1)

  const kindAt = rest.lastIndexOf('_')
  if (kindAt === -1) return null
  const kind = rest.slice(kindAt + 1)
  rest = rest.slice(0, kindAt)

  const groupAt = rest.lastIndexOf('_')
  if (groupAt === -1) return null
  const group = rest.slice(groupAt + 1)
  const name = rest.slice(0, groupAt).replaceAll('__', ':')

  if (!name || !kind) return null
  return { namespace, name, group, kind }
}
