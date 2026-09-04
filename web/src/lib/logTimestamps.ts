/**
 * Splits a log line's leading Kubernetes timestamp from its text, and
 * formats it for display.
 *
 * The backend always asks for timestamps (`domain.LogOptions.Timestamps`),
 * whatever the operator has this mode set to — see CLAUDE.md, "Log
 * timestamps are always requested". Re-opening the whole stream just to
 * change how a column looks would cost a fresh tail read for a display
 * preference, so the choice between off/local/UTC/relative lives here,
 * applied at render time against a line the backend already sent stamped.
 */

/** Which form a line's timestamp is shown in, or hidden entirely. */
export type TimestampMode = 'off' | 'local' | 'utc' | 'relative'

/** A line split into its parsed timestamp (if any) and the rest of the text. */
export interface ParsedLogLine {
  /** `null` when the line carried no RFC 3339 prefix — a line the container
      wrote before the kubelet attached, or one already stripped upstream. */
  timestamp: Date | null
  /** The line with the timestamp and its separating space removed. Equal to
      the input when `timestamp` is `null`, so a caller never has to branch
      on which one to render. */
  rest: string
}

/**
 * Kubernetes writes RFC 3339 with nanosecond precision followed by a single
 * space — `2024-01-15T10:30:00.123456789Z Hello world`. The offset form
 * (`+01:00`) is matched too, since that is what RFC 3339 itself allows, even
 * though the kubelet always emits `Z`.
 */
const TIMESTAMP_PREFIX = /^(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})) (.*)$/s

/** Splits `line` into its leading timestamp and the rest, if it has one. */
export function parseLogTimestamp(line: string): ParsedLogLine {
  const match = TIMESTAMP_PREFIX.exec(line)
  if (!match) return { timestamp: null, rest: line }

  const timestamp = new Date(match[1])
  // A string that merely LOOKS like RFC 3339 but does not parse (an
  // out-of-range component) must not be reported as a valid timestamp with
  // a garbage date attached.
  if (Number.isNaN(timestamp.getTime())) return { timestamp: null, rest: line }

  return { timestamp, rest: match[2] }
}

function pad(value: number, width = 2): string {
  return String(value).padStart(width, '0')
}

function clock(h: number, m: number, s: number, ms: number): string {
  return `${pad(h)}:${pad(m)}:${pad(s)}.${pad(ms, 3)}`
}

/**
 * Formats `timestamp` for display under `mode`.
 *
 * Returns `''` for `'off'` and for a `null` timestamp under any other mode —
 * a caller renders nothing rather than a placeholder, the same convention
 * every other "nothing to show" value in this codebase follows.
 *
 * `now` is a parameter, not `new Date()` read internally, so `'relative'` is
 * deterministic in a test — the same reason `buildExportFilename` takes a
 * clock instead of reading one.
 */
export function formatLogTimestamp(
  timestamp: Date | null,
  mode: TimestampMode,
  now: Date = new Date(),
): string {
  if (mode === 'off' || timestamp === null) return ''

  switch (mode) {
    case 'local':
      return clock(
        timestamp.getHours(),
        timestamp.getMinutes(),
        timestamp.getSeconds(),
        timestamp.getMilliseconds(),
      )
    case 'utc':
      return (
        clock(
          timestamp.getUTCHours(),
          timestamp.getUTCMinutes(),
          timestamp.getUTCSeconds(),
          timestamp.getUTCMilliseconds(),
        ) + 'Z'
      )
    case 'relative':
      return formatRelative(timestamp, now)
  }
}

/**
 * "5s ago", "3m ago", "2h ago", "4d ago" — coarsened to the largest unit
 * that applies, matching how every other relative time in the application
 * reads. A negative difference (the line's clock is very slightly ahead of
 * this machine's) is clamped to zero rather than shown as a time in the
 * future, which would read as a fault in a log line that arrived normally.
 */
function formatRelative(timestamp: Date, now: Date): string {
  const diffMs = Math.max(0, now.getTime() - timestamp.getTime())
  const seconds = Math.floor(diffMs / 1000)

  if (seconds < 1) return 'just now'
  if (seconds < 60) return `${seconds}s ago`

  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) return `${minutes}m ago`

  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours}h ago`

  const days = Math.floor(hours / 24)
  return `${days}d ago`
}
