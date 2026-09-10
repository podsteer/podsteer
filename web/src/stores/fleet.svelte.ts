/**
 * The "All clusters" view's data: every open cluster's pods, workloads or
 * events, read together and kept per cluster.
 *
 * WORKSPACE-LEVEL, NOT PER TAB. A ClusterSession holds one tab's state and
 * these rows are nobody's tab's — they are the workspace's, which is why
 * switching tabs does not refetch them and why the merged table an operator
 * left in one tab is the one they find in the next. What IS per tab — the
 * search, the sort, the page, the chips — stays on the session, which reads
 * `podRows` / `workloadRows` / `eventRows` from here the way it reads its
 * own `pods`.
 *
 * NOTHING HERE POLLS. There is no timer in this module. `refresh` is called
 * from `ClusterSession.#fetch` when, and only when, the session's own poll
 * fires with the fleet view selected — and a session polls only while its
 * tab is in front (ClusterWorkspace mounts one, keyed on the active tab).
 * So the fan-out runs at the tab's refresh cadence while the merged table is
 * on screen, and not at all when it is not: no background cost for a view
 * nobody is looking at, and no second cadence to reason about.
 *
 * `openClusters` is assigned by $stores/workspace rather than imported from
 * it: $stores/session imports this module for its rows, and $stores/workspace
 * imports $stores/session, so a fleet that imported the workspace would
 * close a circle.
 */

import {
  listFleetEvents,
  listFleetPods,
  listFleetTable,
  listFleetWorkloads,
  type ClusterEvents,
  type ClusterPods,
  type ClusterTable,
  type ClusterWorkloads,
  type K8sEvent,
  type Pod,
  type TableColumn,
  type TableRow,
  type Workload,
} from '$lib/api/client'
import {
  flattenFleet,
  mergeFleet,
  mergeFleetTable,
  replacesRows,
  stripModel,
  type ClusterAnswer,
  type ClusterRead,
  type ClusterReadStatus,
  type FleetStripEntry,
  type FleetTab,
} from '$lib/fleet'
import type { LoadStatus } from './session.svelte'
import { timeline } from './timeline.svelte'

/** Lifts a per-kind wire answer into the shape the merge rules read. */
function asRead<T>(
  answer: { cluster: string; status: string; reason: string; missing: string[] | null },
  items: T[] | null | undefined,
): ClusterRead<T> {
  return {
    cluster: answer.cluster,
    status: answer.status as ClusterReadStatus,
    reason: answer.reason,
    // Never nil on the wire — see readHeader in app/adapters/wails — but
    // the generated model does not promise it, and a guard is cheaper than
    // a crash in a status strip.
    missing: answer.missing ?? [],
    items: items ?? [],
  }
}

/**
 * The kind the "Any kind" tab is pointed at.
 *
 * GROUP AND RESOURCE ADDRESS IT — a kind id carries a version and a version
 * is per-cluster — while `kind` and `title` are what the interface says about
 * it: the Kubernetes Kind is what the drawer resolves a row by, and a generic
 * row cannot say what it is on its own.
 */
export interface FleetKind {
  group: string
  resource: string
  kind: string
  title: string
}

class Fleet {
  /** Which merged table is showing. Remembered across tabs, like the rows. */
  tab = $state<FleetTab>('pods')

  /**
   * The ids of every open tab, in tab order. Assigned by $stores/workspace —
   * see the module comment. The backend refuses a cluster that is not open,
   * so this is the workspace's mirror of the registry and nothing else.
   */
  openClusters: () => string[] = () => []

  /**
   * The ids of the open clusters that are NOT answering. Assigned by
   * $stores/workspace, like openClusters and for the same reason: importing
   * it here would close a circle through $stores/session.
   *
   * The strip needs this because a fleet read cannot discover it — see
   * stripModel in $lib/fleet.
   */
  silentClusters: () => string[] = () => []

  /** Each cluster's last answer, per table, in tab order. */
  pods = $state.raw<ClusterAnswer<Pod>[]>([])
  workloads = $state.raw<ClusterAnswer<Workload>[]>([])
  events = $state.raw<ClusterAnswer<K8sEvent>[]>([])

  /**
   * The rows of the chosen arbitrary kind, and the columns each cluster
   * printed for them.
   *
   * TWO PIECES OF STATE RATHER THAN ONE, because they go stale together but
   * arrive apart: mergeFleet keeps a slow cluster's previous rows, and those
   * rows are positioned against the columns that came WITH them. Keeping the
   * columns per cluster — and only replacing a cluster's set when that
   * cluster answers with one — is what stops a kept row being re-indexed
   * against somebody else's headings.
   */
  tableRows = $state.raw<ClusterAnswer<TableRow>[]>([])
  tableColumns = $state.raw<Record<string, TableColumn[]>>({})

  /**
   * Which clusters' shares of the merged table stopped at their cap, and at
   * what.
   *
   * PER CLUSTER, because the cap is per read: one cluster can be complete and
   * the next a prefix of the same kind, and one flag for the whole table
   * could only be wrong in one direction or the other. Kept beside the rows
   * and the columns for the reason they are — it is a fact about a cluster's
   * answer, and it has to travel with it.
   */
  tableTruncated = $state.raw<Record<string, number>>({})

  /**
   * Which kind the "Any kind" tab is showing: its group and resource, and
   * the title to put above it.
   *
   * Null until something is chosen, which is what the tab shows a picker for
   * rather than guessing at a first kind — a cross-cluster list of whatever
   * sorted first is a read of six clusters nobody asked for.
   */
  tableKind = $state<FleetKind | null>(null)

