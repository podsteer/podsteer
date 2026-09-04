/**
 * Line diffing between two manifests, for the Compare feature.
 *
 * Three shapes come out of the same edit script: unified TEXT (for the copy
 * button and anyone who wants to paste a diff somewhere), a SIDE-BY-SIDE
 * model (for DiffView's two-column layout), and the fold-aware HUNK list both
 * of those are built from. Keeping one algorithm underneath all three is what
 * keeps "3 lines changed" meaning the same thing wherever it is read.
 *
 * The algorithm is the classic Longest Common Subsequence dynamic-programming
 * table — O(n·m) time and space, not the O(N·D) of Myers' algorithm — chosen
 * because it is the diff algorithm easiest to get RIGHT: a straight
 * (n+1)×(m+1) table filled once and walked back once, with no triangular
 * trace to reconstruct and no edge case in the backtrack that only shows up
 * on an input nobody tried. A manifest is at most a few thousand lines, so
 * the table is a few megabytes and fills in well under this module's own
 * time-budget test — this is not the algorithm to reach for on a source-file-
 * scale diff, which this application never shows.
 */

import { parseDocument, type Document } from 'yaml'

// --- Splitting -----------------------------------------------------------

/**
 * Splits text into lines the way a diff wants them: newline-delimited, with
 * the trailing empty "line" after a final newline dropped so a document
 * ending in `\n` does not compare as though it had one more blank line than
 * one that does not — every manifest PodSteer reads back from a cluster ends
 * in one, and diffing two of them would otherwise always report a trailing
 * blank-line change that is not there.
 */
export function splitLines(text: string): string[] {
  if (text === '') return []
  const lines = text.split('\n')
  if (lines[lines.length - 1] === '') lines.pop()
  return lines
}

// --- The edit script -------------------------------------------------------

export type DiffOpKind = 'equal' | 'delete' | 'insert'

export interface DiffOp {
  kind: DiffOpKind
  /** 0-based line index in `a`, or null for a pure insert. */
  aLine: number | null
  /** 0-based line index in `b`, or null for a pure delete. */
  bLine: number | null
  text: string
}

/**
 * The edit script turning `a` into `b`, as a sequence of equal/delete/insert
 * operations covering every line of both arrays exactly once.
 *
 * Built from the LCS table by walking it backwards from `[n, m]`: matching
 * lines are `equal` and walked diagonally, otherwise the tie-break prefers an
 * `insert` when the two neighbouring cells are equal — an arbitrary but
 * CONSISTENT choice, which is what keeps a run of pure insertions from
 * alternating with deletions it has no reason to interleave with.
 */
/**
 * The largest LCS table diffLines will fill, in cells. Four million cells is
 * 16 MB of Int32 and a few hundred milliseconds — a 2,000×2,000 diff, which
 * no ordinary manifest reaches once the common prefix and suffix are gone.
 * Beyond it a manifest such as a CRD carrying a 10,000-line OpenAPI schema
 * would ask for a 400 MB table, so the changed region is reported as one
 * block replacement instead; isCoarseDiff says when that happened so the
 * view can say so rather than pass the coarse answer off as a fine one.
 */
export const DIFF_CELL_BUDGET = 4_000_000

/** The lines two inputs share at their start and end, which need no table. */
function commonEnds(a: string[], b: string[]): { prefix: number; suffix: number } {
  const max = Math.min(a.length, b.length)
  let prefix = 0
  while (prefix < max && a[prefix] === b[prefix]) prefix++
  let suffix = 0
  while (suffix < max - prefix && a[a.length - 1 - suffix] === b[b.length - 1 - suffix]) suffix++
  return { prefix, suffix }
}

/**
 * Whether diffLines would give up on a line-by-line answer for these inputs.
 * The same arithmetic diffLines does, exposed so a view can label the result.
 */
export function isCoarseDiff(a: string[], b: string[]): boolean {
  const { prefix, suffix } = commonEnds(a, b)
  return (a.length - prefix - suffix) * (b.length - prefix - suffix) > DIFF_CELL_BUDGET
}

/**
 * Diffs two line arrays: the shared prefix and suffix are matched without a
 * table, the middle goes through the LCS table below, and a middle too large
 * for the budget is reported coarsely as every left line removed and every
 * right line added — an honest "these differ" rather than a stalled tab.
 */
