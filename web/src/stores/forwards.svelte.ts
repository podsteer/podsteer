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

import {
  listPortForwards,
  startPortForward,
  stopPortForward,
  stopAllPortForwards,
  type PortForward,
} from '$lib/api/client'
import { toApiError } from '$lib/api/errors'
import { preferences } from './preferences.svelte'

/**
 * Identifies one forwarded port, and THE CLUSTER IS PART OF IT.
 *
 * `active` holds every forward across every open cluster, which is what makes
 * leaving the cluster out a bug rather than an omission: PodSteer holds
 * several clusters open at once, and a StatefulSet gives them pods with
 * identical names in identically named namespaces. A forward on
 * `default/postgres-0` in staging therefore rendered as open on production's
 * row, showed production the wrong address, and — worst — let Stop tear down
 * the other cluster's forward.
 */
function forwardKey(cluster: string, namespace: string, pod: string, remotePort: number): string {
  return `${cluster}/${namespace}/${pod}/${remotePort}`
}

class Forwards {
  /** Everything forwarded right now, across every cluster. */
  active = $state.raw<PortForward[]>([])

  /** The last failure, for the surface that asked. Cleared by the next attempt. */
  error = $state<string>('')

  /** Whether a start or stop is in flight, keyed so one button can spin. */
  busy = $state.raw<Set<string>>(new Set())

  /** Whether "Stop all" is in flight, so it — and nothing else — spins. */
  stoppingAll = $state(false)

  /**
   * Whether this pod's port is already forwarded, and by which forward.
   *
   * Keyed on the pod and the REMOTE port rather than the local one: the
   * question a button asks is "is this container port already open
   * somewhere", and the local port is the answer to it, not part of it.
   */
  forPort(
    cluster: string,
    namespace: string,
    pod: string,
    remotePort: number,
  ): PortForward | undefined {
    return this.active.find(
      (forward) =>
        forward.clusterId === cluster &&
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

  /**
   * Everything forwarded from one pod.
   *
   * Asked by the pod LIST rather than by a port row, and it is the question a
   * reconnect makes urgent: the forward moves to a replacement pod, so the
   * row holding it is not the row it was started from, and with several
   * replicas of one workload there is otherwise nothing to tell them apart.
   */
  forPod(cluster: string, namespace: string, pod: string): PortForward[] {
    return this.active.filter(
      (forward) =>
        forward.clusterId === cluster &&
        forward.namespace === namespace &&
        forward.pod === pod,
    )
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
    /**
     * The local port to bind, or 0 to let the operating system choose.
     *
     * Defaulted to 0 rather than required: most callers still have no
     * opinion, and asking them to pass a port they do not care about would
     * make every existing call site type a zero it does not mean anything.
     */
    localPort = 0,
  ): Promise<void> {
    const key = forwardKey(clusterId, namespace, pod, remotePort)
    this.#setBusy(key, true)
    this.error = ''

    try {
      const forward = await startPortForward(
        clusterId,
        namespace,
        pod,
        podUID,
        localPort,
        remotePort,
        portName,
        protocol,
        selector,
      )
      // Remembered by remote port and by name, NEVER by pod, namespace or
      // cluster — preferences.rememberLocalPort's own signature has nowhere
      // to put them. Recorded with whatever actually bound, whether the
      // operator typed it or the operating system chose it: both are worth
      // proposing next time.
      preferences.rememberLocalPort(remotePort, portName, forward.localPort)
      await this.refresh()
    } catch (cause) {
      this.error = toApiError(cause).message
    } finally {
      this.#setBusy(key, false)
    }
  }

  async stop(forward: PortForward): Promise<void> {
    const key = forwardKey(forward.clusterId, forward.namespace, forward.pod, forward.remotePort)
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

  /**
   * Closes every running forward, across every cluster.
   *
   * One call rather than looping stop() per entry: the backend already tears
   * every forward down and waits for each port to be released in one pass
   * (StopAllPortForwards), and looping here would mean N round trips racing
   * refresh() N times for what is conceptually one action.
   */
  async stopAll(): Promise<void> {
    if (this.active.length === 0) return
    this.stoppingAll = true

    try {
      await stopAllPortForwards()
      await this.refresh()
    } catch (cause) {
      this.error = toApiError(cause).message
    } finally {
      this.stoppingAll = false
    }
  }

  isBusy(cluster: string, namespace: string, pod: string, remotePort: number): boolean {
    return this.busy.has(forwardKey(cluster, namespace, pod, remotePort))
  }

  #setBusy(key: string, busy: boolean): void {
    const next = new Set(this.busy)
    if (busy) next.add(key)
    else next.delete(key)
    this.busy = next
  }
}

export const forwards = new Forwards()
