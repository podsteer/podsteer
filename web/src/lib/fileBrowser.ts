/**
 * Paths, for the container file browser.
 *
 * NOTHING HERE TOUCHES A CONTAINER. It is the arithmetic a breadcrumb and an
 * Up button need, kept out of the component for the reason $lib/fileTransfer
 * is: a path ending in a slash, a path with spaces in it and the root itself
 * are where this kind of code goes wrong, and they are worth a test rather
 * than a careful read.
 *
 * Every path here is a CONTAINER path — always POSIX, always absolute, never
 * this machine's. There is deliberately no Windows separator handling: the
 * far end is a Linux container whatever the operator is running.
 */

/** One step of a breadcrumb: what to show, and where it goes. */
export interface Crumb {
  label: string
  path: string
}

/**
 * The breadcrumb for a path, root first.
 *
 * The root is always the first crumb and is labelled `/` rather than left
 * empty, so there is something to click when you are three directories deep.
 */
export function crumbsFor(dir: string): Crumb[] {
  const crumbs: Crumb[] = [{ label: '/', path: '/' }]

  let walked = ''
  for (const segment of dir.split('/')) {
    if (segment === '') continue
    walked += '/' + segment
    crumbs.push({ label: segment, path: walked })
  }
  return crumbs
}

/**
 * The parent of a path, CLAMPED AT THE ROOT.
 *
 * Up from `/` is `/`, not `/..`. A container path that walked above the root
 * would be a path the far end resolves back to the root anyway, and an Up
 * button that appears to do nothing is better than one that sends a request
 * to find out.
 */
export function parentOf(dir: string): string {
  const cleaned = normalise(dir)
  if (cleaned === '/') return '/'

  const cut = cleaned.lastIndexOf('/')
  return cut <= 0 ? '/' : cleaned.slice(0, cut)
}

/** A child of a directory, without the double slash at the root. */
export function childOf(dir: string, name: string): string {
  const base = normalise(dir)
  return base === '/' ? '/' + name : base + '/' + name
}

/**
 * Trims a trailing slash, keeping the root a single one.
 *
 * `/var/log/` and `/var/log` are the same directory, and a breadcrumb built
 * from the first would carry an empty final crumb.
 */
export function normalise(dir: string): string {
  const trimmed = dir.trim()
  if (trimmed === '') return '/'
  if (trimmed.length > 1 && trimmed.endsWith('/')) return trimmed.replace(/\/+$/, '') || '/'
  return trimmed
}

/**
 * Renders a name safely for display.
 *
 * A NAME THAT IS NOT VALID TEXT ARRIVES ALREADY DAMAGED — Go's JSON encoder
 * replaces the invalid bytes — so this cannot recover it and does not pretend
 * to. What it does is mark it, so the row can say the name is not what is on
 * disk and refuse to act on it rather than downloading a different file.
 */
export function displayName(name: string, readable: boolean): string {
  return readable ? name : name.replace(/�/g, '?')
}

/** Filters rows by a substring, case-insensitively. Never re-lists. */
export function filterNames<T extends { name: string }>(rows: T[], needle: string): T[] {
  const term = needle.trim().toLowerCase()
  if (term === '') return rows
  return rows.filter((row) => row.name.toLowerCase().includes(term))
}
