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
const setMetricsQuery = vi.fn(async () => {})
vi.mock('$lib/api/client', async () => {
  const actual = await vi.importActual<Record<string, unknown>>('$lib/api/client')
  return {
    ...actual,
    getClusterSettings: (ids: string[]) => getClusterSettings(ids),
    getSettingsState: async () => ({ writable: true, notice: '' }),
    setMetricsQuery: (...args: unknown[]) => setMetricsQuery(...(args as [])),
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
