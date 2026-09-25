import { render, fireEvent, cleanup } from '@testing-library/svelte'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const probeLocalPort = vi.fn()
const freeLocalPort = vi.fn()

vi.mock('$lib/api/client', () => ({
  probeLocalPort: (...args: unknown[]) => probeLocalPort(...args),
  freeLocalPort: (...args: unknown[]) => freeLocalPort(...args),
}))

import PortForwardStart from './PortForwardStart.svelte'
import { preferences } from '$stores/preferences.svelte'

const onstart = vi.fn()

const props = {
  remotePort: 5432,
  portName: 'postgres',
  busy: false,
  onstart,
}

describe('choosing a local port for a forward', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    probeLocalPort.mockReset()
    freeLocalPort.mockReset()
    onstart.mockReset()
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

  it('hands the typed local port to whoever owns the start', async () => {
    // THE WHOLE POINT OF THE CALLBACK. This component no longer knows what is
    // being forwarded — a pod or a Service — so what it must get right is the
    // NUMBER it reports, and that it reports one at all.
    probeLocalPort.mockResolvedValue(true)

    const { getByLabelText, getByRole } = render(PortForwardStart, { ...props, portName: '' })

    const input = getByLabelText('Local port for port 5432') as HTMLInputElement
    await fireEvent.input(input, { target: { value: '8080' } })
    await vi.advanceTimersByTimeAsync(350)

    await fireEvent.click(getByRole('button', { name: 'Forward' }))

    expect(onstart).toHaveBeenCalledWith(8080)
  })

  it('reports 0 for an empty field, which means the operating system chooses', async () => {
    // Not the same as refusing to start: a blank box has always meant "any
    // free port", and 0 is how that reaches the backend. A callback that
    // simply did not fire here would silently remove the feature.
    const { getByRole } = render(PortForwardStart, { ...props, portName: '' })

    await fireEvent.click(getByRole('button', { name: 'Forward' }))

    expect(onstart).toHaveBeenCalledWith(0)
  })
})
