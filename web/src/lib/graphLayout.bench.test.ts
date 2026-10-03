/**
 * The topology layout's time budget: 5,000 boxes and 8,000 lines, grouped by
 * namespace, in under three seconds (about 1.1 s on an M-series laptop). In
 * the application this runs in graphLayout.worker.ts, off the UI thread.
 *
 * Run with `npm run bench:layout` (vitest.bench.config.ts); `npm test` does
 * not, because a wall-clock budget flakes on a loaded or slower runner.
 * Correctness at this size is graphLayoutScale.test.ts, which does run.
 */
import { describe, expect, it } from 'vitest'
import { layoutCompound } from './graphLayout'
import { fixtureTopology } from './topology/fixtures'

const BUDGET_MS = 3_000

describe('layout budget', () => {
  it('lays out 5k boxes and 8k lines within budget', () => {
    const graph = fixtureTopology({ nodes: 5_000, edges: 8_000, namespaces: 20 })
    const parents = new Map(graph.nodes.map((node) => [node.id, `group/ns:${node.namespace}`]))
    // One warm-up, then the best of three: the budget is about the code,
    // not about whatever else the machine was doing for one run.
    layoutCompound({ nodes: graph.nodes, edges: graph.edges }, parents, true)
    let best = Infinity
    for (let run = 0; run < 3; run++) {
      const started = performance.now()
      layoutCompound({ nodes: graph.nodes, edges: graph.edges }, parents, true)
      best = Math.min(best, performance.now() - started)
    }
    console.info(`layoutCompound: ${graph.nodes.length} boxes, ${graph.edges.length} lines, best ${Math.round(best)} ms`)
    expect(best).toBeLessThan(BUDGET_MS)
  })
})
