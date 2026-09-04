/**
 * Pure helpers over a rollout history — RolloutHistory's own RevisionDTO
 * list — for the History tab and RollbackDialog.
 *
 * The backend already returns revisions newest-first with `current` marked
 * (see app/adapters/k8s/revisions.go's markCurrent, which uses the highest
 * revision number for every kind rather than a kind-specific status field).
 * These exist so the frontend does not re-derive that rule from raw numbers
 * at each call site, and so ordering is something a consumer can rely on
 * locally — combining two fetches, say — rather than only ever trusting the
 * wire order.
 */

/** The fields these helpers need from a revision — enough to work with
 * either wails.RevisionDTO directly or a lighter test fixture. */
export interface RevisionLike {
  number: number
  current: boolean
}

/** Orders revisions newest-number-first, the order `kubectl rollout history`
 * prints and the order the History tab shows. */
export function orderByNumberDescending<T extends RevisionLike>(revisions: T[]): T[] {
  return [...revisions].sort((a, b) => b.number - a.number)
}

/** The revision presently in use, or null when none is marked current —
 * an empty list, most often, which must not throw. */
export function currentRevision<T extends RevisionLike>(revisions: T[]): T | null {
  return revisions.find((revision) => revision.current) ?? null
}

/** Whether a revision can be rolled back to: any revision that is not the
 * current one. Rolling back to the current revision is refused server-side
 * too (domain.ErrInvalidRevision) — this lets the UI disable the control
 * before a click ever reaches the backend. */
export function isRollbackable<T extends RevisionLike>(revision: T): boolean {
  return !revision.current
}
