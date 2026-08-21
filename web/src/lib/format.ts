/** Display formatting helpers shared across views. */

import type { Pod } from '$lib/api/client'

/**
 * Semantic tone of a piece of status, mapped onto MD3 colour roles by the
 * components that render it.
 */
export type Tone = 'success' | 'warning' | 'error' | 'info' | 'neutral'

const SECOND = 1
const MINUTE = 60 * SECOND
const HOUR = 60 * MINUTE
const DAY = 24 * HOUR
const YEAR = 365 * DAY

/**
 * Formats an age the way kubectl does: one or two units, coarsening as the
 * value grows.
 *
 * The two-unit form is kept only below a day, where the difference between
 * "2h" and "2h47m" is the difference between a pod that just restarted and one
 * that did not. Past a day nobody is counting minutes.
 */
export function formatAge(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds < 0) return '—'
  if (seconds < MINUTE) return `${Math.floor(seconds)}s`

  if (seconds < HOUR) {
    const minutes = Math.floor(seconds / MINUTE)
    const remainder = Math.floor(seconds % MINUTE)
    return remainder > 0 && minutes < 10 ? `${minutes}m${remainder}s` : `${minutes}m`
  }

  if (seconds < DAY) {
    const hours = Math.floor(seconds / HOUR)
    const minutes = Math.floor((seconds % HOUR) / MINUTE)
    return minutes > 0 ? `${hours}h${minutes}m` : `${hours}h`
  }

  if (seconds < YEAR) {
    const days = Math.floor(seconds / DAY)
    const hours = Math.floor((seconds % DAY) / HOUR)
    return days < 10 && hours > 0 ? `${days}d${hours}h` : `${days}d`
  }

  const years = Math.floor(seconds / YEAR)
  const days = Math.floor((seconds % YEAR) / DAY)
  return days > 0 ? `${years}y${days}d` : `${years}y`
}

/** Formats a Date as a local wall-clock time, or an em dash when absent. */
export function formatClockTime(value: Date | null): string {
  if (!value) return '—'
  return value.toLocaleTimeString(undefined, {
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  })
}

/**
 * The status a pod list should show for a pod.
 *
 * The container-level reason wins over the phase whenever there is one,
 * because that is where the actionable information lives: a pod stuck pulling
 * an image reports phase "Pending", which says nothing, while its container
 * reports "ImagePullBackOff", which says everything.
 */
export function podStatusLabel(pod: Pod): string {
  return pod.statusReason || pod.phase
}

/** Maps a pod's condition onto a tone. */
export function podTone(pod: Pod): Tone {
  if (pod.phase === 'Failed') return 'error'
  if (pod.phase === 'Terminating') return 'warning'
  if (pod.phase === 'Succeeded') return 'info'
  if (pod.phase === 'Unknown') return 'neutral'

  // A pod reporting Running while its containers are not ready is the case
  // that matters: it looks fine in a naive list and is not serving traffic.
  if (pod.phase === 'Running') return pod.isHealthy ? 'success' : 'warning'

  return 'warning'
}

/** Formats a restart count, keeping zero visually quiet. */
export function formatRestarts(restarts: number): string {
  return restarts === 0 ? '0' : String(restarts)
}

/**
 * How long a cluster has been connected, or how long ago it last was.
 *
 * `connectedAt` is when the connection was MADE, so one timestamp phrases both
 * — the elapsed time is the same figure, only the tense differs.
 */
export function formatConnection(
  connectedAt: number | null,
  isOpen: boolean,
  now: number,
): string {
  if (connectedAt === null) {
    // A cluster open without a recorded time is possible: storage can be
    // cleared under a running application. Say the true part rather than
    // counting from a moment nobody knows.
    return isOpen ? 'Connected' : 'Never connected'
  }

  const elapsed = formatAge(Math.max(0, now - connectedAt) / 1000)
  return isOpen ? `Connected for ${elapsed}` : `Last connected ${elapsed} ago`
}

/** The exact moment behind formatConnection's relative phrasing. */
export function formatConnectionTitle(connectedAt: number | null): string | undefined {
  if (connectedAt === null) return undefined
  return `Connected on ${new Date(connectedAt).toLocaleString()}`
}
