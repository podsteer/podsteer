/**
 * What an Argo CD Application says about itself, in Argo CD's own words.
 *
 * QUOTATION, NOT VERDICT. Everything here is lifted verbatim out of the
 * Application's own manifest — the one GET the drawer already made. Sync and
 * health are the controller's conclusions and are shown in the controller's
 * vocabulary (Synced/OutOfSync; Healthy, Progressing, Degraded, Suspended,
 * Missing, Unknown); PodSteer draws no conclusion of its own on top, and does
 * not go and look at the members to check. If a comparison is ever wanted
 * here — "this member is OutOfSync because…" — it belongs in the Go domain
 * with a test, not in this file.
 *
 * The member list is `status.resources`, which is Argo CD's own record of
 * what the Application manages: the objects it rendered from Git plus the
 * live ones it found that are no longer there. It is what the controller
 * acts on, so it is the honest membership, and it reads nothing beyond the
 * Application itself — no LIST of child kinds, no Secret. See panel.ts for
 * why a label-based view was rejected in favour of this.
 *
 * Field names follow the Application CRD (argoproj.io/v1alpha1):
 * https://argo-cd.readthedocs.io/en/stable/operator-manual/application.yaml
 */

import type { GitOpsMember } from './panel'

/** The API group every Argo CD kind lives in. */
export const ARGO_GROUP = 'argoproj.io'

/** One place the Application renders from. */
export interface ArgoSource {
  repoURL: string
  /** For a Git or directory source. Empty when the source is a chart. */
  path: string
  /** For a Helm repository source. Empty when the source is a path. */
  chart: string
  targetRevision: string
}

/** A condition as Argo CD writes it: a type and a message, with no status. */
export interface ArgoCondition {
  type: string
  message: string
  lastTransitionTime: string
}

/** The most recent sync operation, from `status.operationState`. */
export interface ArgoOperation {
  /** Running, Succeeded, Failed, Error or Terminating. */
  phase: string
  message: string
  /** What the operation synced to, from `syncResult.revision`. */
  revision: string
  startedAt: string
  finishedAt: string
}

export interface ArgoApplication {
  project: string
  /** One entry for a single-source Application, one per source otherwise. */
  sources: ArgoSource[]
  destination: {
    /** The cluster's API server URL, when addressed that way. */
    server: string
    /** The cluster's registered name, when addressed that way. */
    name: string
    namespace: string
  }
  syncPolicy: {
    automated: boolean
    prune: boolean
    selfHeal: boolean
  }
  /** `status.sync`: whether the live state matches the target revision. */
  sync: {
    /** Synced, OutOfSync or Unknown. */
    status: string
    /** The revision(s) the live state was compared to. */
    revision: string
  }
  /** `status.health`, aggregated by Argo CD over the members. */
  health: {
    status: string
    message: string
  }
  /** The last sync operation, or null when none has been recorded. */
  operation: ArgoOperation | null
  /** When Argo CD last reconciled the Application. */
  reconciledAt: string
  conditions: ArgoCondition[]
  /** `status.resources`, in the order Argo CD wrote them. */
  resources: GitOpsMember[]
}

/** The parts of the manifest this reads. */
interface ApplicationManifest {
  spec?: {
    project?: string
    source?: RawSource
    sources?: RawSource[]
    destination?: { server?: string; name?: string; namespace?: string }
    syncPolicy?: { automated?: { prune?: boolean; selfHeal?: boolean } | null }
  }
  status?: {
    sync?: { status?: string; revision?: string; revisions?: string[] }
    health?: { status?: string; message?: string }
    operationState?: {
      phase?: string
      message?: string
      startedAt?: string
      finishedAt?: string
      syncResult?: { revision?: string; revisions?: string[] }
      operation?: { sync?: { revision?: string; revisions?: string[] } }
    }
    reconciledAt?: string
    conditions?: { type?: string; message?: string; lastTransitionTime?: string }[]
    resources?: {
      group?: string
      version?: string
      kind?: string
      namespace?: string
      name?: string
      status?: string
      health?: { status?: string; message?: string }
      requiresPruning?: boolean
    }[]
  }
}

interface RawSource {
  repoURL?: string
  path?: string
  chart?: string
  targetRevision?: string
}

/**
 * Reads an Application, or null when there is no manifest at all.
 *
 * An Application with no status yet — just created, or one the controller
 * has not looked at — comes back with empty strings rather than null, so the
 * panel renders "Unknown" where Argo CD has said nothing, which is the truth
 * of it, rather than disappearing.
 */
