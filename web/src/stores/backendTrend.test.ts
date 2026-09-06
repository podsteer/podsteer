import { beforeEach, describe, expect, it, vi } from 'vitest'

/**
 * The adapter call, counted.
 *
 * THE UPDATE CHECK'S OWN SHAPE, and deliberately so: an opt-out asserted by
 * reading the resulting state rather than by counting requests is exactly
 * what has silently broken in k9s, Terraform, dotnet, JetBrains and Docker
 * Desktop. The question here is not what the chart shows — it is whether
 * PromQL left this machine.
 */
const getSeries = vi.fn()

vi.mock('$bindings/metricsqueryapi', () => ({
  GetSeries: (clusterId: string, metric: string, scope: string, windowMinutes: number) =>
    getSeries(clusterId, metric, scope, windowMinutes),
}))

const { BackendTrend, shouldQuery } = await import('./backendTrend.svelte')

function answered() {
  return {
    status: 'answered',
    message: '',
    provenance: {
      origin: 'backend',
      source: 'Prometheus in monitoring',
      verification: 'verified',
      filtered: false,
    },
    series: [{ label: '', points: [{ at: 1, value: 1 }] }],
    expression: 'sum(container_memory_working_set_bytes{container!="",pod!=""})',
    spanSeconds: 3600,
    stepSeconds: 60,
    unit: 'bytes',
  }
}

