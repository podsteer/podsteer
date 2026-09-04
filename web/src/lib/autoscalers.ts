/**
 * Locating the autoscaler, if any, that owns a workload's replica count.
 *
 * Scaling a Deployment or StatefulSet by hand while a HorizontalPodAutoscaler
 * or a KEDA ScaledObject targets it is undone within its next sync period,
 * silently — the controller reconciles the replica count right back to
 * whatever it decided. Kubernetes puts no marker on the WORKLOAD saying
 * "something else is scaling me"; the only place that relationship is
 * recorded is on the autoscaler itself, in `spec.scaleTargetRef`.
 *
 * Both kinds are served by the generic table path (`BrowseAPI.ListTable`),
 * which prints the columns `kubectl get` would — an HPA's `REFERENCE` column
 * reads `Deployment/web`, and a ScaledObject prints `SCALETARGETKIND` and
 * `SCALETARGETNAME` as two columns instead of one string. Reading them here
 * is a QUOTATION of what is already on screen in the table view, the same
 * string the API server itself prints — comparing it for an exact match is
 * not a verdict, so this stays in TypeScript rather than crossing into Go.
 * See the QUOTATION vs VERDICT rule in CLAUDE.md.
 */

import type { ResourceTable } from './api/client'

/** One autoscaler found to be targeting a workload. */
export interface AutoscalerRef {
  name: string
  kind: 'HorizontalPodAutoscaler' | 'ScaledObject'
  /** From the table's MINPODS (HPA) or MIN (KEDA) column, when the server printed one. */
  minReplicas?: string
  /** From the table's MAXPODS (HPA) or MAX (KEDA) column, when the server printed one. */
  maxReplicas?: string
}

/**
 * Whether the Scale dialog could establish the answer.
 *
 * A THIRD STATE, DELIBERATELY. Collapsing "asked and found nothing" and
 * "could not ask" into the same `autoscalers: []` would tell an operator
 * nothing manages their workload when the honest answer is "unreadable" — the
 * same distinction `domain.MetricsStatus` draws for the overview (see
 * CLAUDE.md): an absent answer and a refused one are different things, and
 * only one of them is safe to read as permission to scale freely.
 */
export type AutoscalerCheck =
  | { status: 'known'; autoscalers: AutoscalerRef[] }
  | { status: 'unknown'; reason: string }

/** Finds a column by its header, case-insensitively. -1 when the server did not print it. */
function columnIndex(table: ResourceTable, header: string): number {
  return table.columns.findIndex((column) => column.name.toLowerCase() === header.toLowerCase())
}

/**
 * Finds every row of an autoscaler table that targets `target`.
 *
 * MISSING COLUMNS ARE NOT AN ERROR, NEVER A THROW. A server that prints a
 * table without a REFERENCE or a SCALETARGETKIND/SCALETARGETNAME column has
 * printed something this function does not recognise — additionalPrinterColumns
 * are not guaranteed to be stable across API versions — and the honest answer
 * is "found nothing here", the same as a table with no matching row.
 *
 * THE MATCH IS EXACT ON BOTH KIND AND NAME. `Deployment/web` naming a
 * Deployment called "web" must not match a StatefulSet also called "web", and
 * must not match a Deployment called "web-canary" — a substring or prefix
 * match would silently point an operator at the wrong autoscaler, or hide a
 * real one behind what looked like a match.
 */
export function findAutoscalers(
  table: ResourceTable,
  kindHint: 'hpa' | 'keda',
  target: { kind: string; name: string },
): AutoscalerRef[] {
  return kindHint === 'hpa' ? findHorizontalPodAutoscalers(table, target) : findScaledObjects(table, target)
}

function findHorizontalPodAutoscalers(
  table: ResourceTable,
  target: { kind: string; name: string },
): AutoscalerRef[] {
  const referenceIdx = columnIndex(table, 'REFERENCE')
  if (referenceIdx === -1) return []

  const minIdx = columnIndex(table, 'MINPODS')
  const maxIdx = columnIndex(table, 'MAXPODS')

  const found: AutoscalerRef[] = []
  for (const row of table.rows) {
    const reference = row.cells[referenceIdx]
    if (!reference) continue

    // "Deployment/web" — Kubernetes' own separator between the target's kind
    // and its name, and one a resource name may never contain itself.
    const slash = reference.indexOf('/')
    if (slash === -1) continue
    const kind = reference.slice(0, slash)
    const name = reference.slice(slash + 1)
    if (kind !== target.kind || name !== target.name) continue

    found.push({
      name: row.name,
      kind: 'HorizontalPodAutoscaler',
      minReplicas: minIdx === -1 ? undefined : row.cells[minIdx],
      maxReplicas: maxIdx === -1 ? undefined : row.cells[maxIdx],
    })
  }
  return found
}

/**
 * KEDA prints SCALETARGETKIND from `status.scaleTargetKind`, which is the
 * GroupVersion-qualified form — "apps/v1.Deployment", not "Deployment" — so
 * the bare kind is whatever follows the last dot. A cell that carries no
 * qualifier (an older KEDA, or a hand-written row) is returned unchanged.
 */
function bareKind(cell: string | undefined): string {
  if (!cell) return ''
  const dot = cell.lastIndexOf('.')
  return dot === -1 ? cell : cell.slice(dot + 1)
}

function findScaledObjects(
  table: ResourceTable,
  target: { kind: string; name: string },
): AutoscalerRef[] {
  // Two columns, unlike an HPA's single "Kind/name" REFERENCE — KEDA prints
  // the target's kind and name separately, so there is no string to split.
  const kindIdx = columnIndex(table, 'SCALETARGETKIND')
  const nameIdx = columnIndex(table, 'SCALETARGETNAME')
  if (kindIdx === -1 || nameIdx === -1) return []

  const minIdx = columnIndex(table, 'MIN')
  const maxIdx = columnIndex(table, 'MAX')

  const found: AutoscalerRef[] = []
  for (const row of table.rows) {
    if (bareKind(row.cells[kindIdx]) !== target.kind || row.cells[nameIdx] !== target.name) continue

    found.push({
      name: row.name,
      kind: 'ScaledObject',
      minReplicas: minIdx === -1 ? undefined : row.cells[minIdx],
      maxReplicas: maxIdx === -1 ? undefined : row.cells[maxIdx],
    })
  }
  return found
}
