/**
 * The merged, cross-cluster tables behind the "All clusters" view: what one
 * cluster's answer becomes once it sits beside the others'.
 *
 * A plain module, no Svelte — the same reason `query.ts` is one. The rules
 * here are the kind that get argued over: what a cluster that is slow keeps
 * showing, what a refused one shows, which tab a row opens in. They live
 * where a table-driven test can argue with them and `$stores/fleet` merely
 * applies them.
 *
 * Every row carries the cluster it came from, and every function here keeps
 * clusters in the order the backend answered — the registry's tab order —
 * so the merged table groups clusters the way the tab bar does.
 */

import type { K8sEvent, Pod, Workload } from './api/client'
import type { Tone } from './format'
import { tokenize } from './query'

/** The three merged tables. */
export type FleetTab = 'pods' | 'workloads' | 'events'

export const FLEET_TABS: ReadonlyArray<{ id: FleetTab; label: string }> = [
  { id: 'pods', label: 'Pods' },
  { id: 'workloads', label: 'Workloads' },
  { id: 'events', label: 'Events' },
]

/** Mirrors `domain.ClusterReadStatus` — see app/domain/fleet.go for what
    each one means and why they are told apart. */
export type ClusterReadStatus = 'ok' | 'partial' | 'slow' | 'forbidden' | 'unreachable' | 'failed'

/** One cluster's share of a fleet read, as the wire carries it, with the
    per-kind row field already lifted into `items`. */
export interface ClusterRead<T> {
  cluster: string
  status: ClusterReadStatus
  /** The operator-facing sentence for a status that is not ok or slow. */
  reason: string
  /** What a partial read did not get — workload kinds. */
  missing: string[]
  items: T[]
}

/** What the view keeps per cluster between reads. */
export interface ClusterAnswer<T> {
  cluster: string
  status: ClusterReadStatus
  reason: string
  missing: string[]
  rows: T[]
  /** When `rows` were read, in ms since the epoch; null while there are none. */
  rowsAt: number | null
  /** Whether `rows` are older than the read that produced `status` — kept
      from an earlier answer because this one brought none. */
  stale: boolean
}

/** A row of a merged table: the DTO plus which cluster it came from. */
export type FleetRow<T> = T & { cluster: string }

/**
 * Folds a new read into what the view was showing.
 *
 * THE RULE IS WHAT A CLUSTER THAT DID NOT ANSWER KEEPS SHOWING. A cluster
 * that answered replaces its rows, partial or not. One that is slow or
 * unreachable keeps the rows it last showed, marked stale, because the rows
 * were true a moment ago and a table that empties a cluster's rows on every
 * blip is a table nobody can read — the status strip says the cluster is
 * not answering, and how old the rows are. One that refused, or failed
 * outright, shows nothing: its rows are not late, they are not permitted or
 * not knowable, and stale rows under a "forbidden" mark would claim a view
 * the account does not have. A cluster the read did not include — its tab
 * closed — is dropped.
 *
 * A slow cluster whose read brought rows is one whose PREVIOUS read finished
 * late (see readOne in app/application/fleet.go), and those rows replace
 * what was showing. A late answer of zero rows is indistinguishable on the
 * wire from no late answer at all, so it keeps the previous rows for one
 * more tick rather than blanking a cluster on a guess.
 */
export function mergeFleet<T>(
  previous: readonly ClusterAnswer<T>[],
  reads: readonly ClusterRead<T>[],
  now: number,
): ClusterAnswer<T>[] {
  const before = new Map(previous.map((answer) => [answer.cluster, answer]))

  return reads.map((read) => {
    const last = before.get(read.cluster)
    const kept = last?.rows ?? []
    const head = {
      cluster: read.cluster,
      status: read.status,
      reason: read.reason,
      missing: read.missing,
    }

    if (read.status === 'ok' || read.status === 'partial') {
      return { ...head, rows: read.items, rowsAt: now, stale: false }
    }
    if (read.status === 'slow' && read.items.length > 0) {
      return { ...head, rows: read.items, rowsAt: now, stale: false }
    }
    if (read.status === 'slow' || read.status === 'unreachable') {
      return { ...head, rows: kept, rowsAt: last?.rowsAt ?? null, stale: kept.length > 0 }
    }
    return { ...head, rows: [], rowsAt: null, stale: false }
  })
}

/** Every cluster's rows in one list, each stamped with its cluster, in the
    order the clusters answered. */
export function flattenFleet<T>(answers: readonly ClusterAnswer<T>[]): FleetRow<T>[] {
  const rows: FleetRow<T>[] = []
  for (const answer of answers) {
    for (const row of answer.rows) rows.push({ ...row, cluster: answer.cluster })
  }
  return rows
}

/** One cluster's chip in the status strip above a merged table. */
export interface FleetStripEntry {
  cluster: string
  status: ClusterReadStatus
  tone: Tone
  label: string
  rows: number
  stale: boolean
  /** How old the rows shown are, in seconds; null when there are none. */
  ageSeconds: number | null
  /** The whole story, for the chip's tooltip. */
  title: string
}

/**
 * The same three colours every status mark in the application uses, and the
 * word each one stands for. Slow is blue rather than amber: it is a cluster
 * still being read, which is not something wrong yet.
 */
