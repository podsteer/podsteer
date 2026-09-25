/**
 * How an image pane reads what Kubernetes reported.
 *
 * Every judgement about an image — which reference is the subject, whether the
 * node's digest disagrees with the kubelet's, which of three reasons there is
 * no size — is made in `app/domain/imagereport.go`. What is here is the rest:
 * formatting a byte count, and deciding which rows are worth a line at all.
 *
 * ONE RULE, AND IT IS THE ONE THIS PANE EXISTS TO KEEP: a size that was not
 * measured is a DASH with a reason beside it, never a nought. A node that
 * refused a read and a node that has garbage-collected the image are different
 * facts calling for different next steps, and zero bytes is a claim about
 * neither.
 */

import type { ImageReport } from '$lib/api/client'

/** One line of the pane: a label, a value, and whether the value is real. */
export interface ImageRow {
  label: string
  value: string
  /**
   * True when the value is an explanation standing in for a figure, so the
   * pane can render it as prose rather than as data.
   */
  absent?: boolean
}

/**
 * Formats a size the way a container runtime's own tooling does — powers of
 * ten, because that is what `docker images` and `crictl images` print and a
 * pane that disagreed with both would have people checking their arithmetic.
 */
export function formatImageSize(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes <= 0) return '—'

  const units = ['B', 'kB', 'MB', 'GB', 'TB']
  let value = bytes
  let unit = 0
  while (value >= 1000 && unit < units.length - 1) {
    value /= 1000
    unit += 1
  }

  const digits = unit === 0 || value >= 100 ? 0 : 1
  return `${value.toFixed(digits)} ${units[unit]}`
}

/**
 * The size cell: a figure only when one was measured, and the reason
 * otherwise.
 *
 * Read `sizeStatus` first and `sizeBytes` second, always — the report sets
 * the bytes to zero for every status but "measured" precisely so a caller that
 * forgets shows nothing rather than something false.
 */
export function sizeRow(report: ImageReport): ImageRow {
  if (report.sizeStatus === 'measured') {
    return { label: 'Size', value: formatImageSize(report.sizeBytes) }
  }
  return { label: 'Size', value: report.sizeSource || 'not reported', absent: true }
}

/**
 * The identity rows, in the order somebody scans them.
 *
 * The RESOLVED reference leads, because what the kubelet says it is running is
 * a fact and what the manifest asks for is an intention. The declared one is
 * shown separately only when the two differ — repeating an identical string
 * under two headings is how a pane teaches people to stop reading it.
 */
export function identityRows(report: ImageReport): ImageRow[] {
  const rows: ImageRow[] = []

  if (report.resolved) rows.push({ label: 'Running', value: report.resolved })
  if (report.drift && report.declared) {
    rows.push({ label: 'Declared', value: report.declared })
  }
  if (!report.resolved && report.declared) {
    rows.push({ label: 'Declared', value: report.declared })
  }

  if (report.referenceReadable) {
    rows.push({ label: 'Registry', value: report.registry })
    rows.push({ label: 'Repository', value: report.repository })
    if (report.tag) rows.push({ label: 'Tag', value: report.tag })
  }

  if (report.digest) rows.push({ label: 'Digest', value: report.digest })
  if (report.pullPolicy) rows.push({ label: 'Pull policy', value: report.pullPolicy })

  return rows
}

/**
 * Whether the pane has anything to show at all.
 *
 * A container whose image nothing reported — an unscheduled pod whose manifest
 * the drawer has not resolved — gets the bounded line and nothing else, which
 * is honest, but it is not worth a section heading.
 */
export function hasIdentity(report: ImageReport | null): boolean {
  return !!report && (!!report.resolved || !!report.declared)
}

/**
 * The one-line summary above the rows.
 *
 * Names the drift where there is drift, because a container running something
 * other than what its manifest asks for is the single most useful thing this
 * pane can say, and it is buried if it is only a row among ten.
 */
export function imageHeadline(report: ImageReport): string {
  if (report.drift) {
    return 'This container is running a different reference than its spec declares.'
  }
  if (report.digest) {
    return 'Pinned by digest to exactly the content below.'
  }
  return ''
}
