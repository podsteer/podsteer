import { render, fireEvent, cleanup } from '@testing-library/svelte'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const probeLocalPort = vi.fn()
const freeLocalPort = vi.fn()
const startPortForward = vi.fn()
const listPortForwards = vi.fn()
const stopPortForward = vi.fn()
const stopAllPortForwards = vi.fn()

vi.mock('$lib/api/client', () => ({
  probeLocalPort: (...args: unknown[]) => probeLocalPort(...args),
  freeLocalPort: (...args: unknown[]) => freeLocalPort(...args),
  startPortForward: (...args: unknown[]) => startPortForward(...args),
  listPortForwards: (...args: unknown[]) => listPortForwards(...args),
  stopPortForward: (...args: unknown[]) => stopPortForward(...args),
  stopAllPortForwards: (...args: unknown[]) => stopAllPortForwards(...args),
}))

import PortForwardStart from './PortForwardStart.svelte'
import { forwards } from '$stores/forwards.svelte'
import { preferences } from '$stores/preferences.svelte'

const props = {
  clusterId: 'dev',
  namespace: 'web',
  podName: 'postgres-0',
  podUID: 'uid-1',
  remotePort: 5432,
  portName: 'postgres',
  protocol: 'TCP',
  labels: {},
  busy: false,
}

describe('starting a forward with a chosen local port', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    probeLocalPort.mockReset()
    freeLocalPort.mockReset()
    startPortForward.mockReset()
    listPortForwards.mockReset().mockResolvedValue([])
    forwards.active = []
    forwards.error = ''
    // Each test starts with no memory of its own — the pre-fill test writes
    // one deliberately, and leaving it would leak into every test after it.
    preferences.localPortByRemotePort = {}
    preferences.localPortByPortName = {}
  })

  afterEach(() => {
    // Every render() appends to document.body, and the queries returned from
    // it are body-scoped rather than confined to their own container — so
    // without this, the second test's "Local port for port 5432" matches
    // both its own input and the first test's, which is still there.
    cleanup()
    vi.useRealTimers()
  })

  it('pre-fills from a remembered port', () => {
    preferences.rememberLocalPort(5432, 'postgres', 25432)

    const { getByLabelText } = render(PortForwardStart, props)

    expect((getByLabelText('Local port for postgres') as HTMLInputElement).value).toBe('25432')
  })

  it('disables Start and explains why once the probe reports the port in use', async () => {
    probeLocalPort.mockResolvedValue(false)

    const { getByLabelText, getByRole, findByText } = render(PortForwardStart, {
      ...props,
      portName: '',
    })

    const input = getByLabelText('Local port for port 5432') as HTMLInputElement
    await fireEvent.input(input, { target: { value: '8080' } })

    // The debounce, then the (mocked) probe's own promise settling.
    await vi.advanceTimersByTimeAsync(350)

    await findByText('Port 8080 is in use on this machine')
    expect((getByRole('button', { name: 'Forward' }) as HTMLButtonElement).disabled).toBe(true)
  })

  it('leaves Start enabled once the probe reports the port free', async () => {
    probeLocalPort.mockResolvedValue(true)

    const { getByLabelText, getByRole } = render(PortForwardStart, { ...props, portName: '' })

    const input = getByLabelText('Local port for port 5432') as HTMLInputElement
    await fireEvent.input(input, { target: { value: '8080' } })
    await vi.advanceTimersByTimeAsync(350)

    expect((getByRole('button', { name: 'Forward' }) as HTMLButtonElement).disabled).toBe(false)
  })

  it('refuses a port outside 1-65535 without asking the backend', async () => {
    const { getByLabelText, getByRole, findByText } = render(PortForwardStart, {
      ...props,
      portName: '',
    })

    const input = getByLabelText('Local port for port 5432') as HTMLInputElement
    await fireEvent.input(input, { target: { value: '99999' } })
    await vi.advanceTimersByTimeAsync(350)

    await findByText('Enter a port between 1 and 65535')
    expect((getByRole('button', { name: 'Forward' }) as HTMLButtonElement).disabled).toBe(true)
    expect(probeLocalPort).not.toHaveBeenCalled()
  })

  it('fills the field from "Pick a free port"', async () => {
    freeLocalPort.mockResolvedValue(34567)
    probeLocalPort.mockResolvedValue(true)

    const { getByLabelText, getByRole } = render(PortForwardStart, { ...props, portName: '' })

    await fireEvent.click(getByRole('button', { name: 'Pick a free port' }))

    expect((getByLabelText('Local port for port 5432') as HTMLInputElement).value).toBe('34567')
  })

  it('starts the forward with the typed local port', async () => {
    probeLocalPort.mockResolvedValue(true)
    startPortForward.mockResolvedValue({
      id: '1',
      clusterId: 'dev',
      namespace: 'web',
      pod: 'postgres-0',
      localPort: 8080,
      remotePort: 5432,
      address: 'http://localhost:8080',
      scheme: 'http',
      reconnecting: false,
    })

    const { getByLabelText, getByRole } = render(PortForwardStart, { ...props, portName: '' })

    const input = getByLabelText('Local port for port 5432') as HTMLInputElement
    await fireEvent.input(input, { target: { value: '8080' } })
    await vi.advanceTimersByTimeAsync(350)

    await fireEvent.click(getByRole('button', { name: 'Forward' }))
    await vi.advanceTimersByTimeAsync(0)

    expect(startPortForward).toHaveBeenCalledWith(
      'dev',
      'web',
      'postgres-0',
      'uid-1',
      8080,
      5432,
      '',
      'TCP',
      {},
    )
  })
})
