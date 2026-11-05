import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

// The refresh a revived poll makes is counted here; what it returns is beside
// the point.
const listTable = vi.fn()
vi.mock('$lib/api/client', async () => {
  const actual = await vi.importActual<Record<string, unknown>>('$lib/api/client')
  return {
    ...actual,
    listTable: (...args: unknown[]) => listTable(...args),
    listTableIn: (...args: unknown[]) => listTable(...args),
    getOverview: vi.fn().mockRejectedValue(new Error('[unknown] no cluster in a test')),
    listKinds: vi.fn().mockRejectedValue(new Error('[unknown] no cluster in a test')),
  }
})

import { ClusterSession } from './session.svelte'
import type { Cluster } from '$lib/api/client'

const cluster = { id: 'dev', name: 'dev', defaultNamespace: 'default' } as unknown as Cluster
const INTERVAL = 10_000

function session(): ClusterSession {
  const open = new ClusterSession(cluster)
  open.selectedKindId = 'networking.k8s.io/v1/ingresses'
  return open
}

describe('reviving a poll whose timer stopped firing', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    vi.spyOn(console, 'warn').mockImplementation(() => {})
    listTable.mockReset().mockResolvedValue({ kindId: '', title: '', namespaced: true, columns: [], rows: [] })
  })

  afterEach(() => {
    vi.useRealTimers()
    vi.restoreAllMocks()
  })

  it('leaves a healthy poll alone', () => {
    // A burst of clicks must not become a burst of refreshes.
    const open = session()
    open.startAutoRefresh(INTERVAL)
    vi.advanceTimersByTime(INTERVAL * 3)
    const calls = listTable.mock.calls.length

    open.resume(INTERVAL)
    open.resume(INTERVAL)

    expect(listTable.mock.calls.length).toBe(calls)
    open.stopAutoRefresh()
  })

  it('restarts and refreshes at once when no tick has fired for too long', async () => {
    // The report: a laptop closed overnight, the timer suspended with the
    // web view, and "last refreshed" stuck hours behind the wall clock. The
    // wall clock moving without the timer firing is that, simulated.
    const open = session()
    open.startAutoRefresh(INTERVAL)
    vi.setSystemTime(Date.now() + 3 * 60 * 60 * 1000)

    open.resume(INTERVAL)
    expect(listTable).toHaveBeenCalledTimes(1)

    // And it is a poll again, not a single refresh.
    listTable.mockClear()
    await vi.advanceTimersByTimeAsync(INTERVAL)
    expect(listTable).toHaveBeenCalledTimes(1)
    open.stopAutoRefresh()
  })

  it('starts a poll whose timer is gone', async () => {
    // The other way the poll can die: the effect that owned the timer torn
    // down while the page stayed on screen.
    const open = session()
    open.startAutoRefresh(INTERVAL)
    open.stopAutoRefresh()

    open.resume(INTERVAL)
    expect(listTable).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(INTERVAL)
    expect(listTable).toHaveBeenCalledTimes(2)
    open.stopAutoRefresh()
  })

  it('does nothing when refresh is manual', () => {
    const open = session()
    open.resume(0)
    expect(listTable).not.toHaveBeenCalled()
  })
})
