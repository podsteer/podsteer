/**
 * Where search matches fall through a terminal's buffer.
 *
 * Separated from the component so the rule can be argued with in a test: the
 * xterm SearchAddon reports how many matches there are and which one is
 * current, but not WHERE they are, and the ruler needs positions.
 */

/** The little of an xterm buffer this needs, so a test can supply one. */
export interface BufferLines {
  /** How many rows the buffer holds, scrollback included. */
  readonly length: number
  /** The row's text, or undefined past the end. */
  lineAt(row: number): string | undefined
}

/**
 * How many buckets the track is divided into.
 *
 * Matches a row apart would otherwise stack into one heavier mark that reads
 * as a longer run than it is. Two hundred is what the log viewer uses, and the
 * two tracks sit in the same place at the same width.
 */
const BUCKETS = 200

/**
 * Fractions down the buffer at which the query matches, deduplicated.
 *
 * OVER THE WHOLE BUFFER, scrollback included. The point of the ruler is to
 * show what is off screen; marking only the visible rows would draw a track
 * that agrees with what somebody can already see.
 *
 * Case-insensitive plain substring, matching what the component asks the
 * search addon for — a ruler that marked different rows from the ones Enter
 * steps through would be worse than no ruler.
 */
export function matchFractions(buffer: BufferLines, query: string): number[] {
  if (query === '' || buffer.length === 0) return []

  const needle = query.toLowerCase()
  const seen = new Set<number>()
  const found: number[] = []

  for (let row = 0; row < buffer.length; row++) {
    const line = buffer.lineAt(row)
    if (!line || !line.toLowerCase().includes(needle)) continue

    const fraction = row / buffer.length
    const bucket = Math.round(fraction * BUCKETS)
    if (seen.has(bucket)) continue

    seen.add(bucket)
    found.push(fraction)
  }

  return found
}
