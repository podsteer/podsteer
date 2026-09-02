import { beforeEach, describe, expect, it } from 'vitest'

import { usageHistory, usageKey } from './usageHistory.svelte'

const sample = (at: number, cpu: number) => ({ at, cpuCores: cpu, memoryBytes: 0 })

describe('usage history', () => {
  beforeEach(() => {
    usageHistory.forget('staging')
    usageHistory.forget('production')
  })

  it('keeps two clusters’ objects apart', () => {
    // THE BUG THIS GUARDS, and it showed one cluster's numbers on another's
    // object. Several clusters are open at once, one per tab, and two of them
    // routinely hold a pod with the same name in a namespace with the same
    // name. Without the cluster in the key they share one series, so the
    // second pod's chart is built partly from the first pod's measurements —
    // not a missing number but a plausible wrong one.
    usageHistory.record(usageKey('staging', 'pod', 'development', 'web-abc'), sample(Date.now(), 1))
    usageHistory.record(
      usageKey('production', 'pod', 'development', 'web-abc'),
      sample(Date.now(), 9),
    )

    const staging = usageHistory.since(usageKey('staging', 'pod', 'development', 'web-abc'))
    const production = usageHistory.since(usageKey('production', 'pod', 'development', 'web-abc'))

    expect(staging).toHaveLength(1)
    expect(production).toHaveLength(1)
    expect(staging[0].cpuCores).toBe(1)
    expect(production[0].cpuCores).toBe(9)
  })

  it('forgets one cluster without blanking another', () => {
    // Closing a tab takes its charts with it. Clearing everything would blank
    // the charts in the tabs still open.
    const staged = usageKey('staging', 'pod', 'development', 'web')
    const live = usageKey('production', 'pod', 'development', 'web')
    usageHistory.record(staged, sample(Date.now(), 1))
    usageHistory.record(live, sample(Date.now(), 2))

    usageHistory.forget('staging')

    expect(usageHistory.since(staged)).toHaveLength(0)
    expect(usageHistory.since(live)).toHaveLength(1)
  })

  it('does not hand back samples that have aged out', () => {
    const key = usageKey('staging', 'pod', 'development', 'old')
    usageHistory.record(key, sample(Date.now() - 6 * 60 * 60_000, 1))

    expect(usageHistory.since(key)).toHaveLength(0)
  })
})
