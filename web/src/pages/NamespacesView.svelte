<!--
  The namespace list.

  Kubernetes reports a namespace as a name and a phase, which is why every
  client's namespace list is three columns that answer nothing. The questions
  actually asked of one are whether anything is still running in it, whether
  any of that is broken, and how much of the cluster it is holding — none of
  which is knowable without looking inside, so PodSteer looks: see
  domain.NewNamespaceSummaries.

  The meters are the pod list's, drawn from the same fields and the same rules
  — see $lib/meter. A namespace's usage IS the sum of its pods', so measuring
  it against the sum of their requests or their limits is the same question
  the pod list asks one pod at a time, and it deserves the same answer rather
  than two numbers in a column of text.
-->
<script lang="ts">
  import DataTable, { type Column } from '$lib/components/DataTable.svelte'
  import type { CSVExport } from '$stores/activeTable.svelte'
  import StatusIndicator from '$lib/components/StatusIndicator.svelte'
  import MeterBar from '$lib/components/MeterBar.svelte'
  import EmptyState from '$lib/components/EmptyState.svelte'
  import RowMenu, { type RowAction } from '$lib/components/RowMenu.svelte'
  import CustomCells from '$lib/components/CustomCells.svelte'
  import { customCell, parseCustomColumnId, toColumns } from '$lib/customColumns'
  import { formatAge } from '$lib/format'
  import { cpuMeter, cpuTitle, memoryMeter, memoryTitle } from '$lib/meter'
  import { preferences } from '$stores/preferences.svelte'
  import { get as kubectlGet } from '$lib/kubectl'
  import type { ClusterSession } from '$stores/session.svelte'
  import type { NamespaceSummary } from '$lib/api/client'
  import { Boxes, CircleDot } from '@lucide/svelte'

  interface Props {
    session: ClusterSession
  }

  let { session }: Props = $props()

  /** Namespaces are cluster-scoped, so there is no namespace to pass. */
  function actionsFor(namespace: NamespaceSummary): RowAction[] {
    return [
      {
        label: 'Copy as kubectl',
        kind: 'copy',
        onclick: () => copyKubectl(kubectlGet(session.cluster.id, 'namespaces', namespace.name)),
      },
    ]
  }

  function copyKubectl(command: string): void {
    void navigator.clipboard?.writeText(command).catch(() => {})
  }

  /**
   * The same denominator the pod list is set to.
   *
   * One setting for both, because it is one question — is this measured
   * against what was reserved, or against what it will be stopped at — and a
   * namespace's answer is the sum of its pods'. Two settings would let the
   * two lists disagree about the same figure.
   */
  const byLimit = $derived(preferences.podMeasure === 'limits')

  const COLUMNS: Column[] = [
    { id: 'status', label: 'Status', width: 44, icon: CircleDot },
    { id: 'name', label: 'Name', width: 320, pinned: true },
    { id: 'pods', label: 'Pods', width: 90, numeric: true },
    { id: 'notReady', label: 'Not ready', width: 100, numeric: true },
    { id: 'cpu', label: 'CPU', width: 220, minWidth: 200 },
    { id: 'memory', label: 'Memory', width: 220, minWidth: 200 },
    { id: 'cpuRequests', label: 'CPU requested', width: 130, numeric: true, defaultHidden: true },
    {
      id: 'memoryRequests',
      label: 'Memory requested',
      width: 150,
      numeric: true,
      defaultHidden: true,
    },
    { id: 'age', label: 'Age', width: 80, numeric: true },
  ]

  /** The built-in columns, then the operator's own — see $lib/customColumns. */
  const columns = $derived<Column[]>([...COLUMNS, ...toColumns(session.customColumns)])

  /** Same rule ColumnMenu and DataTable apply — see PodsView for why it is
      repeated here rather than asked of either. */
  function isColumnVisible(column: Column): boolean {
    const stored = preferences.columns[session.selectedKindId]?.[column.id]?.hidden
    return column.pinned || (stored === undefined ? !column.defaultHidden : !stored)
  }

  /** The namespace list's CSV export, mirroring exactly what each cell
      shows — the meters export the aggregated usage with its unit, not the
      bare percentage. */
  function exportCSV(): CSVExport {
    const visible = columns.filter(isColumnVisible)

    function cell(namespace: NamespaceSummary, id: string): string {
      const custom = parseCustomColumnId(id)
      if (custom) return customCell(namespace, custom)
      switch (id) {
        case 'status':
          return namespace.phase
        case 'name':
          return namespace.name
        case 'pods':
          return String(namespace.pods)
        case 'notReady':
          return String(namespace.notReady)
        case 'cpu':
          return namespace.hasMetrics ? namespace.cpu : '—'
        case 'memory':
          return namespace.hasMetrics ? namespace.memory : '—'
        case 'cpuRequests':
          return namespace.cpuRequest
        case 'memoryRequests':
          return namespace.memoryRequest
        case 'age':
          return formatAge(namespace.ageSeconds)
        default:
          return ''
      }
    }

    return {
      columns: visible.map((column) => column.label),
      rows: session.sortedNamespaces.map((namespace) =>
        visible.map((column) => cell(namespace, column.id)),
      ),
    }
  }
