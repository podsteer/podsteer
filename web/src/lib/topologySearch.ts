/**
 * Finding one object on a topology of thousands.
 *
 * Searched over the WHOLE graph, not over what is drawn: an object folded into
 * a set or collapsed into a group still exists, and a search that could not
 * find it would say it does not. A match is then located on the drawing — the
 * box that is it, or the set or group standing in for it — and that is where
 * the view goes.
 *
 * The query is words, any of which may be qualified: `kind:Service`,
 * `ns:payments`. Unqualified words must all appear in the name, kind or
 * namespace. Ranking puts exact names first, then names starting with the
 * text, then the rest, each in name order, so the first Enter goes where
 * somebody who typed a whole name meant.
 */

export interface SearchableNode {
  id: string
  apiKind: string
  name: string
  namespace: string
}

export interface ParsedQuery {
  kind: string
  namespace: string
  words: string[]
}

export function parseQuery(query: string): ParsedQuery {
  const parsed: ParsedQuery = { kind: '', namespace: '', words: [] }
  for (const token of query.trim().toLowerCase().split(/\s+/).filter(Boolean)) {
    if (token.startsWith('kind:')) parsed.kind = token.slice(5)
    else if (token.startsWith('ns:')) parsed.namespace = token.slice(3)
    else if (token.startsWith('namespace:')) parsed.namespace = token.slice(10)
    else parsed.words.push(token)
  }
  return parsed
}

/** Ids of matching objects, best first. Empty for an empty query. */
export function searchNodes(nodes: readonly SearchableNode[], query: string, limit = 500): string[] {
  const parsed = parseQuery(query)
  if (!parsed.kind && !parsed.namespace && parsed.words.length === 0) return []

  const text = parsed.words.join(' ')
  const ranked: { id: string; rank: number; name: string }[] = []
  for (const node of nodes) {
    const kind = node.apiKind.toLowerCase()
    const namespace = node.namespace.toLowerCase()
    const name = node.name.toLowerCase()
    if (parsed.kind && kind !== parsed.kind) continue
    if (parsed.namespace && namespace !== parsed.namespace) continue
    const haystack = `${kind} ${namespace} ${name}`
    if (!parsed.words.every((word) => haystack.includes(word))) continue
    const rank = !text ? 3 : name === text ? 0 : name.startsWith(text) ? 1 : name.includes(text) ? 2 : 3
    ranked.push({ id: node.id, rank, name })
  }
  ranked.sort((a, b) => a.rank - b.rank || (a.name < b.name ? -1 : a.name > b.name ? 1 : a.id < b.id ? -1 : 1))
  return ranked.slice(0, limit).map((entry) => entry.id)
}

/**
 * The drawn box an object is on: itself, or whatever stands in for it — a
 * folded set, then a collapsed group around that. Null when it is not drawn at
 * all (its Kind is switched off).
 */
export function locate(
  id: string,
  drawn: ReadonlySet<string>,
  standIns: readonly ReadonlyMap<string, string>[],
): string | null {
  let at = id
  for (const standIn of standIns) at = standIn.get(at) ?? at
  return drawn.has(at) ? at : null
}

/**
 * The pan that centres a box in the pane at a zoom where it can be read.
 *
 * Never zooms OUT to show a match — if somebody has zoomed in, they did it on
 * purpose — and zooms in only as far as READABLE_ZOOM, where a box's text
 * can be read, rather than to life size.
 */
export const READABLE_ZOOM = 0.8

export function centreOn(
  box: { x: number; y: number },
  pane: { width: number; height: number },
  zoom: number,
): { panX: number; panY: number; zoom: number } {
  const next = Math.max(zoom, READABLE_ZOOM)
  return {
    zoom: next,
    panX: pane.width / 2 - box.x * next,
    panY: pane.height / 2 - box.y * next,
  }
}
