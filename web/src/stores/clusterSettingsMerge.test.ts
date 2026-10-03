/**
 * Loading one cluster's row must never clobber the others: the overview loads
 * its own cluster, and Settings → Clusters then showed every other cluster as
 * Off — and a save from such a row wrote that back over the file.
 */
import { describe, expect, it, vi } from 'vitest'

const stored: Record<string, Record<string, unknown>> = {
  a: { clusterId: 'a', nodeHistory: false, metricsQueryMode: 'auto', preferredNamespace: 'mon', preferredService: 'prom', fleetPolicy: 'filter' },
  b: { clusterId: 'b', nodeHistory: false, metricsQueryMode: 'manual', preferredNamespace: '', preferredService: '', fleetPolicy: 'filter' },
}
const getClusterSettings = vi.fn(async (ids: string[]) => ids.filter((id) => stored[id]).map((id) => stored[id]))
const setMetricsQuery = vi.fn(async (..._args: unknown[]) => {})
vi.mock('$lib/api/client', async () => {
  const actual = await vi.importActual<Record<string, unknown>>('$lib/api/client')
  return {
    ...actual,
    getClusterSettings: (ids: string[]) => getClusterSettings(ids),
    getSettingsState: async () => ({ writable: true, notice: '' }),
    setMetricsQuery: (...args: unknown[]) => setMetricsQuery(...args),
  }
})

import { clusterSettings } from './clusterSettings.svelte'

describe('cluster settings load', () => {
  it('merges one cluster into the rows already held, and a later save keeps them', async () => {
    await clusterSettings.load(['a', 'b'])
    await clusterSettings.load(['b'])

    expect(clusterSettings.for('a').metricsQueryMode).toBe('auto')
    expect(clusterSettings.for('a').preferredService).toBe('prom')

    // A save from b's row reloads EVERY id asked about, not only b.
    await clusterSettings.save('b', { fleetPolicy: 'refuse' })
    expect(getClusterSettings).toHaveBeenLastCalledWith(['a', 'b'])
    expect(clusterSettings.for('a').preferredNamespace).toBe('mon')
  })

  it('drops a row the file no longer holds when that id is asked about', async () => {
    await clusterSettings.load(['a', 'b'])
    delete stored.b
    await clusterSettings.load(['b'])
    expect(clusterSettings.for('b').metricsQueryMode).toBe('off')
    expect(clusterSettings.for('a').metricsQueryMode).toBe('auto')
  })
})

describe('saving never writes a guess', () => {
  it('reads a row it never loaded before saving, so the rest of it is the file’s', async () => {
    stored.c = { clusterId: 'c', nodeHistory: false, metricsQueryMode: 'auto', preferredNamespace: 'mon', preferredService: 'vm', fleetPolicy: 'filter' }
    setMetricsQuery.mockClear()
    await clusterSettings.save('c', { fleetPolicy: 'refuse' })
    // Written from c's real row, not from "off, no pin".
    expect(setMetricsQuery).toHaveBeenCalledWith('c', 'auto', 'mon', 'vm', 'refuse')
  })

  it('refuses when the row cannot be read', async () => {
    getClusterSettings.mockRejectedValueOnce(new Error('[unavailable] no file'))
    setMetricsQuery.mockClear()
    await clusterSettings.save('never-read', { metricsQueryMode: 'auto' })
    expect(setMetricsQuery).not.toHaveBeenCalled()
    expect(clusterSettings.error).toContain('could not be read')
  })

  it('saving one cluster never alters another', async () => {
    stored.a = { clusterId: 'a', nodeHistory: false, metricsQueryMode: 'auto', preferredNamespace: 'mon', preferredService: 'prom', fleetPolicy: 'filter' }
    stored.b = { clusterId: 'b', nodeHistory: false, metricsQueryMode: 'manual', preferredNamespace: '', preferredService: '', fleetPolicy: 'filter' }
    await clusterSettings.load(['a', 'b'])
    setMetricsQuery.mockClear()
    await clusterSettings.save('a', { fleetPolicy: 'refuse' })
    expect(setMetricsQuery).toHaveBeenCalledTimes(1)
    expect(setMetricsQuery.mock.calls[0][0]).toBe('a')
    expect(clusterSettings.for('b').metricsQueryMode).toBe('manual')
  })

  it('prunes contexts removed from the kubeconfig', async () => {
    await clusterSettings.load(['a', 'b'])
    clusterSettings.prune(['a'])
    getClusterSettings.mockClear()
    await clusterSettings.save('a', { fleetPolicy: 'filter' })
    expect(getClusterSettings).toHaveBeenLastCalledWith(['a'])
    expect(clusterSettings.isLoaded('b')).toBe(false)
  })
})
