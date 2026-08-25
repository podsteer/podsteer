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
  import type { ClusterSession } from '$stores/session.svelte'

  interface Props {
    session: ClusterSession
  }

  let { session }: Props = $props()

  /**
   * When first, message last.
   *
   * "When did this happen" is the first question asked of an event and the
   * column answering it used to be the last one, which on any ordinary window
   * put it past the right edge — the widths totalled more than the table had,
   * so the one figure that dates the row was reachable only by scrolling.
   *
   * Message goes last for the opposite reason: it is the longest and the only
   * one that loses nothing by being truncated, since the full text is in the
   * tooltip and the row opens onto it.
   */
  const COLUMNS: Column[] = [
    { id: 'age', label: 'Last seen', width: 110, numeric: true },
    { id: 'type', label: 'Type', width: 110 },
    { id: 'reason', label: 'Reason', width: 190, pinned: true },
    { id: 'object', label: 'Object', width: 280 },
    { id: 'namespace', label: 'Namespace', width: 150 },
    { id: 'count', label: 'Count', width: 80, numeric: true },
    { id: 'message', label: 'Message', width: 460 },
    { id: 'source', label: 'Source', width: 150, defaultHidden: true },
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
        {#if isVisible('age')}
          <td class="truncate py-1.5 pr-3 pl-6 text-right tabular-nums text-on-surface-variant">
            {formatAge(event.ageSeconds)}
          </td>
        {/if}
        {#if isVisible('type')}
          <td class="overflow-hidden px-3 py-1.5">
            <StatusIndicator tone={event.isWarning ? 'warning' : 'neutral'} label={event.type} />
          </td>
        {/if}
        <td class="truncate px-3 py-1.5 text-on-surface">{event.reason}</td>
        {#if isVisible('object')}
          <td class="truncate px-3 py-1.5 text-on-surface-variant" title={event.involvedObject}>
            {event.involvedObject}
          </td>
        {/if}
        {#if isVisible('namespace')}
          <td class="truncate px-3 py-1.5 text-on-surface-variant">{event.namespace}</td>
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
        {#if isVisible('message')}
          <td class="truncate px-3 py-1.5 text-on-surface-variant" title={event.message} data-selectable>
            {event.message}
          </td>
        {/if}
        {#if isVisible('source')}
          <td class="truncate px-3 py-1.5 text-on-surface-variant">{event.source || '—'}</td>
        {/if}
        <td></td>
      </tr>
    {/each}
  {/snippet}
</DataTable>
