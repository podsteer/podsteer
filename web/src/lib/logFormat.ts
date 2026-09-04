/**
 * Detects a structured (JSON or logfmt) log line and pulls its well-known
 * fields out for display — level, message and timestamp promoted ahead of
 * everything else, the same three columns a structured logging library
 * (zap, logrus, slog) puts first regardless of which key name it chose.
 *
 * A line that is neither is `'plain'`, carrying no fields — LogViewer falls
 * back to rendering `raw` exactly as it does today, so turning "Parse
 * structured lines" off is the same code path as every line failing to
 * parse.
 */

/** One field surviving after level/message/timestamp/error are promoted out. */
export interface StructuredField {
  key: string
  value: string
}

export type StructuredKind = 'json' | 'logfmt' | 'plain'

export interface StructuredLine {
  kind: StructuredKind
  /** The raw level string as the line spelled it (`"warn"`, `"WARNING"`) —
      unnormalised, because the chip shows what the process actually said. */
  level?: string
  message?: string
  /** The raw timestamp field's value, as text — display formatting for a
      Kubernetes-supplied timestamp is `logTimestamps.ts`'s job; this is
      whatever the PROCESS itself put in its own `time`/`ts` field, which is
      free-form and not assumed to be RFC 3339. */
  timestamp?: string
  error?: string
  /** Every other key, in the order it appeared. */
  fields: StructuredField[]
  /** The line exactly as received, for the raw-text fallback. */
  raw: string
}

const LEVEL_KEYS = ['level', 'lvl', 'severity']
const MESSAGE_KEYS = ['msg', 'message']
const TIMESTAMP_KEYS = ['time', 'ts', 'timestamp']
const ERROR_KEYS = ['error', 'err']

/** Turns any JSON value into display text — objects and arrays as compact
    JSON, everything else via String(), so a chip never renders `[object
    Object]`. */
function toDisplayText(value: unknown): string {
  if (typeof value === 'string') return value
  if (value === null || value === undefined) return ''
  if (typeof value === 'object') {
    try {
      return JSON.stringify(value)
    } catch {
      return String(value)
    }
  }
  return String(value)
}

/** Pulls the promoted keys out of a parsed object, first match wins per
    category — an object carrying both `msg` and `message` is not expected,
    but if it did, the first name in MESSAGE_KEYS is the one shown. */
function promote(entries: Array<[string, unknown]>, raw: string, kind: 'json' | 'logfmt'): StructuredLine {
  const result: StructuredLine = { kind, fields: [], raw }
  const used = new Set<string>()

  const claim = (keys: string[], assign: (text: string) => void): void => {
    for (const key of keys) {
      const entry = entries.find(([k]) => k.toLowerCase() === key && !used.has(k))
      if (entry) {
        assign(toDisplayText(entry[1]))
        used.add(entry[0])
        return
      }
    }
  }

  claim(LEVEL_KEYS, (text) => (result.level = text))
  claim(MESSAGE_KEYS, (text) => (result.message = text))
  claim(TIMESTAMP_KEYS, (text) => (result.timestamp = text))
  claim(ERROR_KEYS, (text) => (result.error = text))

  for (const [key, value] of entries) {
    if (used.has(key)) continue
    result.fields.push({ key, value: toDisplayText(value) })
  }

  return result
}

/**
 * A conservative logfmt tokenizer: `key=value` pairs, value either a
 * double-quoted string (backslash-escaped, decoded via JSON.parse so `\"`
 * and `\n` behave the way every logfmt writer emits them) or a bare run of
 * non-space characters.
 *
 * Requires at least two pairs, AND that the matched pairs account for most
 * of the line's own length — a sentence containing one incidental `a=b`
 * ("retry count=3 after failure") must not be misread as structured, but
 * `level=info msg="started" component=api` should be.
 */
const LOGFMT_PAIR = /([A-Za-z_][\w.]*)=("(?:[^"\\]|\\.)*"|\S*)/g

