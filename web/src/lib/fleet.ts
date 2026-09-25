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

import type { K8sEvent, Pod, TableColumn, TableRow, Workload } from './api/client'
import type { Tone } from './format'
import { tokenize } from './query'

/**
 * The merged tables.
 *
 * Three of them are typed reads with a fixed shape. The fourth is ANY KIND —
 * whatever the operator picks, read through the same generic table path a
 * single cluster's list uses, which is what lets a CRD nobody wrote code for
 * be read across six clusters at once.
 */
export type FleetTab = 'pods' | 'workloads' | 'events' | 'kinds'

/**
 * The tabs that offer quick-filter chips: the typed three.
 *
 * The generic table has none and cannot: a chip is a claim about health, and
 * the columns of an arbitrary kind are whatever that CRD's author chose to
 * print. Naming the exclusion in the type is what stops a Record keyed by tab
 * quietly gaining an empty entry nothing can fill.
 */
export type FleetChipTab = Exclude<FleetTab, 'kinds'>

export const FLEET_TABS: ReadonlyArray<{ id: FleetTab; label: string }> = [
  { id: 'pods', label: 'Pods' },
  { id: 'workloads', label: 'Workloads' },
  { id: 'events', label: 'Events' },
  { id: 'kinds', label: 'Any kind' },
]

/** Mirrors `domain.ClusterReadStatus` — see app/domain/fleet.go for what
    each one means and why they are told apart. */
export type ClusterReadStatus =
  | 'ok'
  | 'partial'
  | 'slow'
  | 'forbidden'
  | 'unreachable'
  | 'failed'
  | 'unserved'

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

    if (replacesRows(read)) {
      return { ...head, rows: read.items, rowsAt: now, stale: false }
    }
    if (read.status === 'slow' || read.status === 'unreachable') {
      return { ...head, rows: kept, rowsAt: last?.rowsAt ?? null, stale: kept.length > 0 }
    }
    return { ...head, rows: [], rowsAt: null, stale: false }
  })
}

/**
 * Whether this read replaces the cluster's rows, or leaves the kept ones up.
 *
 * EXPORTED SO NOTHING HAS TO RE-DERIVE IT. Anything stored per cluster
 * BESIDE the rows — the columns they are positioned against, whether the read
 * stopped at its cap — has to change on exactly the ticks the rows do, or the
 * caveat and the rows it qualifies come apart: a cluster's kept rows sitting
 * under a cap that was cleared by somebody else's answer, or a fresh complete
 * answer still carrying the previous tick's warning. One predicate, used by
 * mergeFleet and by whatever else travels with a cluster's share.
 */
