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
  import DataTable, { ROW_MENU_COLUMN, type Column } from '$lib/components/DataTable.svelte'
  import type { CSVExport } from '$stores/activeTable.svelte'
  import EmptyState from '$lib/components/EmptyState.svelte'
  import { type RowAction } from '$lib/components/RowMenu.svelte'
  import RowMenuCell from '$lib/components/RowMenuCell.svelte'
  import { copyText } from '$lib/clipboard'
  import { rowActionsFor, toRowActions } from '$lib/rowActions'
  import { organisation } from '$stores/organisation.svelte'
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

  /** See PodsView: read fresh so a change in Organise applies at once. */
  const placement = $derived(organisation.placementOf(session.cluster.id))
  const isReadOnly = $derived(
    organisation.settingsFor(placement.project, placement.group).readOnly,
  )

  /**
   * Absent for a row with no name — the header-ish placeholder rows the
   * server's own table printer occasionally sends, which the click handler
   * already treats as unopenable. There is no object to name a command for.
   */
  function actionsFor(row: TableRow): RowAction[] {
    if (!resource || !row.name) return []
    const namespace = session.selectedKind?.namespaced ? row.namespace : undefined

    // Delete and the kubectl copy, whatever the kind — the two verbs a
    // server-printed row supports. Delete opens the object with the drawer's
    // own DeleteDialog engaged, which is what types the object's name on a
    // production cluster; nothing is confirmed here.
    return toRowActions(
      rowActionsFor(session.selectedKind?.kind ?? ''),
      {
        overview: () =>
          void session.openDetailFor({ tab: 'overview' }, row.name, namespace ?? ''),
        delete: () =>
          void session.openDetailFor({ action: 'delete' }, row.name, namespace ?? ''),
        kubectl: () => copyText(kubectlGet(session.cluster.id, resource, row.name, namespace)),
      },
      isReadOnly,
    )
  }

  const table = $derived(session.table)

  /** The cap that stopped the read, grouped for reading. */
  const cap = $derived((table?.cap ?? 0).toLocaleString())

  /** This kind in the plural, lowercased, for a sentence. */
  const kindLabel = $derived(session.selectedKind?.title.toLowerCase() ?? 'objects')

  /**
   * Why the table is empty, in words that say which of the reasons.
   *
   * A TRUNCATED LIST CHANGES WHAT "nothing matches" MEANS. The search runs
   * over the rows that arrived, so on a capped read a match sitting past the
   * cut is reported as an absence — the one case where the honest answer is
   * that PodSteer does not know.
   */
  function emptyDescription(): string {
    if (session.search) {
      return table?.truncated
        ? `Nothing in the first ${cap} ${kindLabel} matches "${session.search}" — there are more that were not read.`
        : `Nothing matches "${session.search}".`
    }
    return `No ${kindLabel} in this namespace.`
  }

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
    // The row menu last, because it is the end of the row.
    ROW_MENU_COLUMN,
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
  {#snippet notice()}
    {#if table?.truncated}
      <!--
        THE ONE THING THIS LIST CANNOT LEAVE UNSAID. A capped read comes back
        looking exactly like a complete one, so every question answered below
        it is wrong in the same silent direction: the search misses a match
        past the cut, the sort names the wrong newest, and the count is a
        floor shown as a total. Outside the scrolling region — see DataTable's
        `notice` — because a caveat that scrolls away from the rows it
        qualifies is not a caveat.
      -->
      <p
        class="border-b border-outline-variant/60 px-3 py-2 text-body-medium text-gauge-warn"
        role="status"
      >
        More than {cap} {kindLabel}. PodSteer listed the first {cap} and stopped, so the
        search, the sort and the count below describe those {cap} only. Narrow the namespace,
        or use a terminal for the whole set.
      </p>
    {/if}
  {/snippet}

  {#snippet empty()}
    <EmptyState
      title="Nothing here"
      description={emptyDescription()}
    />
  {/snippet}

  {#snippet rows(isVisible)}
    {#each session.pagedTableRows as row, rowIndex (row.namespace + '/' + row.name + rowIndex)}
      {@const selected =
        session.selectedName === row.name && session.selectedNamespace === row.namespace}
      {@const key = keyOf(row)}
      {@const ticked = !!row.name && session.selection.has(key)}
      <!-- The state grounds are OPAQUE tokens rather than the translucent
           tints they used to be: the pinned columns inherit this row's own
           colour, and a translucent one lets the cells scrolling underneath
           show through them. See the row grounds in app.css. -->
      <tr
        class="group/row border-t border-outline-variant/40 transition-colors duration-100
               {row.name ? 'cursor-pointer' : ''}
               {selected
          ? 'bg-row-open-secondary'
          : ticked
            ? 'bg-row-ticked'
            : 'bg-surface hover:bg-surface-container-low'}"
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
          <!-- A row the server printed with no name has nothing to tick, but
               it still needs a cell in the selection COLUMN — and that cell
               has to be pinned like every other one, or the columns scrolling
               past show through this row alone while the rows above and below
               it stay covered. -->
          <td data-edge="select"></td>
        {/if}
        {#if KindIcon && isVisible('kind')}
          <td class="py-1.5 pr-3 pl-5">
            <!-- BLOCK-LEVEL FLEX, the same rule StatusIndicator follows and for
                 the same reason: an inline box puts the icon on the row's
                 baseline, and an SVG is a replaced element whose baseline is
                 synthesised at its bottom edge, so it rides high against the
                 text beside it. This view is the one that draws its own icon
                 rather than using StatusIndicator, which is why it was the one
                 left behind — and why every kind without a purpose-built view
                 (Autoscalers, Disruption Budgets, and the whole of Config,
                 Network, Storage, Access Control and Custom Resources) was
                 misaligned while Pods, Workloads and Nodes were not. -->
            <span class="flex" title={session.selectedKind?.singular}>
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
        <!-- A nameless row offers no actions, so the cell is drawn and the
             control is not — see RowMenu, which renders nothing for an empty
             list. The CELL still has to be there: it is a column now. -->
        <RowMenuCell actions={row.name ? actionsFor(row) : []} label={row.name} />
      </tr>
    {/each}
  {/snippet}
</DataTable>
