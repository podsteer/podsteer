/**
 * Finding literal text, shared by the editor and the box that drives it.
 */

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
