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
  import type { CSVExport } from '$stores/activeTable.svelte'
  import EmptyState from '$lib/components/EmptyState.svelte'
  import { iconForKind } from '$lib/kindIcons'
  import { preferences } from '$stores/preferences.svelte'
  import { CircleDot } from '@lucide/svelte'
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
  const printed = $derived<Column[]>(
    (table?.columns ?? []).map((column, index) => ({
      id: `c${index}`,
      label: column.name,
      width: index === 0 ? 320 : column.type === 'date' ? 100 : 170,
      numeric: column.type === 'integer' || column.type === 'number',
      pinned: index === 0,
      defaultHidden: column.wide,
    })),
  )

  /** The kind's own icon, so every list begins the way the built-in ones do. */
  const KindIcon = $derived(
    session.selectedKind ? iconForKind(session.selectedKind) : undefined,
  )

  /**
   * A leading icon column, ahead of whatever the server printed.
   *
   * Identity only, and deliberately not coloured: these rows come from the
   * API server's table printer, which reports whatever a CRD's author chose
   * to print and models no health at all. Tinting one would mean guessing at
   * a status from a column that happens to be called "Status", and a guess
   * dressed as a verdict is worse than no verdict.
   */
  const columns = $derived<Column[]>(
    KindIcon
      ? [{ id: 'kind', label: 'Kind', width: 44, icon: CircleDot, pinned: true }, ...printed]
      : printed,
  )

  /** Same rule ColumnMenu and DataTable apply — see PodsView for why it is
      repeated here rather than asked of either. */
  function isColumnVisible(column: Column): boolean {
    const stored = preferences.columns[session.selectedKindId]?.[column.id]?.hidden
    return column.pinned || (stored === undefined ? !column.defaultHidden : !stored)
  }

  /**
   * The generic table's CSV export: the server's own printed column names,
   * exactly as it named them — not the icon column, which carries no text of
   * its own, only a mark this view drew in front of the kind's rows.
   */
  function exportCSV(): CSVExport {
    const visible = printed.filter(isColumnVisible)
    const indices = visible.map((column) => Number(column.id.slice(1)))

    return {
      columns: visible.map((column) => column.label),
      rows: session.sortedTableRows.map((row) => indices.map((index) => row.cells[index] ?? '')),
    }
  }
</script>

<DataTable
  kindId={session.selectedKindId}
  {columns}
  isEmpty={session.pagedTableRows.length === 0}
  sort={session.sort}
  onsort={session.toggleSort}
  exportRows={exportCSV}
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
        {#if KindIcon && isVisible('kind')}
          <td class="py-1.5 pr-3 pl-5">
            <span class="inline-flex" title={session.selectedKind?.singular}>
              <KindIcon class="size-4 shrink-0 text-on-surface-variant/60" strokeWidth={1.75} />
            </span>
          </td>
        {/if}
        {#each printed as column, index (column.id)}
          {#if isVisible(column.id)}
            <td
              class="truncate py-1.5
                     {index === 0 ? 'pr-3 pl-3 text-on-surface' : 'px-3 text-on-surface-variant'}
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
