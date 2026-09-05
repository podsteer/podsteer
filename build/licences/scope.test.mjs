/**
 * Tests for the Go scope partition.
 *
 * `node:test` rather than vitest deliberately: vitest is scoped to `web/` and
 * configured for a browser-ish environment, and `build/` is plain Node with no
 * runner of its own. `node --test build/licences/` needs nothing installed,
 * which matters for a check that gates releases.
 *
 * The fixtures are the real modules that made this decision necessary, named
 * literally so a regression arrives with the name of the thing it broke.
 */

import assert from 'node:assert/strict'
import { test } from 'node:test'

import { partitionGoModules } from './scope.mjs'

/** The module whose misclassification prompted this partition. */
const QSORT = 'github.com/konoui/go-qsort@v0.1.0'
/** Reached by tests only — the case the old cache predicate could not see. */
const TESTIFY = 'github.com/stretchr/testify@v1.10.0'
/** Linked on every platform. */
const WAILS = 'github.com/wailsapp/wails/v3@v3.0.0-beta.16'
/** Linked under GOOS=windows and nowhere else. */
const TOAST = 'git.sr.ht/~jackmordaunt/go-toast/v2@v2.0.1'

test('a module both linked and reached is shipped, a reached-only module is build, and a module in the graph that nothing reaches is graph-only', () => {
  const result = partitionGoModules({
    graph: [WAILS, TESTIFY, QSORT],
    linked: [WAILS],
    reached: [WAILS, TESTIFY],
  })

  assert.deepEqual(result.shipped, [WAILS])
  assert.deepEqual(result.build, [TESTIFY])
  // go-qsort publishes no licence file and participates in no build; it is in
  // the graph only because it is an unused require of wails/v3. Classifying it
  // at all is what failed the gate.
  assert.deepEqual(result.graphOnly, [QSORT])
})

test('a linked module missing from the reached set throws, because the two go list runs disagreed', () => {
  assert.throws(
    () =>
      partitionGoModules({
        graph: [WAILS, TESTIFY],
        linked: [WAILS],
        reached: [TESTIFY],
      }),
    (error) => error instanceof Error && error.message.includes(WAILS),
  )
})

test('a linked module missing from the module graph throws, because the graph does not describe this module', () => {
  assert.throws(
    () =>
      partitionGoModules({
        graph: [TESTIFY],
        linked: [WAILS],
        reached: [WAILS, TESTIFY],
      }),
    (error) => error instanceof Error && error.message.includes(WAILS),
  )
})

test('a module linked on one platform only is still shipped, because the caller unions the platforms before partitioning', () => {
  // The recorded go-webview2 lesson, which now applies to go-toast: v3 reaches
  // the toast stack under GOOS=windows and nowhere else, so a partition fed a
  // single platform's listing would classify a shipped module as build-only.
  const darwin = [WAILS]
  const windows = [WAILS, TOAST]

  const result = partitionGoModules({
    graph: [WAILS, TOAST, QSORT],
    linked: new Set([...darwin, ...windows]),
    reached: new Set([...darwin, ...windows]),
  })

  assert.deepEqual(result.shipped, [TOAST, WAILS].sort())
  assert.deepEqual(result.build, [])
  assert.deepEqual(result.graphOnly, [QSORT])
})

test('the partition is sorted and insensitive to the order of its inputs', () => {
  const graph = [WAILS, TESTIFY, QSORT, TOAST]
  const linked = [WAILS, TOAST]
  const reached = [WAILS, TOAST, TESTIFY]

  const forwards = partitionGoModules({ graph, linked, reached })
  const backwards = partitionGoModules({
    graph: [...graph].reverse(),
    linked: [...linked].reverse(),
    reached: [...reached].reverse(),
  })

  assert.deepEqual(forwards, backwards)
  for (const key of ['shipped', 'build', 'graphOnly']) {
    assert.deepEqual(forwards[key], [...forwards[key]].sort(), `${key} is not sorted`)
  }
})
