/**
 * Custom columns: one label or annotation key, shown verbatim as a column.
 *
 * QUOTATION ONLY. A custom column shows the value of one metadata key exactly
 * as the object carries it — no JSONPath into spec, no derivation — which is
 * what lets the same code serve every list in the application, the generic
 * server-printed tables included: every row of every kind already carries
 * its labels, and carries the annotations somebody asked for.
 *
 * The specs are persisted per KIND in preferences.svelte.ts. A kind id and a
 * label key are not object names — "this operator watches the `team` label
 * on Deployments" says nothing about which Deployments exist — so they may
 * live in storage, unlike the Recent section's object names (see
 * ClusterSession.recentObjects for that rule).
 *
 * No Svelte import belongs here, for the reason query.ts gives: the rules —
 * what a valid key is, how a column id round-trips, how absent values sort —
 * are arguable in a table-driven test without a component around them.
 */

import type { Column } from './components/DataTable.svelte'
import type { SortValue } from './sort'

/** Where a custom column reads its value from. */
export type ColumnSource = 'label' | 'annotation'

/** One custom column: a source and the key to read from it. */
export interface CustomColumnSpec {
  source: ColumnSource
  key: string
}

/** The metadata a row must carry for custom columns to read it. Every list
    DTO carries both (empty objects when the object has none). */
export interface MetadataRow {
  labels?: Record<string, string>
  annotations?: Record<string, string>
}

/** The keys present on the rows on screen, for the column picker. */
export interface MetadataKeys {
  labels: string[]
  annotations: string[]
}

/**
 * The one annotation that can never be a column.
 *
 * It is `kubectl apply`'s copy of the whole previous manifest — tens of
 * kilobytes on a Deployment — and the backend refuses to project it for that
 * reason and one more: the watch store strips it, so a column of it would
 * read blank on a cluster the watch is serving and the full manifest on one
 * it is not. Refusing it here too means the picker never offers a column the
 * backend will silently leave empty.
 */
export const LAST_APPLIED_ANNOTATION = 'kubectl.kubernetes.io/last-applied-configuration'

/** Default width of a custom column. A label value is short by API rule (63
    characters at most); an annotation may not be, and truncates. */
const CUSTOM_COLUMN_WIDTH = 160

const SOURCES: readonly ColumnSource[] = ['label', 'annotation']

/**
 * Whether `key` can name a label or annotation.
 *
 * The same rule the backend's domain.NewProjection applies: non-empty, no
 * whitespace, no comma — and for an annotation, not the last-applied
 * manifest. Deliberately looser than Kubernetes' full qualified-name syntax:
 * a key that does not exist merely reads as a dash, which is the honest
 * answer, while a picker that second-guesses the API's own validation would
 * refuse keys the cluster happily carries.
 */
export function isValidKey(source: ColumnSource, key: string): boolean {
  if (key === '' || /[\s,]/.test(key)) return false
  return !(source === 'annotation' && key === LAST_APPLIED_ANNOTATION)
}

/** The stable column id a spec persists its width and visibility under. */
export function customColumnId(spec: CustomColumnSpec): string {
  return `${spec.source}:${spec.key}`
}

/** Whether a column id names a custom column rather than a built-in one. */
export function isCustomColumnId(id: string): boolean {
  return parseCustomColumnId(id) !== null
}

/**
 * The spec behind a column id, or null for a built-in column.
 *
 * Split on the FIRST colon only: an annotation key routinely contains no
 * colon, but a hand-typed one could, and the prefix is the only part whose
 * shape this module owns.
 */
export function parseCustomColumnId(id: string): CustomColumnSpec | null {
  const at = id.indexOf(':')
  if (at <= 0) return null
  const source = id.slice(0, at)
  const key = id.slice(at + 1)
  if (!(SOURCES as readonly string[]).includes(source)) return null
  if (!isValidKey(source as ColumnSource, key)) return null
  return { source: source as ColumnSource, key }
}

