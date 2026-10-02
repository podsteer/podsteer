/**
 * The namespace filter is a SET: some namespaces, or all of them.
 *
 * One shape for every list, the sidebar's picker, a saved view and the
 * remembered filter per cluster — `namespaces` sorted and deduplicated, and
 * `all` exactly when `namespaces` is empty. There is no "nothing selected":
 * the picker will not apply an empty set, so an empty list always means All.
 * The Go side has the same type (domain.NamespaceScope) and the same rule;
 * see CLAUDE.md, "Lists take a namespace set".
 */

/** Some namespaces, or all of them. */
export interface NamespaceScope {
  namespaces: string[]
  all: boolean
}

/** Every namespace. */
export const ALL_NAMESPACES_SCOPE: NamespaceScope = Object.freeze({
  namespaces: [],
  all: true,
}) as NamespaceScope

/** Trims, drops blanks, deduplicates and sorts — the one canonical order. */
export function normaliseNamespaces(names: readonly string[]): string[] {
  const kept = new Set<string>()
  for (const name of names) {
    if (typeof name !== 'string') continue
    const trimmed = name.trim()
    if (trimmed) kept.add(trimmed)
  }
  return [...kept].sort()
}

/** The scope a list of names describes; an empty list is All. */
export function scopeOf(names: readonly string[]): NamespaceScope {
  const namespaces = normaliseNamespaces(names)
  return { namespaces, all: namespaces.length === 0 }
}

/**
 * A stable string for a scope: '' for All, else the sorted names joined by
 * commas. A namespace name is a DNS label, so it can never hold a comma.
 * What in-flight guards and per-scope caches compare.
 */
export function scopeKeyOf(scope: NamespaceScope): string {
  return scope.all ? '' : scope.namespaces.join(',')
}

/** Whether two name lists are the same set, whatever their order. */
export function sameNamespaces(a: readonly string[], b: readonly string[]): boolean {
  const left = normaliseNamespaces(a)
  const right = normaliseNamespaces(b)
  return left.length === right.length && left.every((name, index) => name === right[index])
}

/** What a scope is called on a trigger, and the tooltip naming every member. */
export interface NamespaceLabel {
  label: string
  title: string
}

/**
 * The label rule, everywhere a scope is named in a few characters:
 * All → "All namespaces"; one → its name; two or three → "keda +2" (the
 * title lists them all); four or more → "N namespaces".
 */
export function namespaceLabelOf(scope: NamespaceScope): NamespaceLabel {
  if (scope.all || scope.namespaces.length === 0) {
    return { label: 'All namespaces', title: 'All namespaces' }
  }
  const names = scope.namespaces
  const title = names.join(', ')
  if (names.length === 1) return { label: names[0], title }
  if (names.length <= 3) return { label: `${names[0]} +${names.length - 1}`, title }
  return { label: `${names.length} namespaces`, title }
}

/** A namespace the picker offers, with an optional detail beside it. */
export interface NamespaceChoice {
  name: string
  hint?: string
}

/**
 * The window-wide picker's choices on All clusters: the UNION of every open
 * cluster's namespaces, sorted, so a name in the window's set is offered
 * rather than "not found" because the tab in front lacks it. A name only
 * some clusters have says which — a set means nothing on the others.
 */
export function fleetNamespaceChoices(byCluster: Record<string, readonly string[]>): NamespaceChoice[] {
  const clusters = Object.keys(byCluster)
  const holders = new Map<string, string[]>()
  for (const cluster of clusters) {
    for (const name of new Set(byCluster[cluster])) {
      const list = holders.get(name)
      if (list) list.push(cluster)
      else holders.set(name, [cluster])
    }
  }
  return [...holders.keys()].sort().map((name) => {
    const on = holders.get(name)!
    return on.length === clusters.length ? { name } : { name, hint: `only on ${on.join(', ')}` }
  })
}