export function replacesRows<T>(read: ClusterRead<T>): boolean {
  if (read.status === 'ok' || read.status === 'partial') return true
  return read.status === 'slow' && read.items.length > 0
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

/**
 * A merged generic table: one column set, and rows from every cluster.
 *
 * TWO CLUSTERS DO NOT NECESSARILY PRINT THE SAME COLUMNS. The columns come
 * from each API server's own table printer, and a CRD installed at v1alpha1
 * on one cluster and v1 on another routinely prints a different set — which
 * is exactly the case this whole tab exists for. So the columns are UNIONED
 * BY NAME rather than taken from whichever cluster answered first, and a row
 * from a cluster that has no such column shows an empty cell rather than the
 * value of whatever column happened to sit at that index.
 *
 * Positional cells are the trap here: `cells[2]` means "Status" on one
 * cluster and "Age" on another, so re-indexing every row into the merged
 * order is not a nicety, it is the difference between a table and a lie.
 */
export interface MergedTable {
  columns: TableColumn[]
  rows: FleetRow<TableRow>[]
}

export function mergeFleetTable(
  answers: readonly ClusterAnswer<TableRow>[],
  columnsByCluster: Readonly<Record<string, TableColumn[]>>,
): MergedTable {
  const columns: TableColumn[] = []
  const indexOf = new Map<string, number>()

  // First seen wins the position, so the first answering cluster's order is
  // the table's order and later clusters only ever append.
  for (const answer of answers) {
    for (const column of columnsByCluster[answer.cluster] ?? []) {
      if (indexOf.has(column.name)) continue
      indexOf.set(column.name, columns.length)
      columns.push(column)
    }
  }

  const rows: FleetRow<TableRow>[] = []
  for (const answer of answers) {
    const own = columnsByCluster[answer.cluster] ?? []

    // The mapping from this cluster's cell positions to the merged ones,
    // computed once per cluster rather than once per row.
    const positions = own.map((column) => indexOf.get(column.name) ?? -1)

    for (const row of answer.rows) {
      const cells = new Array<string>(columns.length).fill('')
      for (const [index, cell] of (row.cells ?? []).entries()) {
        const at = positions[index]
        if (at !== undefined && at >= 0) cells[at] = cell
      }
      rows.push({ ...row, cells, cluster: answer.cluster })
    }
  }

  return { columns, rows }
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
  // NEUTRAL, NOT A WARNING. A cluster without the CRD is a correct answer to
  // "list Widgets everywhere", and colouring it amber would make the ordinary
  // shape of a fleet — an operator installed on two clusters of six — look
  // like four things going wrong.
  unserved: { tone: 'neutral', label: 'Not installed' },
}

/**
 * The status strip: one entry per cluster, in tab order.
 *
 * `silent` names the clusters that are not answering, which the read cannot
 * find out for itself.
 *
 * A LIST THAT SUCCEEDED IS NOT PROOF THE CLUSTER IS THERE. On a watched kind
 * the Go side answers from an in-memory informer store without touching the
 * network, so a cluster whose VPN went away goes on returning a confident 188
 * pods, with an ok verdict, for as long as its reflector takes to notice its
 * stream is dead — which is the same staleness OverviewService.assess was
 * taught to see through by making /version authoritative. The strip cannot
 * ask /version, but something already has: the workspace pings every cluster
 * whose tab is not in front, and the tab in front records contact on every
 * refresh. So the fact is brought in rather than re-derived.
 *
 * The Go verdict is left saying exactly what it knows — the read succeeded —
 * and the chip says what the operator needs, which is a question about the
 * CLUSTER rather than about one read of it.
 */
export function stripModel<T>(
  answers: readonly ClusterAnswer<T>[],
  now: number,
  silent: ReadonlySet<string> = new Set(),
): FleetStripEntry[] {
  return answers.map((answer) => {
    const quiet = silent.has(answer.cluster)
    const status: ClusterReadStatus = quiet ? 'unreachable' : answer.status
    const { tone, label } = STRIP_TONES[status]

    // NO AGE FOR A SILENT CLUSTER, rather than a reassuring zero. A read
    // served from a store is stamped with the moment it was served, so the
    // honest answer to "how old are these rows" is that nobody knows.
    const ageSeconds =
      quiet || answer.rowsAt === null
        ? null
        : Math.max(0, Math.round((now - answer.rowsAt) / 1000))

    return {
      cluster: answer.cluster,
      status,
      tone,
      label,
      rows: answer.rows.length,
      // Whatever is on screen for a cluster that is not answering was read
      // before it stopped, whether or not the last read said so.
      stale: quiet ? answer.rows.length > 0 : answer.stale,
      ageSeconds,
      title: quiet ? silentTitle(answer) : stripTitle(answer, ageSeconds),
    }
  })
}

/** The sentence for a cluster nothing can reach, whatever its last read said. */
function silentTitle<T>(answer: ClusterAnswer<T>): string {
  const count = `${answer.rows.length} row${answer.rows.length === 1 ? '' : 's'}`
  if (answer.rows.length === 0) return `${answer.cluster} — not answering`
  return `${answer.cluster} — not answering; showing ${count} read before it stopped`
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
    case 'unserved':
      return `${answer.cluster} — this kind is not installed here`
    default:
      return `${answer.cluster} — ${answer.reason}`
  }
}