function tryParseLogfmt(text: string): Array<[string, unknown]> | null {
  LOGFMT_PAIR.lastIndex = 0
  const entries: Array<[string, unknown]> = []
  let matchedLength = 0
  let match: RegExpExecArray | null

  while ((match = LOGFMT_PAIR.exec(text)) !== null) {
    let value: string = match[2]
    if (value.startsWith('"') && value.endsWith('"')) {
      try {
        value = JSON.parse(value)
      } catch {
        value = value.slice(1, -1)
      }
    }
    entries.push([match[1], value])
    matchedLength += match[0].length
  }

  if (entries.length < 2) return null
  // Whitespace between pairs is expected and does not count against
  // coverage — comparing matched text to the length with whitespace
  // collapsed is what lets `level=info  msg="ok"` (extra spacing) still
  // pass while a mostly-prose line with one `a=b` in it does not.
  const collapsed = text.replace(/\s+/g, '')
  if (collapsed.length === 0 || matchedLength / collapsed.length < 0.6) return null

  return entries
}

/** Parses `line` as JSON, then logfmt, falling back to `'plain'`. Never
    throws — a malformed line is exactly the case this exists to handle
    gracefully, matching `parseQuery`'s own "never throws" contract. */
export function parseStructuredLine(line: string): StructuredLine {
  const trimmed = line.trim()

  if (trimmed.startsWith('{') && trimmed.endsWith('}')) {
    try {
      const parsed: unknown = JSON.parse(trimmed)
      if (parsed !== null && typeof parsed === 'object' && !Array.isArray(parsed)) {
        return promote(Object.entries(parsed as Record<string, unknown>), line, 'json')
      }
    } catch {
      // Falls through to logfmt/plain — a line that merely starts and ends
      // with braces (a Go struct's %v output, say) is not JSON.
    }
  }

  const logfmt = tryParseLogfmt(trimmed)
  if (logfmt) return promote(logfmt, line, 'logfmt')

  return { kind: 'plain', fields: [], raw: line }
}

export type Severity = 'error' | 'warn' | 'info' | 'debug'

/** Every spelling a level field is seen to use, mapped to the four chips —
    a QUOTATION of what the categories above already promoted, not a new
    classification of its own. */
const LEVEL_ALIASES: Record<string, Severity> = {
  error: 'error',
  err: 'error',
  fatal: 'error',
  panic: 'error',
  critical: 'error',
  crit: 'error',
  warn: 'warn',
  warning: 'warn',
  info: 'info',
  information: 'info',
  informational: 'info',
  notice: 'info',
  debug: 'debug',
  trace: 'debug',
  verbose: 'debug',
}

/** A whole-word, case-insensitive ERROR/WARN/INFO/DEBUG token in the first
    64 characters — the heuristic `detectSeverity` falls back to for a PLAIN
    line, which carries no level field at all. */
const PLAIN_LEVEL_TOKEN = /\b(ERROR|WARN|INFO|DEBUG)\b/i

/**
 * Guesses which severity chip `line` belongs under, or `undefined` when
 * none applies.
 *
 * For a structured line this is a QUOTATION: the level field is exactly
 * what the process wrote, only normalised to one of the four categories. For
 * a plain line it is a HEURISTIC — a token match in the first 64 characters
 * — and must be presented as one: the caller labels the chips "by level,
 * where a line says one" rather than claiming every matched line was
 * actually tagged at that severity by the process that wrote it.
 */
export function detectSeverity(line: StructuredLine): Severity | undefined {
  if (line.level) {
    const normalised = LEVEL_ALIASES[line.level.toLowerCase()]
    if (normalised) return normalised
  }

  if (line.kind === 'plain') {
    const match = PLAIN_LEVEL_TOKEN.exec(line.raw.slice(0, 64))
    if (match) return match[1].toLowerCase() as Severity
  }

  return undefined
}
