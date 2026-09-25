/**
 * The status quick-filters on the Pods page — Failing, Pending, Restarting,
 * OOMKilled, ImagePullBackOff, Terminating.
 *
 * Every predicate below is a SELECTION on a field the domain already put on
 * the Pod DTO — `phase`, `statusReason` (Go's `Pod.StatusReason()`), and each
 * container's `reason` / `lastTermination.reason`. See CLAUDE.md, "The pod
 * pane assesses too": comparisons, thresholds and conclusions belong in the
 * domain, where they can be argued with in a Go test; a chip here only picks
 * out rows by a value Go already computed. None of these re-derive what
 * "OOMKilled" or "CrashLoopBackOff" means — that classification already
 * happened in `app/domain/pod.go` and `app/domain/termination.go`.
 */

import type { Pod } from './api/client'

export interface PodStatusChip {
  id: string
  label: string
  /** True when `pod` belongs in this chip. */
  predicate: (pod: Pod) => boolean
}

export const POD_STATUS_CHIPS: PodStatusChip[] = [
  {
    id: 'failing',
    label: 'Failing',
    // Quotes Pod.isHealthy — Go's Pod.IsHealthy(), which is NOT phase ==
    // Running: a crash-looping pod and one whose container sits in Error both
    // report Running while serving nothing, and the domain already says so
    // (see CLAUDE.md, "Pod.IsHealthy is not phase == Running"). Quoting the
    // phase alone would show "Failing" only for the rare phase Kubernetes
    // calls Failed and hide every broken-but-Running pod, which is most of
    // them. Pending and Terminating are unhealthy too, and each has its own
    // chip, so they are excluded here rather than counted twice.
    predicate: (pod) =>
      !pod.isHealthy && pod.phase !== 'Pending' && pod.phase !== 'Terminating',
  },
  {
    id: 'pending',
    label: 'Pending',
    // Quotes Pod.phase.
    predicate: (pod) => pod.phase === 'Pending',
  },
  {
    id: 'restarting',
    label: 'Restarting',
    // Quotes Pod.statusReason — Go's Pod.StatusReason(), the same string the
    // Status column already renders — for the one value that means a
    // container is currently looping rather than merely having restarted at
    // some point in its history.
    predicate: (pod) => pod.statusReason === 'CrashLoopBackOff',
  },
  {
    id: 'oomkilled',
    label: 'OOMKilled',
    // Quotes Pod.statusReason for a container still reporting the kill
    // (state Terminated, reason OOMKilled) OR any container's
    // lastTermination.reason for one that has since restarted into
    // CrashLoopBackOff and carries the OOM only in its PREVIOUS life.
    // StatusReason() reports the CURRENT state only, which is exactly why
    // the per-container field is needed here and nowhere else on this page.
    predicate: (pod) =>
      pod.statusReason === 'OOMKilled' ||
      (pod.containers ?? []).some((container) => container.lastTermination?.reason === 'OOMKilled'),
  },
  {
    id: 'imagepullbackoff',
    label: 'ImagePullBackOff',
    // Quotes Pod.statusReason.
    predicate: (pod) => pod.statusReason === 'ImagePullBackOff',
  },
  {
    id: 'terminating',
    label: 'Terminating',
    // Quotes Pod.phase. "Terminating" is not a real Kubernetes phase — the
    // mapper substitutes it when deletionTimestamp is set, as kubectl does
    // (see CLAUDE.md, "PodPhaseTerminating is not a Kubernetes phase") — but
    // it is already computed, so this is still a selection, not a new rule.
    predicate: (pod) => pod.phase === 'Terminating',
  },
]

/**
 * Whether `pod` satisfies the active chip selection.
 *
 * OR across the selected chips — "show me anything wrong" reads more useful
 * than requiring a pod to match every chip picked — and a pass-through when
 * nothing is selected, so an empty selection means "no chip filter" rather
 * than "match nothing". Combined with the text query by the caller, which
 * ANDs this result in alongside it.
 */
export function matchesPodStatusChips(pod: Pod, activeIds: readonly string[]): boolean {
  if (activeIds.length === 0) return true
  return POD_STATUS_CHIPS.some((chip) => activeIds.includes(chip.id) && chip.predicate(pod))
}