/**
 * Which clusters the merged table is showing, as the strip's chips set it.
 *
 * THE CHIPS USED TO WRITE `cluster:` TERMS INTO THE SEARCH BOX, and that was
 * a control lying about what it could do. Query terms are ANDed — see
 * matches() in $lib/query — and a row belongs to exactly one cluster, so
 * pressing two chips produced `cluster:a cluster:b`, which matches nothing.
 * Both chips rendered pressed, over an empty table, with `aria-pressed`
 * telling a screen reader they were both on. A control shaped like a
 * multi-select has to be one.
 *
 * EMPTY MEANS EVERY CLUSTER, which is what keeps the resting state the one
 * an operator already has: no chip pressed, every cluster in the table. It
 * also means "all of them selected" and "none of them selected" cannot be
 * two different-looking states that show the same rows — selecting the last
 * one normalises back to empty. See toggleClusterSelection.
 *
 * The typed `cluster:` term still works and is untouched: it is a search,
 * this is a selection, and the search box remains the place a narrowing
 * somebody typed can be seen and edited.
 */
export function includesCluster(selection: readonly string[], cluster: string): boolean {
  return selection.length === 0 || selection.includes(cluster)
}

/**
 * Adds or removes one cluster, keeping the "empty means all" invariant.
 *
 * Three rules, in the order they are reached:
 *
 *   - Pressing a chip while nothing is selected ISOLATES that cluster, which
 *     is what pressing one chip did before and what an operator expects from
 *     a row of filters that are all off.
 *   - Pressing another adds it. This is the case that was broken.
 *   - Selecting every open cluster is the same table as selecting none, so it
 *     collapses to none rather than leaving every chip lit.
 */
export function toggleClusterSelection(
  selection: readonly string[],
  cluster: string,
  open: readonly string[],
): string[] {
  const next = selection.includes(cluster)
    ? selection.filter((id) => id !== cluster)
    : [...selection, cluster]

  return liveClusterSelection(next, open)
}

/**
 * The selection as it applies RIGHT NOW: only clusters still open, and
 * nothing at all when that is all of them.
 *
 * WHY THIS IS SEPARATE FROM THE TOGGLE. The rule above — a tab closed while
 * its chip was pressed must not go on filtering a table it has no rows in —
 * was applied only when somebody pressed a chip, so between closing that tab
 * and the next press it was simply not true. The table narrowed to a cluster
 * with no rows in it and emptied, the strip showed no chip pressed because
 * the chip's cluster was gone, and the empty state read "No pods across the
 * 1 selected cluster" — naming a selection nothing on screen could show or
 * release. Read through this, the selection is a function of what is open,
 * so it cannot describe a tab that is not there.
 *
 * The stored selection is left as it is rather than pruned: reopening that
 * cluster brings its chip back pressed, which is at least a state the strip
 * can display and the operator can undo.
 */
export function liveClusterSelection(selection: readonly string[], open: readonly string[]): string[] {
  const live = selection.filter((id) => open.includes(id))
  return live.length === open.length ? [] : live
}

/** What a scrolling strip can still reveal, in each direction. */
export interface StripScroll {
  overflowing: boolean
  atStart: boolean
  atEnd: boolean
}

/**
 * Reads a scroller's geometry into the two facts the arrows and the mask need.
 *
 * A PURE FUNCTION OF THREE NUMBERS, extracted from the view so the rule can
 * be tested without a layout engine. The slack of one pixel in each
 * comparison is not sloppiness: a scroll position is fractional on a
 * trackpad and on a display with a non-integer device ratio, so `scrollLeft
 * === 0` is a test that a strip scrolled fully back to the left can fail —
 * leaving an arrow pointing at nothing.
 */
export function stripScrollState(metrics: {
  scrollWidth: number
  clientWidth: number
  scrollLeft: number
}): StripScroll {
  const slack = metrics.scrollWidth - metrics.clientWidth
  if (slack <= 1) return { overflowing: false, atStart: true, atEnd: true }

  return {
    overflowing: true,
    atStart: metrics.scrollLeft <= 1,
    atEnd: metrics.scrollLeft >= slack - 1,
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
