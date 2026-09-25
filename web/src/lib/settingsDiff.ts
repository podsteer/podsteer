/**
 * The vocabulary a settings import is reviewed and merged in.
 *
 * Separate from `settingsFile.ts` so the two stores can speak it without
 * importing that module: `settingsFile` imports the stores, so anything the
 * stores imported back would be a cycle. Nothing here knows what a preference
 * or a project is — it is the grammar of "this changed, that was added, this
 * was left alone", which both halves then use to describe their own fields.
 */

/** Merge keeps what the file does not mention; replace does not. */
export type ImportMode = 'merge' | 'replace'

/** What will happen to one setting. */
export type ImportOutcome = 'add' | 'change' | 'same' | 'remove'

/** Which half of the document a reviewed line came from. */
export type ImportSection = 'Preferences' | 'Organisation'

/** One reviewable line: what it is, what happens, and both values. */
export interface ImportEntry {
  section: ImportSection
  label: string
  outcome: ImportOutcome
  /** What is set now, rendered for reading. Empty when nothing is. */
  from: string
  /** What will be set, rendered the same way. Empty when it goes away. */
  to: string
}

/**
 * What a reader of one payload half reports back.
 *
 * `unknown` and `invalid` are kept apart because they mean opposite things
 * about who is behind. An unknown field is a NEWER build's; an invalid one is
 * a value this build knows the name of and refuses, which is either
 * corruption or a hand edit.
 */
export interface FieldRead<T> {
  value: Partial<T>
  unknown: string[]
  invalid: string[]
}

/**
 * Whether two settings values are the same thing.
 *
 * Serialised with sorted keys rather than compared field by field: every
 * value in either payload came out of `JSON.parse` or out of a store that
 * only ever holds JSON-safe values, so this is total over what it is given —
 * and a hand-written deep compare is one more thing to keep in step with a
 * shape that grows.
 */
export function sameValue(a: unknown, b: unknown): boolean {
  return stableJSON(a) === stableJSON(b)
}

/** JSON with object keys in a fixed order, so key order is not a difference. */
function stableJSON(value: unknown): string {
  return JSON.stringify(value, (_key, item: unknown) => {
    if (item === null || typeof item !== 'object' || Array.isArray(item)) return item
    const sorted: Record<string, unknown> = {}
    for (const key of Object.keys(item as Record<string, unknown>).sort()) {
      sorted[key] = (item as Record<string, unknown>)[key]
    }
    return sorted
  })
}

/**
 * One value, rendered for somebody reading a review.
 *
 * A collection is rendered as its SIZE rather than its contents: a review of
 * four hundred column widths one line each is a review nobody reads, and the
 * question at this level is "did my column layouts change", not "by how many
 * pixels". Fields where the contents genuinely are the decision — a group's
 * environment, the threshold lines — pass their own renderer instead.
 */
export function describeValue(value: unknown, unit = 'entries'): string {
  if (value === undefined) return ''
  if (typeof value === 'boolean') return value ? 'On' : 'Off'
  if (typeof value === 'number' || typeof value === 'string') return String(value)
  if (Array.isArray(value)) return countOf(value.length, unit)
  if (value === null) return ''
  return countOf(Object.keys(value as Record<string, unknown>).length, unit)
}

/** "3 kinds", and "1 kind" rather than "1 kinds". */
function countOf(count: number, unit: string): string {
  if (count === 0) return `no ${unit}`
  return `${count} ${count === 1 ? singular(unit) : unit}`
}

/** Enough of English for the handful of units this file uses. */
function singular(unit: string): string {
  if (unit.endsWith('ies')) return `${unit.slice(0, -3)}y`
  if (unit.endsWith('es') && !unit.endsWith('ses')) return unit.slice(0, -2)
  if (unit.endsWith('s')) return unit.slice(0, -1)
  return unit
}

/** Which of the four outcomes a before/after pair is. */
export function outcomeOf(before: unknown, after: unknown): ImportOutcome {
  if (sameValue(before, after)) return 'same'
  if (before === undefined) return 'add'
  if (after === undefined) return 'remove'
  return 'change'
}

/** One review line, from a before/after pair and how to render each. */
export function entryFor(
  section: ImportSection,
  label: string,
  before: unknown,
  after: unknown,
  render: (value: unknown) => string = (value) => describeValue(value),
): ImportEntry {
  return {
    section,
    label,
    outcome: outcomeOf(before, after),
    from: before === undefined ? '' : render(before),
    to: after === undefined ? '' : render(after),
  }
}

/**
 * Combines two keyed maps under an import mode.
 *
 * Merge is a union with the incoming value winning a collision — the file is
 * what the operator just chose to import, so on the one key both describe it
 * is the answer. Replace is the incoming map alone, so a local-only key goes.
 * An incoming map that is absent entirely leaves merge alone and takes
 * replace back to `fallback`, which is this build's default rather than an
 * empty map: a file that omits a field is not asking for that field to be
 * emptied, it is asking for it not to be carried.
 */
export function mergeRecord<V>(
  current: Record<string, V>,
  incoming: Record<string, V> | undefined,
  mode: ImportMode,
  fallback: Record<string, V> = {},
): Record<string, V> {
  if (mode === 'replace') return { ...(incoming ?? fallback) }
  if (!incoming) return { ...current }
  return { ...current, ...incoming }
}

/**
 * Picks one scalar under an import mode.
 *
 * The same rule as mergeRecord in the degenerate case: the file's value where
 * it has one, and otherwise what is set now (merge) or the default (replace).
 */
export function mergeValue<V>(current: V, incoming: V | undefined, mode: ImportMode, fallback: V): V {
  if (incoming !== undefined) return incoming
  return mode === 'replace' ? fallback : current
}
