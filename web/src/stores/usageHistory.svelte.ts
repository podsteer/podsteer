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

/** Identifies one object across refreshes. */
export function usageKey(
  kind: 'pod' | 'node' | 'workload' | 'namespace' | 'application',
  namespace: string,
  name: string,
): string {
  return `${kind}:${namespace}/${name}`
}

class UsageHistory {
  /**
   * Plain Map, not `$state`. Nothing renders from this directly — the drawer
   * copies out of it once, when it opens — so making it reactive would put
   * every list refresh through the reactivity graph to update something
   * nobody is watching.
   */
  #series = new Map<string, UsageSample[]>()

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