describe('a monitoring backend is queried only when somebody asks', () => {
  beforeEach(() => {
    getSeries.mockReset()
    getSeries.mockResolvedValue(answered())
  })

  it('sends nothing at all for a cluster with the setting off', async () => {
    const trend = new BackendTrend('dev', 'off')

    await trend.consider('open')
    await trend.consider('range')
    await trend.consider('manual')
    for (let i = 0; i < 5; i++) await trend.consider('tick')

    expect(getSeries).not.toHaveBeenCalled()
    expect(trend.result.status).toBe('not-enabled')
  })

  /**
   * THE RULE THE WHOLE FEATURE TURNS ON. `auto` means a query when a chart
   * opens or its range changes — never on the refresh tick, which is the
   * entire difference between this and querying on a poll, and the thing ADR
   * 7 refused outright.
   *
   * Counted across several DRIVEN refreshes rather than checked as a flag,
   * because a flag says what somebody intended and a count says what left.
   */
  it('sends nothing on a refresh tick in auto, however many ticks arrive', async () => {
    const trend = new BackendTrend('dev', 'auto')

    await trend.consider('open')
    expect(getSeries).toHaveBeenCalledTimes(1)

    for (let i = 0; i < 12; i++) await trend.consider('tick')

    expect(getSeries).toHaveBeenCalledTimes(1)
  })

  it('sends nothing on a refresh tick in manual either', async () => {
    const trend = new BackendTrend('dev', 'manual')

    await trend.consider('open')
    for (let i = 0; i < 12; i++) await trend.consider('tick')

    expect(getSeries).not.toHaveBeenCalled()
  })

  it('asks when a chart opens under auto, and only once for the same question', async () => {
    const trend = new BackendTrend('dev', 'auto')

    // A component's effect re-runs for reasons that are not a change — a
    // theme flip, a parent re-render — and each would otherwise be a request
    // on somebody's Prometheus.
    await trend.consider('open')
    await trend.consider('open')
    await trend.consider('open')

    expect(getSeries).toHaveBeenCalledTimes(1)
  })

  /**
   * ONE GESTURE, ONE QUERY.
   *
   * The range control and the effect that draws the chart both notice a range
   * change, and the guard against a repeat compared against a key that was
   * only set once the first call had RESOLVED — so both went out, both
   * reached the backend, and both landed in the operator's audit log. On a
   * cold verification cache that is two of the most expensive query this
   * feature makes.
   */
  it('sends one query when the range change and the effect arrive together', async () => {
    const trend = new BackendTrend('dev', 'auto')
    await trend.consider('open')
    getSeries.mockClear()

    // The control assigns the window and asks; the effect re-runs in the same
    // turn and asks for the same thing. Neither has awaited the other.
    const fromControl = trend.setWindow(360)
    trend.windowMinutes = 360
    const fromEffect = trend.consider('open')
    await Promise.all([fromControl, fromEffect])

    expect(getSeries).toHaveBeenCalledTimes(1)
    expect(getSeries).toHaveBeenLastCalledWith('dev', 'cpu', 'cluster', 360)
  })

  it('sends one query when a chart opens twice before the first answer lands', async () => {
    const trend = new BackendTrend('dev', 'auto')

    await Promise.all([trend.consider('open'), trend.consider('open'), trend.consider('open')])

    expect(getSeries).toHaveBeenCalledTimes(1)
  })

  it('asks again when the range changes under auto', async () => {
    const trend = new BackendTrend('dev', 'auto')

    await trend.consider('open')
    await trend.setWindow(360)
    await trend.setWindow(1440)

    expect(getSeries).toHaveBeenCalledTimes(3)
    expect(getSeries).toHaveBeenLastCalledWith('dev', 'cpu', 'cluster', 1440)
  })

  it('asks again when the metric changes under auto', async () => {
    const trend = new BackendTrend('dev', 'auto')

    await trend.consider('open')
    await trend.setMetric('memory')

    expect(getSeries).toHaveBeenCalledTimes(2)
    expect(getSeries).toHaveBeenLastCalledWith('dev', 'memory', 'cluster', 60)
  })

  it('sends nothing until the control is pressed under manual', async () => {
    const trend = new BackendTrend('dev', 'manual')

    await trend.consider('open')
    await trend.setWindow(360)
    expect(getSeries).not.toHaveBeenCalled()

    await trend.consider('manual')
    expect(getSeries).toHaveBeenCalledTimes(1)
    expect(getSeries).toHaveBeenLastCalledWith('dev', 'cpu', 'cluster', 360)
  })

  it('asks again every time the control is pressed, same question or not', async () => {
    const trend = new BackendTrend('dev', 'auto')

    await trend.consider('open')
    await trend.consider('manual')
    await trend.consider('manual')

    expect(getSeries).toHaveBeenCalledTimes(3)
  })

  it('stops sending and clears what was drawn when the mode is turned off', async () => {
    const trend = new BackendTrend('dev', 'auto')

    await trend.consider('open')
    expect(getSeries).toHaveBeenCalledTimes(1)

    await trend.setMode('off')
    await trend.consider('open')
    await trend.consider('manual')
    for (let i = 0; i < 5; i++) await trend.consider('tick')

    expect(getSeries).toHaveBeenCalledTimes(1)
    expect(trend.result.status).toBe('not-enabled')
    expect(trend.hasSeries).toBe(false)
  })
})

describe('the reason rule on its own', () => {
  it('never lets a tick send anything, in any mode', () => {
    for (const mode of ['off', 'manual', 'auto'] as const) {
      expect(shouldQuery(mode, 'tick')).toBe(false)
    }
  })

  it('never lets an off cluster send anything, for any reason', () => {
    for (const reason of ['open', 'range', 'manual', 'tick'] as const) {
      expect(shouldQuery('off', reason)).toBe(false)
    }
  })

  it('lets auto answer a chart opening and a range changing', () => {
    expect(shouldQuery('auto', 'open')).toBe(true)
    expect(shouldQuery('auto', 'range')).toBe(true)
    expect(shouldQuery('auto', 'manual')).toBe(true)
  })

  it('lets manual answer only the control', () => {
    expect(shouldQuery('manual', 'manual')).toBe(true)
    expect(shouldQuery('manual', 'open')).toBe(false)
    expect(shouldQuery('manual', 'range')).toBe(false)
  })
})

