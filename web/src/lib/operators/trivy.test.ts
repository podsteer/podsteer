import { describe, expect, it } from 'vitest'

import {
  advisoryLink,
  fixableCount,
  hasFix,
  severityTone,
  trivyVulnerabilityReport,
} from './trivy'
import type { Vulnerability } from './trivy'

/** A report on a clean-enough image — everything populated, nothing critical. */
const clean = {
  apiVersion: 'aquasecurity.github.io/v1alpha1',
  kind: 'VulnerabilityReport',
  metadata: {
    name: 'replicaset-web-6b7c9d4f5-app',
    namespace: 'shop',
    labels: {
      'trivy-operator.resource.kind': 'ReplicaSet',
      'trivy-operator.resource.name': 'web-6b7c9d4f5',
      'trivy-operator.container.name': 'app',
    },
  },
  report: {
    artifact: { repository: 'library/nginx', tag: '1.27.3' },
    scanner: { name: 'Trivy', version: '0.58.1' },
    updateTimestamp: '2026-09-04T06:00:00Z',
    summary: { criticalCount: 0, highCount: 1, mediumCount: 3, lowCount: 12, unknownCount: 0, noneCount: 0 },
    vulnerabilities: [
      {
        vulnerabilityID: 'CVE-2025-11543',
        severity: 'HIGH',
        resource: 'libssl3',
        installedVersion: '3.0.14-1',
        fixedVersion: '3.0.15-1',
        title: 'openssl: denial of service in certificate parsing',
        primaryLink: 'https://avd.aquasec.com/nvd/cve-2025-11543',
        target: 'library/nginx:1.27.3 (debian 12.8)',
        score: 7.5,
      },
      {
        vulnerabilityID: 'GHSA-9wx4-h78v-vm56',
        severity: 'MEDIUM',
        resource: 'requests',
        installedVersion: '2.31.0',
        fixedVersion: '2.32.0',
        title: 'requests: leaks proxy credentials on redirect',
        primaryLink: '',
        target: 'usr/lib/python3/dist-packages',
        score: 5.6,
      },
    ],
  },
}

/** A report on a badly out-of-date image, with criticals and no fixes. */
const critical = {
  apiVersion: 'aquasecurity.github.io/v1alpha1',
  kind: 'VulnerabilityReport',
  metadata: {
    name: 'daemonset-collector-agent',
    namespace: 'observability',
    labels: {
      'trivy-operator.resource.kind': 'DaemonSet',
      'trivy-operator.resource.name': 'collector',
      'trivy-operator.container.name': 'agent',
    },
  },
  report: {
    // Pinned by digest, so there is no tag to show.
    artifact: { repository: 'internal/collector', digest: 'sha256:0f1e2d3c4b5a' },
    scanner: { name: 'Trivy', version: '0.58.1' },
    updateTimestamp: '2026-09-04T06:00:00Z',
    summary: { criticalCount: 2, highCount: 9, mediumCount: 41, lowCount: 88, unknownCount: 3, noneCount: 1 },
    vulnerabilities: [
      {
        vulnerabilityID: 'CVE-2024-3094',
        severity: 'CRITICAL',
        resource: 'xz-utils',
        installedVersion: '5.6.0',
        fixedVersion: '',
        title: 'xz: malicious code in distributed source',
        primaryLink: 'https://avd.aquasec.com/nvd/cve-2024-3094',
        target: 'internal/collector (debian 11.9)',
        score: 10,
      },
      {
        vulnerabilityID: 'RUSTSEC-2021-0079',
        severity: 'CRITICAL',
        resource: 'hyper',
        installedVersion: '0.14.4',
        fixedVersion: '0.14.10',
        title: 'hyper: integer overflow in header parsing',
        primaryLink: '',
        target: 'usr/bin/collector',
      },
    ],
  },
}

