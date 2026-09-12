/**
 * Merging tables that do not share a column set.
 *
 * TWO VIEWS ASK THE SAME QUESTION OF DIFFERENT AXES. The All-clusters view
 * puts six clusters' answers for ONE kind into one table; the combined view
 * puts one cluster's answers for SEVERAL KINDS into one table. In both cases
 * the sources print whatever columns they print — a CRD's author chose them,
 * or a Deployment prints READY/UP-TO-DATE/AVAILABLE where a Service prints
 * TYPE/CLUSTER-IP — and the rows have to line up anyway.
 *
 * So the rule lives here once rather than twice: **columns are matched by
 * NAME, first seen wins the position, and later sources only ever append.**
 * A source that prints a column nobody else does gets its own column with
 * every other source's cells empty, which is the honest rendering — an empty
 * cell says "this kind does not print that", and inventing a value would say
 * something else.
 *
 * Cells are mapped by POSITION through a per-source index computed once, not
 * once per row: a hundred rows of a kind that prints eight columns is eight
 * lookups, not eight hundred.
 *
 * A plain module, no Svelte, for the reason `fleet.ts` and `query.ts` are:
 * this is a rule worth arguing with in a table-driven test.
 */
import type { TableColumn, TableRow } from './api/client'

/** One source's answer: its rows, and the columns they were printed with. */
export interface TableSource {
  /** What distinguishes this source — a cluster id, or a kind id. */
  key: string
  columns: TableColumn[]
  rows: TableRow[]
}

/** A row that remembers which source it came from. */
export type SourcedRow = TableRow & { source: string }

export interface MergedTables {
  columns: TableColumn[]
  rows: SourcedRow[]
}

/**
 * Folds several sources into one table.
 *
 * Sources are consumed in the order given and rows come out in that order, so
 * the caller decides the grouping — the fleet view groups by the registry's
 * tab order, the combined view by the order the operator picked the kinds.
 */
export function mergeTables(sources: readonly TableSource[]): MergedTables {
  const columns: TableColumn[] = []
  const indexOf = new Map<string, number>()

  for (const source of sources) {
    for (const column of source.columns) {
      if (indexOf.has(column.name)) continue
      indexOf.set(column.name, columns.length)
      columns.push(column)
    }
  }

  const rows: SourcedRow[] = []
  for (const source of sources) {
    // Computed once per source rather than once per row.
    const positions = source.columns.map((column) => indexOf.get(column.name) ?? -1)

    for (const row of source.rows) {
      const cells = new Array<string>(columns.length).fill('')
      for (const [index, cell] of (row.cells ?? []).entries()) {
        const at = positions[index]
        if (at !== undefined && at >= 0) cells[at] = cell
      }
      rows.push({ ...row, cells, source: source.key })
    }
  }

  return { columns, rows }
}
