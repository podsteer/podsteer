import { describe, expect, it } from 'vitest'
import { parse } from 'yaml'

import {
  DIFF_CELL_BUDGET,
  diffLines,
  foldSegments,
  isCoarseDiff,
  hunks,
  normaliseForDiff,
  sideBySide,
  splitLines,
  unified,
} from './diff'

describe('splitLines', () => {
  it('splits on newlines', () => {
    expect(splitLines('a\nb\nc')).toEqual(['a', 'b', 'c'])
  })

  it('drops the trailing empty line a final newline produces', () => {
    // Every manifest read back from the cluster ends in one — without this a
    // document that ends in \n would always diff as though it had one more
    // blank line than an otherwise-identical one that does not.
    expect(splitLines('a\nb\n')).toEqual(['a', 'b'])
  })

  it('is empty for an empty string', () => {
    expect(splitLines('')).toEqual([])
  })
})

describe('diffLines', () => {
  it('reports every line as equal for identical input', () => {
    const a = ['one', 'two', 'three']
    const ops = diffLines(a, [...a])
    expect(ops.every((op) => op.kind === 'equal')).toBe(true)
    expect(ops.map((op) => op.text)).toEqual(a)
  })

  it('finds an added line', () => {
    const ops = diffLines(['a', 'b'], ['a', 'x', 'b'])
    expect(ops.map((op) => `${op.kind}:${op.text}`)).toEqual(['equal:a', 'insert:x', 'equal:b'])
  })

  it('finds a removed line', () => {
    const ops = diffLines(['a', 'b', 'c'], ['a', 'c'])
    expect(ops.map((op) => `${op.kind}:${op.text}`)).toEqual(['equal:a', 'delete:b', 'equal:c'])
  })

  it('finds a changed line as a delete immediately followed by an insert', () => {
    const ops = diffLines(['a', 'b', 'c'], ['a', 'B', 'c'])
    expect(ops.map((op) => op.kind)).toEqual(['equal', 'delete', 'insert', 'equal'])
  })

  it('reports an empty script for two empty inputs', () => {
    expect(diffLines([], [])).toEqual([])
  })

  it('reports every line as inserted when the left side is empty', () => {
    const ops = diffLines([], ['a', 'b'])
    expect(ops.map((op) => op.kind)).toEqual(['insert', 'insert'])
  })

  it('reports every line as deleted when the right side is empty', () => {
    const ops = diffLines(['a', 'b'], [])
    expect(ops.map((op) => op.kind)).toEqual(['delete', 'delete'])
  })
})

describe('hunks', () => {
  it('is empty for an unchanged file', () => {
    const ops = diffLines(['a', 'b', 'c'], ['a', 'b', 'c'])
    expect(hunks(ops)).toEqual([])
  })

  it('bounds context to the requested number of lines on each side', () => {
    const a = Array.from({ length: 10 }, (_, i) => `line ${i}`)
    const b = a.map((line, i) => (i === 5 ? 'CHANGED' : line))
    const ops = diffLines(a, b)
    const result = hunks(ops, 2)

    expect(result).toHaveLength(1)
    const text = result[0].ops.map((op) => op.text)
    expect(text).toContain('line 3')
    expect(text).toContain('line 7')
    expect(text).not.toContain('line 0')
    expect(text).not.toContain('line 9')
  })

  it('merges two changes whose context windows overlap into one hunk', () => {
    const a = Array.from({ length: 12 }, (_, i) => `line ${i}`)
    const b = a.map((line, i) => (i === 3 || i === 6 ? `CHANGED ${i}` : line))
    const ops = diffLines(a, b)
    expect(hunks(ops, 3)).toHaveLength(1)
  })

  it('keeps two changes far enough apart as separate hunks', () => {
    const a = Array.from({ length: 30 }, (_, i) => `line ${i}`)
    const b = a.map((line, i) => (i === 3 || i === 26 ? `CHANGED ${i}` : line))
    const ops = diffLines(a, b)
    expect(hunks(ops, 2)).toHaveLength(2)
  })

  it('numbers a hunk from where it starts in each file, 1-based', () => {
    const a = ['a', 'b', 'c', 'd', 'e']
    const b = ['a', 'b', 'X', 'd', 'e']
    const ops = diffLines(a, b)
    const [hunk] = hunks(ops, 1)
    // Context 1 around index 2 (0-based) covers lines 1..3 (0-based) → a
    // 1-based start of 2.
    expect(hunk.aStart).toBe(2)
    expect(hunk.bStart).toBe(2)
  })
})

