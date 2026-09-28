<!--
  The Permissions page: what this kubeconfig may do in the tab's namespace.

  THE SAME TABLE EVERY LIST USES. The rules review is the one question this
  page answers on arrival, and its answer is rows — so it is drawn by
  DataTable, with the toolbar's search, pager, column chooser and CSV export,
  and an operator moves through it exactly as through Pods. Namespaced rules
  and the cluster-scoped URL paths are ONE table with a Type column rather
  than two, because two tables on one page cannot share one search box.

  It is a table and not a list (`session.hasTable`, not `isList`): there is
  no selection, no saved view, and nothing fetched on the tick. The page
  filters, sorts and pages its own rows through the session's `query`,
  `sort` and `pageStart`, and reports the filtered count back as
  `standaloneCount` for the pager.

  The other two questions are tools, in the header toolbar, each a dialog:
  Can I… (`CanIDialog`) and Who holds a role (`RoleHoldersDialog`).

  THE API SERVER DECIDES AND PODSTEER ONLY FLAGS. The rules review and the
  access review are quotations, rendered as they arrived; the blast-radius
  flags in the role dialog are the one verdict, and they live in Go where each
  has a test (`app/domain/rbac.go`).

  NOTHING HERE POLLS. Every read happens because the page opened, its
  namespace changed, or a button was pressed — which is why
  `ClusterSession`'s tick has a case for this view that fetches nothing: an
  allow shown from a previous tick would keep reading as granted after the
  permission behind it was revoked.
