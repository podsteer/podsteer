import { beforeEach, describe, expect, it, vi } from 'vitest'

const vulnerabilitySummaries = vi.fn()
vi.mock('$lib/api/client', () => ({
  vulnerabilitySummaries: (...args: unknown[]) => vulnerabilitySummaries(...args),
}))

import {
  ensureVulnerabilities,
  forgetVulnerabilities,
  vulnerabilitiesFor,
} from './vulnerabilities.svelte'

/** One summary shaped the way the Go side hands them over. */
function summary(subject: string, critical: number, high: number) {
  return { subject, critical, high, medium: 0, low: 0, unknown: 0, reports: 1 }
}

/** Lets the promise chain inside ensureVulnerabilities settle. */
const settle = () => new Promise((resolve) => setTimeout(resolve, 0))

describe('the severity counts a scanner already in the cluster recorded', () => {
  beforeEach(() => {
    forgetVulnerabilities('dev')
    forgetVulnerabilities('staging')
    vulnerabilitySummaries.mockReset()
  })

  it('matches a pod by the workload that owns it, never by its own name', async () => {
    // The operator writes one report per container of the WORKLOAD — a
    // ReplicaSet for a Deployment's pods — and every replica runs the same
    // images, so keying on the pod's name would find nothing for any of them.
    vulnerabilitySummaries.mockResolvedValue([summary('ReplicaSet/web-abc123', 2, 5)])

    ensureVulnerabilities('dev', 'shop')
    await settle()

    const found = vulnerabilitiesFor('dev', 'shop', {
      name: 'web-abc123-xyz',
      controlledBy: 'ReplicaSet/web-abc123',
    })
    expect(found?.critical).toBe(2)
    expect(found?.high).toBe(5)
  })

  it('falls back to the pod itself for a bare pod nothing owns', async () => {
    // A pod with no controller is scanned under its own name, which is the
    // one case where the pod IS the subject.
    vulnerabilitySummaries.mockResolvedValue([summary('Pod/debug', 0, 1)])

    ensureVulnerabilities('dev', 'shop')
    await settle()

    expect(vulnerabilitiesFor('dev', 'shop', { name: 'debug', controlledBy: '' })?.high).toBe(1)
  })

  it('answers nothing for a workload the scanner has not reported on', async () => {
    vulnerabilitySummaries.mockResolvedValue([summary('ReplicaSet/web-abc123', 1, 0)])

    ensureVulnerabilities('dev', 'shop')
    await settle()

    expect(
      vulnerabilitiesFor('dev', 'shop', { name: 'api-1', controlledBy: 'ReplicaSet/api-def456' }),
    ).toBeUndefined()
  })

  it('reads one cluster and namespace once, however many rows ask', async () => {
    // The whole design: this must never ride the refresh tick. Five hundred
    // rows and ten ticks are still one call.
    vulnerabilitySummaries.mockResolvedValue([])

    ensureVulnerabilities('dev', 'shop')
    ensureVulnerabilities('dev', 'shop')
    await settle()
    ensureVulnerabilities('dev', 'shop')

    expect(vulnerabilitySummaries).toHaveBeenCalledTimes(1)
  })

  it('keeps clusters and namespaces apart', async () => {
    vulnerabilitySummaries.mockResolvedValue([])

    ensureVulnerabilities('dev', 'shop')
    ensureVulnerabilities('dev', 'admin')
    ensureVulnerabilities('staging', 'shop')
    await settle()

    expect(vulnerabilitySummaries).toHaveBeenCalledTimes(3)
  })

  it('treats a refusal as nothing to show and never asks again', async () => {
    // An account that may read pods and not the reports is ordinary, and a
    // cluster with no scanner is ordinary. Neither is worth a banner on a pod
    // list, and retrying either would be one request per render.
    vulnerabilitySummaries.mockRejectedValue(new Error('forbidden'))

    ensureVulnerabilities('dev', 'shop')
    await settle()
    ensureVulnerabilities('dev', 'shop')
    await settle()

    expect(vulnerabilitySummaries).toHaveBeenCalledTimes(1)
    expect(vulnerabilitiesFor('dev', 'shop', { name: 'web', controlledBy: '' })).toBeUndefined()
  })

  it('says nothing has been reported before the read answers', () => {
    // The list is drawn first and the marks arrive later, so every row has to
    // render correctly with no answer at all.
    vulnerabilitySummaries.mockResolvedValue([summary('ReplicaSet/web', 9, 9)])

    ensureVulnerabilities('dev', 'shop')

    expect(
      vulnerabilitiesFor('dev', 'shop', { name: 'web-1', controlledBy: 'ReplicaSet/web' }),
    ).toBeUndefined()
  })

  it('forgets one cluster without costing another its answers', async () => {
    // Closing one tab must not make every other tab read again.
    vulnerabilitySummaries.mockResolvedValue([summary('ReplicaSet/web', 1, 1)])

    ensureVulnerabilities('dev', 'shop')
    ensureVulnerabilities('staging', 'shop')
    await settle()

    forgetVulnerabilities('dev')

    const owner = { name: 'web-1', controlledBy: 'ReplicaSet/web' }
    expect(vulnerabilitiesFor('dev', 'shop', owner)).toBeUndefined()
    expect(vulnerabilitiesFor('staging', 'shop', owner)?.critical).toBe(1)
  })
})