describe('reading a Trivy VulnerabilityReport', () => {
  it('names the artefact and the scanner that produced the report', () => {
    expect(trivyVulnerabilityReport(clean)?.artifact).toBe('library/nginx:1.27.3')
    expect(trivyVulnerabilityReport(clean)?.scanner).toBe('Trivy 0.58.1')
    expect(trivyVulnerabilityReport(clean)?.updateTimestamp).toBe('2026-09-04T06:00:00Z')
  })

  it('falls back to the digest for an image pinned without a tag', () => {
    // A bare repository names a different image on every push and could not
    // be matched against what is running.
    expect(trivyVulnerabilityReport(critical)?.artifact).toBe('internal/collector:sha256:0f1e2d3c4b5a')
  })

  it('reads the counts Trivy wrote rather than counting the rows', () => {
    // The list is capped by the operator's own configuration on a badly
    // affected image, so a panel that counted rows would report fewer
    // criticals than the scanner found.
    expect(trivyVulnerabilityReport(critical)?.summary).toEqual({
      critical: 2,
      high: 9,
      medium: 41,
      low: 88,
      unknown: 3,
      none: 1,
    })
    expect(trivyVulnerabilityReport(critical)?.vulnerabilities).toHaveLength(2)
  })

  it('names the workload and the container the report is about', () => {
    // A report is written per CONTAINER, so two reports naming one Deployment
    // are two containers rather than a duplicate.
    expect(trivyVulnerabilityReport(clean)?.subject).toEqual({
      kind: 'ReplicaSet',
      name: 'web-6b7c9d4f5',
      container: 'app',
    })
    expect(trivyVulnerabilityReport(critical)?.subject).toEqual({
      kind: 'DaemonSet',
      name: 'collector',
      container: 'agent',
    })
  })

  it('carries each vulnerability as Trivy wrote it', () => {
    expect(trivyVulnerabilityReport(clean)?.vulnerabilities[0]).toEqual({
      id: 'CVE-2025-11543',
      severity: 'HIGH',
      resource: 'libssl3',
      installedVersion: '3.0.14-1',
      fixedVersion: '3.0.15-1',
      title: 'openssl: denial of service in certificate parsing',
      primaryLink: 'https://avd.aquasec.com/nvd/cve-2025-11543',
      target: 'library/nginx:1.27.3 (debian 12.8)',
      score: 7.5,
    })
  })

  it('leaves an unfixed vulnerability’s fixed version empty and an unscored one null', () => {
    // No published fix is the fact an operator is looking for, and 0.0 is a
    // real CVSS score meaning harmless — neither may be invented.
    const vulnerabilities = trivyVulnerabilityReport(critical)?.vulnerabilities ?? []

    expect(vulnerabilities[0]?.fixedVersion).toBe('')
    expect(vulnerabilities[0]?.score).toBe(10)
    expect(vulnerabilities[1]?.score).toBeNull()
  })

  it('says nothing where the operator has written no report yet', () => {
    // Zero counts and an empty list rather than a throw, so the panel says
    // the report exists and holds nothing yet.
    const report = trivyVulnerabilityReport({
      metadata: { name: 'replicaset-web-6b7c9d4f5-app' },
    })

    expect(report?.artifact).toBe('')
    expect(report?.scanner).toBe('')
    expect(report?.updateTimestamp).toBe('')
    expect(report?.summary).toEqual({ critical: 0, high: 0, medium: 0, low: 0, unknown: 0, none: 0 })
    expect(report?.vulnerabilities).toEqual([])
    expect(report?.subject).toEqual({ kind: '', name: '', container: '' })
  })

  it('answers null for no manifest', () => {
    expect(trivyVulnerabilityReport(null)).toBeNull()
    expect(trivyVulnerabilityReport('not an object')).toBeNull()
  })
})

describe('grading a severity the way Trivy does', () => {
  it('colours CRITICAL and HIGH, which are the two the operator’s dashboards colour', () => {
    expect(severityTone('CRITICAL')).toBe('critical')
    expect(severityTone('HIGH')).toBe('warn')
  })

  it('leaves every other word plain, including one it has never seen', () => {
    // Any real image carries dozens of MEDIUM and LOW findings, and colouring
    // those is how a list stops being read. A severity from some other
    // scanner's scale is left uncoloured rather than mapped onto Trivy's.
    expect(severityTone('MEDIUM')).toBeUndefined()
    expect(severityTone('LOW')).toBeUndefined()
    expect(severityTone('UNKNOWN')).toBeUndefined()
    expect(severityTone('NONE')).toBeUndefined()
    expect(severityTone('Moderate')).toBeUndefined()
    expect(severityTone('critical')).toBeUndefined()
    expect(severityTone('')).toBeUndefined()
  })
})

