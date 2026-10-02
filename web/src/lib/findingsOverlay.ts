/**
 * The overview's findings, laid over the topology.
 *
 * NOTHING HERE ASSESSES ANYTHING. The findings are the assessment the session
 * already holds — the one the overview draws, the navigator badges and the
 * desktop notifications read — indexed by the objects they name, so a box can
 * say "two findings name me" and lead to them. A second opinion computed from
 * the topology's own states would be a second set of rules to keep in step
 * with domain/overview.go, and the two would disagree.
 *
 * What is passed in is the session's ACTIVE issues: info findings and fully
 * snoozed ones are already gone, so a badge never shouts about something the
 * operator chose to live with.
 */

/** The parts of a finding this needs — structural, so a literal passes in a test. */
export interface FindingLike {
  id: string
  severity: string
  title: string
  subjects: { kind: string; namespace: string; name: string }[] | null
}

export interface FindingRef {
  id: string
  severity: string
  title: string
}

export interface FindingsIndex {
  bySubject: Map<string, FindingRef[]>
}

/** What a box shows: how many distinct findings, and the worst of them. */
export interface FindingBadge {
  count: number
  severity: string
  findings: FindingRef[]
}

/** Kind, namespace and name — the identity a finding's subject carries. */
export function subjectKey(kind: string, namespace: string, name: string): string {
  return `${kind}\u0000${namespace}\u0000${name}`
}

const SEVERITY_RANK: Record<string, number> = { critical: 3, warning: 2, info: 1 }

export function indexFindings(findings: readonly FindingLike[] | null | undefined): FindingsIndex {
  const bySubject = new Map<string, FindingRef[]>()
  for (const finding of findings ?? []) {
    const ref = { id: finding.id, severity: finding.severity, title: finding.title }
    for (const subject of finding.subjects ?? []) {
      const key = subjectKey(subject.kind, subject.namespace, subject.name)
      const list = bySubject.get(key)
      if (!list) bySubject.set(key, [ref])
      else if (!list.some((held) => held.id === ref.id)) list.push(ref)
    }
  }
  return { bySubject }
}

/**
 * The badge for a box standing for these objects — one object, a folded set,
 * or a whole collapsed group. Distinct findings, so thirty pods named by the
 * same finding count once; null when nothing names any of them.
 */
export function badgeFor(
  index: FindingsIndex,
  members: readonly { apiKind: string; namespace: string; name: string }[],
): FindingBadge | null {
  if (index.bySubject.size === 0) return null
  const found = new Map<string, FindingRef>()
  for (const member of members) {
    for (const ref of index.bySubject.get(subjectKey(member.apiKind, member.namespace, member.name)) ?? []) {
      found.set(ref.id, ref)
    }
  }
  if (found.size === 0) return null
  const findings = [...found.values()].sort(
    (a, b) => (SEVERITY_RANK[b.severity] ?? 0) - (SEVERITY_RANK[a.severity] ?? 0) || (a.title < b.title ? -1 : 1),
  )
  return { count: findings.length, severity: findings[0].severity, findings }
}
