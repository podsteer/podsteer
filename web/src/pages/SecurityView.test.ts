/**
 * What the security page says, and — more importantly — what it refuses to
 * let an absence say.
 *
 * A page called "Security" that renders a blank section on a cluster with no
 * scanner has made a claim nobody made: "nothing found" where the truth is
 * "nothing looked". Four ordinary outcomes leave the section without rows —
 * no scanner, no permission, nothing scanned yet, and a read that failed —
 * and only one of them is good news. These tests are about telling them
 * apart.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { render, cleanup } from '@testing-library/svelte'

const read = vi.fn()
const summaries = vi.fn()
vi.mock('$stores/vulnerabilities.svelte', () => ({
  ensureVulnerabilities: () => {},
  vulnerabilityReadFor: () => read(),
  summariesFor: () => summaries(),
}))

import SecurityView from './SecurityView.svelte'

const words = () => (document.body.textContent ?? '').replace(/\s+/g, ' ')

function session(findings: unknown[] = []) {
  return {
    cluster: { id: 'dev' },
    overview: { findings },
    kinds: [],
    selectKind: () => {},
    openObject: async () => {},
  } as never
}

function summary(overrides: Record<string, unknown> = {}) {
  return {
    subject: 'Deployment/web',
    critical: 0,
    high: 0,
    medium: 0,
    low: 0,
    unknown: 0,
    images: ['library/nginx:1.27'],
    reports: 1,
    ...overrides,
  }
}

function finding(overrides: Record<string, unknown> = {}) {
  return {
    id: 'security/privileged',
    severity: 'info',
    category: 'Security',
    title: 'Privileged containers',
    summary: '2 pods run a privileged container',
    advice: 'Drop privileged unless the workload needs the host.',
    subjects: [],
    count: 2,
    kindId: 'core/v1/pods',
    oldestSeconds: 0,
    ...overrides,
  }
}

beforeEach(() => {
  read.mockReset()
  summaries.mockReset()
  summaries.mockReturnValue([])
})

afterEach(() => cleanup())

describe('SecurityView — the scanner half', () => {
  it('says no scanner is installed, rather than rendering nothing', () => {
    read.mockReturnValue({ complete: false, truncated: false, status: 'not-installed', read: 0, remaining: 0, cap: 0 })
    render(SecurityView, { session: session() })

    const text = words()
    expect(text).toContain('No vulnerability scanner is installed')
    // The distinction the whole page turns on.
    expect(text).toContain('not because the images are clean')
  })

  it('says the read was refused, rather than showing a clean cluster', () => {
    read.mockReturnValue({ complete: false, truncated: false, status: 'forbidden', read: 0, remaining: 0, cap: 0 })
    render(SecurityView, { session: session() })

    const text = words()
    expect(text).toContain('may not read its reports')
    expect(text).not.toContain('No vulnerability scanner is installed')
  })

  it('says a read that failed did not complete', () => {
    read.mockReturnValue({ complete: false, truncated: false, status: '', read: 0, remaining: 0, cap: 0 })
    render(SecurityView, { session: session() })

    expect(words()).toContain('did not complete')
  })

  it('says how much of the picture a truncated read is', () => {
    read.mockReturnValue({ complete: false, truncated: true, status: 'truncated', read: 2000, remaining: 500, cap: 2000 })
    summaries.mockReturnValue([summary({ critical: 3 })])
    render(SecurityView, { session: session() })

    const text = words()
    expect(text).toContain('Stopped at 2,000 reports')
    expect(text).toContain('500 more were withheld')
    expect(text).toContain('not all of it')
  })

  it('groups by image and counts the workloads running it', () => {
    // The scanner writes one report per container, so one image running in
    // three Deployments is three identical summaries — three problems where
    // there is one. One tag bump closes all three.
    read.mockReturnValue({ complete: true, truncated: false, status: 'complete', read: 3, remaining: 0, cap: 0 })
    summaries.mockReturnValue([
      summary({ subject: 'Deployment/a', critical: 2, high: 5 }),
      summary({ subject: 'Deployment/b', critical: 2, high: 5 }),
      summary({ subject: 'Deployment/c', critical: 2, high: 5 }),
    ])
    const { container } = render(SecurityView, { session: session() })

    const rows = container.querySelectorAll('tbody tr')
    expect(rows).toHaveLength(1)

    const cells = [...rows[0].querySelectorAll('td')].map((cell) => cell.textContent?.trim())
    expect(cells[0]).toBe('library/nginx:1.27')
    expect(cells[1]).toBe('3')
    // Two criticals, not six: summing would report a number no scanner wrote.
    expect(cells[2]).toBe('2')
    expect(cells[3]).toBe('5')
  })

  it('ranks the images that carry a critical first', () => {
    read.mockReturnValue({ complete: true, truncated: false, status: 'complete', read: 2, remaining: 0, cap: 0 })
    summaries.mockReturnValue([
      summary({ subject: 'Deployment/quiet', images: ['acme/quiet:1'], low: 40 }),
      summary({ subject: 'Deployment/loud', images: ['acme/loud:1'], critical: 1 }),
    ])
    const { container } = render(SecurityView, { session: session() })

    const first = container.querySelector('tbody tr td')
    expect(first?.textContent?.trim()).toBe('acme/loud:1')
  })

  it('keeps Unknown as its own column', () => {
    // trivy-operator >= v0.32.0 files genuine highs and criticals as UNKNOWN
    // when SeveritySource is empty. Folding the bucket into Low would quietly
    // under-report on affected clusters.
    read.mockReturnValue({ complete: true, truncated: false, status: 'complete', read: 1, remaining: 0, cap: 0 })
    summaries.mockReturnValue([summary({ unknown: 7 })])
    const { container } = render(SecurityView, { session: session() })

    const headers = [...container.querySelectorAll('thead th')].map((th) => th.textContent?.trim())
    expect(headers).toContain('Unknown')
    const cells = [...container.querySelectorAll('tbody td')].map((td) => td.textContent?.trim())
    expect(cells).toContain('7')
  })
})

describe('SecurityView — the posture half', () => {
  beforeEach(() => {
    read.mockReturnValue({ complete: true, truncated: false, status: 'complete', read: 0, remaining: 0, cap: 0 })
  })

  it('states what was checked when nothing was found', () => {
    render(SecurityView, { session: session([]) })

    // Not an empty state: these rules fire on a deliberate act with no benign
    // default, so nothing found is a real answer and is worth naming.
    const text = words()
    expect(text).toContain('No workload here runs privileged')
    expect(text).toContain('UID')
  })

  it('shows only security findings, not the rest of the assessment', () => {
    render(SecurityView, {
      session: session([
        finding(),
        finding({ id: 'sizing/no-limits', category: 'Configuration', title: 'Containers with no memory limit' }),
      ]),
    })

    const text = words()
    expect(text).toContain('Privileged containers')
    expect(text).not.toContain('no memory limit')
  })
})

describe('SecurityView — the name', () => {
  it('names what it does not cover, so the title is not a claim', () => {
    read.mockReturnValue({ complete: true, truncated: false, status: 'complete', read: 0, remaining: 0, cap: 0 })
    render(SecurityView, { session: session() })

    const text = words()
    expect(text).toContain('What this page does not cover')
    // The four the positioning work settled on.
    expect(text).toContain('Volumes')
    expect(text).toContain('Anything through time')
    expect(text).toContain('compliance score')
    expect(text).toContain('Permissions')
  })

  it('says plainly that PodSteer scans nothing', () => {
    read.mockReturnValue({ complete: true, truncated: false, status: 'complete', read: 0, remaining: 0, cap: 0 })
    render(SecurityView, { session: session() })

    expect(words()).toContain('PodSteer scans nothing')
  })
})
