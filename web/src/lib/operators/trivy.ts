/**
 * What a Trivy Operator VulnerabilityReport says, in Trivy's own words.
 *
 * QUOTATION, NOT VERDICT. The severities, the counts and the fixed versions
 * are lifted verbatim out of the report the drawer already fetched. Trivy has
 * already done the scanning and the grading; CRITICAL and HIGH are ITS words
 * for what it found, taken from the advisory the finding came from, and
 * nothing here re-grades a vulnerability, scores one Trivy left unscored, or
 * decides whether an image is "safe to deploy". A severity word this file has
 * never seen renders as itself.
 *
 * The report is not summarised either: `report.summary` carries the counts
 * Trivy itself wrote, and they are read rather than recomputed from the
 * vulnerability list. The two can honestly differ — the list is capped by the
 * operator's own configuration on a badly affected image — and a panel that
 * counted the rows would quietly report fewer criticals than the scanner
 * found.
 *
 * A REPORT IS PER CONTAINER, NOT PER WORKLOAD. The Trivy Operator writes one
 * VulnerabilityReport for each container of each workload, which is why the
 * subject carries the container name alongside the workload's kind and name:
 * two reports naming the same Deployment are two containers, not a
 * duplicate. The subject comes from the `trivy-operator.resource.*` labels
 * the operator puts on every report.
 *
 * Field names follow the VulnerabilityReport CRD (aquasecurity.github.io/v1alpha1):
 * https://aquasecurity.github.io/trivy-operator/latest/docs/crds/vulnerability-report/
 */

/** The severity counts a report's own summary carries. */
export interface SeverityCounts {
  critical: number
  high: number
  medium: number
  low: number
  unknown: number
  none: number
}

export interface Vulnerability {
  /** vulnerabilityID: a CVE, a GHSA, or a distribution's own identifier. */
  id: string
  /** CRITICAL, HIGH, MEDIUM, LOW or UNKNOWN — Trivy's word, verbatim. */
  severity: string
  /** The package the vulnerability is in. */
  resource: string
  installedVersion: string
  /** Empty when no fix has been published — which is the fact an operator is looking for. */
  fixedVersion: string
  title: string
  /** primaryLink, as Trivy wrote it. */
  primaryLink: string
  /** The scanned artefact layer/target, when the report names one. */
  target: string
  score: number | null
}

export interface TrivyVulnerabilityReport {
  /** report.artifact, as "repository:tag". */
  artifact: string
  /** report.scanner.name and .version, joined for display. */
  scanner: string
  updateTimestamp: string
  summary: SeverityCounts
  vulnerabilities: Vulnerability[]
  /**
   * The workload the report is about, from the trivy-operator.resource.* labels.
   * A report is written PER CONTAINER of one workload, which is why the container is here.
   */
  subject: { kind: string; name: string; container: string }
}

/** The labels the operator stamps the owning workload onto every report with. */
const KIND_LABEL = 'trivy-operator.resource.kind'
const NAME_LABEL = 'trivy-operator.resource.name'
const CONTAINER_LABEL = 'trivy-operator.container.name'

/** The parts of the manifest this reads. */
interface RawArtifact {
  repository?: string
  tag?: string
  digest?: string
}

interface VulnerabilityReportManifest {
  metadata?: {
    labels?: Record<string, string>
  }
  report?: {
    artifact?: RawArtifact
    scanner?: { name?: string; version?: string }
    updateTimestamp?: string
    summary?: {
      criticalCount?: number
      highCount?: number
      mediumCount?: number
      lowCount?: number
      unknownCount?: number
      noneCount?: number
    }
    vulnerabilities?: RawVulnerability[]
  }
}

interface RawVulnerability {
  vulnerabilityID?: string
  severity?: string
  resource?: string
  installedVersion?: string
  fixedVersion?: string
  title?: string
  primaryLink?: string
  target?: string
  score?: number
}

/**
 * Reads a VulnerabilityReport, or null when there is no manifest at all.
 *
 * A report with no `report` block — which is what an object part-way through
 * being written looks like — comes back with zero counts and an empty list
 * rather than null, so the panel says the report exists and has nothing in it
 * yet instead of disappearing. Zero counts are honest here in a way they are
 * not for a replica bound: a summary Trivy has not written is a scan that has
 * produced no findings so far, and the update timestamp beside it says how
 * current that is.
 */
