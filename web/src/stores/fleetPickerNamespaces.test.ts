/**
 * The All-clusters picker offers every open cluster's namespaces — including
 * a cluster whose tab was never activated, whose namespace list nothing has
 * read yet. Opening the picker reads each open cluster's list.
 */
import { describe, expect, it, vi } from 'vitest'

const listNamespaces = vi.fn(async (clusterId: string) =>
  (clusterId === 'prod' ? ['shop', 'keda'] : ['shop', 'billing']).map((name) => ({ name, isActive: true, phase: 'Active' })),
)
vi.mock('$lib/api/client', async () => {
  const actual = await vi.importActual<Record<string, unknown>>('$lib/api/client')
  return { ...actual, listNamespaces: (clusterId: string) => listNamespaces(clusterId) }
})

import { workspace } from './workspace.svelte'
import { fleet } from './fleet.svelte'
import { ClusterSession } from './session.svelte'
import { fleetNamespaceChoices } from '$lib/namespaceScope'
import type { Cluster, Namespace } from '$lib/api/client'

const cluster = (id: string) => ({ id, name: id, defaultNamespace: '' }) as unknown as Cluster

describe('the All-clusters namespace picker', () => {
  it("reads a background tab's namespaces when it opens, so the union covers both clusters", async () => {
    const active = new ClusterSession(cluster('prod'))
    active.namespaces = [{ name: 'shop' }, { name: 'keda' }] as Namespace[]
    // Never activated: initialise never ran, so nothing listed its namespaces.
    const background = new ClusterSession(cluster('staging'))
    workspace.sessions = [active, background]

    expect(fleetNamespaceChoices(fleet.clusterNamespaces()).map((c) => c.name)).toEqual(['keda', 'shop'])

    fleet.refreshNamespaces()
    await vi.waitFor(() => expect(background.namespaces).toHaveLength(2))

    expect(listNamespaces).toHaveBeenCalledWith('staging')
    expect(fleetNamespaceChoices(fleet.clusterNamespaces())).toEqual([
      { name: 'billing', hint: 'only on staging' },
      { name: 'keda', hint: 'only on prod' },
      { name: 'shop' },
    ])
    workspace.sessions = []
  })
})
