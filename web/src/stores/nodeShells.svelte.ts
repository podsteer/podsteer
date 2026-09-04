/**
 * The node shells running right now.
 *
 * A VIEW OVER WHAT THE BACKEND IS ACTUALLY HOLDING, the same discipline as
 * forwards.svelte.ts and for the same reason: a node shell is a privileged pod
 * PodSteer created, and the one thing that must never happen is the record of
 * it and the pod itself parting company. So this store never invents an entry
 * — it asks the backend what pods still exist, and the backend's list is the
 * live registry.
 *
 * A node shell is stopped by deleting its pod, which the terminal session
 * ending already does; the stop control here is the same delete, for when the
 * operator wants to end one without going back to its terminal.
 */

import { listNodeShells, stopNodeShell, stopAllNodeShells, type NodeShell } from '$lib/api/client'
import { toApiError } from '$lib/api/errors'

class NodeShells {
  /** Everything running right now, across every cluster. */
  active = $state.raw<NodeShell[]>([])

  /** The last failure, for the surface that asked. Cleared by the next attempt. */
  error = $state<string>('')

  /** Which shells are mid-stop, keyed by id so one button can spin. */
  busy = $state.raw<Set<string>>(new Set())

  /** Whether "Stop all" is in flight. */
  stoppingAll = $state(false)

  /**
   * Re-reads the list on a slow tick while anything is running.
   *
   * A node shell can vanish without this store asking — its pod hit the
   * one-hour deadline, or its terminal session ended elsewhere — so polling is
   * how that reaches the screen, and only while something is open.
   */
  watch(): () => void {
    const timer = setInterval(() => {
      if (this.active.length > 0) void this.refresh()
    }, 3000)
    return () => clearInterval(timer)
  }

  async refresh(): Promise<void> {
    try {
      this.active = await listNodeShells()
    } catch {
      // A failure to LIST is not worth a banner: the list is a convenience
      // over state the backend owns, and the next change refreshes it.
    }
  }

  async stop(shell: NodeShell): Promise<void> {
    this.#setBusy(shell.id, true)
    try {
      await stopNodeShell(shell.id)
      await this.refresh()
    } catch (cause) {
      this.error = toApiError(cause).message
    } finally {
      this.#setBusy(shell.id, false)
    }
  }

  async stopAll(): Promise<void> {
    if (this.active.length === 0) return
    this.stoppingAll = true
    try {
      await stopAllNodeShells()
      await this.refresh()
    } catch (cause) {
      this.error = toApiError(cause).message
    } finally {
      this.stoppingAll = false
    }
  }

  isBusy(id: string): boolean {
    return this.busy.has(id)
  }

  #setBusy(id: string, busy: boolean): void {
    const next = new Set(this.busy)
    if (busy) next.add(id)
    else next.delete(id)
    this.busy = next
  }
}

/** The node shells running right now, shared by the activity list. */
export const nodeShells = new NodeShells()
