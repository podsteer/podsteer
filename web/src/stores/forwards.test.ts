import { beforeEach, describe, expect, it, vi } from 'vitest'

const listPortForwards = vi.fn()
const startPortForward = vi.fn()
const stopPortForward = vi.fn()
const stopAllPortForwards = vi.fn()
const startServicePortForward = vi.fn()

vi.mock('$lib/api/client', () => ({
  listPortForwards: (...args: unknown[]) => listPortForwards(...args),
  startPortForward: (...args: unknown[]) => startPortForward(...args),
  startServicePortForward: (...args: unknown[]) => startServicePortForward(...args),
  stopPortForward: (...args: unknown[]) => stopPortForward(...args),
  stopAllPortForwards: (...args: unknown[]) => stopAllPortForwards(...args),
}))

import { forwards } from './forwards.svelte'
import { preferences } from './preferences.svelte'
import type { PortForward } from '$lib/api/client'

function fixtureForward(overrides: Partial<PortForward> = {}): PortForward {
  return {
    id: '1',
    clusterId: 'dev',
    namespace: 'web',
    pod: 'postgres-0',
    localPort: 15432,
    remotePort: 5432,
    address: 'http://localhost:15432',
    scheme: 'http',
    reconnecting: false,
    ...overrides,
  }
}

beforeEach(() => {
  listPortForwards.mockReset().mockResolvedValue([])
  startPortForward.mockReset()
  stopPortForward.mockReset()
  stopAllPortForwards.mockReset()
  startServicePortForward.mockReset()
  forwards.active = []
  forwards.error = ''
})

describe('starting a forward', () => {
  it('passes the typed local port straight through', async () => {
    startPortForward.mockResolvedValue(fixtureForward({ localPort: 25432 }))

    await forwards.start('dev', 'web', 'postgres-0', 'uid-1', 5432, 'postgres', 'TCP', {}, 25432)

    expect(startPortForward).toHaveBeenCalledWith(
      'dev',
      'web',
      'postgres-0',
      'uid-1',
      25432,
      5432,
      'postgres',
      'TCP',
      {},
    )
  })

  it('defaults to zero — the operating system chooses — when nothing was typed', async () => {
    // No localPort argument at all: every existing call site before this
    // feature landed still means "I have no opinion", and that has to keep
    // working unchanged.
    startPortForward.mockResolvedValue(fixtureForward())

    await forwards.start('dev', 'web', 'postgres-0', 'uid-1', 5432, 'postgres', 'TCP', {})

    expect(startPortForward).toHaveBeenCalledWith(
      'dev',
      'web',
      'postgres-0',
      'uid-1',
      0,
      5432,
      'postgres',
      'TCP',
      {},
    )
  })

  it('remembers the port that was actually bound, by remote port and by name', async () => {
    // The OS (or the operator) may not get exactly what was asked for — this
    // asserts what gets remembered is what actually bound, not the request.
    startPortForward.mockResolvedValue(fixtureForward({ localPort: 25432 }))

    await forwards.start('dev', 'web', 'postgres-0', 'uid-1', 5432, 'postgres', 'TCP', {})

    expect(preferences.proposeLocalPort(5432, 'postgres')).toBe(25432)
    expect(preferences.proposeLocalPort(5432, '')).toBe(25432)
  })

  it('reports a failure without touching the list', async () => {
    startPortForward.mockRejectedValue(new Error('[forbidden] not allowed'))

    await forwards.start('dev', 'web', 'postgres-0', 'uid-1', 5432, 'postgres', 'TCP', {})

    expect(forwards.error).not.toBe('')
    // A failed start must not refresh — there is nothing new to show, and a
    // stale list flashing would read as the start having half-worked.
    expect(listPortForwards).not.toHaveBeenCalled()
  })
})

describe('stopping every forward', () => {
  it('does nothing when nothing is running', async () => {
    await forwards.stopAll()

    expect(stopAllPortForwards).not.toHaveBeenCalled()
  })

  it('stops everything in one call and refreshes the list', async () => {
    forwards.active = [fixtureForward()]
    stopAllPortForwards.mockResolvedValue(undefined)
    listPortForwards.mockResolvedValue([])

    await forwards.stopAll()

    // ONE call, not one per forward: the backend already tears every forward
    // down and waits for each port to be released in a single pass.
    expect(stopAllPortForwards).toHaveBeenCalledOnce()
    expect(forwards.active).toEqual([])
  })

  it('reports a failure to stop everything', async () => {
    forwards.active = [fixtureForward()]
    stopAllPortForwards.mockRejectedValue(new Error('[internal] failed'))

    await forwards.stopAll()

    expect(forwards.error).not.toBe('')
    expect(forwards.stoppingAll).toBe(false)
  })
})

describe('which forward belongs to which Service', () => {
  it('remembers the Service a forward was started for, and forgets it when the forward goes', async () => {
    // THE ONE THING THE BACKEND'S LIST CANNOT ANSWER, and the reason it
    // cannot is the feature: a Service forward moves to another pod behind
    // the same Service, so the pod on the forward is not what was asked for.
    const forward = fixtureForward({ id: '7', pod: 'postgres-0' })
    startServicePortForward.mockResolvedValue(forward)
    listPortForwards.mockResolvedValue([forward])

    await forwards.startService('dev', 'web', 'postgres', 'postgres', 5432, 15432)

    expect(forwards.forService('dev', 'web', 'postgres', 5432)).toMatchObject({ id: '7' })
    expect(forwards.serviceOf('7')).toEqual({ service: 'postgres', servicePort: 5432 })

    // The backend stops listing it — pruned on the next refresh, because this
    // store holds ids and never invents a forward.
    listPortForwards.mockResolvedValue([])
    await forwards.refresh()

    expect(forwards.forService('dev', 'web', 'postgres', 5432)).toBeUndefined()
    expect(forwards.serviceOf('7')).toBeNull()
  })

  it('says nothing about a forward that was started on a pod', async () => {
    // An ordinary pod forward has no Service to name, and the kubectl line
    // beside it must say pod/… rather than invent one.
    const forward = fixtureForward({ id: '3' })
    startPortForward.mockResolvedValue(forward)
    listPortForwards.mockResolvedValue([forward])

    await forwards.start('dev', 'web', 'postgres-0', 'uid-1', 5432, 'postgres', 'TCP', {}, 15432)

    expect(forwards.serviceOf('3')).toBeNull()
  })
})
