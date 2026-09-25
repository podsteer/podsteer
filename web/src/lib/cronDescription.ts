/**
 * A CronJob's schedule, said in words: `*\/5 * * * *` → "every 5 minutes".
 *
 * WRITTEN HERE RATHER THAN TAKEN AS A DEPENDENCY. The dialect is small and
 * fixed — Kubernetes parses schedules with robfig/cron's standard parser:
 * five fields, `*` and `?`, steps, ranges, lists, month and weekday names,
 * and the `@hourly`-style descriptors including `@every` — and a general
 * cron-to-English library would be a shipped dependency (see
 * docs/LICENCE-POLICY.md) to translate a handful of shapes.
 *
 * NULL WHEN UNSURE, NEVER A GUESS. A shape this does not recognise gets no
 * description and the expression stands alone, which is what the panel showed
 * before. A wrong sentence beside a schedule is worse than none: it is the
 * thing somebody reads instead of the expression.
 *
 * Times are in the CronJob's `spec.timeZone` when it sets one and in the
 * controller manager's zone otherwise; the panel's own Time zone row says
 * which, so nothing here claims one.
 */

const MONTHS = ['January', 'February', 'March', 'April', 'May', 'June', 'July', 'August', 'September', 'October', 'November', 'December']
const WEEKDAYS = ['Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday']
const MONTH_NAMES = ['JAN', 'FEB', 'MAR', 'APR', 'MAY', 'JUN', 'JUL', 'AUG', 'SEP', 'OCT', 'NOV', 'DEC']
const WEEKDAY_NAMES = ['SUN', 'MON', 'TUE', 'WED', 'THU', 'FRI', 'SAT']

interface FieldSpec {
  min: number
  max: number
  names?: string[]
  /** Where names[0] sits: 1 for months, 0 for weekdays. */
  nameBase?: number
}

const FIELDS: Record<'minute' | 'hour' | 'dom' | 'month' | 'dow', FieldSpec> = {
  minute: { min: 0, max: 59 },
  hour: { min: 0, max: 23 },
  dom: { min: 1, max: 31 },
  month: { min: 1, max: 12, names: MONTH_NAMES, nameBase: 1 },
  // 7 is Sunday as well as 0, which robfig/cron accepts.
  dow: { min: 0, max: 7, names: WEEKDAY_NAMES, nameBase: 0 },
}

/** One field, parsed. `any` is `*` or `?`. */
type Field =
  | { kind: 'any' }
  | { kind: 'step'; from: number; to: number; step: number; open: boolean }
  | { kind: 'list'; values: number[]; ranges: [number, number][] }

function number(token: string, spec: FieldSpec): number | null {
  const upper = token.toUpperCase()
  const named = spec.names?.indexOf(upper) ?? -1
  if (named >= 0) return named + (spec.nameBase ?? 0)
  if (!/^\d+$/.test(token)) return null
  const value = Number(token)
  return value >= spec.min && value <= spec.max ? value : null
}

function parseField(text: string, spec: FieldSpec): Field | null {
  if (text === '*' || text === '?') return { kind: 'any' }

  // A step: `*/5`, `10-40/5`, `5/15` (from 5, every 15, to the end).
  const stepped = /^(\*|[^/]+)\/(\d+)$/.exec(text)
  if (stepped) {
    const step = Number(stepped[2])
    if (step < 1) return null
    if (stepped[1] === '*') return { kind: 'step', from: spec.min, to: spec.max, step, open: true }
    const range = stepped[1].split('-')
    const from = number(range[0], spec)
    const to = range.length === 2 ? number(range[1], spec) : spec.max
    if (from === null || to === null || from > to || range.length > 2) return null
    return { kind: 'step', from, to, step, open: range.length === 1 }
  }

  const values: number[] = []
  const ranges: [number, number][] = []
  for (const part of text.split(',')) {
    const range = part.split('-')
    if (range.length === 1) {
      const value = number(part, spec)
      if (value === null) return null
      values.push(value)
    } else if (range.length === 2) {
      const from = number(range[0], spec)
      const to = number(range[1], spec)
      if (from === null || to === null || from > to) return null
      ranges.push([from, to])
    } else {
      return null
    }
  }
  return { kind: 'list', values, ranges }
}

const pad = (value: number): string => String(value).padStart(2, '0')
const clock = (hour: number, minute: number): string => `${pad(hour)}:${pad(minute)}`

/** "a", "a and b", "a, b and c". */
function joined(items: string[]): string {
  if (items.length <= 1) return items.join('')
  return `${items.slice(0, -1).join(', ')} and ${items[items.length - 1]}`
}

const weekday = (value: number): string => WEEKDAYS[value % 7]
const ordinal = (value: number): string => {
  const tens = value % 100
  if (tens >= 11 && tens <= 13) return `${value}th`
  return `${value}${['th', 'st', 'nd', 'rd'][value % 10] ?? 'th'}`
}

/** A single value, when a field is exactly one. */
function single(field: Field): number | null {
  return field.kind === 'list' && field.ranges.length === 0 && field.values.length === 1 ? field.values[0] : null
}

function singles(field: Field): number[] | null {
  return field.kind === 'list' && field.ranges.length === 0 ? field.values : null
}