describe('foldSegments', () => {
  it('is empty for an empty edit script', () => {
    expect(foldSegments([])).toEqual([])
  })

  it('is one gap covering everything when nothing changed', () => {
    const ops = diffLines(['a', 'b', 'c'], ['a', 'b', 'c'])
    const segments = foldSegments(ops, 1)
    expect(segments).toHaveLength(1)
    expect(segments[0].kind).toBe('gap')
    expect(segments[0].ops).toHaveLength(3)
  })

  it('brackets a single change with gap, hunk, gap', () => {
    const a = Array.from({ length: 20 }, (_, i) => `line ${i}`)
    const b = a.map((line, i) => (i === 10 ? 'CHANGED' : line))
    const ops = diffLines(a, b)
    const segments = foldSegments(ops, 2)
    expect(segments.map((s) => s.kind)).toEqual(['gap', 'hunk', 'gap'])
    // Every op accounted for exactly once.
    expect(segments.reduce((sum, s) => sum + s.ops.length, 0)).toBe(ops.length)
  })
})

describe('unified', () => {
  it('is empty for identical text', () => {
    const text = 'a\nb\nc\n'
    expect(unified(text, text)).toBe('')
  })

  it('marks an added line with a leading +', () => {
    const result = unified('a\nb\n', 'a\nx\nb\n')
    expect(result).toContain('\n+x\n')
  })

  it('marks a removed line with a leading -', () => {
    const result = unified('a\nb\nc\n', 'a\nc\n')
    expect(result).toContain('\n-b\n')
  })

  it('marks an unchanged context line with a leading space', () => {
    const result = unified('a\nb\nc\n', 'a\nx\nc\n')
    expect(result).toContain('\n a\n')
    expect(result).toContain('\n c')
  })

  it('carries a @@ header per hunk with 1-based line numbers and counts', () => {
    const result = unified('a\nb\nc\n', 'a\nX\nc\n')
    expect(result.split('\n')[0]).toMatch(/^@@ -\d+,\d+ \+\d+,\d+ @@$/)
  })

  it('writes one @@ header for two changes close enough to share context', () => {
    const a = Array.from({ length: 12 }, (_, i) => `line ${i}`).join('\n') + '\n'
    const b =
      Array.from({ length: 12 }, (_, i) => (i === 3 || i === 6 ? `CHANGED ${i}` : `line ${i}`)).join(
        '\n',
      ) + '\n'
    expect(unified(a, b, 3).match(/^@@/gm)).toHaveLength(1)
  })
})

describe('sideBySide', () => {
  it('marks identical lines as same on both sides, with matching line numbers', () => {
    const { left, right } = sideBySide('a\nb\n', 'a\nb\n')
    expect(left.map((l) => l.kind)).toEqual(['same', 'same'])
    expect(right.map((l) => l.kind)).toEqual(['same', 'same'])
    expect(left.map((l) => l.lineNumber)).toEqual([1, 2])
    expect(right.map((l) => l.lineNumber)).toEqual([1, 2])
  })

  it('pairs a one-line replace as changed on both sides, at the same row', () => {
    const { left, right } = sideBySide('a\nb\nc\n', 'a\nB\nc\n')
    const row = left.findIndex((l) => l.kind === 'changed')
    expect(row).toBeGreaterThanOrEqual(0)
    expect(left[row].text).toBe('b')
    expect(right[row].kind).toBe('changed')
    expect(right[row].text).toBe('B')
  })

  it('pads the left column with an empty filler row for a pure addition', () => {
    const { left, right } = sideBySide('a\nb\n', 'a\nx\nb\n')
    const row = right.findIndex((l) => l.kind === 'added')
    expect(row).toBeGreaterThanOrEqual(0)
    expect(left[row].kind).toBe('empty')
    expect(left[row].lineNumber).toBeNull()
  })

  it('pads the right column with an empty filler row for a pure removal', () => {
    const { left, right } = sideBySide('a\nb\nc\n', 'a\nc\n')
    const row = left.findIndex((l) => l.kind === 'removed')
    expect(row).toBeGreaterThanOrEqual(0)
    expect(right[row].kind).toBe('empty')
    expect(right[row].lineNumber).toBeNull()
  })

  it('keeps both columns the same length, for a synced-scroll grid', () => {
    const { left, right } = sideBySide('a\nb\nc\nd\n', 'a\nX\nY\nZ\nd\n')
    expect(left).toHaveLength(right.length)
  })

  it('is empty for two empty texts', () => {
    const { left, right } = sideBySide('', '')
    expect(left).toEqual([])
    expect(right).toEqual([])
  })
})

