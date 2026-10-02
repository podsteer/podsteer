import { afterEach, describe, expect, it, vi } from 'vitest'
import { backendOptions, listMetricsBackends, parseBackendValue, setBackendLister } from './metricsBackends'

afterEach(() => setBackendLister(undefined))

describe('metrics backend picker', () => {
  it('lists nothing while the bindings have no Backends call', async () => {
    setBackendLister(null)
    expect(await listMetricsBackends('dev')).toBeNull()
  })

  it('reads the candidates defensively', async () => {
    setBackendLister(
      vi.fn().mockResolvedValue([
        { kind: 'prometheus', product: 'Prometheus', namespace: 'monitoring', service: 'prometheus-operated', detail: 'Verified for this cluster' },
        { kind: 'prometheus', namespace: 'linkerd-viz', service: 'prometheus' },
        { namespace: '', service: 'broken' },
      ]),
    )
    expect(await listMetricsBackends('dev')).toEqual([
      { namespace: 'monitoring', service: 'prometheus-operated', product: 'Prometheus', detail: 'Verified for this cluster' },
      { namespace: 'linkerd-viz', service: 'prometheus', product: 'prometheus', detail: '' },
    ])
  })

  it('offers automatic first, every candidate, and a pin discovery no longer offers', () => {
    const options = backendOptions(
      [{ namespace: 'monitoring', service: 'prom', product: 'Prometheus', detail: 'verified' }],
      { preferredNamespace: 'old', preferredService: 'gone' },
    )
    expect(options.map((o) => o.value)).toEqual(['', 'monitoring/prom', 'old/gone'])
    expect(options[2].hint).toBe('pinned, not found now')
    expect(parseBackendValue('monitoring/prom')).toEqual({ preferredNamespace: 'monitoring', preferredService: 'prom' })
    expect(parseBackendValue('')).toEqual({ preferredNamespace: '', preferredService: '' })
  })
})
