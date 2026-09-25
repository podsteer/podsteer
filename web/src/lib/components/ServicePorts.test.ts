import { render, fireEvent, cleanup } from '@testing-library/svelte'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const probeLocalPort = vi.fn()
const freeLocalPort = vi.fn()

vi.mock('$lib/api/client', () => ({
  probeLocalPort: (...args: unknown[]) => probeLocalPort(...args),
  freeLocalPort: (...args: unknown[]) => freeLocalPort(...args),
  forwardBrowserURL: (forward: { address: string }) => forward.address,
  openURL: vi.fn(),
}))

import ServicePorts from './ServicePorts.svelte'
import { forwards } from '$stores/forwards.svelte'

const service = (ports: unknown[], extra: Record<string, unknown> = {}) => ({
  spec: { type: 'ClusterIP', selector: { app: 'web' }, ports, ...extra },
})

const props = (manifest: unknown) => ({
  manifest,
  clusterId: 'dev',
  namespace: 'web',
  name: 'postgres',
})

describe('the Ports section of a Service', () => {
  beforeEach(() => {
    probeLocalPort.mockReset().mockResolvedValue(true)
    freeLocalPort.mockReset()
    forwards.active = []
    vi.spyOn(forwards, 'startService').mockResolvedValue(undefined)
  })

  afterEach(() => {
    cleanup()
    vi.restoreAllMocks()
  })

  it('asks for the port by NAME, and reports the service port alongside it', async () => {
    // The two arguments this wiring can silently swap. The backend accepts a
    // name or a number for `servicePort` and takes the number separately for
    // the key and the remembered local port — so a call that passed the
    // container port, or the number where the name belongs, would still be a
    // valid call and would forward the wrong thing.
    const { getByRole } = render(
      ServicePorts,
      props(service([{ name: 'postgres', port: 5432, targetPort: 'db' }])),
    )

    await fireEvent.click(getByRole('button', { name: 'Forward' }))

    expect(forwards.startService).toHaveBeenCalledWith('dev', 'web', 'postgres', 'postgres', 5432, 0)
  })

  it('asks for an unnamed port by its number', async () => {
    const { getByRole } = render(ServicePorts, props(service([{ port: 5432 }])))

    await fireEvent.click(getByRole('button', { name: 'Forward' }))

    expect(forwards.startService).toHaveBeenCalledWith('dev', 'web', 'postgres', '5432', 5432, 0)
  })

  it('shows what a port targets, including a targetPort that is not set', () => {
    const { getByText } = render(ServicePorts, props(service([{ name: 'http', port: 80 }])))
    expect(getByText(/80\/TCP → 80/)).toBeTruthy()
  })

  it('offers no Forward for UDP, and says why instead', () => {
    // THE POINT OF THE REFUSAL BEING IN THE UI. The backend refuses a UDP
    // forward, so a button here would be a promise it cannot keep — and the
    // port itself still has to be listed, or the panel looks broken to
    // anybody looking for a DNS Service's 53.
    const { queryByRole, getByText } = render(
      ServicePorts,
      props(service([{ name: 'dns', port: 53, protocol: 'UDP' }])),
    )

    expect(queryByRole('button', { name: 'Forward' })).toBeNull()
    expect(getByText(/TCP only/)).toBeTruthy()
    expect(getByText(/53\/UDP/)).toBeTruthy()
  })

  it('offers no Forward on a Service that selects no pods', () => {
    const { queryByRole, getByText } = render(
      ServicePorts,
      props({ spec: { type: 'ClusterIP', ports: [{ port: 5432 }] } }),
    )

    expect(queryByRole('button', { name: 'Forward' })).toBeNull()
    expect(getByText(/selects no pods/)).toBeTruthy()
  })

  it('draws nothing at all for a Service with no ports', () => {
    const { container } = render(ServicePorts, props({ spec: { selector: { app: 'web' } } }))
    expect(container.textContent?.trim()).toBe('')
  })

  it('offers Stop, not Forward, once the store reports this Service port open', async () => {
    // Found through the store's Service association rather than by pod name:
    // the pod under a Service forward changes, which is the whole feature.
    vi.spyOn(forwards, 'forService').mockReturnValue({
      id: '1',
      clusterId: 'dev',
      namespace: 'web',
      pod: 'postgres-0',
      localPort: 15432,
      remotePort: 5432,
      address: 'http://localhost:15432',
      scheme: 'http',
      reconnecting: false,
    } as never)

    const { getByRole, queryByRole } = render(
      ServicePorts,
      props(service([{ name: 'postgres', port: 5432 }])),
    )

    expect(getByRole('button', { name: 'Stop' })).toBeTruthy()
    expect(queryByRole('button', { name: 'Forward' })).toBeNull()
  })
})
