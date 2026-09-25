/**
 * A set of kinds somebody named and kept, for the multi-kind view.
 *
 * WHY IT IS NOT JUST `multiKindSelection`. That records what one cluster is
 * showing right now — a working state, replaced every time the chips change.
 * This is the other thing: "the kinds my application is made of", named once
 * and reached in one click on any cluster. OpenShift's Search page has the
 * same pair, where the saved half is called "Add to navigation".
 *
 * NOT KEYED BY CLUSTER, for the reason a saved view is not: "Deployments,
 * Services, Ingresses" is a question about a shape of application, not about
 * one cluster, and the tab it is applied in is the one the operator is
 * looking at. A kind the current cluster does not serve is skipped when the
 * set is applied rather than treated as an error — which is the same answer
 * the navigator gives for a kind an operator pinned before an operator was
 * uninstalled.
 *
 * IT IS DELIBERATELY NOT IN THE SETTINGS EXPORT, and this is the half worth
 * arguing. The kind ids in it are safe — `multiKindSelection` already travels
 * for exactly that reason. The NAME is not: an operator naming a set after
 * the thing it describes writes "notification-service stack", and that file's
 * own header promises no object names appear in it. Saved views are excluded
 * on identical grounds (see savedViews.ts), and a rule that held for one and
 * not the other would be a rule nobody could state.
 */
import { savedViewId, cleanViewName } from './savedViews'

/** How many sets are worth keeping before the navigator becomes a list of them. */
export const MAX_KIND_SETS = 12

export interface KindSet {
  /** Stable, derived from the name. */
  id: string
  /** What the operator called it. */
  name: string
  /** Catalogue kind ids, in the order they were chosen. */
  kinds: string[]
}

/**
 * Derives a set's id from its name.
 *
 * Reuses the saved-view slug rather than growing a second one: both are "a
 * name somebody typed, made into a stable handle, unique among its siblings",
 * and two implementations of that would differ the first time somebody used
 * an emoji.
 */
export function kindSetId(name: string, taken: readonly string[]): string {
  return savedViewId(name, taken)
}

/** Trims and bounds a typed name, as a saved view's is. */
export function cleanKindSetName(name: string): string {
  return cleanViewName(name)
}

/**
 * Whether this set is the one on screen.
 *
 * Compared as a SET, not as a sequence. The order decides which kind's
 * columns claim a position in the merged table, so it is worth keeping — but
 * an operator who added Services before Deployments this time is still
 * looking at the set they saved, and telling them otherwise would be pedantry
 * dressed as precision.
 */
export function kindSetMatches(set: KindSet, current: readonly string[]): boolean {
  if (set.kinds.length !== current.length) return false
  const have = new Set(current)
  return set.kinds.every((kind) => have.has(kind))
}
