import { describe, expect, it, vi } from 'vitest'

/**
 * A cluster that never answers.
 *
 * `listKinds` is what a restored tab calls first, and it is left pending
 * FOREVER on purpose: that is what an API server behind a VPN that is not up
 * yet looks like from here — not a rejection, which the code already handles,
 * but silence.
 */
const listKinds = vi.fn((_clusterId: string) => new Promise(() => {}))
const listNamespaces = vi.fn((_clusterId: string) => new Promise(() => {}))

vi.mock('$lib/api/client', async () => {
  const actual = await vi.importActual<Record<string, unknown>>('$lib/api/client')
  return {
    ...actual,
    listClusters: vi.fn().mockResolvedValue([]),
    connections: vi.fn().mockResolvedValue([
      { id: 'dev', name: 'dev', defaultNamespace: 'default' },
    ]),
    setReadOnly: vi.fn().mockResolvedValue(undefined),
    onClusterUnreachable: vi.fn(() => () => {}),
    listKinds: (clusterId: string) => listKinds(clusterId),
    listNamespaces: (clusterId: string) => listNamespaces(clusterId),
  }
})

import { workspace } from './workspace.svelte'

describe('starting up', () => {
  it('does not wait for the cluster the last session had open', async () => {
    // THE BUG THIS EXISTS FOR. The splash screen waits on this function, and
    // this function used to await the restored tab's own initialise — which
    // lists kinds, lists namespaces and reads a view, every one of them a
    // round trip. An operator whose cluster was slow that morning got a
    // splash screen for as long as the network took: no cluster list, no
    // settings, and no way to reach the clusters that were answering fine.
    //
    // Racing against a timer rather than asserting on a flag, because what
    // failed was that the promise never settled at all.
    const settled = await Promise.race([
      workspace.initialise().then(() => 'booted'),
      new Promise((resolve) => setTimeout(() => resolve('still waiting'), 750)),
    ])

    expect(settled).toBe('booted')

    // And the tab it could not reach IS there, loading, rather than being
    // dropped to make the start-up quick.
    expect(workspace.sessions.map((session) => session.cluster.id)).toEqual(['dev'])
    expect(listKinds).toHaveBeenCalledWith('dev')
  })
})