describe('linking to an advisory', () => {
  /** A vulnerability with only the fields the link depends on. */
  const vulnerability = (id: string, primaryLink = ''): Vulnerability => ({
    id,
    severity: 'HIGH',
    resource: 'example',
    installedVersion: '1.0.0',
    fixedVersion: '',
    title: '',
    primaryLink,
    target: '',
    score: null,
  })

  it('prefers the report’s own primaryLink, whatever the identifier is', () => {
    // The quotation: Trivy took it from the database the finding came out of.
    expect(advisoryLink(vulnerability('CVE-2024-3094', 'https://avd.aquasec.com/nvd/cve-2024-3094'))).toBe(
      'https://avd.aquasec.com/nvd/cve-2024-3094',
    )
    expect(advisoryLink(vulnerability('RUSTSEC-2021-0079', 'https://rustsec.org/advisories/RUSTSEC-2021-0079'))).toBe(
      'https://rustsec.org/advisories/RUSTSEC-2021-0079',
    )
  })

  it('composes an NVD link for a CVE with no primaryLink', () => {
    expect(advisoryLink(vulnerability('CVE-2024-3094'))).toBe('https://nvd.nist.gov/vuln/detail/CVE-2024-3094')
    expect(advisoryLink(vulnerability('cve-2024-3094'))).toBe('https://nvd.nist.gov/vuln/detail/cve-2024-3094')
  })

  it('composes a GitHub advisory link for a GHSA with no primaryLink', () => {
    expect(advisoryLink(vulnerability('GHSA-9wx4-h78v-vm56'))).toBe(
      'https://github.com/advisories/GHSA-9wx4-h78v-vm56',
    )
  })

  it('returns empty for an identifier whose registry cannot be guessed', () => {
    // A distribution's own advisory id has no universal URL: every vendor
    // hosts its own under a path the identifier does not carry, and a
    // composed one 404s — which reads as a withdrawn advisory rather than as
    // PodSteer guessing.
    expect(advisoryLink(vulnerability('RUSTSEC-2021-0079'))).toBe('')
    expect(advisoryLink(vulnerability('DSA-5378-1'))).toBe('')
    expect(advisoryLink(vulnerability('ALAS2-2023-1234'))).toBe('')
    expect(advisoryLink(vulnerability('CVE-2024-309'))).toBe('')
    expect(advisoryLink(vulnerability('GHSA-9wx4-h78v'))).toBe('')
    expect(advisoryLink(vulnerability(''))).toBe('')
  })
})

/** One finding, with only the fields these rules read. */
function finding(id: string, fixedVersion: string): Vulnerability {
  return {
    id,
    severity: 'HIGH',
    resource: 'openssl',
    installedVersion: '1.1.1',
    fixedVersion,
    title: '',
    primaryLink: '',
    target: '',
    score: null,
  }
}

describe('whether a finding can be closed by upgrading', () => {
  it('reads an empty fixedVersion as no fix published', () => {
    // `fixedVersion` is a REQUIRED field of the CRD, so empty is Trivy's own
    // statement that nothing has been released — not a field the operator
    // declined to write.
    expect(hasFix(finding('CVE-2024-0001', ''))).toBe(false)
    expect(hasFix(finding('CVE-2024-0002', '1.1.1w'))).toBe(true)
  })

  it('treats whitespace as no fix, not as a version', () => {
    expect(hasFix(finding('CVE-2024-0003', '   '))).toBe(false)
  })
})

describe('how much of a report is actionable today', () => {
  it('counts the findings with a published fix, against the list shown', () => {
    // THE NUMBER PRACTITIONERS ASK FOR. A severity count says how bad the
    // image is; this says how much of it can be dealt with this afternoon,
    // which is the question that decides whether anybody opens a ticket.
    const report = trivyVulnerabilityReport({
      report: {
        summary: { criticalCount: 2, highCount: 1 },
        vulnerabilities: [
          { vulnerabilityID: 'CVE-1', severity: 'CRITICAL', fixedVersion: '2.0' },
          { vulnerabilityID: 'CVE-2', severity: 'CRITICAL' },
          { vulnerabilityID: 'CVE-3', severity: 'HIGH', fixedVersion: '3.1' },
        ],
      },
    })!

    expect(fixableCount(report)).toEqual({ fixable: 2, total: 3 })
  })

  it('says none of nothing rather than dividing by zero', () => {
    const report = trivyVulnerabilityReport({ report: { summary: {} } })!

    expect(fixableCount(report)).toEqual({ fixable: 0, total: 0 })
  })

  it('counts the list, not the summary, and says so where the two differ', () => {
    // Everywhere else this file reads report.summary, because the LIST can be
    // capped by the operator's own configuration. There is no fixable count
    // in the summary to read, so this is the only source — and it must be
    // counted against the same list the panel renders, or the panel would
    // show three rows under a sentence claiming forty.
    const report = trivyVulnerabilityReport({
      report: {
        summary: { criticalCount: 40 },
        vulnerabilities: [{ vulnerabilityID: 'CVE-1', severity: 'CRITICAL', fixedVersion: '2.0' }],
      },
    })!

    expect(report.summary.critical).toBe(40)
    expect(fixableCount(report)).toEqual({ fixable: 1, total: 1 })
  })
})