export function trivyVulnerabilityReport(manifest: unknown): TrivyVulnerabilityReport | null {
  if (!manifest || typeof manifest !== 'object') return null
  const { metadata = {}, report = {} } = manifest as VulnerabilityReportManifest

  const labels = metadata.labels ?? {}
  const summary = report.summary ?? {}

  return {
    artifact: artifactOf(report.artifact),
    scanner: [report.scanner?.name, report.scanner?.version].filter(Boolean).join(' '),
    updateTimestamp: report.updateTimestamp ?? '',
    summary: {
      critical: summary.criticalCount ?? 0,
      high: summary.highCount ?? 0,
      medium: summary.mediumCount ?? 0,
      low: summary.lowCount ?? 0,
      unknown: summary.unknownCount ?? 0,
      none: summary.noneCount ?? 0,
    },
    vulnerabilities: (report.vulnerabilities ?? []).map((vulnerability) => ({
      id: vulnerability.vulnerabilityID ?? '',
      severity: vulnerability.severity ?? '',
      resource: vulnerability.resource ?? '',
      installedVersion: vulnerability.installedVersion ?? '',
      // Empty means no fix has been published. It is deliberately NOT
      // rendered as "none available" here: that is a sentence, and the panel
      // owns the wording of an absence.
      fixedVersion: vulnerability.fixedVersion ?? '',
      title: vulnerability.title ?? '',
      primaryLink: vulnerability.primaryLink ?? '',
      target: vulnerability.target ?? '',
      // Null rather than zero: an unscored vulnerability is one no CVSS
      // vector was published for, and 0.0 is a real score meaning harmless.
      score: vulnerability.score ?? null,
    })),
    subject: {
      kind: labels[KIND_LABEL] ?? '',
      name: labels[NAME_LABEL] ?? '',
      container: labels[CONTAINER_LABEL] ?? '',
    },
  }
}

/**
 * The artefact as "repository:tag".
 *
 * A report on an image pinned by digest carries no tag, so the digest stands
 * in for it — an artefact line with a bare repository names a different image
 * on every push and could not be matched to what is running.
 */
function artifactOf(artifact: RawArtifact | undefined): string {
  const repository = artifact?.repository ?? ''
  if (!repository) return ''
  const reference = artifact?.tag || artifact?.digest || ''
  return reference ? `${repository}:${reference}` : repository
}

/**
 * Trivy's severity word as a DetailList tone. CRITICAL and HIGH only.
 *
 * THIS IS TRIVY'S GRADING, NOT OURS. The words come from the advisory the
 * finding was published in, and the two that are coloured are the two the
 * operator's own dashboards colour. MEDIUM and LOW are left plain
 * deliberately: a report on any real image carries dozens of them, and
 * colouring those is how a list stops being read at all. A word this table
 * does not know — a scanner writing "Moderate", a severity from a
 * distribution's own scale — is left uncoloured rather than guessed at.
 */
export function severityTone(severity: string): 'warn' | 'critical' | undefined {
  switch (severity) {
    case 'CRITICAL':
      return 'critical'
    case 'HIGH':
      return 'warn'
    default:
      return undefined
  }
}

/** A CVE id, as the NVD publishes them. */
const CVE_ID = /^CVE-\d{4}-\d{4,}$/i

/** A GitHub Security Advisory id, whose three groups are a fixed base32 alphabet. */
const GHSA_ID = /^GHSA-[0-9a-z]{4}-[0-9a-z]{4}-[0-9a-z]{4}$/i

/**
 * Where to read about one vulnerability, or empty when there is nowhere
 * honest to send anyone.
 *
 * THE REPORT'S OWN `primaryLink` IS ALWAYS PREFERRED, because it is the
 * quotation: Trivy took it from the advisory database the finding came out
 * of, and it points at what the scanner actually read. Only when the report
 * carries none is a URL composed, and only from an identifier whose registry
 * is unambiguous — the NVD for a CVE, GitHub's advisory database for a GHSA.
 *
 * ANYTHING ELSE RETURNS EMPTY, and that is the whole discipline of this
 * function. A distribution's own advisory id — RUSTSEC-2021-0079,
 * DSA-5378-1, ALAS2-2023-1234 — has no universal URL: every vendor hosts its
 * own, under a path that is not derivable from the identifier, and composing
 * one produces a link that 404s. A dead link is worse than no link, because
 * it looks like the advisory has been withdrawn rather than like PodSteer
 * guessed.
 */
export function advisoryLink(vulnerability: Vulnerability): string {
  if (vulnerability.primaryLink) return vulnerability.primaryLink

  const id = vulnerability.id
  if (CVE_ID.test(id)) return `https://nvd.nist.gov/vuln/detail/${id}`
  if (GHSA_ID.test(id)) return `https://github.com/advisories/${id}`
  return ''
}
