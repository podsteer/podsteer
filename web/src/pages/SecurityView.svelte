<!--
  What this cluster's own objects say about its security posture, and what a
  scanner somebody else installed has already written down.

  THE NAME IS A PROMISE THIS PAGE CANNOT KEEP ON ITS OWN, so the first thing
  it does is say what it covers. A page called "Security" that renders empty
  on a cluster with no scanner is making a claim nobody made — "nothing found"
  where the truth is "nothing looked". Every section here states which of the
  two it is showing, and the closing section names what is deliberately not
  here and where it lives instead.

  TWO HALVES WITH DIFFERENT WARRANTIES, and they are kept visibly apart:

  - POSTURE is read from the specs operators wrote. Privileged, host
    namespaces, escalation, dangerous capabilities, UID 0. No feed, no
    database, no judgement that could go stale — a privileged container will
    mean the same thing in five years. PodSteer owns these rules
    (domain/security_findings.go).
  - VULNERABILITIES are QUOTED. PodSteer scans nothing and never will: seven
    mature scanners show zero general agreement, NIST abandoned universal CVE
    enrichment in April 2026, and a number we computed that disagreed with the
    operator's own scanner would be worse than no number. Every count here was
    written by the scanner the operator chose, before this read it. See
    domain/vulnerability.go.

  TWO TABLES, ONE AT A TIME — Posture and Vulnerabilities, each its own entry
  in the navigator's SECURITY section beside Permissions — each the same DataTable every list
  uses, with the toolbar's search, pager, column chooser, sorting and CSV
  export. A table and not a list (`session.hasTable`): the page filters, sorts
  and pages its own rows and reports the count as `standaloneCount`. Posture
  is one row per workload a finding names, so a search for a namespace or an
  object finds it; the overview keeps its cards for the same findings, and a
  snooze set there is shown here. What the page deliberately does not cover
  is the `security` help topic, under the toolbar's (?).

  NOTHING ON THIS PAGE POLLS. The posture findings ride the assessment that
  runs under every view anyway; the scanner read is one bounded cluster-wide
  call, cached in $stores/vulnerabilities and in Go behind it. A cluster-wide
  LIST of VulnerabilityReports on a ten-second timer would be the Helm page's
  audit problem over a larger collection.