const STRIP_TONES: Record<ClusterReadStatus, { tone: Tone; label: string }> = {
  ok: { tone: 'success', label: 'Read' },
  partial: { tone: 'warning', label: 'Partial' },
  slow: { tone: 'info', label: 'Slow' },
  forbidden: { tone: 'warning', label: 'Forbidden' },
  unreachable: { tone: 'error', label: 'Unreachable' },
  failed: { tone: 'error', label: 'Failed' },
}

/** The status strip: one entry per cluster, in tab order. */
export function stripModel<T>(answers: readonly ClusterAnswer<T>[], now: number): FleetStripEntry[] {
  return answers.map((answer) => {
    const { tone, label } = STRIP_TONES[answer.status]
    const ageSeconds =
      answer.rowsAt === null ? null : Math.max(0, Math.round((now - answer.rowsAt) / 1000))
    return {
      cluster: answer.cluster,
      status: answer.status,
      tone,
      label,
      rows: answer.rows.length,
      stale: answer.stale,
      ageSeconds,
      title: stripTitle(answer, ageSeconds),
    }
  })
}

/**
 * The sentence behind a chip. Says WHY a cluster shows what it shows — the
 * reason the backend classified, never a paraphrase of it — and, when the
 * rows are older than the verdict, how much older.
 */
function stripTitle<T>(answer: ClusterAnswer<T>, ageSeconds: number | null): string {
  const count = `${answer.rows.length} row${answer.rows.length === 1 ? '' : 's'}`
  const shown = answer.stale ? `; showing ${count} from ${ageSeconds ?? 0}s ago` : ''

  switch (answer.status) {
    case 'ok':
      return `${answer.cluster} — ${count}`
    case 'partial':
      return `${answer.cluster} — ${count}; ${answer.missing.join(', ')} not read: ${answer.reason}`
    case 'slow':
      return `${answer.cluster} — still reading${shown}`
    case 'unreachable':
      return `${answer.cluster} — ${answer.reason}${shown}`
    default:
      return `${answer.cluster} — ${answer.reason}`
  }
}

/** Where a row of a merged table leads: one object, in one cluster. */
export interface FleetTarget {
  cluster: string
  /** The Kubernetes Kind, verbatim — what the drawer resolves references by. */
  kind: string
  name: string
  namespace: string
}

/**
 * Resolves a row click. The Kind is the row's own for a workload and the
 * table's for the rest; an event opens as itself, the way the single-cluster
 * event list opens one, because its message is what a click is after.
 */
export function fleetRowTarget(
  tab: FleetTab,
  row: FleetRow<Pod> | FleetRow<Workload> | FleetRow<K8sEvent>,
): FleetTarget {
  const kind = tab === 'pods' ? 'Pod' : tab === 'events' ? 'Event' : (row as FleetRow<Workload>).kind
  return { cluster: row.cluster, kind, name: row.name, namespace: row.namespace }
}

/** A quick-filter chip over a merged table's rows. Like the pod chips in
    `$lib/podStatusFilters`, each one SELECTS on a field Go already computed. */
export interface FleetChip<T> {
  id: string
  label: string
  predicate: (row: T) => boolean
}

export const WORKLOAD_CHIPS: readonly FleetChip<Workload>[] = [
  // Quotes Workload.isHealthy — Go's Workload.IsHealthy(), which judges a
  // Job by whether it failed rather than whether it finished.
  { id: 'unhealthy', label: 'Unhealthy', predicate: (workload) => !workload.isHealthy },
  { id: 'rolling', label: 'Rolling', predicate: (workload) => workload.isRolling },
  { id: 'suspended', label: 'Suspended', predicate: (workload) => workload.suspended },
]

export const EVENT_CHIPS: readonly FleetChip<K8sEvent>[] = [
  { id: 'warnings', label: 'Warnings', predicate: (event) => event.isWarning },
]

/** OR across the selected chips, pass-through when none is — the pod chips'
    own rule, see `matchesPodStatusChips`. */
export function matchesChips<T>(
  row: T,
  chips: readonly FleetChip<T>[],
  activeIds: readonly string[],
): boolean {
  if (activeIds.length === 0) return true
  return chips.some((chip) => activeIds.includes(chip.id) && chip.predicate(row))
}

/** The `cluster:` term that selects exactly this cluster in the search box. */
function clusterTerm(cluster: string): string {
  return /\s/.test(cluster) ? `cluster:"${cluster}"` : `cluster:${cluster}`
}

/** Whether `search` already carries this cluster's own term. Tokenised by
    the grammar's own tokeniser, so a quoted name with a space is one term
    here exactly as it is to the filter. */
export function hasClusterTerm(search: string, cluster: string): boolean {
  const term = clusterTerm(cluster).toLowerCase()
  return tokenize(search).some((token) => token.toLowerCase() === term)
}

/**
 * Adds this cluster's `cluster:` term to a search, or removes it if it is
 * already there — what clicking a chip in the status strip does. Only that
 * one token changes; whatever else was typed stays.
 */
export function toggleClusterTerm(search: string, cluster: string): string {
  const term = clusterTerm(cluster)
  const tokens = tokenize(search)
  const without = tokens.filter((token) => token.toLowerCase() !== term.toLowerCase())
  return (without.length === tokens.length ? [...tokens, term] : without).join(' ')
}