/**
 * Validates whatever storage handed back into a list of specs.
 *
 * Storage outlives the code that wrote it and may have been hand-edited, so
 * every entry is checked rather than trusted: the wrong shape, an unknown
 * source, an invalid key and a duplicate are each dropped, in order, keeping
 * the first of any pair. Never throws.
 */
export function normaliseSpecs(raw: unknown): CustomColumnSpec[] {
  if (!Array.isArray(raw)) return []

  const seen = new Set<string>()
  const specs: CustomColumnSpec[] = []
  for (const entry of raw) {
    if (typeof entry !== 'object' || entry === null) continue
    const { source, key } = entry as { source?: unknown; key?: unknown }
    if (typeof source !== 'string' || typeof key !== 'string') continue
    if (!(SOURCES as readonly string[]).includes(source)) continue
    const trimmed = key.trim()
    if (!isValidKey(source as ColumnSource, trimmed)) continue

    const spec = { source: source as ColumnSource, key: trimmed }
    const id = customColumnId(spec)
    if (seen.has(id)) continue
    seen.add(id)
    specs.push(spec)
  }
  return specs
}

/**
 * The annotation keys a list must be asked for — the projection parameter
 * every list call takes. Sorted and unique, so two column orders make the
 * same request. Label columns need nothing: every row carries its labels.
 */
export function annotationKeysOf(specs: readonly CustomColumnSpec[]): string[] {
  const keys = new Set<string>()
  for (const spec of specs) if (spec.source === 'annotation') keys.add(spec.key)
  return [...keys].sort()
}

/** The value a row shows under a custom column, or '' when it has none. */
export function customValue(row: MetadataRow, spec: CustomColumnSpec): string {
  const source = spec.source === 'label' ? row.labels : row.annotations
  return source?.[spec.key] ?? ''
}

/** The cell text: the value, or a dash for a row without the key. */
export function customCell(row: MetadataRow, spec: CustomColumnSpec): string {
  return customValue(row, spec) || '—'
}

/** DataTable columns for a list's specs, in the operator's order. */
export function toColumns(specs: readonly CustomColumnSpec[]): Column[] {
  return specs.map((spec) => ({
    id: customColumnId(spec),
    label: spec.key,
    width: CUSTOM_COLUMN_WIDTH,
  }))
}

/**
 * The sort accessor for a custom column id, or null for a built-in one.
 *
 * A row without the key sorts LAST in both directions — the same rule an
 * unmeasured CPU follows in $lib/sort — rather than as an empty string that
 * would float every dash to the top of an ascending sort.
 */
export function customSortAccessor<T extends MetadataRow>(
  columnId: string,
): ((row: T) => SortValue) | null {
  const spec = parseCustomColumnId(columnId)
  if (!spec) return null
  return (row) => customValue(row, spec) || null
}

/**
 * The text a row's custom columns contribute to the plain-substring search.
 *
 * A built-in column's text is searchable — typing part of a node name finds
 * the pod on it — so a custom column's must be too, or a value visibly on
 * screen would be invisible to the box above it.
 */
export function customSearchText(row: MetadataRow, specs: readonly CustomColumnSpec[]): string[] {
  return specs.map((spec) => customValue(row, spec)).filter((value) => value !== '')
}

/**
 * Every label and annotation key the given rows carry, sorted and unique —
 * what the column picker offers, so an operator chooses from keys the
 * cluster actually uses rather than typing one from memory.
 *
 * Annotations are only ever the projected ones (see LAST_APPLIED_ANNOTATION
 * for why the whole map never travels), so the annotation list is what the
 * kind's existing columns already asked for, not everything the objects
 * carry. That is why the picker also takes free text.
 */
export function keysOnScreen(rows: readonly MetadataRow[]): MetadataKeys {
  const labels = new Set<string>()
  const annotations = new Set<string>()
  for (const row of rows) {
    for (const key of Object.keys(row.labels ?? {})) labels.add(key)
    for (const key of Object.keys(row.annotations ?? {})) {
      if (isValidKey('annotation', key)) annotations.add(key)
    }
  }
  return { labels: [...labels].sort(), annotations: [...annotations].sort() }
}
