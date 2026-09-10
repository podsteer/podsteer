/**
 * What a cluster is, for the Home list.
 *
 * TWO SOURCES, AND THE BACKEND'S WINS. The Go side identifies a cluster from
 * its version string when it has one and from its API server's address when it
 * does not — see app/domain/distribution.go for why the evidence is ranked
 * that way. What this adds is MEMORY: the strongest evidence only exists while
 * a cluster is open, so the answer is kept against the context name and used
 * on the next launch, when the list is drawn before anything is connected.
 *
 * A REMEMBERED MARK IS NEVER PREFERRED TO A FRESH ONE. If the backend has an
 * answer now it is the current one, and remembering exists only to fill the
 * gap where it has none.
 */

import type { Cluster, Distribution } from '$lib/api/client'

export interface ClusterMark {
  label: string
  /** A managed control plane — somebody else runs it. */
  hosted: boolean
  /** Why the mark is there, for its tooltip. */
  evidence: string
}

/**
 * The mark for one cluster, or null when nothing has identified it.
 *
 * NULL IS A REAL ANSWER and must render as nothing at all. A cluster PodSteer
 * cannot identify is common — a kubeadm cluster behind a private address gives
 * away nothing until it is opened — and a hedge in that space ("Unknown",
 * "Other") is a label somebody reads as information.
 */
export function markFor(
  cluster: Pick<Cluster, 'id' | 'distribution' | 'distributionHosted' | 'distributionEvidence'>,
  remembered: (clusterId: string) => ClusterMark | null,
): ClusterMark | null {
  if (cluster.distribution) {
    return {
      label: cluster.distribution,
      hosted: cluster.distributionHosted,
      evidence: cluster.distributionEvidence,
    }
  }
  return remembered(cluster.id)
}

/**
 * The table, read once from the backend and kept.
 *
 * A MODULE-LEVEL CACHE rather than a store, because it cannot change while the
 * application runs: it is compiled into the binary. The read is fired on first
 * use and everything before it answers null, which renders as no mark — the
 * same as a cluster nothing identified, and correct for the half-second before
 * the table arrives.
 */
let table: Record<string, { label: string; hosted: boolean }> | null = null

export function loadDistributionTable(read: () => Promise<Distribution[]>): Promise<void> {
  return read()
    .then((rows) => {
      table = {}
      for (const row of rows) table[row.id] = { label: row.label, hosted: row.hosted }
    })
    .catch(() => {
      // A table that did not load leaves every remembered mark unresolved,
      // which shows nothing. Nothing here is worth an error in front of
      // somebody opening the cluster list.
      table = {}
    })
}

/** Resolves an id remembered against a context, or null. */
export function rememberedMark(id: string | undefined): ClusterMark | null {
  if (!id || !table) return null

  const row = table[id]
  if (!row) return null
  return {
    label: row.label,
    hosted: row.hosted,
    // SAYS ITS OWN AGE. This is not evidence about the cluster now; it is what
    // PodSteer learned the last time it was open.
    evidence: 'identified when you last opened it',
  }
}
