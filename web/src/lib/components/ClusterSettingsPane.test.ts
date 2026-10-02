/**
 * Settings → Clusters opened AFTER the overview loaded its own cluster's row:
 * every other context still has to be read, or it shows as Off and a save
 * writes that guess over the file.
 */
import { afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup, render, screen, waitFor } from '@testing-library/svelte'

const stored: Record<string, Record<string, unknown>> = {
  one: { clusterId: 'one', nodeHistory: false, metricsQueryMode: 'manual', preferredNamespace: '', preferredService: '', fleetPolicy: 'filter' },
  two: { clusterId: 'two', nodeHistory: false, metricsQueryMode: 'auto', preferredNamespace: 'mon', preferredService: 'prom', fleetPolicy: 'filter' },
}
const getClusterSettings = vi.fn(async (ids: string[]) => ids.filter((id) => stored[id]).map((id) => stored[id]))
vi.mock('$lib/api/client', async () => {
  const actual = await vi.importActual<Record<string, unknown>>('$lib/api/client')
  return {
    ...actual,
    getClusterSettings: (ids: string[]) => getClusterSettings(ids),
    getSettingsState: async () => ({ writable: true, notice: '' }),
    setMetricsQuery: vi.fn(async () => {}),
  }
})

import ClusterSettingsPane from './ClusterSettingsPane.svelte'
import { clusterSettings } from '$stores/clusterSettings.svelte'
import { workspace } from '$stores/workspace.svelte'

afterEach(() => cleanup())

describe('ClusterSettingsPane', () => {
  it('reads every context, even after the overview loaded one of them', async () => {
    workspace.clusters = [{ id: 'one' }, { id: 'two' }] as never
    // The overview's load of its own cluster, before the pane opens.
    await clusterSettings.load(['one'])
    expect(clusterSettings.isLoaded('two')).toBe(false)

    render(ClusterSettingsPane)

    await waitFor(() => expect(clusterSettings.isLoaded('two')).toBe(true))
    expect(getClusterSettings).toHaveBeenLastCalledWith(['two'])
    // Shown with its real mode, not as Off.
    await waitFor(() => expect(screen.getByText('two')).toBeTruthy())
    expect(clusterSettings.for('two').metricsQueryMode).toBe('auto')
    expect(document.body.textContent).toContain('When a chart opens')
  })
})
