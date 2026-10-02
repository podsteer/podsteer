/**
 * The layout benchmark, as a test with a budget: 5,000 boxes and 8,000 lines,
 * grouped by namespace, laid out in under three seconds — the plan's budget
 * for what the worker may take before the page is worse than the summary tier
 * it would otherwise fall back to.
 *
 * Measured on the main thread here because that is where vitest runs; in the
 * application the same call runs in graphLayout.worker.ts, so this is the
 * worker's time and the interface thread pays none of it.
 */
import { describe, expect, it } from 'vitest'
import { layoutCompound } from './graphLayout'
import { fixtureTopology } from './topology/fixtures'

const BUDGET_MS = 3_000

describe('layout budget', () => {
  it('lays out 5k boxes and 8k lines within budget', () => {
    const graph = fixtureTopology({ nodes: 5_000, edges: 8_000, namespaces: 20 })
    expect(graph.nodes.length).toBeGreaterThanOrEqual(5_000)
    expect(graph.edges.length).toBe(8_000)

    const parents = new Map(graph.nodes.map((node) => [node.id, `group/ns:${node.namespace}`]))
    const started = performance.now()
    const layout = layoutCompound({ nodes: graph.nodes, edges: graph.edges }, parents, true)
    const ms = performance.now() - started

    console.info(`layoutCompound: ${graph.nodes.length} boxes, ${graph.edges.length} lines, ${Math.round(ms)} ms`)
    expect(layout.nodes).toHaveLength(graph.nodes.length)
    expect(layout.groups).toHaveLength(20)
    expect(ms).toBeLessThan(BUDGET_MS)
  })
})
