import { beforeEach, describe, expect, it, vi } from 'vitest'

const capability = vi.fn()
const notify = vi.fn()
const request = vi.fn()

vi.mock('$bindings/notificationapi', () => ({
  Capability: () => capability(),
  Notify: (options: unknown) => notify(options),
  Request: () => request(),
}))

const { notifications } = await import('./notifications.svelte')
const { NOTIFY_COOLDOWN_MS } = await import('$lib/notify')

const critical = { id: 'pods:crashloop', severity: 'critical', title: 'CrashLoopBackOff' }

/** Every gate open, so each test closes exactly one. */
function raising(overrides: Record<string, unknown> = {}) {
  return {
    clusterId: 'staging',
    enabled: true,
    appeared: [critical],
    comparable: true,
    isSnoozed: () => false,
    ...overrides,
  }
}

describe('the desktop notification store', () => {
  beforeEach(async () => {
    capability.mockReset()
    notify.mockReset()
    request.mockReset()

    capability.mockResolvedValue({ supported: true, authorised: true })
    notify.mockResolvedValue(undefined)
    await notifications.probe()
    notifications.forget('staging')
    notifications.forget('production')
  })

  it('posts nothing at all while the preference is off', async () => {
    // THE OPT-OUT, asserted the way updates.test.ts asserts its own: not by
    // reading state back but by counting calls, because an opt-out that is
    // shipped and never counted is the one that silently breaks.
    const plan = await notifications.raise(raising({ enabled: false }))

    expect(plan).toBeNull()
    expect(notify).not.toHaveBeenCalled()
  })

  it('posts nothing when the platform reported it cannot', async () => {
    capability.mockResolvedValue({ supported: false, authorised: false })
    await notifications.probe()

    const plan = await notifications.raise(raising())

    expect(plan).toBeNull()
    expect(notify).not.toHaveBeenCalled()
  })

  it('posts nothing when permission has been refused', async () => {
    // SUPPORTED AND NOT AUTHORISED is a different state from unsupported, and
    // the Settings pane says different things about them — but neither posts.
    capability.mockResolvedValue({ supported: true, authorised: false })
    await notifications.probe()

    expect(await notifications.raise(raising())).toBeNull()
    expect(notify).not.toHaveBeenCalled()
  })

  it('posts one notification carrying no object name', async () => {
    const plan = await notifications.raise(raising())

    expect(plan).not.toBeNull()
    expect(notify).toHaveBeenCalledTimes(1)
    expect(notify).toHaveBeenCalledWith({
      id: 'podsteer-findings-staging',
      title: 'New critical finding',
      body: 'staging — CrashLoopBackOff',
      clusterId: 'staging',
    })
  })

  it('does not post twice for one cluster inside the cooldown', async () => {
    await notifications.raise(raising())
    await notifications.raise(raising())

    expect(notify).toHaveBeenCalledTimes(1)
  })

  it('keeps one cluster from using up another cluster budget', async () => {
    // PER CLUSTER, so a staging cluster that flaps all afternoon cannot use
    // up the window's whole budget and leave production silent.
    await notifications.raise(raising())
    await notifications.raise(raising({ clusterId: 'production' }))

    expect(notify).toHaveBeenCalledTimes(2)
  })

  it('posts again once the cooldown has passed', async () => {
    vi.useFakeTimers()
    try {
      vi.setSystemTime(new Date(1_700_000_000_000))
      await notifications.raise(raising())

      vi.setSystemTime(new Date(1_700_000_000_000 + NOTIFY_COOLDOWN_MS))
      await notifications.raise(raising())
    } finally {
      vi.useRealTimers()
    }

    expect(notify).toHaveBeenCalledTimes(2)
  })

  it('stamps the cooldown before the call, not after it', async () => {
    // A post that is slow to resolve must not let the next refresh's batch
    // through underneath it — the same reason #refreshKinds stamps before its
    // own call rather than on success.
    let release: () => void = () => {}
    notify.mockImplementation(
      () =>
        new Promise<void>((resolve) => {
          release = resolve
        }),
    )

    const first = notifications.raise(raising())
    const second = await notifications.raise(raising())
    release()
    await first

    expect(second).toBeNull()
    expect(notify).toHaveBeenCalledTimes(1)
  })

  it('swallows a failed post rather than failing the assessment it came from', async () => {
    // The same rule the alert sound follows: a notification that could not be
    // shown must never become an error on the refresh that produced it.
    notify.mockRejectedValue(new Error('[internal] no'))

    await expect(notifications.raise(raising())).resolves.toBeNull()
  })

  it('reports nothing supported when the platform cannot be asked', async () => {
    capability.mockRejectedValue(new Error('[internal] no'))

    await notifications.probe()

    expect(notifications.capability).toEqual({ supported: false, authorised: false })
    expect(notifications.deliverable).toBe(false)
  })

  it('asks the operating system only when permission is requested', async () => {
    // Requesting is a visible system prompt on macOS, so it belongs to the
    // gesture that caused it and to nothing else — probing must never ask.
    await notifications.probe()
    expect(request).not.toHaveBeenCalled()

    request.mockResolvedValue(true)
    await expect(notifications.authorise()).resolves.toBe(true)
    expect(request).toHaveBeenCalledTimes(1)
  })

  it('reads a refused request as not authorised rather than as an error', async () => {
    request.mockResolvedValue(false)

    expect(await notifications.authorise()).toBe(false)
    expect(notifications.deliverable).toBe(false)
  })
})
