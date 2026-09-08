import { render } from '@testing-library/svelte'
import { describe, expect, it, vi } from 'vitest'

const podGraph = vi.fn()

vi.mock('$lib/api/client', () => ({
  podGraph: (...args: unknown[]) => podGraph(...args),
  workloadGraph: vi.fn(),
  objectGraph: vi.fn(),
}))

import DependencyMap from './DependencyMap.svelte'

const SVG = 'http://www.w3.org/2000/svg'

/**
 * THE MAP DREW ITS EDGES AND NONE OF ITS NODES, AND NOTHING NOTICED.
 *
 * A node's box, icon and label live in a snippet. Svelte decides an element's
 * XML namespace from where its template is WRITTEN, not from where it is
 * rendered — so with the snippet declared at the component's top level, every
 * `<rect>`, `<path>` and `<text>` in it was created as an HTML element. A
 * browser draws nothing for an HTMLUnknownElement called "rect", so the map
 * showed dashed edges floating in empty space for eight days.
 *
 * IT IS ASSERTED AS A NAMESPACE, NOT AS AN APPEARANCE, and that is the whole
 * point of the test existing: the elements were all present in the DOM, with
 * the right tag names, the right attributes and the right geometry. Every
 * question a test can usually ask answered yes. The only thing that was wrong
 * is the one thing that decides whether a browser paints them.
 */
async function drawMap() {
  podGraph.mockResolvedValue({
    nodes: [
      { id: 's1', kind: 'service', apiKind: 'Service', name: 'api', namespace: 'web', tier: 1, healthy: true, subject: false, detail: '' },
      { id: 'p1', kind: 'pod', apiKind: 'Pod', name: 'api-0', namespace: 'web', tier: 3, healthy: true, subject: true, detail: '' },
      // A container has no apiKind: it renders through the other branch, which
      // is a second call site of the same snippet and was equally invisible.
      { id: 'c1', kind: 'container', apiKind: '', name: 'app', namespace: 'web', tier: 4, healthy: true, subject: false, detail: '' },
    ],
    edges: [
      { from: 's1', to: 'p1', kind: 'routes' },
      { from: 'p1', to: 'c1', kind: 'runs' },
    ],
  })

  const rendered = render(DependencyMap, {
    clusterId: 'dev',
    namespace: 'web',
    name: 'api-0',
    kind: 'Pod',
  })

  await vi.waitFor(() => expect(podGraph).toHaveBeenCalled())
  // One tick past the fetch for the derived layout to reach the DOM.
  await new Promise((resolve) => setTimeout(resolve, 50))
  return rendered.container
}

describe('what the map actually draws', () => {
  it('creates every part of a node in the SVG namespace', async () => {
    const container = await drawMap()

    const nodes = [...container.querySelectorAll('[data-node]')]
    expect(nodes.length).toBeGreaterThan(0)

    for (const node of nodes) {
      for (const element of [node, ...node.querySelectorAll('*')]) {
        expect(
          element.namespaceURI,
          `<${element.tagName}> is in the wrong namespace, so nothing is painted`,
        ).toBe(SVG)
      }
    }
  })

  it('draws a box, an icon and a label for each node', async () => {
    const container = await drawMap()

    for (const node of container.querySelectorAll('[data-node]')) {
      expect(node.querySelector('rect'), 'no box').not.toBeNull()
      expect(node.querySelector('path'), 'no icon geometry').not.toBeNull()
      expect(node.querySelector('text'), 'no label').not.toBeNull()
    }
  })

  it('draws an edge for every relationship, in the same namespace', async () => {
    // The edges were the half that always worked — they are written inline in
    // the svg rather than in a snippet — so they are what the broken nodes
    // were compared against. Kept as an assertion so a future refactor that
    // moves THEM into a snippet fails here rather than in somebody's window.
    const container = await drawMap()

    const edges = [...container.querySelectorAll('[data-edge]')]
    expect(edges.length).toBeGreaterThan(0)
    for (const edge of edges) {
      expect(edge.namespaceURI).toBe(SVG)
    }
  })
})