export function argoApplication(manifest: unknown): ArgoApplication | null {
  if (!manifest || typeof manifest !== 'object') return null
  const { spec = {}, status = {} } = manifest as ApplicationManifest

  const automated = spec.syncPolicy?.automated
  const operation = status.operationState

  return {
    project: spec.project ?? '',
    sources: sourcesOf(spec),
    destination: {
      server: spec.destination?.server ?? '',
      name: spec.destination?.name ?? '',
      namespace: spec.destination?.namespace ?? '',
    },
    syncPolicy: {
      // Present — even as an empty object — means automated. `automated: {}`
      // is the documented way to turn it on with neither option.
      automated: automated !== undefined && automated !== null,
      prune: automated?.prune ?? false,
      selfHeal: automated?.selfHeal ?? false,
    },
    sync: {
      status: status.sync?.status ?? '',
      revision: revisionOf(status.sync?.revision, status.sync?.revisions),
    },
    health: {
      status: status.health?.status ?? '',
      message: status.health?.message ?? '',
    },
    operation: operation
      ? {
          phase: operation.phase ?? '',
          message: operation.message ?? '',
          // The result names what was actually synced; the request is the
          // fallback for an operation still running, which has no result yet.
          revision:
            revisionOf(operation.syncResult?.revision, operation.syncResult?.revisions) ||
            revisionOf(operation.operation?.sync?.revision, operation.operation?.sync?.revisions),
          startedAt: operation.startedAt ?? '',
          finishedAt: operation.finishedAt ?? '',
        }
      : null,
    reconciledAt: status.reconciledAt ?? '',
    conditions: (status.conditions ?? []).map((condition) => ({
      type: condition.type ?? '',
      message: condition.message ?? '',
      lastTransitionTime: condition.lastTransitionTime ?? '',
    })),
    resources: (status.resources ?? []).map((resource) => ({
      group: resource.group ?? '',
      version: resource.version ?? '',
      kind: resource.kind ?? '',
      namespace: resource.namespace ?? '',
      name: resource.name ?? '',
      sync: resource.status ?? '',
      health: resource.health?.status ?? '',
      healthMessage: resource.health?.message ?? '',
      requiresPruning: resource.requiresPruning ?? false,
    })),
  }
}

/**
 * `spec.sources` wins over `spec.source` when both are present, which is the
 * CRD's own rule for a multi-source Application: the singular field is
 * ignored once the plural one is set.
 */
function sourcesOf(spec: NonNullable<ApplicationManifest['spec']>): ArgoSource[] {
  const raw = spec.sources && spec.sources.length > 0 ? spec.sources : spec.source ? [spec.source] : []
  return raw.map((source) => ({
    repoURL: source.repoURL ?? '',
    path: source.path ?? '',
    chart: source.chart ?? '',
    targetRevision: source.targetRevision ?? '',
  }))
}

/** A multi-source Application records one revision per source. */
function revisionOf(single: string | undefined, many: string[] | undefined): string {
  if (single) return single
  return (many ?? []).filter(Boolean).join(', ')
}

/**
 * How Argo CD's own UI grades one of its words, as a DetailList tone.
 *
 * THIS IS ARGO CD'S GRADING, NOT OURS. OutOfSync and Missing are amber and
 * Degraded is red in Argo CD's interface, and a word this table does not
 * know — including a misspelt one — is left uncoloured rather than guessed
 * at. Nothing here compares two fields or applies a threshold; it is a
 * one-to-one reading of a vocabulary the controller documents, which is what
 * keeps it a quotation.
 */
export function argoTone(word: string): 'warn' | 'critical' | undefined {
  switch (word) {
    case 'OutOfSync':
    case 'Missing':
      return 'warn'
    case 'Degraded':
    case 'Failed':
    case 'Error':
      return 'critical'
    default:
      return undefined
  }
}

/**
 * The tone of an Argo CD condition, from the convention in its type name.
 *
 * Argo CD names its condition types by severity — ComparisonError,
 * SyncError, InvalidSpecError; SharedResourceWarning, OrphanedResourceWarning
 * — and colours them accordingly. The suffix is therefore the controller's
 * own statement of how serious it is.
 */
export function argoConditionTone(type: string): 'warn' | 'critical' | undefined {
  if (type.endsWith('Error')) return 'critical'
  if (type.endsWith('Warning')) return 'warn'
  return undefined
}

/**
 * A git revision shortened the way Argo CD's own UI shortens it.
 *
 * Only a full 40-hex SHA is cut; a chart version, a branch name or a tag is
 * already short and is shown as written.
 */
export function shortRevision(revision: string): string {
  return /^[0-9a-f]{40}$/i.test(revision) ? revision.slice(0, 7) : revision
}