-->
<script lang="ts">
  import CanIDialog from '$lib/components/CanIDialog.svelte'
  import DataTable, { type Column } from '$lib/components/DataTable.svelte'
  import EmptyState from '$lib/components/EmptyState.svelte'
  import ErrorBanner from '$lib/components/ErrorBanner.svelte'
  import RoleHoldersDialog from '$lib/components/RoleHoldersDialog.svelte'
  import { toApiError, type ApiError } from '$lib/api/errors'
  import { subjectRules as askSubjectRules, type SubjectRules } from '$lib/api/client'
  import { isControlColumn } from '$lib/fixedColumns'
  import { matches } from '$lib/query'
  import { pathRows, reviewState, verbRows } from '$lib/rbac'
  import { sortRows, type SortAccessors } from '$lib/sort'
  import type { CSVExport } from '$stores/activeTable.svelte'
  import { preferences } from '$stores/preferences.svelte'
  import type { ClusterSession } from '$stores/session.svelte'
  import { untrack } from 'svelte'
  import StatusIndicator from '$lib/components/StatusIndicator.svelte'
  import { CircleDot, KeyRound, Route } from '@lucide/svelte'

  interface Props {
    session: ClusterSession
    /** Whether the Can I dialog is open; the toolbar opens it. */
    canIOpen?: boolean
    /** Whether the role dialog is open; the toolbar opens it. */
    rolesOpen?: boolean
  }

  let { session, canIOpen = $bindable(false), rolesOpen = $bindable(false) }: Props = $props()

  let rules = $state<SubjectRules | null>(null)
  let rulesError = $state<ApiError | null>(null)
  let loading = $state(false)

  /** Which cluster and namespace the answer on screen is about. */
  let rulesFor = $state('')

  /**
   * Counts the reads issued, so a slow one cannot overwrite a later answer —
   * switching namespace twice quickly would otherwise land the first
   * namespace's permissions under the second one's name.
   */
  let rulesGeneration = 0

  async function loadRules(clusterId: string, namespace: string): Promise<void> {
    rulesFor = `${clusterId} ${namespace}`
    loading = true
    rulesError = null
    const generation = ++rulesGeneration
    try {
      const answer = await askSubjectRules(clusterId, namespace)
      if (generation !== rulesGeneration) return
      rules = answer
    } catch (cause) {
      if (generation !== rulesGeneration) return
      rules = null
      rulesError = toApiError(cause)
    } finally {
      if (generation === rulesGeneration) loading = false
    }
  }

  /**
   * One request when the page opens and one whenever the tab's namespace
   * changes — never on the refresh tick. Keyed on the pair, because "what may
   * I do here" is a different question in every namespace.
   */
  $effect(() => {
    const key = `${session.cluster.id} ${session.namespace}`
    if (key === rulesFor) return
    void loadRules(session.cluster.id, session.namespace)
  })

  /**
   * The application's own Refresh re-asks — and ONLY a person pressing it.
   * The tick fetches nothing here by design; `manualRefreshes` counts the
   * presses, so this effect runs once per press and never on the timer.
   */
  let seenRefreshes = untrack(() => session.manualRefreshes)
  $effect(() => {
    const presses = session.manualRefreshes
    if (presses === seenRefreshes) return
    seenRefreshes = presses
    void loadRules(session.cluster.id, session.namespace)
  })

  const rulesState = $derived(reviewState(rules?.status ?? 'answered', rules?.refusal ?? ''))

  /** The namespace the review actually named. */
  const reviewedNamespace = $derived(rules?.namespace || session.namespace || 'default')

  /** One row of the table: a resource rule or a non-resource URL path. */
  interface PermissionRow {
    key: string
    type: 'Resource' | 'URL path'
    group: string
    target: string
    only: string
    verbs: string[]
  }

  const allRows = $derived<PermissionRow[]>([
    ...verbRows(rules?.namespaced ?? []).map((row) => ({
      key: `r ${row.group}/${row.resource}/${row.resourceNames.join(',')}`,
      type: 'Resource' as const,
      group: row.group || '(core)',
      target: row.resource,
      only: row.resourceNames.join(', '),
      verbs: row.verbs,
    })),
    ...pathRows(rules?.clusterScoped ?? []).map((row) => ({
      key: `p ${row.path}`,
      type: 'URL path' as const,
      group: '',
      target: row.path,
      only: '',
      verbs: row.verbs,
    })),
  ])

  /**
   * How much a row reaches, for its mark — red, amber or blue like every
   * list. A PRESENTATION of what the rule says, not a verdict on the account:
   * the API server's enumeration is quoted as it arrived, and the role
   * dialog's flags (from the Go domain) remain the one assessment here.
   *
   *   critical — every verb on every resource (or every path).
   *   warning  — a wildcard verb or resource, escalate/bind/impersonate, or
   *              reading Secrets.
   *   normal   — anything narrower.
   */
  function reach(row: PermissionRow): 'critical' | 'warning' | 'normal' {
    const everyVerb = row.verbs.includes('*')
    const everyTarget = row.target === '*'
    if (everyVerb && everyTarget) return 'critical'
    if (everyVerb || everyTarget) return 'warning'
    if (row.verbs.some((verb) => ['escalate', 'bind', 'impersonate'].includes(verb))) return 'warning'
    if (row.target === 'secrets' && row.verbs.some((verb) => ['get', 'list', 'watch'].includes(verb))) {
      return 'warning'
    }
    return 'normal'
  }

  const TONES = { critical: 'error', warning: 'warning', normal: 'success' } as const
  const REACH_RANK = { critical: 0, warning: 1, normal: 2 } as const

  const COLUMNS: Column[] = [
    { id: 'mark', label: 'Reach', width: 44, icon: CircleDot },
    { id: 'type', label: 'Type', width: 120 },
    { id: 'target', label: 'Resource or path', width: 300, pinned: true },
    { id: 'group', label: 'API group', width: 240 },
    { id: 'only', label: 'Only named', width: 200 },
    { id: 'verbs', label: 'Verbs', width: 360 },
  ]

  const SORT: SortAccessors<PermissionRow> = {
    mark: (row) => REACH_RANK[reach(row)],
    type: (row) => row.type,
    target: (row) => row.target,
    group: (row) => row.group,
    only: (row) => row.only,
    verbs: (row) => row.verbs.join(' '),
  }

  function textOf(row: PermissionRow): string {
    return [row.type, row.target, row.group, row.only, ...row.verbs].join(' ')
  }

  const visibleRows = $derived(
    session.query.terms.length === 0
      ? allRows
      : allRows.filter((row) =>
          matches(session.query, { text: textOf(row), labels: {}, cluster: session.cluster.id }),
        ),
  )
  const sortedRows = $derived(sortRows(visibleRows, session.sort, SORT))
  const pagedRows = $derived(
    sortedRows.slice(session.pageStart, session.pageStart + preferences.pageSize),
  )

  // The pager counts what this page's own filter kept.
  $effect(() => {
    session.standaloneCount = visibleRows.length
  })

  function isColumnVisible(column: Column): boolean {
    const stored = preferences.columns[session.selectedKindId]?.[column.id]?.hidden
    return column.pinned || (stored === undefined ? !column.defaultHidden : !stored)
  }

  /** The CSV export, mirroring what each cell shows. */
  function exportCSV(): CSVExport {
    const visible = COLUMNS.filter((column) => !isControlColumn(column) && isColumnVisible(column))
    const cell = (row: PermissionRow, id: string): string => {
      switch (id) {
        case 'mark':
          return reach(row)
        case 'type':
          return row.type
        case 'target':
          return row.target
        case 'group':
          return row.group || '—'
        case 'only':
          return row.only || '—'
        case 'verbs':
          return row.verbs.join(', ')
        default:
          return ''
      }
    }
    return {
      columns: visible.map((column) => column.label),
      rows: sortedRows.map((row) => visible.map((column) => cell(row, column.id))),
    }
  }
