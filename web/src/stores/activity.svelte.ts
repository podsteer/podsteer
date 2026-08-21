/**
 * When each cluster was last connected to.
 *
 * The picker's job is to help someone choose, and on a machine with forty
 * contexts the most useful thing to know about one is whether you have ever
 * used it and how recently. That is the question this answers.
 *
 * It replaced the server version and platform, which were worse than useless
 * there: both are read FROM a cluster, so neither exists until you have
 * already connected — the line was empty on every card at exactly the moment
 * the picker is being read.
 *
 * ONE timestamp serves both phrasings, and it has to be persisted rather than
 * held for the session. `workspace.initialise` adopts connections the backend
 * already has, so a reloaded frontend finds clusters open that it never opened
 * itself; an in-memory "opened at" would be missing for precisely those, and
 * "Connected for" would have nothing to count from.
 *
 * Stored in localStorage for the same reasons the organisation is: per
 * machine, no credentials, and it must not stall startup on an IPC round trip.
 */

const STORAGE_KEY = 'podsteer.activity.v1'

interface PersistedShape {
  /** Cluster context name -> epoch milliseconds of the last connection. */
  connectedAt: Record<string, number>
}

class ClusterActivity {
  #connectedAt = $state<Record<string, number>>({})

  constructor() {
    this.#load()
  }

  /** When this cluster was last connected to, or null if it never has been. */
  connectedAt = (clusterId: string): number | null => this.#connectedAt[clusterId] ?? null

  /**
   * Records a connection, as of now.
   *
   * Written when the connection is MADE rather than when it ends, so the value
   * reads the same whether the cluster is still open ("connected for") or was
   * closed a while ago ("last connected").
   */
  markConnected = (clusterId: string): void => {
    this.#connectedAt = { ...this.#connectedAt, [clusterId]: Date.now() }
    this.#save()
  }

  #load(): void {
    try {
      const raw = localStorage.getItem(STORAGE_KEY)
      if (!raw) return

      const stored = JSON.parse(raw) as Partial<PersistedShape>
      if (!stored.connectedAt || typeof stored.connectedAt !== 'object') return

      const clean: Record<string, number> = {}
      for (const [clusterId, at] of Object.entries(stored.connectedAt)) {
        // A timestamp in the future is a clock that moved, not a fact; it
        // would render as "connected for —" and confuse rather than inform.
        if (typeof at === 'number' && Number.isFinite(at) && at > 0 && at <= Date.now()) {
          clean[clusterId] = at
        }
      }
      this.#connectedAt = clean
    } catch {
      // Corrupt storage must not stop the app starting. Every cluster simply
      // reads as never connected, which is the state a new machine is in.
    }
  }

  #save(): void {
    try {
      const payload: PersistedShape = { connectedAt: this.#connectedAt }
      localStorage.setItem(STORAGE_KEY, JSON.stringify(payload))
    } catch {
      // The figures still hold for this session; only persistence is lost.
    }
  }
}

/** When each cluster was last connected to, shared by the picker. */
export const clusterActivity = new ClusterActivity()
