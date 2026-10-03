import { beforeEach, describe, expect, it, vi } from 'vitest'

const listPortForwards = vi.fn()
const startPortForward = vi.fn()
const stopPortForward = vi.fn()
const stopAllPortForwards = vi.fn()
const startServicePortForward = vi.fn()
const listPausedPortForwards = vi.fn()
const setPortForwardKept = vi.fn()
const resumePausedPortForward = vi.fn()
const forgetPausedPortForward = vi.fn()
const reconnectPortForward = vi.fn()

vi.mock('$lib/api/client', () => ({
  listPortForwards: (...args: unknown[]) => listPortForwards(...args),
  startPortForward: (...args: unknown[]) => startPortForward(...args),
  startServicePortForward: (...args: unknown[]) => startServicePortForward(...args),
  stopPortForward: (...args: unknown[]) => stopPortForward(...args),
  stopAllPortForwards: (...args: unknown[]) => stopAllPortForwards(...args),
  listPausedPortForwards: (...args: unknown[]) => listPausedPortForwards(...args),
  setPortForwardKept: (...args: unknown[]) => setPortForwardKept(...args),
  resumePausedPortForward: (...args: unknown[]) => resumePausedPortForward(...args),
  forgetPausedPortForward: (...args: unknown[]) => forgetPausedPortForward(...args),
  reconnectPortForward: (...args: unknown[]) => reconnectPortForward(...args),
}))

import { forwards } from './forwards.svelte'
import { preferences } from './preferences.svelte'
import { notices } from './notices.svelte'
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
    lost: false,
    kept: false,
    targetKind: 'pod',
    targetName: 'postgres-0',
    ...overrides,
  }
}

beforeEach(() => {
  listPortForwards.mockReset().mockResolvedValue([])
  startPortForward.mockReset()
  stopPortForward.mockReset()
  stopAllPortForwards.mockReset()
  startServicePortForward.mockReset()
  listPausedPortForwards.mockReset().mockResolvedValue([])
  setPortForwardKept.mockReset().mockResolvedValue(undefined)
  resumePausedPortForward.mockReset().mockResolvedValue(undefined)
  forgetPausedPortForward.mockReset().mockResolvedValue(undefined)
  reconnectPortForward.mockReset().mockResolvedValue(undefined)
  notices.clear()
  forwards.paused = []
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

describe('keeping a forward across restarts', () => {
  it('saves nothing unless asked, and saves the started forward when asked', async () => {
    startPortForward.mockResolvedValue(fixtureForward({ id: '7' }))

    await forwards.start('dev', 'web', 'postgres-0', 'uid-1', 5432, 'postgres', 'TCP', {}, 0)
    expect(setPortForwardKept).not.toHaveBeenCalled()

    await forwards.start('dev', 'web', 'postgres-0', 'uid-1', 5432, 'postgres', 'TCP', {}, 0, true)
    expect(setPortForwardKept).toHaveBeenCalledExactlyOnceWith('7', true)
  })

  it('keeps a Service forward through the same switch', async () => {
    startServicePortForward.mockResolvedValue(fixtureForward({ id: '9', targetKind: 'service' }))

    await forwards.startService('dev', 'web', 'pg', 'postgres', 5432, 0, true)

    expect(setPortForwardKept).toHaveBeenCalledExactlyOnceWith('9', true)
  })

  it('a failed save is reported without undoing the forward', async () => {
    startPortForward.mockResolvedValue(fixtureForward({ id: '7' }))
    setPortForwardKept.mockRejectedValue(new Error('[settings_read_only] settings are read-only'))

    await forwards.start('dev', 'web', 'postgres-0', 'uid-1', 5432, 'postgres', 'TCP', {}, 0, true)

    expect(forwards.error).not.toBe('')
    expect(listPortForwards).toHaveBeenCalled()
  })

  it('lists what the backend reports as paused, never anything of its own', async () => {
    listPausedPortForwards.mockResolvedValue([
      { clusterId: 'prod', namespace: 'data', targetKind: 'service', targetName: 'pg', port: 'postgres', localPort: 15432, state: 'paused', reason: '' },
    ])

    await forwards.refresh()

    expect(forwards.paused).toHaveLength(1)
    expect(forwards.paused[0]?.state).toBe('paused')
  })

  it('forgets and resumes through the backend and re-reads', async () => {
    const paused = { clusterId: 'prod', namespace: 'data', targetKind: 'service', targetName: 'pg', port: 'postgres', localPort: 15432, state: 'failed', reason: 'in use' }

    await forwards.resume(paused)
    await forwards.forget(paused)

    expect(resumePausedPortForward).toHaveBeenCalledWith('prod', 15432)
    expect(forgetPausedPortForward).toHaveBeenCalledWith('prod', 15432)
    expect(listPausedPortForwards).toHaveBeenCalledTimes(2)
  })
})

describe('a forward that was lost', () => {
  it('says so once when it turns lost, and not on every tick after', async () => {
    listPortForwards.mockResolvedValue([fixtureForward({ id: '1' })])
    await forwards.refresh()
    expect(notices.items).toHaveLength(0)

    listPortForwards.mockResolvedValue([fixtureForward({ id: '1', lost: true })])
    await forwards.refresh()
    await forwards.refresh()

    expect(notices.items).toHaveLength(1)
    expect(notices.items[0]?.message).toContain('was lost')
    expect(forwards.lost).toHaveLength(1)
  })

  it('asks the backend to reconnect it', async () => {
    const lost = fixtureForward({ id: '1', lost: true })
    await forwards.reconnect(lost)
    expect(reconnectPortForward).toHaveBeenCalledWith('1')
  })
})