</script>

<DataTable
  kindId={session.selectedKindId}
  columns={COLUMNS}
  isEmpty={pagedRows.length === 0}
  sort={session.sort}
  onsort={session.toggleSort}
  exportRows={exportCSV}
>
  {#snippet notice()}
    {#if rules?.incomplete}
      <!-- Only when the answer is partial: a partial list that does not say
           so reads as a complete one. -->
      <p class="border-b border-outline-variant/60 px-6 py-2 text-body-medium text-gauge-warn-ink" role="status">
        The API server could not enumerate everything, so this list may be short.
        {rules.incompleteReason}
      </p>
    {/if}
    <ErrorBanner error={rulesError} ondismiss={() => (rulesError = null)} class="mx-6 my-3" />
  {/snippet}

  {#snippet empty()}
    {#if rulesState.kind === 'unavailable'}
      <EmptyState title="The rules could not be read" description={rulesState.message} />
    {:else if loading && !rules}
      <EmptyState title="Asking the cluster…" description="One rules review for this namespace." />
    {:else if session.search}
      <EmptyState title="No permissions match" description={`Nothing matches "${session.search}".`} />
    {:else}
      <EmptyState
        title="No permissions here"
        description="This account holds no permissions on objects in this namespace."
      />
    {/if}
  {/snippet}

  {#snippet rows(isVisible)}
    {#each pagedRows as row (row.key)}
      {@const level = reach(row)}
      <tr class="border-t border-outline-variant/40 bg-surface transition-colors duration-100 hover:bg-surface-container-low">
        {#if isVisible('mark')}
          <td class="overflow-hidden py-1.5 pr-3 pl-6">
            <StatusIndicator
              tone={TONES[level]}
              label={level === 'critical' ? 'Every verb on everything' : level === 'warning' ? 'Wide reach' : 'Narrow'}
              icon={row.type === 'Resource' ? KeyRound : Route}
            />
          </td>
        {/if}
        {#if isVisible('type')}
          <td class="truncate px-3 py-1.5 text-on-surface-variant">{row.type}</td>
        {/if}
        <td class="truncate px-3 py-1.5 font-medium text-on-surface" title={row.target} data-selectable>
          {row.target}
        </td>
        {#if isVisible('group')}
          <td class="truncate px-3 py-1.5 text-on-surface-variant" title={row.group}>{row.group || '—'}</td>
        {/if}
        {#if isVisible('only')}
          <td class="truncate px-3 py-1.5 text-on-surface-variant" title={row.only}>{row.only || '—'}</td>
        {/if}
        {#if isVisible('verbs')}
          <td class="truncate px-3 py-1.5 text-on-surface-variant" title={row.verbs.join(', ')}>
            {row.verbs.join(', ')}
          </td>
        {/if}
      </tr>
    {/each}
  {/snippet}
</DataTable>

<CanIDialog
  open={canIOpen}
  clusterId={session.cluster.id}
  namespace={session.namespace}
  onclose={() => (canIOpen = false)}
/>
<RoleHoldersDialog open={rolesOpen} clusterId={session.cluster.id} onclose={() => (rolesOpen = false)} />
