/**
 * Finding literal text, shared by the editor and the box that drives it.
 */

import type { Query } from './query'

/**
 * Every occurrence of `needle` in `text`, as `[start, end]` offsets.
 *
 * Case-insensitive and literal. Somebody scanning a manifest is looking for
 * `imagePullPolicy` or a pod name, not writing a pattern — and treating the
 * input as a regex would turn a typed `[` into an error message rather than a
 * search.
 *
 * Lives here rather than inside the editor because the toolbar needs the same
 * answer to show a count. Counting through the editor instead would mean
 * asking a CodeMirror state field that the query has not necessarily reached
 * yet, so the number would lag the typing by a keystroke.
 *
 * `offset` shifts the results, for callers scanning a slice of a larger
 * document.
 */
export function findMatches(text: string, needle: string, offset = 0): Array<[number, number]> {
  const found: Array<[number, number]> = []
  if (!needle) return found

  const haystack = text.toLowerCase()
  const target = needle.toLowerCase()

  let at = haystack.indexOf(target)
  while (at !== -1) {
    found.push([offset + at, offset + at + needle.length])
    // Step past this match rather than one character on, so overlapping
    // occurrences are not reported twice for the same span.
    at = haystack.indexOf(target, at + needle.length)
  }
  return found
}

/**
 * Splits `text` into runs, marking which ones matched.
 *
 * For rendering a highlight in plain DOM, where the editor's decorations are
 * not available — the log pane draws its own lines, so it needs the match
 * positions as markup rather than as CodeMirror ranges.
 *
 * Always returns at least one run, so a caller can render the result without
 * special-casing "no query" or "no matches".
 */
export function splitOnMatches(
  text: string,
  needle: string,
): Array<{ text: string; match: boolean }> {
  const found = findMatches(text, needle)
  if (found.length === 0) return [{ text, match: false }]

  const runs: Array<{ text: string; match: boolean }> = []
  let at = 0
  for (const [start, end] of found) {
    if (start > at) runs.push({ text: text.slice(at, start), match: false })
    runs.push({ text: text.slice(start, end), match: true })
    at = end
  }
  if (at < text.length) runs.push({ text: text.slice(at), match: false })
  return runs
}

/**
 * The regex counterpart to `splitOnMatches`, for a query term that came from
 * `re:`/`/pattern/` (see `$lib/query`) rather than a plain substring — the
 * log pane's filter box accepts both, and needs the same run-splitting
 * either way to highlight what it found.
 *
 * `regex` is re-run with a `g` flag regardless of what it already carries —
 * `query.ts` compiles its terms case-insensitively but never globally, since
 * `matches()` only needs a yes/no answer — so a caller does not have to
 * remember to add one for iterating every occurrence in a line.
 *
 * A zero-width match (a pattern like `x*` matching between characters) is
 * skipped rather than highlighted: there is no text to mark, and colouring
 * nothing does not help anybody find their match.
 */
export function splitOnRegex(
  text: string,
  regex: RegExp,
): Array<{ text: string; match: boolean }> {
  const flags = regex.flags.includes('g') ? regex.flags : regex.flags + 'g'
  const global = new RegExp(regex.source, flags)

  const runs: Array<{ text: string; match: boolean }> = []
  let at = 0

  for (const match of text.matchAll(global)) {
    const start = match.index ?? 0
    const value = match[0]
    if (value.length === 0) continue

    if (start > at) runs.push({ text: text.slice(at, start), match: false })
    runs.push({ text: value, match: true })
    at = start + value.length
  }

  if (at < text.length) runs.push({ text: text.slice(at), match: false })
  if (runs.length === 0) return [{ text, match: false }]
  return runs
}

/**
 * Merges match ranges that overlap, touch, or are separated only by
 * whitespace, into the fewest ranges that cover the same characters.
 *
 * The whitespace case is what makes an AND query of adjacent words read as
 * one highlight: `query.ts` splits an unquoted "issuer unavailable" into two
 * separate substring terms, and each lands on its own word — "issuer" ending
 * right where a single space precedes "unavailable". Merging only touching
 * ranges would leave that space unmarked, rendering as two marks either side
 * of a gap where an operator typed one phrase and expects to see one.
 */
function mergeRanges(ranges: Array<[number, number]>, text: string): Array<[number, number]> {
  if (ranges.length === 0) return []
  const sorted = [...ranges].sort((a, b) => a[0] - b[0] || a[1] - b[1])

  const merged: Array<[number, number]> = [[sorted[0][0], sorted[0][1]]]
  for (let i = 1; i < sorted.length; i++) {
    const [start, end] = sorted[i]
    const last = merged[merged.length - 1]
    if (start <= last[1] || /^\s*$/.test(text.slice(last[1], start))) {
      last[1] = Math.max(last[1], end)
    } else {
      merged.push([start, end])
    }
  }
  return merged
}

/**
 * Every highlight range `query` finds in `text`, from EVERY non-negated
 * text/regex term rather than one — `query.ts`'s grammar ANDs terms
 * together, so a plain multi-word query like `issuer unavailable` is two
 * substring terms, both of which must match for a line to pass the filter at
 * all, and both of which belong in what lights up. A negated term (`-foo`)
 * says the text must NOT contain something, so it contributes no range; a
 * label term (`key=value`, `label:key`, `cluster:name`) has nothing to
 * highlight in log or manifest text either.
 */
function rangesForQuery(text: string, query: Query): Array<[number, number]> {
  const ranges: Array<[number, number]> = []
  for (const term of query.terms) {
    if (term.negated) continue
    if (term.kind === 'text' && term.value !== '') {
      ranges.push(...findMatches(text, term.value))
    } else if (term.kind === 'regex' && term.regex) {
      const flags = term.regex.flags.includes('g') ? term.regex.flags : term.regex.flags + 'g'
      const global = new RegExp(term.regex.source, flags)
      for (const match of text.matchAll(global)) {
        const value = match[0]
        if (value.length === 0) continue
        const start = match.index ?? 0
        ranges.push([start, start + value.length])
      }
    }
  }
  return mergeRanges(ranges, text)
}

/**
 * The query counterpart to `splitOnMatches`/`splitOnRegex`: splits `text`
 * into runs covering every term of `query` at once, so a caller with a
 * `Query` already parsed (the log pane's filter box, which accepts the same
 * `re:`/`/pattern/` forms as a plain substring) does not have to pick just
 * one term to highlight and silently drop the rest.
 *
 * Always returns at least one run, for the same reason `splitOnMatches` does.
 */
export function splitOnQuery(text: string, query: Query): Array<{ text: string; match: boolean }> {
  const ranges = rangesForQuery(text, query)
  if (ranges.length === 0) return [{ text, match: false }]

  const runs: Array<{ text: string; match: boolean }> = []
  let at = 0
  for (const [start, end] of ranges) {
    if (start > at) runs.push({ text: text.slice(at, start), match: false })
    runs.push({ text: text.slice(start, end), match: true })
    at = end
  }
  if (at < text.length) runs.push({ text: text.slice(at), match: false })
  return runs
}