describe('what a backend answered', () => {
  beforeEach(() => {
    getSeries.mockReset()
    getSeries.mockResolvedValue(answered())
  })

  /**
   * A Prometheus that scrapes application endpoints and not kubelets answers
   * 200 with nothing. Collapsed into "answered" that is a blank chart under a
   * green label, which invites the operator to conclude their cluster was
   * idle.
   */
  it('does not draw an empty answer as an answer', async () => {
    getSeries.mockResolvedValue({
      ...answered(),
      status: 'answered-empty',
      series: [],
      message: 'Prometheus in monitoring answered, and holds nothing for this cluster.',
    })

    const trend = new BackendTrend('dev', 'auto')
    await trend.consider('open')

    expect(trend.result.status).toBe('answered-empty')
    expect(trend.hasSeries).toBe(false)
    expect(trend.result.message).not.toBe('')
  })

  /**
   * A RESULT CARRIES NO METRIC OF ITS OWN. Left in place while a new question
   * is in flight, a CPU answer is drawn on the memory chart — scaled by a
   * thousand, under a legend naming the service that answered — and if the
   * new call throws it stays there for ever.
   */
  it('clears the previous answer the moment a different question is asked', async () => {
    let release: (value: unknown) => void = () => {}
    const trend = new BackendTrend('dev', 'auto')

    await trend.consider('open')
    expect(trend.hasSeries).toBe(true)
    expect(trend.result.unit).toBe('bytes')

    getSeries.mockImplementation(() => new Promise((resolve) => (release = resolve)))
    const pending = trend.setMetric('memory')

    // While the memory answer is in flight, the CPU answer must not still be
    // on the chart.
    expect(trend.hasSeries).toBe(false)
    expect(trend.result.status).toBe('not-enabled')

    release(answered())
    await pending
  })

  it('does not keep a stale answer when the new question fails', async () => {
    const trend = new BackendTrend('dev', 'auto')
    await trend.consider('open')
    expect(trend.hasSeries).toBe(true)

    getSeries.mockRejectedValue(new Error('boom'))
    await trend.setMetric('memory')

    expect(trend.status).toBe('error')
    expect(trend.hasSeries).toBe(false)
    expect(trend.result.provenance.source).toBe('')
  })

  it('carries the provenance of every series it draws', async () => {
    const trend = new BackendTrend('dev', 'auto')
    await trend.consider('open')

    expect(trend.hasSeries).toBe(true)
    expect(trend.result.provenance.origin).toBe('backend')
    expect(trend.result.provenance.source).toBe('Prometheus in monitoring')
    expect(trend.result.provenance.verification).toBe('verified')
  })

  it('keeps a refusal as a status with a sentence rather than as an error', async () => {
    getSeries.mockResolvedValue({
      ...answered(),
      status: 'forbidden',
      series: [],
      message: 'Your account may not reach Prometheus in monitoring through the API server proxy.',
    })

    const trend = new BackendTrend('dev', 'auto')
    await trend.consider('open')

    expect(trend.status).toBe('ready')
    expect(trend.error).toBeNull()
    expect(trend.result.status).toBe('forbidden')
    expect(trend.result.message).toContain('account')
  })

  it('reports a failed call as an error without pretending to a status', async () => {
    getSeries.mockRejectedValue(new Error('[unreachable] cluster unreachable'))

    const trend = new BackendTrend('dev', 'auto')
    await trend.consider('open')

    expect(trend.status).toBe('error')
    expect(trend.error).not.toBeNull()
  })

  // A failed call left nothing asked, so pressing again asks again rather
  // than being swallowed by the "same question" guard.
  it('asks again after a failure', async () => {
    getSeries.mockRejectedValueOnce(new Error('boom'))

    const trend = new BackendTrend('dev', 'auto')
    await trend.consider('open')
    await trend.consider('open')

    expect(getSeries).toHaveBeenCalledTimes(2)
  })
})
