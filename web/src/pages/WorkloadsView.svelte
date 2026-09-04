<!--
  The controller list, shared by all six workload kinds.

  One view rather than six because the question is the same in every case: how
  many replicas should be running, how many are, and is a rollout in progress.
  CronJobs are the exception and get their schedule columns instead.
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
  import { cpuMeter, cpuTitle, memoryMeter, memoryTitle, type Measured } from '$lib/meter'
  import { preferences } from '$stores/preferences.svelte'
  import { get as kubectlGet, resourceArgForKind } from '$lib/kubectl'
  import type { Tone } from '$lib/format'
  import type { ClusterSession } from '$stores/session.svelte'
  import type { Workload } from '$lib/api/client'
  import { Container, CircleDot } from '@lucide/svelte'
  import { iconForKind } from '$lib/kindIcons'
  import { gitOpsOwner } from '$lib/gitops'
  import GitOpsBadge from '$lib/components/GitOpsBadge.svelte'

  const UNMEASURED: Measured = {
    hasMetrics: false,
    cpu: '',
    memory: '',
    cpuRequest: '',
    memoryRequest: '',
    cpuLimit: '',
    memoryLimit: '',
    hasCpuRequest: false,
    hasMemoryRequest: false,
    hasCpuLimit: false,
    hasMemoryLimit: false,
    cpuPercent: 0,
    memoryPercent: 0,
    cpuLimitPercent: 0,
    memoryLimitPercent: 0,
  }

  interface Props {
    session: ClusterSession
  }

  let { session }: Props = $props()

  /** The same denominator the pod list is set to. See NamespacesView. */
  const byLimit = $derived(preferences.podMeasure === 'limits')

  const isCronJob = $derived(session.selectedKindId === 'batch/v1/cronjobs')

  const columns = $derived<Column[]>([
    { id: 'status', label: 'Status', width: 44, icon: CircleDot },
    { id: 'name', label: 'Name', width: 300, pinned: true },
    { id: 'namespace', label: 'Namespace', width: 150 },
    ...(isCronJob
      ? [
          { id: 'schedule', label: 'Schedule', width: 140 },
          { id: 'lastRun', label: 'Last run', width: 190 },
        ]
      : [
          { id: 'ready', label: 'Ready', width: 90, numeric: true },
          { id: 'updated', label: 'Up-to-date', width: 116, numeric: true },
          { id: 'available', label: 'Available', width: 110, numeric: true, defaultHidden: true },
        ]),
    // The same meters the pod and namespace lists draw. A controller has no
    // usage of its own — the figures are the sum over the pods it currently
    // has — which is why they arrive a beat after the rows and are allowed to
    // be absent entirely on a cluster with no metrics API.
    { id: 'cpu', label: 'CPU', width: 220, minWidth: 200 },
    { id: 'memory', label: 'Memory', width: 220, minWidth: 200 },
    { id: 'images', label: 'Images', width: 300 },
    // Hidden until asked for. A cluster with no GitOps controller would
    // otherwise carry an always-empty column, and one with a controller
    // everywhere would carry a column reading the same word on every row —
    // useful when you go looking for it, noise the rest of the time.
    { id: 'gitops', label: 'GitOps', width: 150, defaultHidden: true },
    { id: 'controlledBy', label: 'Controlled By', width: 200, defaultHidden: true },
    { id: 'age', label: 'Age', width: 80, numeric: true },
    // The operator's own, after the built-in set — see $lib/customColumns.
    ...toColumns(session.customColumns),
  ])

  function tone(workload: Workload): Tone {
    if (workload.suspended) return 'neutral'
    if (!workload.isHealthy) return workload.readyCount === 0 ? 'error' : 'warning'
    if (workload.isRolling) return 'info'
    return 'success'
  }

  /**
   * The kind is the SAME for every row of this table — WorkloadsView shows
   * one kind at a time — so it is computed once rather than re-derived per
   * row.
   */
  const resource = $derived(session.selectedKind ? resourceArgForKind(session.selectedKind) : null)

  function actionsFor(workload: Workload): RowAction[] {
    if (!resource) return []
    return [
      {
        label: 'Copy as kubectl',
        kind: 'copy',
        onclick: () =>
          copyKubectl(kubectlGet(session.cluster.id, resource, workload.name, workload.namespace)),
      },
    ]
  }

  function copyKubectl(command: string): void {
    void navigator.clipboard?.writeText(command).catch(() => {})
  }

  /** Same rule ColumnMenu and DataTable apply — see PodsView for why it is
      repeated here rather than asked of either. */
  function isColumnVisible(column: Column): boolean {
    const stored = preferences.columns[session.selectedKindId]?.[column.id]?.hidden
    return column.pinned || (stored === undefined ? !column.defaultHidden : !stored)
  }

  /** The controller list's CSV export. Every field is the text its own cell
      shows — the meter columns export the aggregated usage with its unit,
      not the bare percentage, exactly as WorkloadsView derives it for the
      row. */
  function exportCSV(): CSVExport {
    const visible = columns.filter(isColumnVisible)

    function cell(workload: Workload, id: string): string {
      const custom = parseCustomColumnId(id)
      if (custom) return customCell(workload, custom)
      const usage = session.workloadUsage[`${workload.namespace}/${workload.name}`] ?? UNMEASURED
      switch (id) {
        case 'status':
          return workload.status
        case 'name':
          return workload.name
        case 'namespace':
          return workload.namespace
        case 'schedule':
          return workload.schedule || '—'
        case 'lastRun':
          return workload.lastScheduled ? new Date(workload.lastScheduled).toLocaleString() : 'never'
        case 'ready':
          return workload.ready
        case 'updated':
          return String(workload.updated)
        case 'available':
          return String(workload.available)
        case 'cpu':
          return usage.hasMetrics ? usage.cpu : '—'
        case 'memory':
          return usage.hasMetrics ? usage.memory : '—'
        case 'images':
          return workload.images.join(', ') || '—'
        case 'gitops': {
          const owner = gitOpsOwner({
            metadata: { labels: workload.labels, annotations: workload.annotations },
          })
          if (!owner) return '—'
          return owner.source ? `${owner.label}/${owner.source}` : owner.label
        }
        case 'controlledBy':
          return workload.controlledBy || '—'
        case 'age':
          return formatAge(workload.ageSeconds)
        default:
          return ''
      }
    }

    return {
      columns: visible.map((column) => column.label),
      rows: session.sortedWorkloads.map((workload) => visible.map((column) => cell(workload, column.id))),
    }
  }
