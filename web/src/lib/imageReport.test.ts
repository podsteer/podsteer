import { describe, expect, it } from 'vitest'

import type { ImageReport } from '$lib/api/client'
import {
  formatImageSize,
  hasIdentity,
  identityRows,
  imageHeadline,
  sizeRow,
} from './imageReport'

function report(overrides: Partial<ImageReport> = {}): ImageReport {
  return {
    container: 'app',
    declared: 'ghcr.io/team/app:v1',
    resolved: 'ghcr.io/team/app:v1',
    drift: false,
    registry: 'ghcr.io',
    repository: 'team/app',
    tag: 'v1',
    referenceReadable: true,
    digest: '',
    digestNote: '',
    sizeBytes: 0,
    sizeStatus: 'not_reported',
    sizeSource: '',
    otherNames: [],
    pullPolicy: '',
    pullSecrets: [],
    credentialed: false,
    credentialNote: '',
    bounded: 'Layers live in a registry PodSteer does not read.',
    ...overrides,
  } as ImageReport
}

describe('formatImageSize', () => {
  it('uses the powers of ten a container runtime prints', () => {
    // docker images and crictl images both print decimal units. A pane that
    // disagreed with both would have people checking their arithmetic.
    expect(formatImageSize(41_000_000)).toBe('41.0 MB')
    expect(formatImageSize(999)).toBe('999 B')
    expect(formatImageSize(1_500)).toBe('1.5 kB')
    expect(formatImageSize(1_200_000_000)).toBe('1.2 GB')
  })

  it('renders a dash rather than a nought for anything that is not a size', () => {
    expect(formatImageSize(0)).toBe('—')
    expect(formatImageSize(-1)).toBe('—')
    expect(formatImageSize(Number.NaN)).toBe('—')
  })
})

describe('sizeRow', () => {
  it('shows the figure when the node reported one', () => {
    const row = sizeRow(report({ sizeStatus: 'measured', sizeBytes: 41_000_000 }))

    expect(row.value).toBe('41.0 MB')
    expect(row.absent).toBeFalsy()
  })

  // THE RULE THIS PANE EXISTS TO KEEP. A refusal and an absence are different
  // facts calling for different next steps, and zero bytes is a claim about
  // neither.
  it('shows the reason, never a nought, when nothing was measured', () => {
    const unreadable = sizeRow(
      report({
        sizeStatus: 'unreadable',
        sizeSource: 'node node-1 could not be read (forbidden)',
      }),
    )
    expect(unreadable.value).toContain('could not be read')
    expect(unreadable.absent).toBe(true)
    expect(unreadable.value).not.toContain('0 B')

    const notReported = sizeRow(
      report({
        sizeStatus: 'not_reported',
        sizeSource: 'node node-1 does not list this image; the kubelet garbage-collects images it is not using',
      }),
    )
    expect(notReported.value).toContain('garbage-collects')
    expect(notReported.absent).toBe(true)
  })

  it('still says something when the backend sent no reason at all', () => {
    expect(sizeRow(report({ sizeStatus: 'unreadable', sizeSource: '' })).value).toBe('not reported')
  })
})

describe('identityRows', () => {
  it('leads with what is running and does not repeat an identical declaration', () => {
    const rows = identityRows(report())
    const labels = rows.map((row) => row.label)

    expect(labels[0]).toBe('Running')
    expect(labels).not.toContain('Declared')
  })

  it('shows the declared reference beside the running one only when they differ', () => {
    const rows = identityRows(report({ drift: true, declared: 'ghcr.io/team/app:v2' }))
    const declared = rows.find((row) => row.label === 'Declared')

    expect(declared?.value).toBe('ghcr.io/team/app:v2')
  })

  it('falls back to the declared reference on a pod that has not started', () => {
    const rows = identityRows(report({ resolved: '', declared: 'ghcr.io/team/app:v1' }))

    expect(rows[0]).toEqual({ label: 'Declared', value: 'ghcr.io/team/app:v1' })
  })

  it('invents no registry for a reference nothing could parse', () => {
    const rows = identityRows(
      report({ referenceReadable: false, registry: '', repository: '', tag: '' }),
    )

    expect(rows.map((row) => row.label)).not.toContain('Registry')
    expect(rows[0].value).toBe('ghcr.io/team/app:v1')
  })

  it('shows a digest when one was reported', () => {
    const rows = identityRows(report({ digest: 'sha256:abc' }))
    expect(rows.find((row) => row.label === 'Digest')?.value).toBe('sha256:abc')
  })
})

describe('imageHeadline', () => {
  it('leads with drift, which is the most useful thing this pane can say', () => {
    expect(imageHeadline(report({ drift: true }))).toContain('different reference')
  })

  it('says a pinned image is pinned, and nothing at all otherwise', () => {
    expect(imageHeadline(report({ digest: 'sha256:abc' }))).toContain('Pinned by digest')
    expect(imageHeadline(report())).toBe('')
  })
})

describe('hasIdentity', () => {
  it('is false only when nothing at all was reported', () => {
    expect(hasIdentity(report())).toBe(true)
    expect(hasIdentity(report({ resolved: '', declared: '' }))).toBe(false)
    expect(hasIdentity(null)).toBe(false)
  })
})
