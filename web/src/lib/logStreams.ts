/**
 * Which log streams a pane should open, given what the operator chose.
 *
 * EXTRACTED BECAUSE THE PANE GOT IT WRONG SILENTLY. The container control
 * offers "All", and the pane resolved it as `selectedContainer ||
 * pod.containers[0]` — so "All" streamed the FIRST container of each pod and
 * said nothing. On any pod with a sidecar (a mesh proxy, a log shipper, a
 * cloud-SQL proxy) the operator was reading one container and being told they
 * were reading all of them. A wrong answer that looks like a right one is the
 * worst kind, and it survived because this decision lived inside a 1400-line
 * component with no test.
 *
 * Kubernetes has no server-side "all containers" for logs: `PodLogOptions`
 * names exactly one container, and `kubectl logs --all-containers` opens one
 * request per container client-side. So does this.
 */

/** A pod as the pane knows it: its name and the containers it declares. */
export interface LogPod {
  name: string
  containers: string[]
}

/** One stream to open. */
export interface PlannedStream {
  pod: string
  container: string
}

export interface LogStreamPlan {
  /** The streams to open, in pod order then container order. */
  streams: PlannedStream[]
  /**
   * Pods that do not have the chosen container.
   *
   * A named container is picked from the UNION across pods, so a selection
   * that fits one pod can be absent from another — asking for it anyway earns
   * "container X is not valid for pod Y" from the API server, once per pod.
   * Naming them is better than either hiding them or failing on them.
   */
  missing: string[]
  /**
   * How many streams the cap left unopened.
   *
   * Streaming every container of every pod of a large workload is N×M
   * requests against a client that shares its rate limiter with the tab's
   * polling. The cap exists so the logs pane cannot starve the view behind
   * it, and a plan that hit the cap has to SAY so — a truncated read that
   * looks complete is the same fault this file exists to fix.
   */
  truncated: number
}

/** The most streams one pane opens at once. */
export const MAX_LOG_STREAMS = 40

/**
 * Plans the streams for a selection.
 *
 * `selectedContainer` empty means every container of every pod. A pod with no
 * containers contributes nothing rather than an empty stream name, which the
 * API server would reject.
 *
 * THE CAP APPLIES ONLY TO "ALL", and that limit on the limit matters. Naming a
 * container has always opened one stream per pod, however many pods there are;
 * capping that here would quietly stop showing logs somebody was already
 * getting, which is the same class of fault as "All" streaming one container.
 * What is new is the MULTIPLICATION — every container of every pod — and that
 * is what the cap exists to bound.
 */
export function planLogStreams(
  pods: LogPod[],
  selectedContainer: string,
  cap: number = MAX_LOG_STREAMS,
): LogStreamPlan {
  const wanted: PlannedStream[] = []
  const missing: string[] = []

  for (const pod of pods) {
    const containers = pod.containers ?? []
    if (selectedContainer === '') {
      for (const container of containers) wanted.push({ pod: pod.name, container })
      continue
    }
    if (containers.includes(selectedContainer)) {
      wanted.push({ pod: pod.name, container: selectedContainer })
    } else {
      missing.push(pod.name)
    }
  }

  if (selectedContainer !== '') {
    return { streams: wanted, missing, truncated: 0 }
  }

  const limit = Math.max(0, cap)
  return {
    streams: wanted.slice(0, limit),
    missing,
    truncated: Math.max(0, wanted.length - limit),
  }
}

/** The key a stream is held under while it runs. */
export function streamLabel(pod: string, container: string): string {
  return `${pod}/${container}`
}
