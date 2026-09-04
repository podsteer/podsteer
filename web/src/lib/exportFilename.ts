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
 * `<cluster>-<kind>-<namespace or all>-<YYYYMMDD-HHMMSS>.csv`.
 *
 * Named for what is IN the file rather than left as "export.csv": a person
 * exporting three namespaces' worth of Pods across two clusters over a
 * session ends up with files a save dialog's own list already tells apart,
 * instead of a pile of "export (3).csv" only the export time distinguishes.
 *
 * `namespace` takes the empty string for "every namespace", matching
 * `ALL_NAMESPACES` in `$lib/api/client` — the caller is not required to
 * import that just to call this.
 */
export function buildExportFilename(
  cluster: string,
  kind: string,
  namespace: string,
  now: Date = new Date(),
): string {
  const scope = namespace === '' ? 'all' : namespace
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
