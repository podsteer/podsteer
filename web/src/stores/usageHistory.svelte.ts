/**
 * Recent usage for every object the lists have shown, kept in memory.
 *
 * WHY THIS EXISTS RATHER THAN A QUERY. metrics-server cannot be asked for
 * history: a PodMetrics is one point, carrying a Timestamp and the Window it
 * covers, and the API has no range parameter because the server keeps only
 * the latest scrape in memory. Its own documentation says not to use it as a
 * metrics source. So a chart that wanted five minutes of past had nowhere to
 * ask for it.
 *
 * But the application already fetches it. Every refresh of the pod or node
 * list carries usage for EVERY object in the list, and until now all of it
 * except the row being drawn was discarded. Keeping it costs no request, no
 * permission and no new failure mode — the measurements were already on the
 * wire — and means a drawer opened after five minutes of browsing has five
 * minutes of history in it.
 *
 * IN MEMORY AND NOWHERE ELSE. Not written to disk, deliberately: the cluster
 * history that IS on disk deliberately carries no object names, and a file of
 * per-pod series would reverse that. This dies with the process.
 */

import type { UsageSample } from './session.svelte'
import { preferences } from './preferences.svelte'

/**
 * The most samples kept for one object, whatever the window says.
 *
 * A ceiling on memory rather than on time. At a two-second refresh a
 * thirty-minute window would be nine hundred samples per object, and on a
 * cluster of five thousand pods that is tens of megabytes for a chart nobody
 * has opened. The window still bounds the AGE; this bounds the count.
 */
const MAX_SAMPLES_PER_OBJECT = 200

/**
 * How many records pass between sweeps of the key set.
 *
 * One tick records a sample per row on screen, so this is a sweep every few
 * refreshes on a busy list and rarely on a quiet one — which is the right
 * shape: keys accumulate in proportion to what has been browsed.
 */
const SWEEP_EVERY = 500

/**
 * Identifies one object across refreshes.
 *
 * THE CLUSTER IS PART OF THE IDENTITY, and leaving it out was a bug that
 * showed one cluster's numbers on another's object. This application holds
 * several clusters open at once, one per tab, and two of them routinely
 * contain a pod with the same name in a namespace with the same name — a
 * `web-abc` in `development` on staging and on production. Without the
 * cluster those two share one series, so opening the second pod draws a chart
 * built from the first's measurements interleaved with its own.
 *
 * That is the worst class of defect this application can have: not a missing
 * number but a plausible wrong one.
 */
export function usageKey(
  cluster: string,
  kind: 'pod' | 'node' | 'workload' | 'namespace' | 'application',
  namespace: string,
  name: string,
): string {
  return `${cluster}|${kind}:${namespace}/${name}`
}

class UsageHistory {
  /**
   * Plain Map, not `$state`. Nothing renders from this directly — the drawer
   * copies out of it once, when it opens — so making it reactive would put
   * every list refresh through the reactivity graph to update something
   * nobody is watching.
   */
  #series = new Map<string, UsageSample[]>()

  /**
   * Records since the last sweep of keys that have gone quiet.
   *
   * The SAMPLES in a series are bounded by the window and by
   * MAX_SAMPLES_PER_OBJECT, but nothing bounded the number of KEYS: an object
   * seen once kept its entry for the life of the process, so a long session
   * browsing several clusters accumulated an entry per object ever listed.
   * Sweeping on a counter rather than on every record keeps the cost
   * amortised — a full pass is proportional to the key set, and the key set
   * is what the pass exists to keep small.
   */
  #sinceSweep = 0

  /** How far back to keep, in milliseconds. Zero disables the whole thing. */
  get #windowMs(): number {
    return preferences.usageWindowMinutes * 60_000
  }

  /**
   * Records one measurement, dropping anything past the window.
   *
   * Called for every object in every refresh, so it is deliberately cheap:
   * one array push and, when the window is exceeded, a slice.
   */
  record(key: string, sample: UsageSample): void {
    const windowMs = this.#windowMs
    if (windowMs === 0) return

    const existing = this.#series.get(key) ?? []
    existing.push(sample)

    const cutoff = sample.at - windowMs
    let fresh = existing.filter((entry) => entry.at >= cutoff)
    if (fresh.length > MAX_SAMPLES_PER_OBJECT) {
      fresh = fresh.slice(-MAX_SAMPLES_PER_OBJECT)
    }

    this.#series.set(key, fresh)

    this.#sinceSweep++
    if (this.#sinceSweep >= SWEEP_EVERY) {
      this.#sinceSweep = 0
      this.#sweep(sample.at - windowMs)
    }
  }

  /** Drops series whose newest sample has aged out of the window entirely. */
  #sweep(cutoff: number): void {
    for (const [key, samples] of this.#series) {
      const newest = samples[samples.length - 1]
      if (!newest || newest.at < cutoff) this.#series.delete(key)
    }
  }

  /**
   * Forgets one cluster's history, for a tab being closed.
   *
   * Per-cluster rather than wholesale now that the key carries the cluster:
   * closing one tab must not blank the charts in another, which is what
   * clearing everything would do.
   */
  forget(cluster: string): void {
    const prefix = `${cluster}|`
    for (const key of this.#series.keys()) {
      if (key.startsWith(prefix)) this.#series.delete(key)
    }
  }

  /**
   * What is known about one object, oldest first.
   *
   * Filtered on the way out as well as on the way in: an object that stopped
   * appearing in the list keeps whatever it had, and a drawer opened on it an
   * hour later must not be handed an hour-old line as though it were current.
   */
  since(key: string): UsageSample[] {
    const windowMs = this.#windowMs
    if (windowMs === 0) return []

    const cutoff = Date.now() - windowMs
    return (this.#series.get(key) ?? []).filter((entry) => entry.at >= cutoff)
  }

  /**
   * Forgets everything, for a cluster being disconnected.
   *
   * Whole-map rather than per-object: the alternative is tracking which keys
   * belonged to which cluster, and two clusters can hold identically named
   * pods in identically named namespaces.
   */
  clear(): void {
    this.#series.clear()
  }
}

export const usageHistory = new UsageHistory()