/** The time-of-day half. Null for a combination not worth guessing at. */
function describeTime(minute: Field, hour: Field): string | null {
  const m = single(minute)
  const h = single(hour)

  if (minute.kind === 'any' && hour.kind === 'any') return 'every minute'
  if (minute.kind === 'step' && minute.open && minute.from === 0 && hour.kind === 'any') {
    return minute.step === 1 ? 'every minute' : `every ${minute.step} minutes`
  }
  if (m !== null && hour.kind === 'any') {
    return m === 0 ? 'every hour' : `every hour at ${m} minute${m === 1 ? '' : 's'} past`
  }
  if (m !== null && hour.kind === 'step' && hour.open && hour.from === 0) {
    const every = hour.step === 1 ? 'every hour' : `every ${hour.step} hours`
    return m === 0 ? every : `${every}, at ${m} minute${m === 1 ? '' : 's'} past`
  }
  if (m !== null && h !== null) return `at ${clock(h, m)}`

  const hours = singles(hour)
  if (m !== null && hours && hours.length > 1) {
    return `at ${joined(hours.map((value) => clock(value, m)))}`
  }
  const minutes = singles(minute)
  if (minutes && minutes.length > 1 && h !== null) {
    return `at ${joined(minutes.map((value) => clock(h, value)))}`
  }
  if (hour.kind === 'list' && hour.ranges.length === 1 && hour.values.length === 0) {
    const [from, to] = hour.ranges[0]
    if (m !== null) return `every hour from ${clock(from, m)} to ${clock(to, m)}`
    if (minute.kind === 'step' && minute.open && minute.from === 0) {
      return `every ${minute.step} minutes between ${clock(from, 0)} and ${clock(to, 59)}`
    }
  }
  return null
}

/** The weekday half, or null when unrestricted. Undefined for a shape not handled. */
function describeWeekdays(dow: Field): string | null | undefined {
  if (dow.kind === 'any') return null
  if (dow.kind === 'step') return undefined
  const days = [
    ...dow.values.map(weekday),
    ...dow.ranges.map(([from, to]) => (from === to ? weekday(from) : `${weekday(from)} to ${weekday(to)}`)),
  ]
  return `on ${joined([...new Set(days)])}`
}

function describeMonthDays(dom: Field): string | null | undefined {
  if (dom.kind === 'any') return null
  if (dom.kind === 'step') {
    return dom.open && dom.from === 1 ? `every ${dom.step} days` : undefined
  }
  const days = [
    ...dom.values.map(ordinal),
    ...dom.ranges.map(([from, to]) => `${ordinal(from)} to ${ordinal(to)}`),
  ]
  return `on the ${joined(days)} of the month`
}

function describeMonths(month: Field): string | null | undefined {
  if (month.kind === 'any') return null
  if (month.kind === 'step') return undefined
  const months = [
    ...month.values.map((value) => MONTHS[value - 1]),
    ...month.ranges.map(([from, to]) => `${MONTHS[from - 1]} to ${MONTHS[to - 1]}`),
  ]
  return `in ${joined(months)}`
}

const DESCRIPTORS: Record<string, string> = {
  '@yearly': 'once a year, at 00:00 on 1 January',
  '@annually': 'once a year, at 00:00 on 1 January',
  '@monthly': 'once a month, at 00:00 on the 1st',
  '@weekly': 'once a week, at 00:00 on Sunday',
  '@daily': 'every day at 00:00',
  '@midnight': 'every day at 00:00',
  '@hourly': 'every hour',
}

/** Capitalises the first letter, for a sentence standing on its own. */
function sentence(text: string): string {
  return text.charAt(0).toUpperCase() + text.slice(1)
}

/**
 * The schedule in words, or null when this cannot say it with confidence.
 */
export function describeCron(expression: string | undefined): string | null {
  const text = expression?.trim()
  if (!text) return null

  if (text.startsWith('@')) {
    const every = /^@every\s+(\S+)$/.exec(text)
    if (every) return `Every ${every[1]}`
    const descriptor = DESCRIPTORS[text.toLowerCase()]
    return descriptor ? sentence(descriptor) : null
  }

  const parts = text.split(/\s+/)
  if (parts.length !== 5) return null

  const minute = parseField(parts[0], FIELDS.minute)
  const hour = parseField(parts[1], FIELDS.hour)
  const dom = parseField(parts[2], FIELDS.dom)
  const month = parseField(parts[3], FIELDS.month)
  const dow = parseField(parts[4], FIELDS.dow)
  if (!minute || !hour || !dom || !month || !dow) return null

  const time = describeTime(minute, hour)
  const days = describeMonthDays(dom)
  const weekdays = describeWeekdays(dow)
  const months = describeMonths(month)
  if (time === null || days === undefined || weekdays === undefined || months === undefined) return null

  // Both day fields restricted: cron fires when EITHER matches, which is the
  // rule people most often get backwards, so it is said as "or".
  const day = days && weekdays ? `${days} or ${weekdays}` : (days ?? weekdays)
  const everyDay = !day && time.startsWith('at ') ? 'every day' : null

  return sentence([time, everyDay, day, months].filter(Boolean).join(', '))
}