</script>

<DataTable
  kindId={session.selectedKindId}
  {columns}
  isEmpty={session.pagedWorkloads.length === 0}
  sort={session.sort}
  onsort={session.toggleSort}
  exportRows={exportCSV}
>
  {#snippet empty()}
    <EmptyState
      title="Nothing here"
      description={session.search
        ? `Nothing matches "${session.search}".`
        : `No ${session.selectedKind?.title.toLowerCase() ?? 'workloads'} in this namespace.`}
    />
  {/snippet}

  {#snippet rows(isVisible)}
    {#each session.pagedWorkloads as workload (workload.namespace + '/' + workload.name)}
      {@const selected =
        session.selectedName === workload.name && session.selectedNamespace === workload.namespace}
      <!--
        Absent until the sums arrive, and absent for good on a cluster with no
        metrics API or an account that cannot list pods. UNMEASURED, not
        empty: a controller measured at zero and one nobody could measure are
        different facts, and the meter draws them differently.
      -->
      {@const usage =
        session.workloadUsage[`${workload.namespace}/${workload.name}`] ?? UNMEASURED}
      <tr
        class="group/row cursor-pointer border-t border-outline-variant/25 transition-colors duration-75
               {selected ? 'bg-primary/8' : 'hover:bg-surface-container-low'}"
        onclick={() => session.openDetail(workload.name, workload.namespace, undefined, workload)}
      >
        {#if isVisible('status')}
          <td class="overflow-hidden py-1.5 pr-3 pl-5">
            <StatusIndicator
              tone={tone(workload)}
              label={workload.status}
              icon={iconForKind({ kind: workload.kind })}
              pulse={workload.isRolling}
            />
          </td>
        {/if}
        <td class="px-3 py-1.5" title={workload.name}>
          <span class="flex items-center gap-2">
            <span class="truncate font-medium text-on-surface">{workload.name}</span>
          </span>
        </td>
        {#if isVisible('namespace')}
          <td class="truncate px-3 py-1.5">
            <span class="rounded bg-surface-container-high px-1.5 py-0.5 text-body-small text-on-surface-variant">
              {workload.namespace}
            </span>
          </td>
        {/if}
        {#if isVisible('schedule')}
          <td class="truncate px-3 py-1.5 font-mono text-body-medium text-on-surface-variant">{workload.schedule || '—'}</td>
        {/if}
        {#if isVisible('lastRun')}
          <td class="truncate px-3 py-1.5 text-on-surface-variant">
            {workload.lastScheduled ? new Date(workload.lastScheduled).toLocaleString() : 'never'}
          </td>
        {/if}
        {#if isVisible('ready')}
          <td class="truncate px-3 py-1.5 text-right tabular-nums {workload.isHealthy ? 'text-on-surface-variant' : 'text-warning font-medium'}">
            {workload.ready}
          </td>
        {/if}
        {#if isVisible('updated')}
          <td class="truncate px-3 py-1.5 text-right tabular-nums text-on-surface-variant">{workload.updated}</td>
        {/if}
        {#if isVisible('available')}
          <td class="truncate px-3 py-1.5 text-right tabular-nums text-on-surface-variant">{workload.available}</td>
        {/if}
        {#if isVisible('cpu')}
          {@const cpu = cpuMeter(usage, byLimit)}
          <td class="overflow-hidden px-3 py-1.5 text-on-surface-variant">
            <MeterBar
              label={usage.hasMetrics ? usage.cpu : '—'}
              scope="pods"
              name="CPU"
              valueWidth="7ch"
              percent={cpu.percent}
              measured={usage.hasMetrics}
              thresholds={cpu.thresholds}
              absent={cpu.absent}
              severity={cpu.severity}
              title={cpuTitle(usage)}
            />
          </td>
        {/if}
        {#if isVisible('memory')}
          {@const memory = memoryMeter(usage, byLimit)}
          <td class="overflow-hidden px-3 py-1.5 text-on-surface-variant">
            <MeterBar
              label={usage.hasMetrics ? usage.memory : '—'}
              scope="pods"
              name="Memory"
              percent={memory.percent}
              measured={usage.hasMetrics}
              thresholds={memory.thresholds}
              absent={memory.absent}
              severity={memory.severity}
              title={memoryTitle(usage)}
            />
          </td>
        {/if}
        {#if isVisible('images')}
          <td class="truncate px-3 py-1.5" title={workload.images.join(', ')}>
            <span class="flex items-center gap-1.5 text-body-medium text-on-surface-variant">
              <Container class="size-3.5 shrink-0 text-on-surface-variant/40" strokeWidth={1.5} />
              <span class="truncate">{workload.images.join(', ') || '—'}</span>
            </span>
          </td>
        {/if}
        {#if isVisible('gitops')}
          {@const owner = gitOpsOwner({
            metadata: { labels: workload.labels, annotations: workload.annotations },
          })}
          <td class="truncate px-3 py-1.5">
            {#if owner}
              <GitOpsBadge {owner} />
            {:else}
              <span class="text-on-surface-variant/40">—</span>
            {/if}
          </td>
        {/if}
        {#if isVisible('controlledBy')}
          <td class="truncate px-3 py-1.5 text-on-surface-variant">{workload.controlledBy || '—'}</td>
        {/if}
        {#if isVisible('age')}
          <td class="truncate px-3 py-1.5 text-right tabular-nums text-on-surface-variant">
            {formatAge(workload.ageSeconds)}
          </td>
        {/if}
        <CustomCells specs={session.customColumns} row={workload} {isVisible} />
        <!-- Stops the click here: the row itself opens the detail drawer,
             and a click aimed at the menu — or at one of its items — must
             not also do that. -->
        <td class="px-2" onclick={(event) => event.stopPropagation()}>
          <div class="flex justify-end">
            <RowMenu actions={actionsFor(workload)} label={workload.name} />
          </div>
        </td>
      </tr>
    {/each}
  {/snippet}
</DataTable>
