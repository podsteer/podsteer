/** Builds the suggested filename for a table's CSV export. */

/** Anything that is not a safe filename character becomes `_`, so a cluster
    id or a kind carrying a slash, a colon or a space cannot produce a name
    the save dialog's own filesystem would reject or misread as a path. */
function safe(segment: string): string {
  return segment.replace(/[^A-Za-z0-9._-]+/g, '_')
}

/** `YYYYMMDD-HHMMSS`, in local time — when the export was made, not when the
    rows themselves were last true. */
function timestamp(now: Date): string {
  const pad = (n: number): string => String(n).padStart(2, '0')
  const date = `${now.getFullYear()}${pad(now.getMonth() + 1)}${pad(now.getDate())}`
  const time = `${pad(now.getHours())}${pad(now.getMinutes())}${pad(now.getSeconds())}`
  return `${date}-${time}`
}

/**
 * The namespace segment of an export's name: `all` for every namespace, the
 * name for one, `N-namespaces` for a set — a set's names would make the
 * filename as long as the selection.
 */
export function exportScopeLabel(namespaces: readonly string[]): string {
  if (namespaces.length === 0) return 'all'
  if (namespaces.length === 1) return namespaces[0]
  return `${namespaces.length}-namespaces`
}

/**
 * `<cluster>-<kind>-<scope>-<YYYYMMDD-HHMMSS>.csv`, the scope being
 * exportScopeLabel's: all, keda, 3-namespaces.
 *
 * Named for what is IN the file rather than left as "export.csv": a person
 * exporting three namespaces' worth of Pods across two clusters over a
 * session ends up with files a save dialog's own list already tells apart,
 * instead of a pile of "export (3).csv" only the export time distinguishes.
 *
 * `namespaces` is the filter's set; empty means every namespace.
 */
export function buildExportFilename(
  cluster: string,
  kind: string,
  namespaces: readonly string[],
  now: Date = new Date(),
): string {
  const scope = exportScopeLabel(namespaces)
  return `${safe(cluster)}-${safe(kind)}-${safe(scope)}-${timestamp(now)}.csv`
}

/**
 * `<pod>-<container>-<YYYYMMDD-HHMMSS>.log`, for the log pane's Download
 * button.
 *
 * Named for the pod and container it came from rather than left as
 * "logs.log", matching `buildExportFilename`'s own reasoning: a person
 * downloading logs from three containers across a session ends up with
 * files a save dialog's own list already tells apart. There is no cluster
 * or namespace segment — unlike a table export, a log pane is already
 * scoped to one pod, so naming a namespace here would repeat what naming
 * the pod already says just as precisely.
 */
export function buildLogFilename(pod: string, container: string, now: Date = new Date()): string {
  return `${safe(pod)}-${safe(container)}-${timestamp(now)}.log`
}