</script>

<DataTable
  kindId={session.selectedKindId}
  {columns}
  isEmpty={session.pagedNamespaces.length === 0}
  sort={session.sort}
  onsort={session.toggleSort}
  exportRows={exportCSV}
>
  {#snippet empty()}
    <EmptyState
      title="No namespaces"
      description="This cluster reports no namespaces you can see."
    />
  {/snippet}

  {#snippet rows(isVisible)}
    {#each session.pagedNamespaces as namespace (namespace.name)}
      {@const selected = session.selectedName === namespace.name}
      <tr
        class="group/row cursor-pointer border-t border-outline-variant/25 transition-colors duration-75
               {selected ? 'bg-primary/8' : 'hover:bg-surface-container-low'}"
        onclick={() => session.openDetail(namespace.name, '')}
      >
        {#if isVisible('status')}
          <td class="overflow-hidden py-1.5 pr-3 pl-5">
            <!-- Terminating is a warning rather than an error: it is a
                 namespace doing what it was asked to. One that has been
                 doing it for an hour is a different matter, and that is what
                 the age column beside it is for. -->
            <StatusIndicator
              tone={namespace.isActive ? 'success' : 'warning'}
              label={namespace.phase}
              icon={Boxes}
            />
          </td>
        {/if}
        <td class="px-3 py-1.5" title={namespace.name}>
          <span class="truncate font-medium text-on-surface">{namespace.name}</span>
        </td>
        {#if isVisible('pods')}
          <!-- Dimmed at zero. An empty namespace is worth spotting and a
               column of bold noughts is not — it is the one number here that
               means "nothing to see". -->
          <td
            class="truncate px-3 py-1.5 text-right tabular-nums {namespace.pods === 0
              ? 'text-on-surface-variant/40'
              : 'text-on-surface-variant'}"
          >
            {namespace.pods}
          </td>
        {/if}
        {#if isVisible('notReady')}
          <td
            class="truncate px-3 py-1.5 text-right tabular-nums {namespace.notReady > 0
              ? 'font-medium text-warning'
              : 'text-on-surface-variant/40'}"
          >
            {namespace.notReady}
          </td>
        {/if}
        <!--
          THE SAME METER THE POD AND NODE LISTS DRAW, from the same fields and
          the same rules — see $lib/meter. A namespace's usage is the sum of
          its pods', so it is measured against the sum of their requests or
          their limits, whichever the pod list is set to. A namespace whose
          pods declared nothing has no denominator and SAYS SO where the bar
          would be, rather than being metered against something invented.
        -->
        {#if isVisible('cpu')}
          {@const cpu = cpuMeter(namespace, byLimit)}
          <td class="overflow-hidden px-3 py-1.5 text-on-surface-variant">
            <MeterBar
              label={namespace.hasMetrics ? namespace.cpu : '—'}
              scope="pods"
              name="CPU"
              valueWidth="7ch"
              percent={cpu.percent}
              measured={namespace.hasMetrics}
              thresholds={cpu.thresholds}
              absent={cpu.absent}
              severity={cpu.severity}
              title={cpuTitle(namespace)}
            />
          </td>
        {/if}
        {#if isVisible('memory')}
          {@const memory = memoryMeter(namespace, byLimit)}
          <td class="overflow-hidden px-3 py-1.5 text-on-surface-variant">
            <MeterBar
              label={namespace.hasMetrics ? namespace.memory : '—'}
              scope="pods"
              name="Memory"
              percent={memory.percent}
              measured={namespace.hasMetrics}
              thresholds={memory.thresholds}
              absent={memory.absent}
              severity={memory.severity}
              title={memoryTitle(namespace)}
            />
          </td>
        {/if}
        {#if isVisible('cpuRequests')}
          <td class="truncate px-3 py-1.5 text-right tabular-nums text-on-surface-variant">
            {namespace.cpuRequest}
          </td>
        {/if}
        {#if isVisible('memoryRequests')}
          <td class="truncate px-3 py-1.5 text-right tabular-nums text-on-surface-variant">
            {namespace.memoryRequest}
          </td>
        {/if}
        {#if isVisible('age')}
          <td class="truncate px-3 py-1.5 text-right tabular-nums text-on-surface-variant">
            {formatAge(namespace.ageSeconds)}
          </td>
        {/if}
        <CustomCells specs={session.customColumns} row={namespace} {isVisible} />
        <!-- Stops the click here: the row itself opens the detail drawer,
             and a click aimed at the menu — or at one of its items — must
             not also do that. -->
        <td class="px-2" onclick={(event) => event.stopPropagation()}>
          <div class="flex justify-end">
            <RowMenu actions={actionsFor(namespace)} label={namespace.name} />
          </div>
        </td>
      </tr>
    {/each}
  {/snippet}
</DataTable>
