import { readFileSync, readdirSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'

/**
 * One sentence, written down six times.
 *
 * "This cluster is marked read-only in PodSteer. Change that under Organise."
 * is the backend's own refusal — CodeReadOnly in app/adapters/wails/errors.go
 * — and the interface repeats it wherever a control is disabled BEFORE it is
 * pressed, so somebody learns why without pressing anything. That is the right
 * design: a control that fails on press to tell you it was never going to work
 * is worse than one that says so first.
 *
 * WHAT IS NOT RIGHT IS SIX COPIES AND ONE ANNOTATION. rowActions.ts marks its
 * copy as verbatim from the backend; the others carry no note and no pointer,
 * so a reword on the Go side leaves five silent disagreements about what
 * PodSteer just refused and why. This is the same drift the error-code parity
 * test exists for, in prose.
 */
const HERE = dirname(fileURLToPath(import.meta.url))
const GO_ERRORS = join(HERE, '..', '..', '..', 'app', 'adapters', 'wails', 'errors.go')

/** The sentence as the Go side writes it, read from the Go source. */
function backendSentence(): string {
  const source = readFileSync(GO_ERRORS, 'utf8')
  const match = source.match(/return CodeReadOnly, "([^"]+)"/)
  expect(match, 'CodeReadOnly no longer returns a single-line literal').not.toBeNull()
  return match?.[1] ?? ''
}

/** Every .svelte and .ts file under web/src, read once. */
function sources(dir: string, out: { path: string; text: string }[] = []) {
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const path = join(dir, entry.name)
    if (entry.isDirectory()) {
      if (entry.name === 'bindings') continue
      sources(path, out)
      continue
    }
    if (/\.(svelte|ts)$/.test(entry.name)) out.push({ path, text: readFileSync(path, 'utf8') })
  }
  return out
}

describe('the read-only refusal', () => {
  it('is worded the same everywhere the interface repeats it', () => {
    const sentence = backendSentence()
    expect(sentence).toContain('read-only')

    // Any file that sends somebody to Organise about the read-only mark is
    // repeating this sentence, so it must repeat it EXACTLY. Matched on the
    // phrase rather than by parsing quotes, because prose here is full of
    // apostrophes and a quote-aware regex reports the punctuation between two
    // unrelated strings as a near-miss.
    const stale: string[] = []
    for (const { path, text } of sources(join(HERE, '..'))) {
      if (path.endsWith('readOnlyReason.node.test.ts')) continue
      if (!text.includes('under Organise')) continue
      if (!text.includes(sentence)) stale.push(path)
    }

    expect(
      stale,
      'these send somebody to Organise about the read-only mark without using the backend\'s own ' +
        'sentence from app/adapters/wails/errors.go, so PodSteer would explain one refusal two ways',
    ).toEqual([])
  })

  it('is repeated where a control is disabled before it is pressed', () => {
    // The design this protects: a control that fails ON PRESS to tell you it
    // was never going to work is worse than one that says so first. These are
    // the surfaces that disable rather than fail.
    const sentence = backendSentence()
    const repeats = sources(join(HERE, '..')).filter(
      ({ path, text }) => !path.endsWith('readOnlyReason.node.test.ts') && text.includes(sentence),
    )

    expect(repeats.length, 'nothing repeats the refusal any more — has a control started failing on press?').toBeGreaterThan(2)
  })
})
