<!--
  The controller list, shared by all six workload kinds.

  One view rather than six because the question is the same in every case: how
  many replicas should be running, how many are, and is a rollout in progress.
  CronJobs are the exception and get their schedule columns instead.
-->
<script lang="ts">
  import DataTable, { type Column } from '$lib/components/DataTable.svelte'
  import StatusIndicator from '$lib/components/StatusIndicator.svelte'
  import EmptyState from '$lib/components/EmptyState.svelte'
  import { formatAge } from '$lib/format'
  import type { Tone } from '$lib/format'
  import type { ClusterSession } from '$stores/session.svelte'
  import type { Workload } from '$lib/api/client'
  import { Container, Layers, CircleDot } from '@lucide/svelte'

  interface Props {
    session: ClusterSession
  }

  let { session }: Props = $props()

  const isCronJob = $derived(session.selectedKindId === 'batch/v1/cronjobs')

  const columns = $derived<Column[]>([
    { id: 'status', label: 'Status', width: 56, icon: CircleDot },
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
    { id: 'images', label: 'Images', width: 300 },
    { id: 'controlledBy', label: 'Controlled By', width: 200, defaultHidden: true },
    { id: 'age', label: 'Age', width: 80, numeric: true },
  ])

  function tone(workload: Workload): Tone {
    if (workload.suspended) return 'neutral'
    if (!workload.isHealthy) return workload.readyCount === 0 ? 'error' : 'warning'
    if (workload.isRolling) return 'info'
    return 'success'
  }
</script>

<DataTable
  kindId={session.selectedKindId}
  {columns}
  isEmpty={session.pagedWorkloads.length === 0}
  sort={session.sort}
  onsort={session.toggleSort}
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
      <tr
        class="group cursor-pointer border-t border-outline-variant/25 transition-colors duration-75
               {selected ? 'bg-primary/8' : 'hover:bg-surface-container-low'}"
        onclick={() => session.openDetail(workload.name, workload.namespace, undefined, workload)}
      >
        {#if isVisible('status')}
          <td class="overflow-hidden py-1.5 pr-3 pl-5">
            <StatusIndicator
              tone={tone(workload)}
              label={workload.status}
              icon={Layers}
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
        {#if isVisible('images')}
          <td class="truncate px-3 py-1.5" title={workload.images.join(', ')}>
            <span class="flex items-center gap-1.5 text-body-medium text-on-surface-variant">
              <Container class="size-3.5 shrink-0 text-on-surface-variant/40" strokeWidth={1.5} />
              <span class="truncate">{workload.images.join(', ') || '—'}</span>
            </span>
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
        <td></td>
      </tr>
    {/each}
  {/snippet}
</DataTable>
