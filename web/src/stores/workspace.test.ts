import { beforeEach, describe, expect, it, vi } from 'vitest'

/**
 * A cluster that never answers.
 *
 * `listKinds` is what a restored tab calls first, and it is left pending
 * FOREVER on purpose: that is what an API server behind a VPN that is not up
 * yet looks like from here — not a rejection, which the code already handles,
 * but silence.
 */
/** Handlers the workspace registered for the kubeconfig-changed event. */
const kubeconfigHandlers: Array<() => void> = []
/** Named, so a test can assert the list was re-read rather than guessing. */
const listClustersMock = vi.fn().mockResolvedValue([])

const listKinds = vi.fn((_clusterId: string) => new Promise(() => {}))
const listNamespaces = vi.fn((_clusterId: string) => new Promise(() => {}))

/**
 * Connects that answer only when the test says so.
 *
 * `slow` is the cluster behind a link that drops packets rather than refusing
 * them: it answers neither yes nor no until something settles it, which is
 * exactly the case that used to hold every other cluster's control hostage.
 */
const pending = new Map<string, { resolve: (value: unknown) => void; reject: (cause: unknown) => void }>()
const connect = vi.fn(
  (clusterId: string) =>
    new Promise((resolve, reject) => {
      pending.set(clusterId, { resolve, reject })
    }),
)
const cancelConnect = vi.fn((clusterId: string) => {
  pending.get(clusterId)?.reject(new Error('[cancelled] The request was cancelled or timed out'))
  return Promise.resolve()
})

vi.mock('$lib/api/client', async () => {
  const actual = await vi.importActual<Record<string, unknown>>('$lib/api/client')
  return {
    ...actual,
    listClusters: () => listClustersMock(),
    connections: vi.fn().mockResolvedValue([
      { id: 'dev', name: 'dev', defaultNamespace: 'default' },
    ]),
    setReadOnly: vi.fn().mockResolvedValue(undefined),
    onClusterUnreachable: vi.fn(() => () => {}),
    onKubeconfigChanged: (handler: () => void) => {
      kubeconfigHandlers.push(handler)
      return () => {}
    },
    listKinds: (clusterId: string) => listKinds(clusterId),
    listNamespaces: (clusterId: string) => listNamespaces(clusterId),
    connect: (clusterId: string) => connect(clusterId),
    cancelConnect: (clusterId: string) => cancelConnect(clusterId),
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


describe('connecting to several clusters', () => {
  beforeEach(() => {
    pending.clear()
    connect.mockClear()
    cancelConnect.mockClear()
    workspace.connecting = []
    workspace.error = null
  })

  it('starts every one asked for, without waiting for the one before', async () => {
    // THE BUG THIS EXISTS FOR. `open` began with `if (this.connectingTo)
    // return`, so an operator with five clusters and two of them unreachable
    // waited out both failures — one request timeout each — before the three
    // that were fine would even begin. Nothing about connecting was serial on
    // the Go side; the queue was here.
    void workspace.open('slow-one', false)
    void workspace.open('slow-two', false)
    void workspace.open('quick', false)
    await Promise.resolve()

    expect(connect.mock.calls.map(([id]) => id)).toEqual(['slow-one', 'slow-two', 'quick'])
    expect(workspace.connecting).toEqual(['slow-one', 'slow-two', 'quick'])
  })

  it('ignores a second click on the same card', async () => {
    // The card that is already trying is the one case where doing nothing is
    // right: a duplicate attempt opens a second connection to the same
    // cluster and leaves one of them unaccounted for.
    void workspace.open('one', false)
    void workspace.open('one', false)
    await Promise.resolve()

    expect(connect).toHaveBeenCalledTimes(1)
  })

  it('stops the attempt the operator stopped, and leaves the others running', async () => {
    void workspace.open('slow-one', false)
    void workspace.open('slow-two', false)
    await Promise.resolve()

    await workspace.stopConnecting('slow-one')
    await vi.waitFor(() => expect(workspace.connecting).toEqual(['slow-two']))

    expect(cancelConnect).toHaveBeenCalledWith('slow-one')
    expect(cancelConnect).toHaveBeenCalledTimes(1)
  })

  it('does not report a stopped attempt as a failure', async () => {
    // A cancellation is an answer, not a fault. Reporting it back as an error
    // banner is the application arguing with a decision it was told about.
    void workspace.open('slow-one', false)
    await Promise.resolve()

    await workspace.stopConnecting('slow-one')
    await vi.waitFor(() => expect(workspace.connecting).toEqual([]))

    expect(workspace.error).toBeNull()
  })

  it('still reports a failure nobody asked for', async () => {
    // The limit of the rule above: a cluster that refuses or times out on its
    // own has to say so, or a failed connect looks like a click that missed.
    void workspace.open('broken', false)
    await Promise.resolve()

    pending.get('broken')?.reject(new Error('[unreachable] Could not reach the cluster'))
    await vi.waitFor(() => expect(workspace.error).not.toBeNull())
  })
})


describe('the kubeconfig changing on disk', () => {
  beforeEach(() => {
    listClustersMock.mockClear()
  })

  it('re-reads the cluster list and leaves every open tab alone', async () => {
    // A kubeconfig changing is somebody running `kubectl config use-context`
    // in another window, or a colleague's file landing in a synced folder.
    // What it must not do is touch a connection this operator made: not
    // reconnect it, not close it, and not follow a current-context that
    // moved, which is a decision about somebody else's terminal.
    await workspace.initialise()
    const openBefore = workspace.sessions.map((session) => session.cluster.id)
    listClustersMock.mockClear()

    expect(kubeconfigHandlers.length).toBeGreaterThan(0)
    for (const handler of kubeconfigHandlers) handler()
    await vi.waitFor(() => expect(listClustersMock).toHaveBeenCalled())

    expect(workspace.sessions.map((session) => session.cluster.id)).toEqual(openBefore)
  })
})
