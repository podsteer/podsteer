<!--
  The generic table: every kind PodSteer has no purpose-built view for,
  including every CRD in the cluster.

  Columns come from the API server's own table printer — the same mechanism
  behind `kubectl get` output — so a freshly installed operator's resources
  render correctly with no code here knowing anything about them.

  The server marks its extended columns (the ones `kubectl get -o wide` adds)
  as secondary; those become `defaultHidden`, so the common case stays readable
  and the column menu still offers them.
-->
<script lang="ts">
  import DataTable, { type Column } from '$lib/components/DataTable.svelte'
  import EmptyState from '$lib/components/EmptyState.svelte'
  import type { ClusterSession } from '$stores/session.svelte'

  interface Props {
    session: ClusterSession
  }

  let { session }: Props = $props()

  const table = $derived(session.table)

  /**
   * Column ids are positional ("c0", "c1"), because a server printer gives no
   * stable identifier and two CRDs routinely both print a column called
   * "Status". Position is stable for a given kind, which is the scope column
   * preferences are stored at.
   */
  const columns = $derived<Column[]>(
    (table?.columns ?? []).map((column, index) => ({
      id: `c${index}`,
      label: column.name,
      width: index === 0 ? 320 : column.type === 'date' ? 100 : 170,
      numeric: column.type === 'integer' || column.type === 'number',
      pinned: index === 0,
      defaultHidden: column.wide,
    })),
  )
</script>

<DataTable
  kindId={session.selectedKindId}
  {columns}
  isEmpty={session.pagedTableRows.length === 0}
  sort={session.sort}
  onsort={session.toggleSort}
>
  {#snippet empty()}
    <EmptyState
      title="Nothing here"
      description={session.search
        ? `Nothing matches "${session.search}".`
        : `No ${session.selectedKind?.title.toLowerCase() ?? 'objects'} in this namespace.`}
    />
  {/snippet}

  {#snippet rows(isVisible)}
    {#each session.pagedTableRows as row, rowIndex (row.namespace + '/' + row.name + rowIndex)}
      {@const selected =
        session.selectedName === row.name && session.selectedNamespace === row.namespace}
      <tr
        class="border-t border-outline-variant/40 transition-colors duration-100
               {row.name ? 'cursor-pointer' : ''}
               {selected ? 'bg-secondary-container/40' : 'hover:bg-surface-container-low'}"
        onclick={() => row.name && session.openDetail(row.name, row.namespace)}
      >
        {#each columns as column, index (column.id)}
          {#if isVisible(column.id)}
            <td
              class="truncate py-1.5
                     {index === 0 ? 'pr-3 pl-6 text-on-surface' : 'px-3 text-on-surface-variant'}
                     {column.numeric ? 'text-right tabular-nums' : ''}"
              title={row.cells[index]}
            >
              {row.cells[index]}
            </td>
          {/if}
        {/each}
        <td></td>
      </tr>
    {/each}
  {/snippet}
</DataTable>