-->
<script lang="ts">
  import type { ClusterSession } from '$stores/session.svelte'
  import { ALL_NAMESPACES } from '$lib/api/client'
  import type { Finding } from '$lib/api/client'
  import { preferences } from '$stores/preferences.svelte'
  import {
    ensureVulnerabilities,
    vulnerabilityReadFor,
    summariesFor,
  } from '$stores/vulnerabilities.svelte'
  import DataTable, { type Column } from '$lib/components/DataTable.svelte'
  import EmptyState from '$lib/components/EmptyState.svelte'
  import StatusIndicator from '$lib/components/StatusIndicator.svelte'
  import { isControlColumn } from '$lib/fixedColumns'
  import { matches } from '$lib/query'
  import { sortRows, type SortAccessors } from '$lib/sort'
  import type { CSVExport } from '$stores/activeTable.svelte'
  import { ShieldAlert, CircleDot, Container } from '@lucide/svelte'

  interface Props {
    session: ClusterSession
  }

  let { session }: Props = $props()

  /** Which table is showing: the navigator entry the operator chose. */
  const tab = $derived(session.securityTab)

  /**
   * One cluster-wide scanner read, when the page opens. ALL_NAMESPACES,
   * because posture is a property of the cluster; the store reads each pair
   * at most once, so re-entering the page costs nothing.
   */
  $effect(() => {
    ensureVulnerabilities(session.cluster.id, session.namespace)
  })

  const read = $derived(vulnerabilityReadFor(session.cluster.id, session.namespace))
  const summaries = $derived(summariesFor(session.cluster.id, session.namespace))

  // --- Posture ---------------------------------------------------------------

  /** The security findings, from the assessment this tab already has. */
  const findings = $derived(
    (session.overview?.findings ?? []).filter((finding) => finding.category === 'Security'),
  )

  /** Whether the tab is narrowed to one namespace — the navigator's filter
      applies here as on every list. */
  const inNamespace = (namespace: string): boolean =>
    session.namespace === ALL_NAMESPACES || namespace === session.namespace

  interface PostureRow {
    key: string
    finding: Finding
    severity: string
    title: string
    kind: string
    namespace: string
    name: string
    detail: string
    snoozed: boolean
  }

  const now = $derived.by(() => Date.now())

  /** One row per workload a finding names: that is what gets opened and fixed. */
  const postureRows = $derived<PostureRow[]>(
    findings.flatMap((finding) =>
      (finding.subjects ?? []).filter((subject) => inNamespace(subject.namespace)).map((subject) => ({
        key: `${finding.id} ${subject.kind}/${subject.namespace}/${subject.name}`,
        finding,
        severity: finding.severity,
        title: finding.title,
        kind: subject.kind,
        namespace: subject.namespace,
        name: subject.name,
        detail: subject.detail || finding.summary,
        snoozed:
          preferences.snoozedUntil(session.cluster.id, finding.id, subject.namespace, subject.name) >
          now,
      })),
    ),
  )

  /** Whether a finding named more workloads than it carried. */
  const postureTruncated = $derived(findings.some((finding) => finding.truncated))

  const POSTURE_COLUMNS: Column[] = [
    { id: 'severity', label: 'Severity', width: 44, icon: CircleDot },
    { id: 'title', label: 'Finding', width: 260, pinned: true },
    { id: 'object', label: 'Workload', width: 300 },
    { id: 'namespace', label: 'Namespace', width: 170 },
    { id: 'kind', label: 'Kind', width: 120, defaultHidden: true },
    { id: 'detail', label: 'Detail', width: 420 },
  ]

  const POSTURE_SORT: SortAccessors<PostureRow> = {
    severity: (row) => SEVERITY_RANK[row.severity] ?? 9,
    title: (row) => row.title,
    object: (row) => row.name,
    namespace: (row) => row.namespace,
    kind: (row) => row.kind,
    detail: (row) => row.detail,
  }

  const SEVERITY_RANK: Record<string, number> = { critical: 0, warning: 1, info: 2 }

  /** The finding's own severity, in the colours every list uses: red
      critical, amber warning, blue for a note. */
  function toneOf(severity: string): 'error' | 'warning' | 'success' {
    if (severity === 'critical') return 'error'
    if (severity === 'warning') return 'warning'
    return 'success'
  }

  // --- Vulnerabilities -------------------------------------------------------

  interface ImageRow {
    image: string
    critical: number
    high: number
    medium: number
    low: number
    unknown: number
    workloads: number
  }

  /**
   * Every scanned image, KEYED BY IMAGE rather than by workload, because the
   * image is what gets fixed. The counts are the MAXIMUM across the workloads
   * running it — summing would count one CVE once per workload.
   */
  const imageRows = $derived.by<ImageRow[]>(() => {
    const byImage = new Map<string, ImageRow>()
    for (const summary of summaries) {
      for (const image of summary.images ?? []) {
        const row = byImage.get(image) ?? {
          image,
          critical: 0,
          high: 0,
          medium: 0,
          low: 0,
          unknown: 0,
          workloads: 0,
        }
        row.critical = Math.max(row.critical, summary.critical)
        row.high = Math.max(row.high, summary.high)
        row.medium = Math.max(row.medium, summary.medium)
        row.low = Math.max(row.low, summary.low)
        row.unknown = Math.max(row.unknown, summary.unknown)
        row.workloads += 1
        byImage.set(image, row)
      }
    }
    // The default order when no column is sorted: worst first.
    return [...byImage.values()].sort(
      (a, b) =>
        b.critical - a.critical ||
        b.high - a.high ||
        b.medium - a.medium ||
        b.workloads - a.workloads ||
        a.image.localeCompare(b.image),
    )
  })

  const IMAGE_COLUMNS: Column[] = [
    { id: 'mark', label: 'Severity', width: 44, icon: CircleDot },
    { id: 'image', label: 'Image', width: 420, pinned: true },
    { id: 'workloads', label: 'Workloads', width: 120, numeric: true },
    { id: 'critical', label: 'Critical', width: 110, numeric: true },
    { id: 'high', label: 'High', width: 100, numeric: true },
    { id: 'medium', label: 'Medium', width: 110, numeric: true },
    { id: 'low', label: 'Low', width: 100, numeric: true },
    // Its own column, not folded into Low: trivy-operator files genuine highs
    // there when a finding's sources state no severity.
    { id: 'unknown', label: 'Unknown', width: 110, numeric: true },
  ]

  /** Red with a critical, amber with only highs, grey otherwise. */
  /** Red with a critical, amber with only highs, blue (normal) otherwise —
      the colours every list uses. */
  function imageTone(row: ImageRow): 'error' | 'warning' | 'success' {
    if (row.critical > 0) return 'error'
    if (row.high > 0) return 'warning'
    return 'success'
  }

  const IMAGE_SORT: SortAccessors<ImageRow> = {
    mark: (row) => (row.critical > 0 ? 0 : row.high > 0 ? 1 : 2),
    image: (row) => row.image,
    workloads: (row) => row.workloads,
    critical: (row) => row.critical,
    high: (row) => row.high,
    medium: (row) => row.medium,
    low: (row) => row.low,
    unknown: (row) => row.unknown,
  }

  // --- The table showing ------------------------------------------------------

  /** Column preferences are kept per table — each navigator entry is its own
      id, so hiding a column in one does not hide it in the other. */
  const tableKind = $derived(session.selectedKindId)

  function search<T>(rows: T[], text: (row: T) => string[]): T[] {
    if (session.query.terms.length === 0) return rows
    return rows.filter((row) =>
      matches(session.query, { text: text(row).join(' '), labels: {}, cluster: session.cluster.id }),
    )
  }

  const visiblePosture = $derived(
    search(postureRows, (row) => [row.title, row.kind, row.namespace, row.name, row.detail, row.severity]),
  )
  const visibleImages = $derived(search(imageRows, (row) => [row.image]))

  const sortedPosture = $derived(sortRows(visiblePosture, session.sort, POSTURE_SORT))
  const sortedImages = $derived(sortRows(visibleImages, session.sort, IMAGE_SORT))

  const pagedPosture = $derived(
    sortedPosture.slice(session.pageStart, session.pageStart + preferences.pageSize),
  )
  const pagedImages = $derived(
    sortedImages.slice(session.pageStart, session.pageStart + preferences.pageSize),
  )

  $effect(() => {
    session.standaloneCount = tab === 'posture' ? visiblePosture.length : visibleImages.length
  })

  function isColumnVisible(column: Column): boolean {
    const stored = preferences.columns[tableKind]?.[column.id]?.hidden
    return column.pinned || (stored === undefined ? !column.defaultHidden : !stored)
  }

  function exportPosture(): CSVExport {
    const visible = POSTURE_COLUMNS.filter((c) => !isControlColumn(c) && isColumnVisible(c))
    const cell = (row: PostureRow, id: string): string => {
      switch (id) {
        case 'severity':
          return row.severity
        case 'title':
          return row.title
        case 'object':
          return row.name
        case 'namespace':
          return row.namespace || '—'
        case 'kind':
          return row.kind
        case 'detail':
          return row.detail
        default:
          return ''
      }
    }
    return {
      columns: visible.map((c) => c.label),
      rows: sortedPosture.map((row) => visible.map((c) => cell(row, c.id))),
    }
  }

  function exportImages(): CSVExport {
    const visible = IMAGE_COLUMNS.filter((c) => !isControlColumn(c) && isColumnVisible(c))
    return {
      columns: visible.map((c) => c.label),
      rows: sortedImages.map((row) =>
        visible.map((c) =>
          c.id === 'mark'
            ? row.critical > 0 ? 'critical' : row.high > 0 ? 'high' : '—'
            : String(row[c.id as keyof ImageRow]),
        ),
      ),
    }
  }

  async function openObject(row: PostureRow): Promise<void> {
    const kindId = row.finding.kindId
    const kind = session.kinds.find((entry) => entry.id === kindId)
    await session.openObject(kindId, row.name, row.namespace, kind?.namespaced ?? true)
  }

  /** A severity count: bold in its colour when it is more than nought. */
  function countTone(id: string, value: number): string {
    if (value === 0) return 'text-on-surface-variant/50'
    if (id === 'critical') return 'font-semibold text-gauge-critical-ink'
    if (id === 'high') return 'font-semibold text-gauge-warn-ink'
    return 'text-on-surface-variant'
  }