describe('normaliseForDiff', () => {
  const FULL = `apiVersion: apps/v1
kind: Deployment
metadata:
  name: web
  namespace: billing
  uid: 3f1c2b4a-0000-0000-0000-000000000000
  resourceVersion: "489213"
  generation: 4
  creationTimestamp: "2024-03-01T12:00:00Z"
  selfLink: /apis/apps/v1/namespaces/billing/deployments/web
  labels:
    app: web
  managedFields:
    - manager: kubectl-client-side-apply
      operation: Update
spec:
  replicas: 3 # deliberately left alone
status:
  readyReplicas: 3
  conditions:
    - type: Available
      status: "True"
`

  it('removes exactly the fields that always differ and never matter', () => {
    const result = parse(normaliseForDiff(FULL))
    expect(result).not.toHaveProperty('status')
    for (const field of [
      'uid',
      'resourceVersion',
      'generation',
      'creationTimestamp',
      'selfLink',
      'managedFields',
    ]) {
      expect(result.metadata, `metadata.${field} should be gone`).not.toHaveProperty(field)
    }
  })

  it('leaves everything else untouched, comments included', () => {
    const result = parse(normaliseForDiff(FULL))
    expect(result.metadata.name).toBe('web')
    expect(result.metadata.namespace).toBe('billing')
    expect(result.metadata.labels).toEqual({ app: 'web' })
    expect(result.spec).toEqual({ replicas: 3 })
    expect(normaliseForDiff(FULL)).toContain('# deliberately left alone')
  })

  it('keeps status when asked', () => {
    const result = parse(normaliseForDiff(FULL, { keepStatus: true }))
    expect(result.status).toEqual({ readyReplicas: 3, conditions: [{ type: 'Available', status: 'True' }] })
  })

  it('returns manifests it cannot parse unchanged, rather than losing them', () => {
    const broken = 'metadata:\n  name: [oops'
    expect(normaliseForDiff(broken)).toBe(broken)
  })

  it('is a no-op on a manifest that already carries none of these fields', () => {
    const minimal = `apiVersion: v1
kind: ConfigMap
metadata:
  name: settings
data:
  color: blue
`
    expect(parse(normaliseForDiff(minimal))).toEqual({
      apiVersion: 'v1',
      kind: 'ConfigMap',
      metadata: { name: 'settings' },
      data: { color: 'blue' },
    })
  })
})

describe('performance', () => {
  it('diffs two 2000-line manifests well under a second', () => {
    const aLines = Array.from({ length: 2000 }, (_, i) => `line ${i}: value`)
    // Every 10th line changed, plus an occasional extra line — a realistic
    // amount of drift between two similar objects, not a worst-case input.
    const bLines: string[] = []
    for (let i = 0; i < 2000; i++) {
      bLines.push(i % 10 === 0 ? `line ${i}: different value` : `line ${i}: value`)
      if (i % 137 === 0) bLines.push(`extra line after ${i}`)
    }
    const a = aLines.join('\n') + '\n'
    const b = bLines.join('\n') + '\n'

    const start = performance.now()
    unified(a, b)
    sideBySide(a, b)
    const elapsed = performance.now() - start

    // "Well under a second" — a full second would already read as broken to
    // somebody who just clicked Compare. This leaves generous headroom.
    expect(elapsed).toBeLessThan(1000)
  })
})

describe('large inputs', () => {
  it('matches a long shared prefix and suffix without a table, and diffs only the middle', () => {
    // 5,000 shared lines around one changed line: the LCS table would be
    // 25 million cells, far over the budget, yet the answer is exact because
    // the shared ends never reach the table.
    const a = Array.from({ length: 5000 }, (_, i) => `line ${i}`)
    const b = [...a]
    b[2500] = 'line 2500 changed'
    const started = Date.now()
    const ops = diffLines(a, b)
    expect(Date.now() - started).toBeLessThan(1000)
    expect(isCoarseDiff(a, b)).toBe(false)
    const changed = ops.filter((op) => op.kind !== 'equal')
    expect(changed).toEqual([
      { kind: 'delete', aLine: 2500, bLine: null, text: 'line 2500' },
      { kind: 'insert', aLine: null, bLine: 2500, text: 'line 2500 changed' },
    ])
    // 2,500 shared lines, one delete, one insert, 2,499 shared lines: 5,001 ops.
    expect(ops).toHaveLength(5001)
    expect(ops[ops.length - 1]).toEqual({ kind: 'equal', aLine: 4999, bLine: 4999, text: 'line 4999' })
  })

  it('falls back to a block replacement when the differing middle exceeds the budget', () => {
    const side = Math.ceil(Math.sqrt(DIFF_CELL_BUDGET)) + 50
    const a = Array.from({ length: side }, (_, i) => `left ${i}`)
    const b = Array.from({ length: side }, (_, i) => `right ${i}`)
    expect(isCoarseDiff(a, b)).toBe(true)
    const started = Date.now()
    const ops = diffLines(a, b)
    expect(Date.now() - started).toBeLessThan(1000)
    expect(ops).toHaveLength(2 * side)
    expect(ops.slice(0, side).every((op) => op.kind === 'delete')).toBe(true)
    expect(ops.slice(side).every((op) => op.kind === 'insert')).toBe(true)
  })
})
