import { beforeEach, describe, expect, it, vi } from 'vitest'

const getManifest = vi.fn()
vi.mock('$lib/api/client', () => ({ getManifest: (...args: unknown[]) => getManifest(...args) }))

import { configMapData, forgetConfigMaps } from './configMaps.svelte'

describe('reading a config map for an environment variable', () => {
  beforeEach(() => {
    forgetConfigMaps()
    getManifest.mockReset()
  })

  it('returns the data, as strings', () => {
    // A ConfigMap's values are strings in the API but YAML will happily parse
    // an unquoted 6379 as a number, and a port rendered as `6379` and one
    // rendered as `"6379"` must look the same in the pane.
    getManifest.mockResolvedValue('data:\n  host: redis-master\n  port: 6379\n')

    return configMapData('dev', 'web', 'redis').then((data) => {
      expect(data).toEqual({ host: 'redis-master', port: '6379' })
    })
  })

  it('reads one config map once, however many variables cite it', async () => {
    // A pane with twenty variables from one map must not make twenty reads.
    getManifest.mockResolvedValue('data:\n  host: redis-master\n')

    await Promise.all([
      configMapData('dev', 'web', 'redis'),
      configMapData('dev', 'web', 'redis'),
      configMapData('dev', 'web', 'redis'),
    ])

    expect(getManifest).toHaveBeenCalledTimes(1)
  })

  it('keeps clusters and namespaces apart', async () => {
    getManifest.mockResolvedValue('data: {}\n')

    await configMapData('dev', 'web', 'redis')
    await configMapData('dev', 'staging', 'redis')
    await configMapData('prod', 'web', 'redis')

    // Two namespaces routinely hold a config map with the same name, and two
    // clusters certainly do.
    expect(getManifest).toHaveBeenCalledTimes(3)
  })

  it('answers with nothing rather than failing when it may not read one', async () => {
    // An account that may read pods and not config maps is ordinary, and the
    // caller's fallback is to keep printing the reference — which is what it
    // was printing anyway. A refusal must not surface as an error in a pane
    // about something else.
    getManifest.mockRejectedValue(new Error('forbidden'))

    await expect(configMapData('dev', 'web', 'redis')).resolves.toEqual({})
  })

  it('survives a config map with no data at all', async () => {
    getManifest.mockResolvedValue('metadata:\n  name: empty\n')

    await expect(configMapData('dev', 'web', 'empty')).resolves.toEqual({})
  })
})