  status = $state<LoadStatus>('idle')
  /** When the last read landed, in ms since the epoch. */
  lastReadAt = $state<number | null>(null)

  /** Which request is the current one, so an older answer cannot land on a
      newer one — the same guard ClusterSession.refresh uses. */
  #generation = 0

  /** Every cluster's rows in one list, each stamped with its cluster. */
  readonly podRows = $derived(flattenFleet(this.pods))
  readonly workloadRows = $derived(flattenFleet(this.workloads))
  readonly eventRows = $derived(flattenFleet(this.events))

  /** The chosen kind's rows and columns, merged across clusters. */
  readonly table = $derived(mergeFleetTable(this.tableRows, this.tableColumns))

  /** The status strip for the table showing. Ages are measured from the
      last read rather than the wall clock so the strip is a pure function
      of state: it changes when a read lands, not every second. */
  readonly strip = $derived.by<FleetStripEntry[]>(() => {
    const now = this.lastReadAt ?? 0
    const silent = new Set(this.silentClusters())
    switch (this.tab) {
      case 'pods':
        return stripModel(this.pods, now, silent)
      case 'workloads':
        return stripModel(this.workloads, now, silent)
      case 'events':
        return stripModel(this.events, now, silent)
      case 'kinds':
        return stripModel(this.tableRows, now, silent)
    }
  })

  /** How many of the strip's clusters did not answer in full. */
  readonly degraded = $derived(this.strip.filter((entry) => entry.status !== 'ok').length)

  /**
   * Reads the showing table across every open cluster, once.
   *
   * Throws only for what the backend refuses whole — a cluster that is not
   * open, a bad namespace — so the session's own error banner reports it
   * the way it reports any other failed refresh. A cluster that refused or
   * did not answer is not a throw: it is a chip in the strip.
   */
  /**
   * Points the "Any kind" tab at a kind, and forgets the last one's rows.
   *
   * CLEARED RATHER THAN LEFT TO BE REPLACED: the next read has not happened
   * yet, and one tick of the previous kind's rows under the new kind's name
   * is a table that is wrong rather than merely old.
   */
  chooseKind = (kind: FleetKind | null): void => {
    this.tableKind = kind
    this.tableRows = []
    this.tableColumns = {}
    this.tableTruncated = {}
  }

  refresh = async (namespace: string): Promise<void> => {
    const ids = this.openClusters()
    const tab = this.tab
    const generation = ++this.#generation
    this.status = 'loading'

    try {
      switch (tab) {
        case 'pods': {
          const answers = await listFleetPods(ids, namespace)
          if (generation !== this.#generation) return
          this.pods = mergeFleet(
            this.pods,
            answers.map((answer: ClusterPods) => asRead(answer, answer.pods)),
            Date.now(),
          )
          break
        }
        case 'workloads': {
          const answers = await listFleetWorkloads(ids, namespace)
          if (generation !== this.#generation) return
          this.workloads = mergeFleet(
            this.workloads,
            answers.map((answer: ClusterWorkloads) => asRead(answer, answer.workloads)),
            Date.now(),
          )
          break
        }
        case 'events': {
          const answers = await listFleetEvents(ids, namespace)
          if (generation !== this.#generation) return
          // Filed on each cluster's own timeline on the way past. The merged
          // table is the one view that reads events for a cluster whose tab
          // is not in front, and these already crossed the bridge — so a tab
          // switched back to later finds what happened while it was behind.
          for (const answer of answers) {
            timeline.recordEvents(answer.cluster, answer.events ?? [])
          }
          this.events = mergeFleet(
            this.events,
            answers.map((answer: ClusterEvents) => asRead(answer, answer.events)),
            Date.now(),
          )
          break
        }
        case 'kinds': {
          const kind = this.tableKind
          // Nothing chosen is not an empty read: it is no read at all, and
          // six clusters are not asked anything until somebody names a kind.
          if (!kind) {
            this.status = 'ready'
            return
          }

          const answers = await listFleetTable(ids, kind.group, kind.resource, namespace)
          if (generation !== this.#generation) return

          // A cluster's columns are replaced only when that cluster answered
          // with some — see tableColumns for why they must stay with the rows
          // they came from.
          const columns = { ...this.tableColumns }
          // Replaced per cluster, exactly as the columns are: a cluster that
          // did not answer this tick keeps whatever was last true of it,
          // rather than having its caveat cleared by somebody else's read.
          const truncated = { ...this.tableTruncated }
          for (const answer of answers) {
            if (answer.columns?.length) columns[answer.cluster] = answer.columns
            // Changed on exactly the ticks mergeFleet replaces this cluster's
            // ROWS — see replacesRows. A caveat that can be cleared while the
            // rows it qualifies are still on screen is worse than none.
            if (replacesRows(asRead(answer, answer.rows))) {
              if (answer.truncated) truncated[answer.cluster] = answer.cap
              else delete truncated[answer.cluster]
            }
          }
          this.tableColumns = columns
          this.tableTruncated = truncated

          this.tableRows = mergeFleet(
            this.tableRows,
            answers.map((answer: ClusterTable) => asRead(answer, answer.rows)),
            Date.now(),
          )
          break
        }
      }
      this.status = 'ready'
      this.lastReadAt = Date.now()
    } catch (cause) {
      if (generation === this.#generation) this.status = 'error'
      throw cause
    }
  }
}

/** The application-wide merged tables. A module singleton for the same
    reason the workspace is one: there is one window, and one set of open
    tabs to merge. */
export const fleet = new Fleet()
