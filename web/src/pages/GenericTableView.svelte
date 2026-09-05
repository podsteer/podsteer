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
  import RowMenu, { type RowAction } from '$lib/components/RowMenu.svelte'
  import CustomCells from '$lib/components/CustomCells.svelte'
  import { customCell, parseCustomColumnId, toColumns } from '$lib/customColumns'
  import RowSelect from '$lib/components/RowSelect.svelte'
  import { rowKey } from '$lib/bulk'
  import { iconForKind } from '$lib/kindIcons'
  import { get as kubectlGet, resourceArgForKind } from '$lib/kubectl'
  import { preferences } from '$stores/preferences.svelte'
  import { CircleDot } from '@lucide/svelte'
  import type { ClusterSession } from '$stores/session.svelte'
  import type { TableRow } from '$lib/api/client'

  interface Props {
    session: ClusterSession
  }

  let { session }: Props = $props()

  /**
   * The kind is the SAME for every row of this table — one kind is browsed
   * at a time — so it is computed once rather than re-derived per row.
   */
  const resource = $derived(session.selectedKind ? resourceArgForKind(session.selectedKind) : null)

  /**
   * Absent for a row with no name — the header-ish placeholder rows the
   * server's own table printer occasionally sends, which the click handler
   * already treats as unopenable. There is no object to name a command for.
   */
  function actionsFor(row: TableRow): RowAction[] {
    if (!resource || !row.name) return []
    const namespace = session.selectedKind?.namespaced ? row.namespace : undefined
    return [
      {
        label: 'Copy as kubectl',
        kind: 'copy',
        onclick: () => copyKubectl(kubectlGet(session.cluster.id, resource, row.name, namespace)),
      },
    ]
  }

  function copyKubectl(command: string): void {
    void navigator.clipboard?.writeText(command).catch(() => {})
  }

  const table = $derived(session.table)

  /** A row's selection key: namespace-qualified only for a namespaced kind. */
  function keyOf(row: TableRow): string {
    return rowKey(session.selectedKind?.namespaced ? row.namespace : '', row.name)
  }

  /** The rows on screen, in display order, for range and select-all — see
      PodsView. Nameless placeholder rows have nothing to select. */
  $effect(() => {
    session.selection.visible = session.pagedTableRows.filter((row) => row.name).map(keyOf)
    return () => {
      session.selection.visible = []
    }
  })

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
  /**
   * The operator's own columns, after everything the server printed. They
   * read the labels and annotations the server attaches to each row's
   * metadata — see $lib/customColumns — which is what lets a CRD nobody
   * wrote code for grow a `team` column exactly as a Deployment can.
   */
  const custom = $derived<Column[]>(toColumns(session.customColumns))

  const columns = $derived<Column[]>([
    { id: 'select', label: 'Select', width: 40, pinned: true, select: true },
    ...(KindIcon ? [{ id: 'kind', label: 'Kind', width: 44, icon: CircleDot, pinned: true }] : []),
    ...printed,
    ...custom,
  ])

  /** Same rule ColumnMenu and DataTable apply — see PodsView for why it is
      repeated here rather than asked of either. */
  function isColumnVisible(column: Column): boolean {
    const stored = preferences.columns[session.selectedKindId]?.[column.id]?.hidden
    return column.pinned || (stored === undefined ? !column.defaultHidden : !stored)
  }

  /**
   * The generic table's CSV export: the server's own printed column names,
   * exactly as it named them, then the operator's own — not the icon column,
   * which carries no text of its own, only a mark this view drew in front of
   * the kind's rows.
   */
  function exportCSV(): CSVExport {
    const visible = [...printed, ...custom].filter(isColumnVisible)

    function cell(row: TableRow, id: string): string {
      const spec = parseCustomColumnId(id)
      if (spec) return customCell(row, spec)
      return row.cells?.[Number(id.slice(1))] ?? ''
    }

    return {
      columns: visible.map((column) => column.label),
      rows: session.sortedTableRows.map((row) => visible.map((column) => cell(row, column.id))),
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
  selectAll={{
    checked: session.selection.allVisibleSelected,
    indeterminate: session.selection.someVisibleSelected,
    ontoggle: () => session.selection.toggleAllVisible(),
  }}
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
      {@const key = keyOf(row)}
      {@const ticked = !!row.name && session.selection.has(key)}
      <tr
        class="group/row border-t border-outline-variant/40 transition-colors duration-100
               {row.name ? 'cursor-pointer' : ''}
               {selected ? 'bg-secondary-container/40' : ticked ? 'bg-primary/5' : 'hover:bg-surface-container-low'}"
        aria-selected={ticked}
        onclick={() => row.name && session.openDetail(row.name, row.namespace)}
      >
        {#if row.name}
          <RowSelect
            selected={ticked}
            label={row.name}
            ontoggle={(range) => session.selection.toggle(key, range)}
          />
        {:else}
          <td></td>
        {/if}
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
              title={row.cells?.[index]}
            >
              {row.cells?.[index]}
            </td>
          {/if}
        {/each}
        <CustomCells specs={session.customColumns} {row} {isVisible} />
        <!-- Stops the click here: the row itself opens the detail drawer,
             and a click aimed at the menu — or at one of its items — must
             not also do that. -->
        <td class="px-2" onclick={(event) => event.stopPropagation()}>
          {#if row.name}
            <div class="flex justify-end">
              <RowMenu actions={actionsFor(row)} label={row.name} />
            </div>
          {/if}
        </td>
      </tr>
    {/each}
  {/snippet}
</DataTable>
