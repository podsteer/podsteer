/**
 * Client-side table sorting.
 *
 * Sorting happens in the session between filtering and pagination, so a sort
 * always applies to the whole filtered set rather than to the page on screen.
 * The state itself lives per kind in the session: sorting Pods by restarts
 * says nothing about how Nodes should be ordered, and keeping it per kind
 * means switching kinds and back finds the view as it was left.
 */

/**
 * Suffix -> multiplier, built once.
 *
 * It was a literal inside parseQuantity, so a CPU or memory sort rebuilt this
 * sixteen-key object for every row it looked at.
 */
const MULTIPLIERS: Record<string, number> = {
  m: 1e-3,
  k: 1e3,
  K: 1e3,
  M: 1e6,
  G: 1e9,
  B: 1,
  Ki: 1024,
  Mi: 1024 ** 2,
  Gi: 1024 ** 3,
  Ti: 1024 ** 4,
  Pi: 1024 ** 5,
  KiB: 1024,
  MiB: 1024 ** 2,
  GiB: 1024 ** 3,
  TiB: 1024 ** 4,
  PiB: 1024 ** 5,
}

/** Age suffix -> seconds, built once for the same reason as MULTIPLIERS. */
const UNIT_SECONDS: Record<string, number> = {
  s: 1,
  m: 60,
  h: 3_600,
  d: 86_400,
  y: 31_536_000,
}

/** Sort direction. */
export type SortDirection = 'asc' | 'desc'

/** The sort applied to one table. */
export interface SortState {
  columnId: string
  direction: SortDirection
}

/** A comparable cell value. Nulls sort last in both directions. */
export type SortValue = string | number | null

/** Column id -> value extractor for one row type. */
export type SortAccessors<T> = Record<string, (row: T) => SortValue>

/**
 * One collator for the whole application, built once.
 *
 * `localeCompare` with an options object cannot take the engine's fast path —
 * it constructs or looks up an Intl.Collator on EVERY call. A five-thousand
 * row sort is on the order of sixty thousand comparisons, so that was sixty
 * thousand collator lookups plus the collation itself, repeated on every
 * ten-second refresh.
 *
 * `numeric` is what makes generated names order the way an operator reads
 * them: pod-2 before pod-10.
 */
const collator = new Intl.Collator(undefined, { numeric: true, sensitivity: 'base' })

/**
 * Returns rows ordered by the state's column, or the rows untouched when no
 * sort applies — either because none is set or because the column id is not
 * one this view knows (a stale sort left over from another kind).
 *
 * Strings compare with `numeric: true` so generated names order the way an
 * operator reads them: pod-2 before pod-10.
 */
export function sortRows<T>(
  rows: T[],
  state: SortState | null,
  accessors: SortAccessors<T>,
): T[] {
  if (!state) return rows
  const accessor = accessors[state.columnId]
  if (!accessor) return rows

  const sign = state.direction === 'asc' ? 1 : -1

  // Each value is read ONCE, not once per comparison.
  //
  // The accessors are not free — several parse a Kubernetes quantity with a
  // regex, and others join an array into a string — and a comparator runs
  // O(n log n) times. Reading them in the comparator therefore did that work
  // roughly twelve times per row on a five-thousand-row table, on every
  // refresh and on every keystroke of a search. Decorating first makes it
  // exactly once per row.
  const decorated = rows.map((row) => ({ row, key: accessor(row) }))

  decorated.sort((a, b) => {
    const va = a.key
    const vb = b.key
    if (va === null && vb === null) return 0
    if (va === null) return 1
    if (vb === null) return -1

    const order =
      typeof va === 'number' && typeof vb === 'number'
        ? va - vb
        : collator.compare(String(va), String(vb))
    return order * sign
  })

  return decorated.map((entry) => entry.row)
}

/**
 * Parses a resource quantity into a plain number for comparison.
 *
 * Accepts both Kubernetes resource quantities ("120m", "1.5Gi") and the IEC
 * binary suffixes the Go side uses for memory ("256.0MiB", "1.2GiB"). Returns
 * null for unmeasured or unparseable values, which sort last.
 */
export function parseQuantity(value: string): number | null {
  // Longer suffixes must come before their prefixes in the alternation so the
  // regex picks "MiB" rather than stopping at "Mi" and leaving a stray "B".
  const match = /^([\d.]+)(m|KiB|MiB|GiB|TiB|PiB|Ki|Mi|Gi|Ti|Pi|k|K|M|G|B)?$/.exec(value.trim())
  if (!match) return null

  return Number.parseFloat(match[1]) * (MULTIPLIERS[match[2] ?? ''] ?? 1)
}

/**
 * Parses a server-printed age ("45s", "36h", "5d12h", "2y") into seconds.
 * Generic table columns arrive as display strings, so without this a date
 * column would order "5d" ahead of "12h". Returns null when the value is not
 * an age at all.
 */
export function parseAgeSeconds(value: string): number | null {
  const trimmed = value.trim()
  if (!trimmed) return null


  let total = 0
  let consumed = ''
  for (const part of trimmed.matchAll(/(\d+)([smhdy])/g)) {
    total += Number.parseInt(part[1], 10) * (UNIT_SECONDS[part[2]] ?? 0)
    consumed += part[0]
  }
  // Reject partial parses: "5d ago" is a sentence, not an age.
  return consumed === trimmed ? total : null
}
