import { beforeEach, describe, expect, it, vi } from 'vitest'

const pingCluster = vi.fn()
vi.mock('$lib/api/client', async () => {
  const actual = await vi.importActual<Record<string, unknown>>('$lib/api/client')
  return {
    ...actual,
    pingCluster: (clusterId: string) => pingCluster(clusterId),
    listClusters: vi.fn().mockResolvedValue([]),
    connections: vi.fn().mockResolvedValue([]),
    onClusterUnreachable: () => () => {},
    onKubeconfigChanged: () => () => {},
  }
})

import { workspace } from './workspace.svelte'
import { ClusterSession } from './session.svelte'
import type { Cluster } from '$lib/api/client'

const session = (id: string) =>
  new ClusterSession({ id, name: id, defaultNamespace: 'default' } as unknown as Cluster)

describe('the heartbeat for tabs that are not in front', () => {
  beforeEach(() => {
    pingCluster.mockReset().mockResolvedValue(undefined)
    workspace.sessions = [session('alpha'), session('beta'), session('gamma')]
    workspace.activeClusterId = 'alpha'
  })

  it('skips the tab in front, which is already polling', () => {
    // Its workspace is mounted and its own refresh records contact on every
    // tick; a second request per interval would buy nothing.
    return workspace.beat().then(() => {
      expect(pingCluster.mock.calls.map(([id]) => id)).toEqual(['beta', 'gamma'])
    })
  })

  it('marks a background cluster silent when its ping times out', async () => {
    // The shape the report came in as: with the network gone, calls do not
    // fail, they EXPIRE — a blackholed packet is never refused — and the Go
    // side now reports that expiry as unreachable rather than as a
    // cancellation.
    pingCluster.mockImplementation((id: string) =>
      id === 'beta'
        ? Promise.reject(new Error('[unreachable] The cluster did not answer before the request timed out'))
        : Promise.resolve(),
    )

    await workspace.beat()

    const beta = workspace.sessions.find((entry) => entry.cluster.id === 'beta')
    const gamma = workspace.sessions.find((entry) => entry.cluster.id === 'gamma')
    expect(beta?.answering).toBe(false)
    expect(gamma?.answering).toBe(true)
  })

  it('does NOT mark one silent for a permission failure', async () => {
    // Painting a tab red because an account may not read something would send
    // somebody to check a VPN over an RBAC rule.
    pingCluster.mockRejectedValue(new Error('[forbidden] your account may not do that'))

    await workspace.beat()

    expect(workspace.sessions.every((entry) => entry.answering)).toBe(true)
  })

  it('clears the mark when the cluster answers again', async () => {
    pingCluster.mockRejectedValue(new Error('[unreachable] gone'))
    await workspace.beat()
    expect(workspace.sessions[1]?.answering).toBe(false)

    pingCluster.mockResolvedValue(undefined)
    await workspace.beat()

    expect(workspace.sessions[1]?.answering).toBe(true)
  })

  it('does not run at all when refresh is manual', () => {
    // Somebody who turned auto-refresh off chose to stop talking to their
    // clusters; a heartbeat they did not ask for would decide otherwise.
    workspace.startHeartbeat(0)

    expect(pingCluster).not.toHaveBeenCalled()
    workspace.stopHeartbeat()
  })
})
