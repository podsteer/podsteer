<!--
  The pod list: the view an operator opens first and stares at longest.

  Every derived value here — the status word, the ready count, CPU and memory,
  the controller — was computed in Go. The table renders; it does not decide
  what "healthy" means.
-->
<script lang="ts">
  import DataTable, { type Column } from '$lib/components/DataTable.svelte'
  import StatusIndicator from '$lib/components/StatusIndicator.svelte'
  import EmptyState from '$lib/components/EmptyState.svelte'
  import { formatAge, podStatusLabel, podTone } from '$lib/format'
  import type { ClusterSession } from '$stores/session.svelte'
  import { Box } from '@lucide/svelte'

  interface Props {
    session: ClusterSession
  }

  let { session }: Props = $props()

  const COLUMNS: Column[] = [
    { id: 'status', label: 'Status', width: 160 },
    { id: 'name', label: 'Name', width: 320, pinned: true },
    { id: 'namespace', label: 'Namespace', width: 150 },
    { id: 'cpu', label: 'CPU', width: 80, numeric: true },
    { id: 'memory', label: 'Memory', width: 90, numeric: true },
    { id: 'ready', label: 'Ready', width: 80, numeric: true },
    { id: 'restarts', label: 'Restarts', width: 90, numeric: true },
    { id: 'controlledBy', label: 'Controlled By', width: 200 },
    { id: 'node', label: 'Node', width: 180 },
    { id: 'qos', label: 'QoS', width: 90 },
    { id: 'ip', label: 'IP', width: 120, defaultHidden: true },
    { id: 'age', label: 'Age', width: 80, numeric: true },
  ]
</script>

<DataTable
  kindId={session.selectedKindId}
  columns={COLUMNS}
  isEmpty={session.pagedPods.length === 0}
  sort={session.sort}
  onsort={session.toggleSort}
>
  {#snippet empty()}
    <EmptyState
      title="No pods here"
      description={session.search
        ? `Nothing matches "${session.search}".`
        : 'This namespace is not running any pods you can see.'}
    />
  {/snippet}

  {#snippet rows(isVisible)}
    {#each session.pagedPods as pod (pod.namespace + '/' + pod.name)}
      {@const selected =
        session.selectedName === pod.name && session.selectedNamespace === pod.namespace}
      <tr
        class="group cursor-pointer border-t border-outline-variant/25 transition-colors duration-75
               {selected ? 'bg-primary/8' : 'hover:bg-surface-container-low'}"
        onclick={() => session.openDetail(pod.name, pod.namespace, pod)}
      >
        {#if isVisible('status')}
          <td class="overflow-hidden py-1.5 pr-3 pl-5">
            <StatusIndicator
              tone={podTone(pod)}
              label={podStatusLabel(pod)}
              pulse={pod.phase === 'Terminating'}
            />
          </td>
        {/if}
        <td class="px-3 py-1.5" title={pod.name}>
          <span class="flex items-center gap-2">
            <Box class="size-3.5 shrink-0 text-on-surface-variant/40" strokeWidth={1.5} />
            <span class="truncate font-medium text-on-surface">{pod.name}</span>
          </span>
        </td>
        {#if isVisible('namespace')}
          <td class="truncate px-3 py-1.5">
            <span class="rounded bg-surface-container-high px-1.5 py-0.5 text-body-small text-on-surface-variant">
              {pod.namespace}
            </span>
          </td>
        {/if}
        {#if isVisible('cpu')}
          <td class="truncate px-3 py-1.5 text-right tabular-nums text-on-surface-variant">{pod.cpu}</td>
        {/if}
        {#if isVisible('memory')}
          <td class="truncate px-3 py-1.5 text-right tabular-nums text-on-surface-variant">{pod.memory}</td>
        {/if}
        {#if isVisible('ready')}
          <td
            class="truncate px-3 py-1.5 text-right tabular-nums
                   {pod.readyContainers === pod.totalContainers
                     ? 'text-on-surface-variant'
                     : 'text-warning font-medium'}"
          >
            {pod.ready}
          </td>
        {/if}
        {#if isVisible('restarts')}
          <td
            class="truncate px-3 py-1.5 text-right tabular-nums
                   {pod.restarts > 0 ? 'text-warning font-medium' : 'text-on-surface-variant'}"
          >
            {pod.restarts}
          </td>
        {/if}
        {#if isVisible('controlledBy')}
          <td class="truncate px-3 py-1.5 text-on-surface-variant" title={pod.controlledBy}>
            {pod.controlledBy || '—'}
          </td>
        {/if}
        {#if isVisible('node')}
          <td class="truncate px-3 py-1.5 text-on-surface-variant" title={pod.nodeName}>
            {pod.nodeName || '—'}
          </td>
        {/if}
        {#if isVisible('qos')}
          <td class="truncate px-3 py-1.5 text-on-surface-variant">{pod.qosClass || '—'}</td>
        {/if}
        {#if isVisible('ip')}
          <td class="truncate px-3 py-1.5 text-on-surface-variant" data-selectable>
            {pod.podIp || '—'}
          </td>
        {/if}
        {#if isVisible('age')}
          <td class="truncate px-3 py-1.5 text-right tabular-nums text-on-surface-variant">
            {formatAge(pod.ageSeconds)}
          </td>
        {/if}
        <td></td>
      </tr>
    {/each}
  {/snippet}
</DataTable>
