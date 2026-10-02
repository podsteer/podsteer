/**
 * The topology opens an object's drawer OVER itself: the page on screen stays
 * the topology, and the drawer reads the object's own kind.
 */
import { describe, expect, it, vi } from 'vitest'

const getManifest = vi.fn()
const listTable = vi.fn()
vi.mock('$lib/api/client', async () => {
  const actual = await vi.importActual<Record<string, unknown>>('$lib/api/client')
  return {
    ...actual,
    getManifest: (...args: unknown[]) => getManifest(...args),
    listTable: (...args: unknown[]) => listTable(...args),
    getOverview: vi.fn().mockRejectedValue(new Error('[unknown] no cluster in a test')),
    listKinds: vi.fn().mockRejectedValue(new Error('[unknown] no cluster in a test')),
  }
})

import { ClusterSession, TOPOLOGY_KIND_ID } from './session.svelte'
import type { Cluster } from '$lib/api/client'

const cluster = { id: 'dev', name: 'dev', defaultNamespace: 'default' } as unknown as Cluster

describe('a drawer over the topology', () => {
  it('keeps the topology on screen and reads the object by its own kind', async () => {
    getManifest.mockResolvedValue('kind: Deployment')
    const session = new ClusterSession(cluster)
    session.selectedKindId = TOPOLOGY_KIND_ID

    await session.openDetailOver('apps/v1/deployments', 'web', 'shop')

    expect(session.viewMode).toBe('topology')
    expect(session.selectedKindId).toBe(TOPOLOGY_KIND_ID)
    expect(session.drawerKindId).toBe('apps/v1/deployments')
    expect(session.selectedName).toBe('web')
    expect(getManifest).toHaveBeenLastCalledWith('dev', 'apps/v1/deployments', 'shop', 'web', false)
    expect(listTable).not.toHaveBeenCalled()

    session.closeDetail()
    expect(session.drawerKindId).toBe(TOPOLOGY_KIND_ID)
    expect(session.viewMode).toBe('topology')
  })

  it('follows a reference from that drawer over the map too', async () => {
    getManifest.mockResolvedValue('kind: Pod')
    const session = new ClusterSession(cluster)
    session.selectedKindId = TOPOLOGY_KIND_ID

    await session.openObject('core/v1/pods', 'web-1', 'shop', true)

    expect(session.selectedKindId).toBe(TOPOLOGY_KIND_ID)
    expect(session.drawerKindId).toBe('core/v1/pods')
    expect(session.selectedName).toBe('web-1')
  })
})
