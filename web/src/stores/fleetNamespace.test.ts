/**
 * All clusters has ONE namespace for the window.
 *
 * Its rows are one set for the window, and they used to be read in whichever
 * tab was in front's own namespace — so two tabs on different namespaces
 * overwrote each other: the second tab showed the first tab's rows and then
 * its own poll wiped them. These pin the store half of the fix: the scope is
 * the store's, a change of scope drops the old scope's rows, and a read still
 * in flight for the old scope cannot land afterwards.
 */
import { beforeEach, describe, expect, it, vi } from 'vitest'

const listFleetPods = vi.fn()
vi.mock('$lib/api/client', async () => {
  const actual = await vi.importActual<Record<string, unknown>>('$lib/api/client')
  return {
    ...actual,
    listFleetPods: (...args: unknown[]) => listFleetPods(...args),
  }
})

import { ALL_NAMESPACES } from '$lib/api/client'
import { fleet } from './fleet.svelte'

function answer(cluster: string, names: string[]) {
  return {
    cluster,
    status: 'ok',
    reason: '',
    missing: [],
    pods: names.map((name) => ({ name, namespace: 'shop', labels: {} })),
  }
}

beforeEach(() => {
  listFleetPods.mockReset()
  fleet.openClusters = () => ['prod', 'staging']
  fleet.tab = 'pods'
  fleet.chooseNamespace(ALL_NAMESPACES)
  fleet.pods = []
})

describe('the All clusters namespace', () => {
  it('starts on every namespace, whatever any tab is filtered to', () => {
    expect(fleet.namespace).toBe(ALL_NAMESPACES)
  })

  it('drops the old scope’s rows when the namespace changes', async () => {
    listFleetPods.mockResolvedValue([answer('prod', ['a', 'b'])])
    await fleet.refresh(fleet.namespace)
    expect(fleet.podRows).toHaveLength(2)

    fleet.chooseNamespace('default')

    expect(fleet.namespace).toBe('default')
    expect(fleet.podRows).toHaveLength(0)
  })

  it('discards a read still in flight for the old namespace', async () => {
    let settle: (value: unknown) => void = () => {}
    listFleetPods.mockReturnValue(new Promise((resolve) => (settle = resolve)))

    const inFlight = fleet.refresh(fleet.namespace)
    fleet.chooseNamespace('default')
    settle([answer('prod', ['stale'])])
    await inFlight

    expect(fleet.podRows).toHaveLength(0)
  })
})