export function diffLines(a: string[], b: string[]): DiffOp[] {
  const { prefix, suffix } = commonEnds(a, b)
  const aMid = a.slice(prefix, a.length - suffix)
  const bMid = b.slice(prefix, b.length - suffix)

  const ops: DiffOp[] = []
  for (let i = 0; i < prefix; i++) ops.push({ kind: 'equal', aLine: i, bLine: i, text: a[i] })

  const middle =
    aMid.length * bMid.length > DIFF_CELL_BUDGET ? coarseDiff(aMid, bMid) : lcsDiff(aMid, bMid)
  for (const op of middle) {
    ops.push({
      kind: op.kind,
      aLine: op.aLine === null ? null : op.aLine + prefix,
      bLine: op.bLine === null ? null : op.bLine + prefix,
      text: op.text,
    })
  }

  for (let k = 0; k < suffix; k++) {
    const ai = a.length - suffix + k
    ops.push({ kind: 'equal', aLine: ai, bLine: b.length - suffix + k, text: a[ai] })
  }
  return ops
}

/** Every left line removed, every right line added: the block replacement. */
function coarseDiff(a: string[], b: string[]): DiffOp[] {
  const ops: DiffOp[] = []
  a.forEach((text, i) => ops.push({ kind: 'delete', aLine: i, bLine: null, text }))
  b.forEach((text, j) => ops.push({ kind: 'insert', aLine: null, bLine: j, text }))
  return ops
}

function lcsDiff(a: string[], b: string[]): DiffOp[] {
  const n = a.length
  const m = b.length
  const width = m + 1
  // Int32Array rather than a nested array: half the memory of a boxed number
  // per cell and no per-row allocation, which is what keeps a 2000×2000 table
  // (≈16MB) fast to fill rather than merely possible to fill.
  const dp = new Int32Array((n + 1) * width)

  for (let i = 1; i <= n; i++) {
    const row = i * width
    const prevRow = (i - 1) * width
    const ai = a[i - 1]
    for (let j = 1; j <= m; j++) {
      if (ai === b[j - 1]) {
        dp[row + j] = dp[prevRow + j - 1] + 1
      } else {
        const up = dp[prevRow + j]
        const left = dp[row + j - 1]
        dp[row + j] = up >= left ? up : left
      }
    }
  }

  const ops: DiffOp[] = []
  let i = n
  let j = m
  while (i > 0 || j > 0) {
    if (i > 0 && j > 0 && a[i - 1] === b[j - 1]) {
      ops.push({ kind: 'equal', aLine: i - 1, bLine: j - 1, text: a[i - 1] })
      i--
      j--
    } else if (j > 0 && (i === 0 || dp[i * width + j - 1] >= dp[(i - 1) * width + j])) {
      ops.push({ kind: 'insert', aLine: null, bLine: j - 1, text: b[j - 1] })
      j--
    } else {
      ops.push({ kind: 'delete', aLine: i - 1, bLine: null, text: a[i - 1] })
      i--
    }
  }
  ops.reverse()
  return ops
}

/** Where each op sits in `a` and `b` — the count of each side's lines
    consumed strictly BEFORE it. Used to number hunks and to fold gaps
    without re-walking the ops list for every question asked of it. */
function positionIndex(ops: DiffOp[]): { aIndexAt: number[]; bIndexAt: number[] } {
  const aIndexAt: number[] = new Array(ops.length)
  const bIndexAt: number[] = new Array(ops.length)
  let aPos = 0
  let bPos = 0
  for (let k = 0; k < ops.length; k++) {
    aIndexAt[k] = aPos
    bIndexAt[k] = bPos
    if (ops[k].kind !== 'insert') aPos++
    if (ops[k].kind !== 'delete') bPos++
  }
  return { aIndexAt, bIndexAt }
}

// --- Hunks and folding ------------------------------------------------------

/** One contiguous slice of the ops list: either something worth showing
    (`hunk`, changes plus their surrounding context) or a run of unchanged
    lines between two hunks (`gap`) — DiffView's "N unchanged lines hidden"
    fold is exactly a `gap` segment collapsed to its count. */
export interface DiffSegment {
  kind: 'hunk' | 'gap'
  ops: DiffOp[]
}

/**
 * Splits an edit script into alternating gap/hunk segments, `context` lines
 * of untouched text kept on each side of a change.
 *
 * Two changes whose context windows overlap merge into ONE hunk rather than
 * two separated by a one-line gap — the same rule `diff -U` follows, and the
 * reason is the same: a "1 line hidden" divider between two changes reads as
 * more noise than the line it is hiding.
 */
