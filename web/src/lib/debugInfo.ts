/**
 * The plain-text diagnostics block for a bug report.
 *
 * WHAT IS DELIBERATELY ABSENT. No cluster name, context name, hostname,
 * namespace or local path. The block is pasted into a public issue tracker, so
 * it is built from an allow-list of facts that identify a build and a platform
 * and nothing about the operator's estate. Clusters appear only as a count and
 * the Kubernetes versions their API servers reported, in an order that does not
 * correspond to any tab order a reader could match to a name.
 */

import type { DebugInfo } from '$lib/api/client'

/** Names the web engine from its user agent, e.g. "Chrome/126.0.0.0". */
export function engineFromUserAgent(userAgent: string): string {
  const match =
    /(Edg|Chrome|Version)\/[\d.]+/.exec(userAgent) ?? /AppleWebKit\/[\d.]+/.exec(userAgent)
  return match ? match[0] : ''
}

/**
 * Formats the block.
 *
 * @param info          what the backend reports about this installation
 * @param clusterVersions Kubernetes versions of the open clusters, one per
 *                      cluster; empty strings are clusters not yet reached
 * @param userAgent     the webview's user agent, the fallback for a webview
 *                      version the backend could not read
 */
export function formatDebugInfo(
  info: DebugInfo,
  clusterVersions: string[],
  userAgent: string,
): string {
  const webview = info.webview || engineFromUserAgent(userAgent) || 'unknown'
  const reached = clusterVersions.filter((version) => version !== '').sort()
  const lines = [
    `PodSteer: ${info.version || 'unknown'}${info.commit ? ` (${info.commit.slice(0, 12)})` : ''}`,
    `OS: ${info.os || 'unknown'} (${info.platform || 'unknown'})`,
    `Wails: ${info.wailsVersion || 'unknown'}`,
    `Webview: ${webview}`,
    `Go: ${info.goVersion || 'unknown'}`,
    `Open clusters: ${clusterVersions.length}`,
    `Kubernetes versions: ${reached.length > 0 ? reached.join(', ') : 'none reported'}`,
  ]
  return lines.join('\n')
}

/** Where "Report a bug" sends the operator. */
export const BUG_REPORT_URL = 'https://github.com/podsteer/podsteer/issues/new?template=bug_report.yml'
