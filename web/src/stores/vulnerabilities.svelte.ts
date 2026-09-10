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

import {
  vulnerabilitySummaries,
  type VulnerabilityListing,
  type VulnerabilitySummary,
} from '$lib/api/client'

/** One cluster-and-namespace's answer, keyed by the subject's "Kind/name". */
type Summaries = Record<string, VulnerabilitySummary>

/**
 * One namespace's read: what it found, and how much of the truth it is.
 *
 * THE STATUS IS NOT A DETAIL. Four ordinary outcomes leave rows undecorated —
 * no scanner, no permission, nothing found, and a read that stopped at its
 * ceiling — and only ONE of them means the workloads are clean. Holding the
 * summaries without the status is what made an absent mark mean all four at
 * once, on a security signal.
 */
interface Reading {
  bySubject: Summaries
  /** '' when the read never produced one; never treated as complete. */
  status: string
  read: number
  remaining: number
  cap: number
}

/** What has been read, and what is being read right now. */
const loaded = $state<Record<string, Reading>>({})
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
    .then((listing: VulnerabilityListing) => {
      const bySubject: Summaries = {}
      for (const summary of listing.summaries ?? []) bySubject[summary.subject] = summary
      loaded[key] = {
        bySubject,
        status: listing.status,
        read: listing.read,
        remaining: listing.remaining,
        cap: listing.cap,
      }
    })
    .catch(() => {
      // Recorded as "asked, and it did not answer" rather than left unasked,
      // so a failed read does not turn into one request per render. The
      // status stays empty, which is not 'complete' — so nothing downstream
      // reads an absent mark here as a clean workload.
      loaded[key] = { bySubject: {}, status: '', read: 0, remaining: 0, cap: 0 }
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
  const reading = loaded[keyOf(clusterId, namespace)]
  if (!reading) return undefined
  return reading.bySubject[pod.controlledBy || `Pod/${pod.name}`]
}

/**
 * Whether a row WITHOUT a mark has been shown to have nothing, or merely not
 * been looked at.
 *
 * The one question the pod list has to ask before letting an absence stand
 * unqualified. Only a completed read answers it: a truncated one has reports
 * it never saw, a refused one saw nothing, and 'not-installed' means no
 * scanner ever wrote anything to see.
 */
export function vulnerabilityReadFor(
  clusterId: string,
  namespace: string,
): { complete: boolean; truncated: boolean; read: number; remaining: number; cap: number } | undefined {
  const reading = loaded[keyOf(clusterId, namespace)]
  if (!reading) return undefined
  return {
    complete: reading.status === 'complete',
    truncated: reading.status === 'truncated',
    read: reading.read,
    remaining: reading.remaining,
    cap: reading.cap,
  }
}

/**
 * How many workloads in this namespace run the same image.
 *
 * THE QUESTION AN OPERATOR IS ACTUALLY ASKING. The scanner writes one report
 * per container, so an image running in twelve Deployments produces twelve
 * summaries with identical counts — and a list of workloads presents that as
 * twelve problems when it is one. Bump the tag once and all twelve change.
 *
 * Counts SUBJECTS, not reports: a workload running the image in two
 * containers is still one workload to fix. Zero when the namespace has not
 * been read, which the caller must not render as "only this one" — see
 * vulnerabilityReadFor.
 */
export function workloadsRunningImage(
  clusterId: string,
  namespace: string,
  image: string,
): number {
  if (!image) return 0
  const reading = loaded[keyOf(clusterId, namespace)]
  if (!reading) return 0

  let count = 0
  for (const summary of Object.values(reading.bySubject)) {
    if ((summary.images ?? []).includes(image)) count += 1
  }
  return count
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
