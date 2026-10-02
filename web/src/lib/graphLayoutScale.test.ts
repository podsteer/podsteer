/**
 * layoutCompound at the size the topology is built for: 5,000 boxes and 8,000
 * lines, grouped by namespace. CORRECTNESS ONLY — every box placed, every
 * frame drawn. How long it takes is a property of the machine, so the
 * three-second budget lives in graphLayout.bench.test.ts (`npm run bench:layout`),
 * which `npm test` does not run: a wall-clock assertion flakes on a loaded or
 * slower runner without anything being wrong.
 */
import { describe, expect, it } from 'vitest'
import { layoutCompound } from './graphLayout'
import { fixtureTopology } from './topology/fixtures'

describe('layout at scale', () => {
  it('places 5k boxes and 8k lines in 20 frames', () => {
    const graph = fixtureTopology({ nodes: 5_000, edges: 8_000, namespaces: 20 })
    expect(graph.nodes.length).toBeGreaterThanOrEqual(5_000)
    expect(graph.edges.length).toBe(8_000)

    const parents = new Map(graph.nodes.map((node) => [node.id, `group/ns:${node.namespace}`]))
    const layout = layoutCompound({ nodes: graph.nodes, edges: graph.edges }, parents, true)

    expect(layout.nodes).toHaveLength(graph.nodes.length)
    expect(layout.groups).toHaveLength(20)
    expect(layout.edges.length).toBeGreaterThan(0)
    expect(layout.nodes.every((node) => Number.isFinite(node.x) && Number.isFinite(node.y))).toBe(true)
  })
})
