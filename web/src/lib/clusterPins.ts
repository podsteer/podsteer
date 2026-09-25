/**
 * Pinned clusters: which ones come first, and which ones are shown.
 *
 * A PIN IS AN ORDERING, NOT A SECOND HOME. The picker already has one place
 * for a cluster — the project and group somebody filed it in — and a pinned
 * strip above that would put the same card in two places, so opening one of
 * them leaves the other looking untouched. Pinning instead lifts the cluster
 * to the top of the group it is already in, and the filter beside the search
 * box narrows the page to the pinned ones when that is the whole question.
 *
 * Both are pure so the rules can be argued with here rather than read out of
 * a template: the order is STABLE, and the filter is not allowed to answer
 * with an empty page when nothing is pinned at all.
 */

/** Anything with an id — a Cluster, or a test's stand-in for one. */
interface Identified {
  id: string
}

/**
 * Pinned first, everything else in the order it arrived.
 *
 * Stable on both sides, which is the point: the underlying order is the
 * kubeconfig's, and an unstable sort would shuffle twenty similarly named
 * contexts every time somebody pinned one of them.
 */
export function pinnedFirst<T extends Identified>(
  items: readonly T[],
  pinned: readonly string[],
): T[] {
  if (pinned.length === 0) return [...items]

  const set = new Set(pinned)
  const first: T[] = []
  const rest: T[] = []
  for (const item of items) {
    if (set.has(item.id)) first.push(item)
    else rest.push(item)
  }
  return [...first, ...rest]
}

/**
 * The clusters the picker should show.
 *
 * WITH THE FILTER ON AND NOTHING STARRED, EVERYTHING IS SHOWN. An empty page
 * with a lit toggle above it is a screen that looks broken to somebody who
 * pressed the toggle before pinning anything — and the honest answer to
 * "show me my pinned clusters" when there are none is not "you have no
 * clusters". The caller says so in words next to the toggle.
 */
export function visibleClusters<T extends Identified>(
  items: readonly T[],
  pinned: readonly string[],
  pinnedOnly: boolean,
): T[] {
  if (!pinnedOnly || pinned.length === 0) return pinnedFirst(items, pinned)

  const set = new Set(pinned)
  return items.filter((item) => set.has(item.id))
}
