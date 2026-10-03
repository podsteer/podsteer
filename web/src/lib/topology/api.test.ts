import { afterEach, describe, expect, it } from 'vitest'
import {
  changeConcerns,
  exportTopologyPNG,
  fixtureBackend,
  normaliseGraph,
  onTopologyChanged,
  topology,
  releaseTopology,
  useTopologyBackend,
} from './api'
import { fixtureTopology } from './fixtures'
import { ApiError } from '$lib/api/errors'

afterEach(() => useTopologyBackend(null))

describe('topology api seam', () => {
  it('goes to the generated bindings unless a backend is installed', async () => {
    // The unit-test runtime refuses every bound call, so reaching it at all is
    // what proves the default path is the bindings — as an ApiError, the way
    // every backend failure reaches the page.
    const failure = await topology('c', ['a'], false).catch((error: unknown) => error)
    expect(failure).toBeInstanceOf(ApiError)

    const backend = fixtureBackend(fixtureTopology({ nodes: 10, namespaces: 1 }))
    useTopologyBackend(backend)
    await topology('c', [], true)
    await releaseTopology('c')
    expect(backend.calls).toHaveLength(1)
    expect(backend.released).toEqual(['c'])
  })

  it('answers from a fixture, scoped to the namespaces asked for', async () => {
    const backend = fixtureBackend(fixtureTopology({ nodes: 60, namespaces: 3 }))
    useTopologyBackend(backend)

    const scoped = await topology('c', ['team-1'], false)
    expect(scoped.nodes.length).toBeGreaterThan(0)
    expect(scoped.nodes.every((node) => node.namespace === 'team-1')).toBe(true)
    const ids = new Set(scoped.nodes.map((n) => n.id))
    expect(scoped.edges.every((e) => ids.has(e.from) && ids.has(e.to))).toBe(true)
    expect(backend.calls).toEqual([{ clusterId: 'c', namespaces: ['team-1'], all: false }])

    expect(await exportTopologyPNG('x.png', 'AAAA')).toBe('/tmp/x.png')
    expect(backend.saved).toEqual([{ name: 'x.png', bytes: 3 }])
  })

  it('delivers change events through the seam', () => {
    const backend = fixtureBackend(fixtureTopology({ nodes: 10 }))
    useTopologyBackend(backend)
    const seen: string[][] = []
    const off = onTopologyChanged((event) => seen.push(event.namespaces))
    backend.emit({ clusterId: 'c', namespaces: ['a'] })
    off()
    backend.emit({ clusterId: 'c', namespaces: ['b'] })
    expect(seen).toEqual([['a']])
  })

  it('decides which changes concern the drawn scope', () => {
    const scope = { namespaces: ['a', 'b'], all: false }
    expect(changeConcerns({ clusterId: 'c', namespaces: ['b'] }, 'c', scope)).toBe(true)
    expect(changeConcerns({ clusterId: 'c', namespaces: ['z'] }, 'c', scope)).toBe(false)
    expect(changeConcerns({ clusterId: 'other', namespaces: ['a'] }, 'c', scope)).toBe(false)
    // A cluster-scoped change names no namespace and concerns every scope.
    expect(changeConcerns({ clusterId: 'c', namespaces: [] }, 'c', scope)).toBe(true)
    expect(changeConcerns({ clusterId: 'c', namespaces: ['z'] }, 'c', { namespaces: [], all: true })).toBe(true)
  })

  it('narrows the generated graph to the contract', () => {
    const graph = normaliseGraph({
      nodes: [
        { id: 'a', kind: 'pod', apiKind: 'Pod', name: 'a', namespace: 'n', state: 'mystery', detail: '', group: '', labels: null, podSummary: null },
      ],
      edges: [
        { from: 'a', to: 'a', kind: 'owns', label: '' },
        { from: 'a', to: 'a', kind: 'telepathy', label: '' },
      ],
      counts: { Pod: 1 },
      unreadable: null,
      bounded: '',
      summarised: false,
      generatedAt: '',
    })
    // An unknown state is "nothing checked", never healthy; an unknown kind of
    // relationship is not drawn.
    expect(graph.nodes[0].state).toBe('neutral')
    expect(graph.nodes[0].labels).toBeUndefined()
    expect(graph.nodes[0].podSummary).toBeUndefined()
    expect(graph.edges.map((e) => e.kind)).toEqual(['owns'])
  })

  it('turns Go nils into empty collections', () => {
    const graph = normaliseGraph({ nodes: null, edges: null, counts: null, unreadable: null } as never)
    expect(graph).toMatchObject({ nodes: [], edges: [], counts: {}, unreadable: [], bounded: '', summarised: false })
  })
})
