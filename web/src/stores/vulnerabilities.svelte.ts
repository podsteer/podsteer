/**
 * The severity counts a scanner already running in the cluster has recorded,
 * for the pod list.
 *
 * NOTHING WAITS ON THIS, and that is the whole design. The pod list is drawn
 * from the poll exactly as it always was; this is a separate, bounded read
 * that fills a mark in when it arrives and is simply ABSENT when no scanner
 * is installed — which is most clusters. It never joins the refresh tick:
 * one read per cluster and namespace, held here for the life of the tab, in
 * front of a Go-side cache that holds it for ten minutes anyway (see
 * `app/adapters/k8s/trivy.go`).
 *
 * WHY IT IS NOT A COLUMN. A column exists on every cluster whether or not
 * anything fills it, so on the great majority — no Trivy Operator — it would
 * be a permanent empty column, and the operators who do run one would still
 * have to go and enable it. The mark beside the name appears only where there
 * is something to say, exactly as the findings mark and the port-forward mark
 * beside it do.
 *
 * PODSTEER SCANS NOTHING. Every number here was computed by the operator and
 * written into a VulnerabilityReport before this read it.
 */

import { vulnerabilitySummaries, type VulnerabilitySummary } from '$lib/api/client'

/** One cluster-and-namespace's answer, keyed by the subject's "Kind/name". */
type Summaries = Record<string, VulnerabilitySummary>

/** What has been read, and what is being read right now. */
const loaded = $state<Record<string, Summaries>>({})
const inFlight = new Set<string>()

function keyOf(clusterId: string, namespace: string): string {
  return `${clusterId}/${namespace}`
}

/**
 * Reads one cluster and namespace, at most once.
 *
 * NEVER REJECTS and never reports an error anywhere. An account that may read
 * pods and not VulnerabilityReports is ordinary, a cluster with no scanner is
 * ordinary, and neither is a reason to put a banner on a pod list about
 * something the operator did not ask for. Both come back as an empty answer
 * from Go, so the only failure this can see is a cluster that has genuinely
 * gone away — which the list beside it is already saying.
 */
export function ensureVulnerabilities(clusterId: string, namespace: string): void {
  if (!clusterId) return

  const key = keyOf(clusterId, namespace)
  if (key in loaded || inFlight.has(key)) return

  inFlight.add(key)
  void vulnerabilitySummaries(clusterId, namespace)
    .then((summaries) => {
      const bySubject: Summaries = {}
      for (const summary of summaries) bySubject[summary.subject] = summary
      loaded[key] = bySubject
    })
    .catch(() => {
      // Recorded as "asked, nothing to show" rather than left unasked, so a
      // refused read does not turn into one request per render.
      loaded[key] = {}
    })
    .finally(() => {
      inFlight.delete(key)
    })
}

/**
 * What the scanner recorded about the workload behind one pod, if anything.
 *
 * MATCHED BY THE CONTROLLING OWNER, never by the pod's own name, because that
 * is what the operator scanned: it writes one report per container of the
 * workload — a ReplicaSet for a Deployment's pods, a Job for a CronJob's —
 * and every replica of that workload runs the same images. A bare pod nothing
 * owns is scanned under its own name, which is the one case the fallback
 * covers.
 */
export function vulnerabilitiesFor(
  clusterId: string,
  namespace: string,
  pod: { name: string; controlledBy: string },
): VulnerabilitySummary | undefined {
  const summaries = loaded[keyOf(clusterId, namespace)]
  if (!summaries) return undefined
  return summaries[pod.controlledBy || `Pod/${pod.name}`]
}

/**
 * Forgets one cluster's reads, for a tab being closed.
 *
 * Per-cluster rather than wholesale, exactly as `forgetConfigMaps` is and for
 * the same reason: closing one tab must not make every other tab read again,
 * and data read out of somebody's cluster should not outlive the connection
 * it came from.
 */
export function forgetVulnerabilities(clusterId: string): void {
  const prefix = `${clusterId}/`
  for (const key of Object.keys(loaded)) {
    if (key.startsWith(prefix)) delete loaded[key]
  }
}
