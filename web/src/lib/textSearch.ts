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
