/**
 * The port-forwards running right now.
 *
 * A VIEW OVER WHAT THE BACKEND IS ACTUALLY HOLDING, not a record of what was
 * asked for. Every leak and lie in the competing clients comes from those two
 * being separate things: a forward shows as active after its connection died,
 * the stop button does nothing because there is nothing left to stop, and the
 * local port stays bound with nothing managing it.
 *
 * So this store never invents an entry. It asks the backend what exists after
 * every change, and the backend's list is the live registry of goroutines
 * holding sockets.
 */

import { listPortForwards, startPortForward, stopPortForward, type PortForward } from '$lib/api/client'
import { toApiError } from '$lib/api/errors'

class Forwards {
  /** Everything forwarded right now, across every cluster. */
  active = $state.raw<PortForward[]>([])

  /** The last failure, for the surface that asked. Cleared by the next attempt. */
  error = $state<string>('')

  /** Whether a start or stop is in flight, keyed so one button can spin. */
  busy = $state.raw<Set<string>>(new Set())

  /**
   * Whether this pod's port is already forwarded, and by which forward.
   *
   * Keyed on the pod and the REMOTE port rather than the local one: the
   * question a button asks is "is this container port already open
   * somewhere", and the local port is the answer to it, not part of it.
   */
  forPort(namespace: string, pod: string, remotePort: number): PortForward | undefined {
    return this.active.find(
      (forward) =>
        forward.namespace === namespace &&
        forward.pod === pod &&
        forward.remotePort === remotePort,
    )
  }

  /**
   * Re-reads the list on a slow tick while anything is forwarded.
   *
   * A forward can change underneath the UI without anything here asking: its
   * pod dies and the supervisor starts looking for a replacement, or gives up
   * and removes it. Polling is how that reaches the screen — and only while
   * something is open, so an application with no forwards does no work.
   */
  watch(): () => void {
    const timer = setInterval(() => {
      if (this.active.length > 0) void this.refresh()
    }, 3000)
    return () => clearInterval(timer)
  }

  async refresh(): Promise<void> {
    try {
      this.active = await listPortForwards()
    } catch {
      // A failure to LIST forwards is not worth a banner: the list is a
      // convenience over state the backend owns, and the next change refreshes
      // it. Leaving the previous list is better than blanking it.
    }
  }

  async start(
    clusterId: string,
    namespace: string,
    pod: string,
    podUID: string,
    remotePort: number,
    portName: string,
    protocol: string,
    /** The pod's own labels, so a replacement can be found if it dies. */
    selector: Record<string, string>,
  ): Promise<void> {
    const key = `${namespace}/${pod}/${remotePort}`
    this.#setBusy(key, true)
    this.error = ''

    try {
      // Local port zero: the operating system chooses. Asking somebody to
      // pick a free port is asking them to guess, and the forward reports
      // back which one it actually bound.
      await startPortForward(
        clusterId,
        namespace,
        pod,
        podUID,
        0,
        remotePort,
        portName,
        protocol,
        selector,
      )
      await this.refresh()
    } catch (cause) {
      this.error = toApiError(cause).message
    } finally {
      this.#setBusy(key, false)
    }
  }

  async stop(forward: PortForward): Promise<void> {
    const key = `${forward.namespace}/${forward.pod}/${forward.remotePort}`
    this.#setBusy(key, true)

    try {
      await stopPortForward(forward.id)
      await this.refresh()
    } catch (cause) {
      this.error = toApiError(cause).message
    } finally {
      this.#setBusy(key, false)
    }
  }

  isBusy(namespace: string, pod: string, remotePort: number): boolean {
    return this.busy.has(`${namespace}/${pod}/${remotePort}`)
  }

  #setBusy(key: string, busy: boolean): void {
    const next = new Set(this.busy)
    if (busy) next.add(key)
    else next.delete(key)
    this.busy = next
  }
}

export const forwards = new Forwards()
