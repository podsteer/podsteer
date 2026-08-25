<!--
  The event list.

  Warnings are floated to the top by the backend, because a cluster emits
  thousands of routine Normal events an hour and a strictly chronological list
  buries the one BackOff that explains everything.
-->
<script lang="ts">
  import DataTable, { type Column } from '$lib/components/DataTable.svelte'
  import StatusIndicator from '$lib/components/StatusIndicator.svelte'
  import EmptyState from '$lib/components/EmptyState.svelte'
  import { formatAge } from '$lib/format'
  import { Activity, CircleDot } from '@lucide/svelte'
  import type { ClusterSession } from '$stores/session.svelte'

  interface Props {
    session: ClusterSession
  }

  let { session }: Props = $props()

  const COLUMNS: Column[] = [
    { id: 'type', label: 'Type', width: 56, icon: CircleDot },
    { id: 'reason', label: 'Reason', width: 200, pinned: true },
    { id: 'object', label: 'Object', width: 300 },
    { id: 'namespace', label: 'Namespace', width: 160 },
    { id: 'message', label: 'Message', width: 520 },
    { id: 'source', label: 'Source', width: 150, defaultHidden: true },
    { id: 'count', label: 'Count', width: 96, numeric: true },
    { id: 'age', label: 'Last seen', width: 116, numeric: true },
  ]
</script>

<DataTable
  kindId={session.selectedKindId}
  columns={COLUMNS}
  isEmpty={session.pagedEvents.length === 0}
  sort={session.sort}
  onsort={session.toggleSort}
>
  {#snippet empty()}
    <EmptyState
      title="No events"
      description={session.search
        ? `Nothing matches "${session.search}".`
        : 'Nothing has happened here recently. Kubernetes discards events after about an hour.'}
    />
  {/snippet}

  {#snippet rows(isVisible)}
    {#each session.pagedEvents as event (event.namespace + '/' + event.name)}
      <tr class="border-t border-outline-variant/40 hover:bg-surface-container-low">
        {#if isVisible('type')}
          <td class="overflow-hidden py-1.5 pr-3 pl-6">
            <StatusIndicator
              tone={event.isWarning ? 'warning' : 'neutral'}
              label={event.type}
              icon={Activity}
            />
          </td>
        {/if}
        <!-- The kind's own icon, as the node list puts a server in front of
             a node. It marks where the row's identity begins, which matters
             more here than elsewhere: the reason is the only column that is
             the event's own name rather than something it points at. -->
        <td class="px-3 py-1.5" title={event.reason}>
          <span class="flex items-center gap-2">
            <span class="truncate font-medium text-on-surface">{event.reason}</span>
          </span>
        </td>
        {#if isVisible('object')}
          <td class="truncate px-3 py-1.5 text-on-surface-variant" title={event.involvedObject}>
            {event.involvedObject}
          </td>
        {/if}
        {#if isVisible('namespace')}
          <td class="truncate px-3 py-1.5 text-on-surface-variant">{event.namespace}</td>
        {/if}
        <!-- In the order the header declares them. Reverting my own column
             reordering left these two the other way round, so every message
             was rendered under "Count" and every count under "Message". -->
        {#if isVisible('message')}
          <td class="truncate px-3 py-1.5 text-on-surface-variant" title={event.message} data-selectable>
            {event.message}
          </td>
        {/if}
        {#if isVisible('source')}
          <td class="truncate px-3 py-1.5 text-on-surface-variant">{event.source || '—'}</td>
        {/if}
        {#if isVisible('count')}
          <!-- Plain. A count is how often something happened, not how bad it
               is: a CronJob that has successfully created three thousand jobs
               was reading as an alarm, while a Warning that fired once read
               as calm. The Type column is what says which is which. -->
          <td class="truncate px-3 py-1.5 text-right tabular-nums text-on-surface-variant">
            {event.count}
          </td>
        {/if}
        {#if isVisible('age')}
          <td class="truncate px-3 py-1.5 text-right tabular-nums text-on-surface-variant">
            {formatAge(event.ageSeconds)}
          </td>
        {/if}
        <td></td>
      </tr>
    {/each}
  {/snippet}
</DataTable>
