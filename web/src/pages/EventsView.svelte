<!--
  The event list.

  Warnings are floated to the top by the backend, because a cluster emits
  thousands of routine Normal events an hour and a strictly chronological list
  buries the one BackOff that explains everything.
-->
<script lang="ts">
  import DataTable, { type Column } from '$lib/components/DataTable.svelte'
  import type { CSVExport } from '$stores/activeTable.svelte'
  import StatusIndicator from '$lib/components/StatusIndicator.svelte'
  import EmptyState from '$lib/components/EmptyState.svelte'
  import RowMenu, { type RowAction } from '$lib/components/RowMenu.svelte'
  import CustomCells from '$lib/components/CustomCells.svelte'
  import { customCell, parseCustomColumnId, toColumns } from '$lib/customColumns'
  import { formatAge } from '$lib/format'
  import { get as kubectlGet } from '$lib/kubectl'
  import { preferences } from '$stores/preferences.svelte'
  import { Activity, CircleDot } from '@lucide/svelte'
  import type { ClusterSession } from '$stores/session.svelte'
  import type { K8sEvent } from '$lib/api/client'

  interface Props {
    session: ClusterSession
  }

  let { session }: Props = $props()

  /** An Event is a core-group, namespaced kind — kubectl's own "events". */
  function actionsFor(event: K8sEvent): RowAction[] {
    return [
      {
        label: 'Copy as kubectl',
        kind: 'copy',
        onclick: () =>
          copyKubectl(kubectlGet(session.cluster.id, 'events', event.name, event.namespace)),
      },
    ]
  }

  function copyKubectl(command: string): void {
    void navigator.clipboard?.writeText(command).catch(() => {})
  }

  const COLUMNS: Column[] = [
    { id: 'type', label: 'Type', width: 44, icon: CircleDot },
    { id: 'reason', label: 'Reason', width: 200, pinned: true },
    { id: 'object', label: 'Object', width: 300 },
    { id: 'namespace', label: 'Namespace', width: 160 },
    { id: 'message', label: 'Message', width: 520 },
    { id: 'source', label: 'Source', width: 150, defaultHidden: true },
    { id: 'count', label: 'Count', width: 96, numeric: true },
    { id: 'age', label: 'Last seen', width: 116, numeric: true },
  ]

  /** The built-in columns, then the operator's own — see $lib/customColumns. */
  const columns = $derived<Column[]>([...COLUMNS, ...toColumns(session.customColumns)])

  /** Same rule ColumnMenu and DataTable apply — see PodsView for why it is
      repeated here rather than asked of either. */
  function isColumnVisible(column: Column): boolean {
    const stored = preferences.columns[session.selectedKindId]?.[column.id]?.hidden
    return column.pinned || (stored === undefined ? !column.defaultHidden : !stored)
  }

  /** The event list's CSV export, mirroring exactly what each cell shows. */
  function exportCSV(): CSVExport {
    const visible = columns.filter(isColumnVisible)

    function cell(event: K8sEvent, id: string): string {
      const custom = parseCustomColumnId(id)
      if (custom) return customCell(event, custom)
      switch (id) {
        case 'type':
          return event.type
        case 'reason':
          return event.reason
        case 'object':
          return event.involvedObject
        case 'namespace':
          return event.namespace
        case 'message':
          return event.message
        case 'source':
          return event.source || '—'
        case 'count':
          return String(event.count)
        case 'age':
          return formatAge(event.ageSeconds)
        default:
          return ''
      }
    }

    return {
      columns: visible.map((column) => column.label),
      rows: session.sortedEvents.map((event) => visible.map((column) => cell(event, column.id))),
    }
  }
</script>

<DataTable
  kindId={session.selectedKindId}
  {columns}
  isEmpty={session.pagedEvents.length === 0}
  sort={session.sort}
  onsort={session.toggleSort}
  exportRows={exportCSV}
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
      {@const selected =
        session.selectedName === event.name && session.selectedNamespace === event.namespace}
      <!-- Clickable like every other list. An event was the one row in the
           application that led nowhere, which left its message readable only
           as much of it as the column happened to fit. -->
      <tr
        class="group/row cursor-pointer border-t border-outline-variant/40 transition-colors duration-100
               {selected ? 'bg-secondary-container/40' : 'hover:bg-surface-container-low'}"
        onclick={() => session.openDetail(event.name, event.namespace)}
      >
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
        <CustomCells specs={session.customColumns} row={event} {isVisible} />
        <!-- Stops the click here: the row itself opens the detail drawer,
             and a click aimed at the menu — or at one of its items — must
             not also do that. Named domEvent, not event: `event` in this
             scope is already the row's own K8sEvent. -->
        <td class="px-2" onclick={(domEvent) => domEvent.stopPropagation()}>
          <div class="flex justify-end">
            <RowMenu actions={actionsFor(event)} label={event.reason} />
          </div>
        </td>
      </tr>
    {/each}
  {/snippet}
</DataTable>
