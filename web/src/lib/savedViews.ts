/**
 * A view somebody named and kept: a kind, a namespace, a search, and the
 * status chips that were pressed.
 *
 * WHAT A VIEW IS NOT. It does not carry the sort, the page, the column set or
 * the page size. Each of those is already remembered somewhere of its own —
 * `sorts` per kind, `pageSize` for the whole application — and a view that
 * also set them would fight those records: applying a view would silently
 * change how every OTHER visit to that kind is sorted, which is not what
 * "show me my broken pods" asked for.
 *
 * WHAT IT DOES CARRY IS THE OPERATOR'S OWN TEXT, and that is a deliberate
 * departure worth naming. The recents list is not persisted anywhere because
 * an object name recorded automatically is a fact about the cluster that
 * nobody asked to write down (see ClusterSession.recentObjects). A saved view
 * is the opposite act: somebody typed a query, pressed Save, and gave it a
 * name. It lives in the webview's own storage beside the other things the
 * operator chose, where it can be read and deleted.
 *
 * IT IS DELIBERATELY NOT IN THE SETTINGS EXPORT. That file's own header
 * promises no object names appear in it — no pod, node, namespace or workload
 * — and a saved view holds a namespace and whatever the operator typed. A
 * shared file people keep in git does not quietly acquire either. See
 * preferences.exportable, which is written out field by field for this
 * reason.
 *
 * NOT KEYED BY CLUSTER, on purpose. "Pods, kube-system, re:crash" is a
 * question about a shape of problem, not about one cluster — and the tab it
 * is applied in is the one the operator is looking at. A namespace that does
 * not exist on the current cluster simply shows nothing, which is the same
 * answer the namespace picker gives.
 */

/** One saved view. */
export interface SavedView {
  /** Stable, derived from the name, and readable in an exported file. */
  id: string
  /** What the operator called it. */
  name: string
  /** The catalog kind id, e.g. "core/v1/pods". */
  kindId: string
  /** The namespace filter, or the all-namespaces sentinel. */
  namespace: string
  /** The search box's text, in the search grammar. */
  search: string
  /** Status chip ids pressed on the Pods page. Empty everywhere else. */
  statusFilters: string[]
}

/** What the current view is, for capturing and for comparing. */
export interface ViewState {
  kindId: string
  namespace: string
  search: string
  statusFilters: string[]
}

/**
 * A ceiling, so a list that is scrolled rather than read cannot grow without
 * bound. Fifty is far past what anybody curates by hand and well under what
 * makes the menu unusable.
 */
export const MAX_SAVED_VIEWS = 50

/** The longest name kept. Longer ones are cut rather than refused. */
export const MAX_VIEW_NAME = 60

/**
 * Turns a name into an id, avoiding the ids already in use.
 *
 * DERIVED FROM THE NAME RATHER THAN RANDOM, because these ids appear in the
 * exported settings file, where `crashing-pods` says what a UUID cannot. A
 * name that slugs to nothing — punctuation, or a script this cannot
 * transliterate — falls back to "view", and the collision suffix does the
 * rest, so two such names are still two views.
 */
export function savedViewId(name: string, taken: readonly string[]): string {
  const slug =
    name
      .toLowerCase()
      .replace(/[^a-z0-9]+/g, '-')
      .replace(/^-+|-+$/g, '')
      .slice(0, 40) || 'view'

  if (!taken.includes(slug)) return slug

  for (let suffix = 2; ; suffix++) {
    const candidate = `${slug}-${suffix}`
    if (!taken.includes(candidate)) return candidate
  }
}

/** Trims a name to something a menu row can hold. Empty stays empty — the
    caller decides whether an unnamed view may be saved. */
export function cleanViewName(name: string): string {
  return name.trim().replace(/\s+/g, ' ').slice(0, MAX_VIEW_NAME)
}

/**
 * Whether this view is the one currently on screen.
 *
 * Compares chips as a SET, because the order they were pressed in is not part
 * of what anybody saved: pressing "pending" then "crashing" is the same view
 * as the other way round, and a menu that failed to tick it would look broken.
 */
export function viewMatches(view: SavedView, current: ViewState): boolean {
  return (
    view.kindId === current.kindId &&
    view.namespace === current.namespace &&
    view.search === current.search &&
    sameSet(view.statusFilters, current.statusFilters)
  )
}

function sameSet(a: readonly string[], b: readonly string[]): boolean {
  if (a.length !== b.length) return false
  const seen = new Set(b)
  return a.every((item) => seen.has(item))
}

/**
 * A one-line summary for the menu row under the name.
 *
 * Says what the view SELECTS, in the order somebody reads it: which kind,
 * where, and what it is filtered to. The kind's title is passed in rather
 * than looked up, because the catalog belongs to a cluster and this list does
 * not — an unknown kind is described by its id, which is still a true answer.
 */
export function describeView(
  view: SavedView,
  kindTitle: string,
  allNamespaces: string,
): string {
  const parts = [kindTitle || view.kindId]
  parts.push(view.namespace === allNamespaces ? 'all namespaces' : view.namespace)
  if (view.search) parts.push(view.search)
  if (view.statusFilters.length > 0) {
    parts.push(
      view.statusFilters.length === 1
        ? view.statusFilters[0]
        : `${view.statusFilters.length} status filters`,
    )
  }
  return parts.join(' · ')
}

/**
 * Reads a stored or imported list back, keeping only entries that are whole.
 *
 * Stored preferences outlive the code that wrote them and an imported file
 * was written by somebody else's build, so every field is checked rather than
 * trusted — the same stance the rest of preferences.svelte.ts takes. A view
 * missing its kind cannot be applied to anything, so it is dropped rather
 * than repaired into something nobody saved.
 */
export function sanitiseViews(value: unknown): SavedView[] {
  if (!Array.isArray(value)) return []

  const views: SavedView[] = []
  const taken: string[] = []

  for (const entry of value) {
    if (!entry || typeof entry !== 'object') continue
    const candidate = entry as Partial<SavedView>

    const name = typeof candidate.name === 'string' ? cleanViewName(candidate.name) : ''
    const kindId = typeof candidate.kindId === 'string' ? candidate.kindId : ''
    if (!name || !kindId) continue

    const id =
      typeof candidate.id === 'string' && candidate.id !== '' && !taken.includes(candidate.id)
        ? candidate.id
        : savedViewId(name, taken)
    taken.push(id)

    views.push({
      id,
      name,
      kindId,
      namespace: typeof candidate.namespace === 'string' ? candidate.namespace : '',
      search: typeof candidate.search === 'string' ? candidate.search : '',
      statusFilters: Array.isArray(candidate.statusFilters)
        ? candidate.statusFilters.filter((chip): chip is string => typeof chip === 'string')
        : [],
    })

    if (views.length === MAX_SAVED_VIEWS) break
  }

  return views
}