function segment(ops: DiffOp[], context: number): DiffSegment[] {
  if (ops.length === 0) return []

  const changed: number[] = []
  ops.forEach((op, index) => {
    if (op.kind !== 'equal') changed.push(index)
  })

  if (changed.length === 0) return [{ kind: 'gap', ops }]

  const ranges: Array<[number, number]> = []
  let start = Math.max(0, changed[0] - context)
  let end = Math.min(ops.length - 1, changed[0] + context)
  for (let k = 1; k < changed.length; k++) {
    const idx = changed[k]
    const rangeStart = Math.max(0, idx - context)
    if (rangeStart <= end + 1) {
      end = Math.min(ops.length - 1, idx + context)
    } else {
      ranges.push([start, end])
      start = rangeStart
      end = Math.min(ops.length - 1, idx + context)
    }
  }
  ranges.push([start, end])

  const segments: DiffSegment[] = []
  let cursor = 0
  for (const [s, e] of ranges) {
    if (s > cursor) segments.push({ kind: 'gap', ops: ops.slice(cursor, s) })
    segments.push({ kind: 'hunk', ops: ops.slice(s, e + 1) })
    cursor = e + 1
  }
  if (cursor < ops.length) segments.push({ kind: 'gap', ops: ops.slice(cursor, ops.length) })

  return segments
}

/**
 * The gap/hunk breakdown of an edit script, for DiffView's "hide unchanged"
 * fold. Exported separately from {@link hunks} because a fold has to draw the
 * gaps too — a count of what a divider is hiding — not just the hunks either
 * side of them.
 */
export function foldSegments(ops: DiffOp[], context = 3): DiffSegment[] {
  return segment(ops, context)
}

export interface Hunk {
  /** 1-based first line shown on the `a` side, or the 0-based line the
      change sits after when `aLines` is 0 (a pure insertion) — the same
      convention `diff -U`'s own `@@` header uses. */
  aStart: number
  aLines: number
  bStart: number
  bLines: number
  /** The operations in this hunk, including its context lines. */
  ops: DiffOp[]
}

/** The hunks of an edit script — the `hunk`-kind segments of
    {@link foldSegments}, numbered against the whole file. */
export function hunks(ops: DiffOp[], context = 3): Hunk[] {
  const { aIndexAt, bIndexAt } = positionIndex(ops)
  const result: Hunk[] = []
  let opIndex = 0
  for (const seg of segment(ops, context)) {
    if (seg.kind === 'hunk') {
      const start = opIndex
      const aLines = seg.ops.filter((op) => op.kind !== 'insert').length
      const bLines = seg.ops.filter((op) => op.kind !== 'delete').length
      result.push({
        aStart: aLines > 0 ? aIndexAt[start] + 1 : aIndexAt[start],
        aLines,
        bStart: bLines > 0 ? bIndexAt[start] + 1 : bIndexAt[start],
        bLines,
        ops: seg.ops,
      })
    }
    opIndex += seg.ops.length
  }
  return result
}

// --- Unified text ------------------------------------------------------------

/**
 * A standard unified diff: `@@ -aStart,aLines +bStart,bLines @@` headers,
 * context lines prefixed with a space, removals with `-`, additions with
 * `+`. Empty when the two texts are identical — there is nothing to show,
 * and an empty string is what "Copy" should put on the clipboard for that.
 */
export function unified(a: string, b: string, context = 3): string {
  const ops = diffLines(splitLines(a), splitLines(b))
  const hunkList = hunks(ops, context)
  if (hunkList.length === 0) return ''

  const lines: string[] = []
  for (const hunk of hunkList) {
    lines.push(`@@ -${hunk.aStart},${hunk.aLines} +${hunk.bStart},${hunk.bLines} @@`)
    for (const op of hunk.ops) {
      const prefix = op.kind === 'equal' ? ' ' : op.kind === 'delete' ? '-' : '+'
      lines.push(prefix + op.text)
    }
  }
  return lines.join('\n')
}

// --- Side-by-side model ------------------------------------------------------

export interface DiffLine {
  /** `empty` is a filler row, not a line of either manifest — see
      {@link zipOpsToRows}'s doc comment for why the two columns need one. */
  kind: 'same' | 'added' | 'removed' | 'changed' | 'empty'
  text: string
  /** 1-based line number on THIS side, or null for a filler row. */
  lineNumber: number | null
}

export interface SideBySideDiff {
  left: DiffLine[]
  right: DiffLine[]
}

/**
 * Lays an edit script out as two equal-length columns, for a synced-scroll
 * grid — every row index means the same thing on both sides, which a plain
 * "left's changes, right's changes" pair of lists does not give you.
 *
 * A run of consecutive deletes and inserts (a Kubernetes patch that edits a
 * value in place produces exactly this: one delete, one insert, same
 * position) is ZIPPED pairwise into `changed` rows, so an edited line sits
 * beside its own before-and-after instead of its removal scrolling to the
 * bottom of one column and its replacement to the bottom of the other. Where
 * the run is lopsided — three lines added, none removed — the shorter side
 * is padded with `empty` filler rows so the grid still lines up.
 */
