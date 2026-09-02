/**
 * The meter cells the pod, node, namespace and controller lists all draw.
 *
 * ONE SET OF RULES, because the question is the same in all of them: this
 * thing is using X, and X is a proportion of something that has to be named.
 * The three lists that aggregate pods — namespaces and the six controllers —
 * carry exactly the fields a pod does (see the Consumption DTO), so the cell
 * logic is shared rather than copied into each view, where three copies would
 * be three chances for one list to disagree with another about what 85% means.
 */

/**
 * The measurement fields every one of those rows carries.
 *
 * Structural rather than a union of the DTO types: Pod, NamespaceSummary and
 * Consumption all satisfy it, and naming them here would make this module
 * depend on all three for no benefit.
 */
export interface Measured {
  /** Whether there are figures to draw. */
  hasMetrics: boolean
  /**
   * Whether the CLUSTER serves metrics at all.
   *
   * Distinct from hasMetrics, and the tooltip depends on which is false: an
   * idle namespace on a metered cluster and a cluster with no metrics-server
   * both measure nothing, and telling somebody to install one they already
   * have is the failure this separates.
   *
   * Optional because only the rows that AGGREGATE pods carry it. A single
   * pod or node that reports nothing is unmeasured either way, so those rows
   * leave it undefined and keep the wording they always had.
   */
  metricsAvailable?: boolean
  cpu: string
  memory: string
  cpuRequest: string
  memoryRequest: string
  cpuLimit: string
  memoryLimit: string
  hasCpuRequest: boolean
  hasMemoryRequest: boolean
  hasCpuLimit: boolean
  hasMemoryLimit: boolean
  cpuPercent: number
  memoryPercent: number
  cpuLimitPercent: number
  memoryLimitPercent: number
}

/** What one meter cell needs, resolved for whichever denominator is chosen. */
export interface Meter {
  /** Null draws `absent` rather than a bar against a ceiling that does not exist. */
  percent: number | null
  /** A second ratio to colour by, when the bar is filled by a different one. */
  severity: number | null
  /** Whether the operator's threshold lines are marked on the track. */
  thresholds: boolean
  absent: string
}

/**
 * Everything one meter cell needs, for whichever mode is on.
 *
 * Gathered in one place rather than spread across eight attributes per cell:
 * the mode changes four things at once — what fills the bar, what colours it,
 * what the empty state says, and whether ticks are drawn — and they have to
 * change together or the cell starts contradicting itself.
 *
 * Measured against the LIMIT, the bar and the operator's thresholds share one
 * denominator, so it can be drawn as a proper gauge with the lines marked.
 * Measured against the REQUEST they do not, so the lines are left unmarked
 * and only the colour carries them.
 */
export function meter(
  byLimit: boolean,
  hasRequest: boolean,
  requestPercent: number,
  hasLimit: boolean,
  limitPercent: number,
): Meter {
  if (byLimit) {
    return {
      percent: hasLimit ? limitPercent : null,
      severity: null,
      thresholds: true,
      absent: 'no limit',
    }
  }

  return {
    percent: hasRequest ? requestPercent : null,
    severity: hasLimit ? limitPercent : null,
    thresholds: false,
    absent: 'no request',
  }
}

/**
 * Spells out what the bar is a proportion of, and what it is heading for.
 *
 * A bare percentage in a list is not self-explanatory — there is no way to
 * tell whether 85% is 85% of a request, a limit or a node, and those mean
 * three different things. The tooltip names the denominator and the number,
 * so the meter never has to be taken on trust.
 *
 * Both ratios are stated when both exist, because the bar can only draw one.
 * The limit clause is what explains a coloured bar, and saying "no limit" out
 * loud is worth as much: something unbounded is not an omission the reader
 * should have to infer from an absence.
 */
export function meterTitle(
  measured: boolean,
  available: boolean | undefined,
  value: string,
  hasRequest: boolean,
  request: string,
  percent: number,
  hasLimit: boolean,
  limit: string,
  limitPercent: number,
): string {
  if (!measured) {
    return available
      ? 'Nothing here reported usage — no running pod has been measured yet'
      : 'Not measured — this cluster has no metrics source'
  }

  const against = hasRequest
    ? `${value} of ${request} requested (${Math.round(percent)}%)`
    : `${value} used — no request declared, so there is nothing to measure it against`

  const ceiling = hasLimit
    ? `${Math.round(limitPercent)}% of its ${limit} limit`
    : 'no limit set'

  return `${against} · ${ceiling}`
}

/** The CPU cell of a row, ready to hand to MeterBar. */
export function cpuMeter(row: Measured, byLimit: boolean): Meter {
  return meter(byLimit, row.hasCpuRequest, row.cpuPercent, row.hasCpuLimit, row.cpuLimitPercent)
}

/** The memory cell of a row. */
export function memoryMeter(row: Measured, byLimit: boolean): Meter {
  return meter(
    byLimit,
    row.hasMemoryRequest,
    row.memoryPercent,
    row.hasMemoryLimit,
    row.memoryLimitPercent,
  )
}

/** The CPU cell's tooltip. */
export function cpuTitle(row: Measured): string {
  return meterTitle(
    row.hasMetrics,
    row.metricsAvailable,
    row.cpu,
    row.hasCpuRequest,
    row.cpuRequest,
    row.cpuPercent,
    row.hasCpuLimit,
    row.cpuLimit,
    row.cpuLimitPercent,
  )
}

/** The memory cell's tooltip. */
export function memoryTitle(row: Measured): string {
  return meterTitle(
    row.hasMetrics,
    row.metricsAvailable,
    row.memory,
    row.hasMemoryRequest,
    row.memoryRequest,
    row.memoryPercent,
    row.hasMemoryLimit,
    row.memoryLimit,
    row.memoryLimitPercent,
  )
}
