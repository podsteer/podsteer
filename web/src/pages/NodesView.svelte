<!--
  The node list.

  Usage is shown against ALLOCATABLE rather than capacity: allocatable is what
  the scheduler actually has to give out, so a node at 90% allocatable is full
  even though capacity says otherwise.
-->
<script lang="ts">
  import DataTable, { type Column } from '$lib/components/DataTable.svelte'
  import StatusIndicator from '$lib/components/StatusIndicator.svelte'
  import MeterBar from '$lib/components/MeterBar.svelte'
  import EmptyState from '$lib/components/EmptyState.svelte'
  import { formatAge } from '$lib/format'
  import type { ClusterSession } from '$stores/session.svelte'
  import { Server, CircleDot } from '@lucide/svelte'

  interface Props {
    session: ClusterSession
  }

  let { session }: Props = $props()

  const COLUMNS: Column[] = [
    { id: 'status', label: 'Status', width: 56, icon: CircleDot },
    { id: 'name', label: 'Name', width: 300, pinned: true },
    { id: 'roles', label: 'Roles', width: 140 },
    { id: 'cpu', label: 'CPU', width: 200 },
    { id: 'memory', label: 'Memory', width: 200 },
    { id: 'version', label: 'Version', width: 110 },
    { id: 'ip', label: 'Internal IP', width: 130, defaultHidden: true },
    { id: 'os', label: 'OS', width: 180, defaultHidden: true },
    { id: 'pods', label: 'Max pods', width: 100, numeric: true, defaultHidden: true },
    { id: 'taints', label: 'Taints', width: 80, numeric: true },
    { id: 'age', label: 'Age', width: 80, numeric: true },
  ]
</script>

<DataTable
  kindId={session.selectedKindId}
  columns={COLUMNS}
  isEmpty={session.pagedNodes.length === 0}
  sort={session.sort}
  onsort={session.toggleSort}
>
  {#snippet empty()}
    <EmptyState title="No nodes" description="This cluster reports no nodes you can see." />
  {/snippet}

  {#snippet rows(isVisible)}
    {#each session.pagedNodes as node (node.name)}
      {@const selected = session.selectedName === node.name}
      <tr
        class="group cursor-pointer border-t border-outline-variant/25 transition-colors duration-75
               {selected ? 'bg-primary/8' : 'hover:bg-surface-container-low'}"
        onclick={() => session.openDetail(node.name, '')}
      >
        {#if isVisible('status')}
          <td class="overflow-hidden py-1.5 pr-3 pl-5">
            <StatusIndicator
              tone={node.isHealthy ? (node.unschedulable ? 'warning' : 'success') : 'error'}
              label={node.status}
              icon={Server}
            />
          </td>
        {/if}
        <td class="px-3 py-1.5" title={node.name}>
          <span class="flex items-center gap-2">
            <span class="truncate font-medium text-on-surface">{node.name}</span>
          </span>
        </td>
        {#if isVisible('roles')}
          <td class="truncate px-3 py-1.5">
            {#if node.roles.length}
              <span class="rounded bg-surface-container-high px-1.5 py-0.5 text-body-small text-on-surface-variant">
                {node.roles.join(', ')}
              </span>
            {:else}
              <span class="rounded bg-surface-container px-1.5 py-0.5 text-body-small text-on-surface-variant/60">
                worker
              </span>
            {/if}
          </td>
        {/if}
        {#if isVisible('cpu')}
          <td class="overflow-hidden px-3 py-1.5 text-on-surface-variant">
            <MeterBar percent={node.cpuPercent} label={node.cpu} measured={node.hasMetrics} />
          </td>
        {/if}
        {#if isVisible('memory')}
          <td class="overflow-hidden px-3 py-1.5 text-on-surface-variant">
            <MeterBar percent={node.memoryPercent} label={node.memory} measured={node.hasMetrics} />
          </td>
        {/if}
        {#if isVisible('version')}
          <td class="truncate px-3 py-1.5 font-mono text-body-medium tabular-nums text-on-surface-variant">{node.version}</td>
        {/if}
        {#if isVisible('ip')}
          <td class="truncate px-3 py-1.5 font-mono text-body-medium text-on-surface-variant" data-selectable>
            {node.internalIp || '—'}
          </td>
        {/if}
        {#if isVisible('os')}
          <td class="truncate px-3 py-1.5 text-on-surface-variant" title={node.osImage}>
            {node.osImage || '—'}
          </td>
        {/if}
        {#if isVisible('pods')}
          <td class="truncate px-3 py-1.5 text-right tabular-nums text-on-surface-variant">
            {node.maxPods || '—'}
          </td>
        {/if}
        {#if isVisible('taints')}
          <td class="truncate px-3 py-1.5 text-right tabular-nums {node.taints > 0 ? 'text-warning font-medium' : 'text-on-surface-variant'}">
            {node.taints}
          </td>
        {/if}
        {#if isVisible('age')}
          <td class="truncate px-3 py-1.5 text-right tabular-nums text-on-surface-variant">
            {formatAge(node.ageSeconds)}
          </td>
        {/if}
        <td></td>
      </tr>
    {/each}
  {/snippet}
</DataTable>