</script>

{#if tab === 'posture'}
  {#key tableKind}
    <DataTable
      kindId={tableKind}
      columns={POSTURE_COLUMNS}
      isEmpty={pagedPosture.length === 0}
      sort={session.sort}
      onsort={session.toggleSort}
      exportRows={exportPosture}
    >
      {#snippet notice()}
        {#if postureTruncated}
          <p class="border-b border-outline-variant/60 px-6 py-2 text-body-medium text-gauge-warn-ink" role="status">
            Some findings name more workloads than are listed here.
          </p>
        {/if}
      {/snippet}

      {#snippet empty()}
        {#if !session.overview}
          <EmptyState title="Assessing the cluster…" description="Posture rides the assessment every view runs." />
        {:else if session.search && postureRows.length > 0}
          <EmptyState title="No findings match" description={`Nothing matches "${session.search}".`} />
        {:else}
          <!-- A real answer, not an absence: these rules fire only on a
               deliberate act, so nothing found means nothing asks for it. -->
          <EmptyState
            title="No privileged workloads"
            description="No workload here runs privileged, shares a host namespace, allows privilege escalation, holds a dangerous capability, or pins itself to UID 0."
          />
        {/if}
      {/snippet}

      {#snippet rows(isVisible)}
        {#each pagedPosture as row (row.key)}
          <tr
            class="group/row cursor-pointer border-t border-outline-variant/40 bg-surface
                   transition-colors duration-100 hover:bg-surface-container-low
                   {row.snoozed ? 'opacity-60' : ''}"
            onclick={() => void openObject(row)}
          >
            {#if isVisible('severity')}
              <td class="overflow-hidden py-1.5 pr-3 pl-6">
                <StatusIndicator tone={toneOf(row.severity)} label={row.severity} icon={ShieldAlert} />
              </td>
            {/if}
            <td class="px-3 py-1.5" title={row.title}>
              <span class="flex items-center gap-2">
                <span class="truncate font-medium text-on-surface">{row.title}</span>
                {#if row.snoozed}
                  <span class="shrink-0 rounded-full bg-surface-container-high px-2 py-0.5 text-label-small text-on-surface-variant">
                    Snoozed
                  </span>
                {/if}
              </span>
            </td>
            {#if isVisible('object')}
              <td class="truncate px-3 py-1.5 text-on-surface" title={row.name} data-selectable>{row.name}</td>
            {/if}
            {#if isVisible('namespace')}
              <td class="truncate px-3 py-1.5 text-on-surface-variant">{row.namespace || '—'}</td>
            {/if}
            {#if isVisible('kind')}
              <td class="truncate px-3 py-1.5 text-on-surface-variant">{row.kind}</td>
            {/if}
            {#if isVisible('detail')}
              <td class="truncate px-3 py-1.5 text-on-surface-variant" title={row.detail}>{row.detail}</td>
            {/if}
          </tr>
        {/each}
      {/snippet}
    </DataTable>
  {/key}
{:else}
  {#key tableKind}
    <DataTable
      kindId={tableKind}
      columns={IMAGE_COLUMNS}
      isEmpty={pagedImages.length === 0}
      sort={session.sort}
      onsort={session.toggleSort}
      exportRows={exportImages}
    >
      {#snippet notice()}
        {#if read?.truncated}
          <!-- A ceiling is a fact about the answer: below it, the list is what
               was read rather than what exists. -->
          <p class="border-b border-outline-variant/60 px-6 py-2 text-body-medium text-gauge-warn-ink" role="status">
            Stopped at {read.cap.toLocaleString()} reports. {read.read.toLocaleString()} were read{read.remaining
              ? `, and the server said ${read.remaining.toLocaleString()} more were withheld`
              : ''}. What follows is that much of the picture, not all of it.
          </p>
        {/if}
      {/snippet}

      {#snippet empty()}
        {#if !read}
          <EmptyState title="Reading what the scanner recorded…" description="One cluster-wide read, cached." />
        {:else if read.status === 'not-installed'}
          <!-- The sentence this page exists to get right: blank because
               nothing has looked, not because the images are clean. -->
          <EmptyState
            title="No vulnerability scanner is installed"
            description="This is blank because nothing has looked — not because the images are clean. Anything writing VulnerabilityReport objects, such as the Trivy Operator, fills it in."
          />
        {:else if read.status === 'forbidden'}
          <EmptyState
            title="The scanner's reports could not be read"
            description="A scanner is installed, but this kubeconfig may not read its reports. Nothing is missing because the images are clean — the read was refused."
          />
        {:else if !read.complete && !read.truncated}
          <EmptyState
            title="The scanner read did not complete"
            description="Nothing here can be read as an account of this cluster."
          />
        {:else if session.search && imageRows.length > 0}
          <EmptyState title="No images match" description={`Nothing matches "${session.search}".`} />
        {:else}
          <EmptyState title="No reports yet" description="The scanner has written no reports for this cluster's workloads yet." />
        {/if}
      {/snippet}

      {#snippet rows(isVisible)}
        {#each pagedImages as row (row.image)}
          <tr class="border-t border-outline-variant/40 bg-surface transition-colors duration-100 hover:bg-surface-container-low">
            {#if isVisible('mark')}
              <td class="overflow-hidden py-1.5 pr-3 pl-6">
                <StatusIndicator
                  tone={imageTone(row)}
                  label={row.critical > 0 ? 'Critical' : row.high > 0 ? 'High' : 'No critical or high'}
                  icon={Container}
                />
              </td>
            {/if}
            <td class="truncate px-3 py-1.5 font-medium text-on-surface" title={row.image} data-selectable>
              {row.image}
            </td>
            {#each IMAGE_COLUMNS.slice(2) as column (column.id)}
              {#if isVisible(column.id)}
                {@const value = row[column.id as keyof ImageRow] as number}
                <td class="truncate px-3 py-1.5 text-right tabular-nums {column.id === 'workloads' ? 'text-on-surface-variant' : countTone(column.id, value)}">
                  {value}
                </td>
              {/if}
            {/each}
          </tr>
        {/each}
      {/snippet}
    </DataTable>
  {/key}
{/if}