export function zipOpsToRows(ops: DiffOp[]): SideBySideDiff {
  const left: DiffLine[] = []
  const right: DiffLine[] = []
  let i = 0

  while (i < ops.length) {
    const op = ops[i]
    if (op.kind === 'equal') {
      left.push({ kind: 'same', text: op.text, lineNumber: (op.aLine as number) + 1 })
      right.push({ kind: 'same', text: op.text, lineNumber: (op.bLine as number) + 1 })
      i++
      continue
    }

    const deletes: DiffOp[] = []
    const inserts: DiffOp[] = []
    while (i < ops.length && ops[i].kind !== 'equal') {
      if (ops[i].kind === 'delete') deletes.push(ops[i])
      else inserts.push(ops[i])
      i++
    }

    const pairs = Math.min(deletes.length, inserts.length)
    for (let p = 0; p < pairs; p++) {
      left.push({ kind: 'changed', text: deletes[p].text, lineNumber: (deletes[p].aLine as number) + 1 })
      right.push({ kind: 'changed', text: inserts[p].text, lineNumber: (inserts[p].bLine as number) + 1 })
    }
    for (let p = pairs; p < deletes.length; p++) {
      left.push({ kind: 'removed', text: deletes[p].text, lineNumber: (deletes[p].aLine as number) + 1 })
      right.push({ kind: 'empty', text: '', lineNumber: null })
    }
    for (let p = pairs; p < inserts.length; p++) {
      left.push({ kind: 'empty', text: '', lineNumber: null })
      right.push({ kind: 'added', text: inserts[p].text, lineNumber: (inserts[p].bLine as number) + 1 })
    }
  }

  return { left, right }
}

/** The side-by-side model of a diff between two whole texts. */
export function sideBySide(a: string, b: string): SideBySideDiff {
  return zipOpsToRows(diffLines(splitLines(a), splitLines(b)))
}

// --- Normalising for comparison ----------------------------------------------

/** Deletes a nested key without `deleteIn`'s own behaviour of throwing when
    an ancestor is entirely absent rather than simply not holding the key —
    the same helper `$lib/duplicate.ts` defines for the same reason, kept
    local rather than imported so this module has no dependency on the
    duplicate-object feature it happens to share a shape with. */
function removeIn(doc: Document.Parsed, path: string[]): void {
  if (path.length === 1) {
    doc.delete(path[0])
    return
  }
  if (!doc.hasIn(path.slice(0, -1))) return
  doc.deleteIn(path)
}

/** Server-assigned identity and bookkeeping that differs between any two
    objects whatever their spec, and would otherwise dominate a diff with
    noise nobody asked to compare. Deliberately narrower than
    `duplicate.ts`'s METADATA_FIELDS: `ownerReferences` is real information
    when comparing two objects (a ReplicaSet's own UID differs from a
    Deployment's), so this list keeps only what is NEVER meaningful between
    two objects — not what is unsafe to REAPPLY, which is a different
    question with a different answer. */
const METADATA_FIELDS = [
  'uid',
  'resourceVersion',
  'creationTimestamp',
  'generation',
  'managedFields',
  'selfLink',
]

export interface NormaliseOptions {
  /** Keeps `status`, for the rare comparison that is deliberately about what
      the cluster is doing right now rather than what the object declares.
      Off by default: two Deployments are usually compared for what they ARE,
      and status differs between any two running objects on principle —
      replica counts settle at different moments even when the specs are
      identical — so leaving it in by default would bury the specs it exists
      to protect under noise from a section nobody came to read. */
  keepStatus?: boolean
}

/**
 * A manifest with the fields that always differ between any two objects, and
 * never say anything about whether they are meaningfully the same, removed —
 * the Compare feature's counterpart to `$lib/duplicate.ts`'s
 * `stripForDuplicate`, with the same "uses the `yaml` Document API so key
 * order and comments survive" property and the same fail-soft contract:
 * anything that cannot be parsed is returned unchanged, because a diff that
 * cannot be normalised should still be shown rather than replaced with
 * nothing.
 */
export function normaliseForDiff(manifestYaml: string, options: NormaliseOptions = {}): string {
  try {
    const doc = parseDocument(manifestYaml)
    if (doc.errors.length > 0) return manifestYaml

    if (!options.keepStatus) removeIn(doc, ['status'])
    for (const field of METADATA_FIELDS) removeIn(doc, ['metadata', field])

    return doc.toString()
  } catch {
    return manifestYaml
  }
}
