import { beforeEach, describe, expect, it, vi } from 'vitest'

const vulnerabilitySummaries = vi.fn()
vi.mock('$lib/api/client', () => ({
  vulnerabilitySummaries: (...args: unknown[]) => vulnerabilitySummaries(...args),
}))

import {
  ensureVulnerabilities,
  forgetVulnerabilities,
  vulnerabilitiesFor,
  vulnerabilityReadFor,
  workloadsRunningImage,
} from './vulnerabilities.svelte'

/** One summary shaped the way the Go side hands them over. */
function summary(subject: string, critical: number, high: number) {
  return {
    subject,
    critical,
    high,
    medium: 0,
    low: 0,
    unknown: 0,
    images: [] as string[],
    reports: 1,
  }
}

/** A completed read carrying those summaries — the ordinary answer. */
function listing(summaries: ReturnType<typeof summary>[], status = 'complete') {
  return { summaries, status, read: summaries.length, remaining: 0, cap: 0 }
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
    vulnerabilitySummaries.mockResolvedValue(listing([summary('ReplicaSet/web-abc123', 2, 5)]))

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
    vulnerabilitySummaries.mockResolvedValue(listing([summary('Pod/debug', 0, 1)]))

    ensureVulnerabilities('dev', 'shop')
    await settle()

    expect(vulnerabilitiesFor('dev', 'shop', { name: 'debug', controlledBy: '' })?.high).toBe(1)
  })

  it('answers nothing for a workload the scanner has not reported on', async () => {
    vulnerabilitySummaries.mockResolvedValue(listing([summary('ReplicaSet/web-abc123', 1, 0)]))

    ensureVulnerabilities('dev', 'shop')
    await settle()

    expect(
      vulnerabilitiesFor('dev', 'shop', { name: 'api-1', controlledBy: 'ReplicaSet/api-def456' }),
    ).toBeUndefined()
  })

  it('reads one cluster and namespace once, however many rows ask', async () => {
    // The whole design: this must never ride the refresh tick. Five hundred
    // rows and ten ticks are still one call.
    vulnerabilitySummaries.mockResolvedValue(listing([]))

    ensureVulnerabilities('dev', 'shop')
    ensureVulnerabilities('dev', 'shop')
    await settle()
    ensureVulnerabilities('dev', 'shop')

    expect(vulnerabilitySummaries).toHaveBeenCalledTimes(1)
  })

  it('keeps clusters and namespaces apart', async () => {
    vulnerabilitySummaries.mockResolvedValue(listing([]))

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
    vulnerabilitySummaries.mockResolvedValue(listing([summary('ReplicaSet/web', 9, 9)]))

    ensureVulnerabilities('dev', 'shop')

    expect(
      vulnerabilitiesFor('dev', 'shop', { name: 'web-1', controlledBy: 'ReplicaSet/web' }),
    ).toBeUndefined()
  })

  it('forgets one cluster without costing another its answers', async () => {
    // Closing one tab must not make every other tab read again.
    vulnerabilitySummaries.mockResolvedValue(listing([summary('ReplicaSet/web', 1, 1)]))

    ensureVulnerabilities('dev', 'shop')
    ensureVulnerabilities('staging', 'shop')
    await settle()

    forgetVulnerabilities('dev')

    const owner = { name: 'web-1', controlledBy: 'ReplicaSet/web' }
    expect(vulnerabilitiesFor('dev', 'shop', owner)).toBeUndefined()
    expect(vulnerabilitiesFor('staging', 'shop', owner)?.critical).toBe(1)
  })
})

describe('whether an unmarked row has been shown to be clean', () => {
  beforeEach(() => {
    forgetVulnerabilities('dev')
    vulnerabilitySummaries.mockReset()
  })

  it('only a completed read says so', async () => {
    // THE RULE THIS STORE EXISTS TO KEEP. An absent mark means "the scanner
    // found nothing" on a complete read and "nobody looked" on every other
    // kind, and on a security signal those must never be the same answer.
    const cases = [
      { status: 'complete', complete: true, truncated: false },
      { status: 'truncated', complete: false, truncated: true },
      { status: 'not-installed', complete: false, truncated: false },
      { status: 'forbidden', complete: false, truncated: false },
      { status: '', complete: false, truncated: false },
    ]

    for (const expected of cases) {
      forgetVulnerabilities('dev')
      vulnerabilitySummaries.mockResolvedValue(listing([], expected.status))

      ensureVulnerabilities('dev', 'shop')
      await settle()

      const read = vulnerabilityReadFor('dev', 'shop')
      expect(read?.complete, expected.status).toBe(expected.complete)
      expect(read?.truncated, expected.status).toBe(expected.truncated)
    }
  })

  it('a failed read is not a clean bill of health', async () => {
    // Recorded as asked so it is not retried per render — but with no status,
    // which is not "complete", so nothing downstream reads the empty result
    // as every workload being clean.
    vulnerabilitySummaries.mockRejectedValue(new Error('[unreachable] gone'))

    ensureVulnerabilities('dev', 'shop')
    await settle()

    expect(vulnerabilityReadFor('dev', 'shop')?.complete).toBe(false)
  })

  it('carries the ceiling and what was left, for the sentence the list shows', async () => {
    vulnerabilitySummaries.mockResolvedValue({
      summaries: [],
      status: 'truncated',
      read: 5000,
      remaining: 1200,
      cap: 5000,
    })

    ensureVulnerabilities('dev', 'shop')
    await settle()

    const read = vulnerabilityReadFor('dev', 'shop')
    expect(read).toMatchObject({ truncated: true, read: 5000, remaining: 1200, cap: 5000 })
  })
})

describe('how many workloads run the same image', () => {
  beforeEach(() => {
    forgetVulnerabilities('dev')
    vulnerabilitySummaries.mockReset()
  })

  /** A summary carrying the images its reports scanned. */
  function withImages(subject: string, images: string[]) {
    return { ...summary(subject, 1, 0), images }
  }

  it('counts workloads, not reports', () => {
    // THE FACT THAT COLLAPSES THE LIST. One report per container means an
    // image in three Deployments produces three summaries with identical
    // counts — three rows for one bump. A workload running it in two
    // containers is still one workload to fix.
    vulnerabilitySummaries.mockResolvedValue(
      listing([
        withImages('ReplicaSet/web', ['library/nginx:1.27', 'acme/sidecar:2.0']),
        withImages('ReplicaSet/api', ['library/nginx:1.27']),
        withImages('ReplicaSet/worker', ['acme/worker:9']),
      ]),
    )

    ensureVulnerabilities('dev', 'shop')
    return settle().then(() => {
      expect(workloadsRunningImage('dev', 'shop', 'library/nginx:1.27')).toBe(2)
      expect(workloadsRunningImage('dev', 'shop', 'acme/sidecar:2.0')).toBe(1)
      expect(workloadsRunningImage('dev', 'shop', 'acme/worker:9')).toBe(1)
    })
  })

  it('answers zero for an unread namespace rather than claiming one', async () => {
    // Nothing has been read, so nothing is known — the caller must not render
    // this as "only this workload".
    expect(workloadsRunningImage('dev', 'never-read', 'library/nginx:1.27')).toBe(0)
  })

  it('answers zero for an image nothing named', async () => {
    vulnerabilitySummaries.mockResolvedValue(listing([withImages('ReplicaSet/web', [])]))

    ensureVulnerabilities('dev', 'shop')
    await settle()

    expect(workloadsRunningImage('dev', 'shop', '')).toBe(0)
    expect(workloadsRunningImage('dev', 'shop', 'library/nginx:1.27')).toBe(0)
  })
})
