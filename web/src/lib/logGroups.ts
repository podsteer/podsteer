/**
 * Groups a stack trace's continuation lines under the line that started it.
 *
 * A stack trace is a handful of lines an operator reads as ONE event — the
 * panic or exception message, then a wall of frames — but a log stream
 * shows it as N independent lines with nothing marking where it ends. This
 * finds the boundary the same way a human does: a line that starts with
 * whitespace (an indented frame) or `at ` (Java/Node/.NET's own frame
 * prefix, which is not always indented) continues whatever line came before
 * it that did NOT.
 *
 * Pure and generic over the line type — LogViewer decides what a "line" is
 * (it carries a sequence number, a pod name, the works); this only needs
 * `text` to decide where the group boundaries fall.
 */

/** One line, grouped under a header if it continues one. */
export interface LineGroup<T> {
  /** The line that started the group — every line reaches the output as
      SOME group's header, even one with no continuations, so a caller never
      has to branch on "is this line part of a group at all". */
  header: T
  /** Continuation lines, in order. Empty for an ordinary line. */
  members: T[]
}

/** Whether `text` reads as a continuation of the line before it — an
    indented frame, or one starting with Java/Node/.NET's `at ` frame
    prefix, which is not always indented (`	at com.foo.Bar.baz`). */
function isContinuation(text: string): boolean {
  return text.startsWith(' ') || text.startsWith('\t') || text.startsWith('at ')
}

/**
 * Groups `lines` into headers with their continuation members.
 *
 * A line that would continue something but arrives FIRST (no header seen
 * yet — a stream that starts mid-trace, or a filtered view whose header line
 * did not match) becomes its own header instead of being dropped: every
 * input line appears exactly once in the output, which is the same
 * completeness rule `graphFold.ts` follows for the dependency map — folding
 * a view must never make something disappear.
 */
export function groupLogLines<T extends { text: string }>(lines: T[]): Array<LineGroup<T>> {
  const groups: Array<LineGroup<T>> = []

  for (const line of lines) {
    const current = groups[groups.length - 1]
    if (current && isContinuation(line.text)) {
      current.members.push(line)
    } else {
      groups.push({ header: line, members: [] })
    }
  }

  return groups
}
