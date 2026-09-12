<!--
  Several kinds in one table.

  WHAT IT ANSWERS. "What does this application consist of" is a question about
  Deployments AND Services AND ConfigMaps at once, and a navigator that selects
  one kind at a time turns it into three visits and a join done in somebody's
  head. k9s answered the same request in 2024 (#771, 141 reactions — the
  largest measured demand anywhere in this category) and answered it inside one
  view rather than with more windows.

  INTERLEAVED, NOT GROUPED, WITH A KIND COLUMN THAT SORTS. Sorting by Age shows
  what changed recently across everything, which is the view grouping cannot
  give; sorting by Kind gives the grouped reading back on demand. One choice
  serves both readings, and the operator makes it.

  COLUMNS ARE MERGED BY NAME. A Deployment prints READY/UP-TO-DATE/AVAILABLE
  where a Service prints TYPE/CLUSTER-IP, so a kind that prints no such column
  gets an empty cell — see $lib/mergeTables. An empty cell says "this kind does
  not print that"; a dash or a zero would say something the API server never
  did.

  ONE REQUEST PER KIND ON EVERY TICK, because Kubernetes has no multi-kind list
  call. That is the whole reason for the cap — see MAX_COMBINED_KINDS.
-->
<script lang="ts">
  import DataTable, { ROW_MENU_COLUMN, type Column } from '$lib/components/DataTable.svelte'
  import type { CSVExport } from '$stores/activeTable.svelte'
  import EmptyState from '$lib/components/EmptyState.svelte'
  import RowMenuCell from '$lib/components/RowMenuCell.svelte'
  import { organisation } from '$stores/organisation.svelte'
  import CustomCells from '$lib/components/CustomCells.svelte'
  import { customCell, parseCustomColumnId, toColumns } from '$lib/customColumns'
  import { preferences, MAX_COMBINED_KINDS } from '$stores/preferences.svelte'
  import { Layers, X } from '@lucide/svelte'
  import type { ClusterSession } from '$stores/session.svelte'
  import { COMBINED_KIND_ID } from '$stores/session.svelte'
  import type { SourcedRow } from '$lib/mergeTables'
  import Select from '$lib/components/Select.svelte'

  interface Props {
    session: ClusterSession
  }

  let { session }: Props = $props()

  const chosen = $derived(preferences.combinedKindsFor(session.cluster.id))
  const canAdd = $derived(preferences.canAddCombinedKind(session.cluster.id))

  /** Kinds still available to add, as the navigator names them. */
  const addable = $derived(
    session.kinds
      .filter((kind) => !chosen.includes(kind.id))
      .map((kind) => ({ value: kind.id, label: kind.title })),
  )

  /** See PodsView: read fresh so a change in Organise applies at once. */
  const placement = $derived(organisation.placementOf(session.cluster.id))
  const isReadOnly = $derived(
    organisation.settingsFor(placement.project, placement.group).readOnly,
  )

  /**
   * Column ids are positional over the MERGED column set, which is stable for
   * a given set of kinds in a given order. Column preferences are stored under
   * this view's own id rather than under any kind's, because the columns here
   * belong to the combination rather than to any one of them.
   */
  const printed = $derived<Column[]>(
    session.combinedTable.columns.map((column, index) => ({
      id: `c${index}`,
      label: column.name,
      width: index === 0 ? 280 : column.type === 'date' ? 100 : 160,
      numeric: column.type === 'integer' || column.type === 'number',
      pinned: index === 0,
      defaultHidden: column.wide,
    })),
  )

  const custom = $derived<Column[]>(toColumns(session.customColumns))

  const columns = $derived<Column[]>([
    // THE KIND, SECOND AND SORTABLE. It is what makes an interleaved table
    // readable, and sorting on it is the grouped reading.
    ...printed.slice(0, 1),
    { id: 'kind', label: 'Kind', width: 150 },
    ...printed.slice(1),
    ...custom,
    ROW_MENU_COLUMN,
  ])

  function isColumnVisible(column: Column): boolean {
    const stored = preferences.columns[COMBINED_KIND_ID]?.[column.id]?.hidden
    return column.pinned || (stored === undefined ? !column.defaultHidden : !stored)
  }

  function exportCSV(): CSVExport {
    const visible = columns.filter(
      (column) => column.id !== ROW_MENU_COLUMN.id && isColumnVisible(column),
    )

    function cell(row: SourcedRow, id: string): string {
      if (id === 'kind') return session.combinedKindTitle(row.source)
      const spec = parseCustomColumnId(id)
      if (spec) return customCell(row, spec)
      return row.cells?.[Number(id.slice(1))] ?? ''
    }

    return {
      columns: visible.map((column) => column.label),
      rows: session.sortedCombinedRows.map((row) => visible.map((column) => cell(row, column.id))),
    }
  }

  /**
   * Opens a row in the list its kind belongs to.
   *
   * NOT IN PLACE, and that is a decision rather than an omission. The detail
   * drawer's live sections are resolved from the session's own row buffers for
   * ONE kind — its pod, its workload, its node — so rendering an object of
   * another kind there would give a drawer whose panels are silently about
   * nothing. Navigating is the honest version of "show me this", and the
   * combined view is one click away again.
   */
  async function open(row: SourcedRow): Promise<void> {
    if (!row.name) return
    await session.openObject(
      row.source,
      row.name,
      row.namespace,
      session.combinedKindNamespaced(row.source),
    )
  }
</script>

<!--
  THE KINDS, ABOVE THE TABLE THEY PRODUCE. A chip row rather than a dialog:
  the set is the view, so changing it belongs in the view rather than a page
  away from what it changes — the same reasoning SavedViewsMenu applies to the
  things it captures.
-->
<div
  class="flex shrink-0 flex-wrap items-center gap-2 border-b border-outline-variant/40
         bg-surface-container-low px-3 py-2"
>
  <Layers class="size-4 shrink-0 text-on-surface-variant" strokeWidth={1.8} />

  {#each chosen as kindId (kindId)}
    <span
      class="flex items-center gap-1 rounded-full bg-primary/12 py-0.5 pr-1 pl-2.5
             text-label-medium text-primary"
    >
      {session.combinedKindTitle(kindId)}
      <button
        type="button"
        class="state-layer grid size-4 place-items-center rounded-full hover:bg-on-surface/10"
        aria-label="Stop showing {session.combinedKindTitle(kindId)}"
        onclick={() => preferences.removeCombinedKind(session.cluster.id, kindId)}
      >
        <X class="size-3" strokeWidth={2.5} />
      </button>
    </span>
  {/each}

  {#if canAdd && addable.length > 0}
    <Select
      value=""
      options={[{ value: '', label: 'Add a kind…' }, ...addable]}
      label="Add a kind"
      compact
      onchange={(value) => value && preferences.addCombinedKind(session.cluster.id, value)}
    />
  {:else if !canAdd}
    <!--
      SAYS WHY, rather than offering a control that does nothing. The limit is
      about the request rate this view costs, so the sentence names that.
    -->
    <span class="text-body-small text-on-surface-variant">
      {MAX_COMBINED_KINDS} kinds is the limit — each one is another request on every refresh.
    </span>
  {/if}
</div>

{#if chosen.length === 0}
  <EmptyState
    title="Pick the kinds to show together"
    description="Kubernetes has no way to list several kinds at once, so PodSteer asks for each one and merges the answers. Add up to {MAX_COMBINED_KINDS}."
  />
{:else}
  <DataTable
    kindId={COMBINED_KIND_ID}
    {columns}
    isEmpty={session.pagedCombinedRows.length === 0}
    sort={session.sort}
    onsort={session.toggleSort}
    exportRows={exportCSV}
  >
    {#snippet notice()}
      {#if session.combinedTruncated}
        <!--
          ONE KIND HITTING ITS CAP MAKES THE WHOLE TABLE A PREFIX, and the
          count, the search and the sort below are then all wrong in the same
          silent direction. Which kind is not named here because more than one
          may be capped; the sentence says what it means for the table.
        -->
        <p
          class="border-b border-outline-variant/60 px-3 py-2 text-body-medium text-gauge-warn"
          role="status"
        >
          At least one of these kinds has more objects than PodSteer listed, so the rows below
          are a part of the picture rather than all of it. Narrow the namespace to see a
          complete one.
        </p>
      {/if}
    {/snippet}

    {#snippet empty()}
      <EmptyState
        title="Nothing here"
        description="None of these kinds has an object matching what is on screen."
      />
    {/snippet}

    {#snippet rows(isVisible)}
      {#each session.pagedCombinedRows as row, rowIndex (row.source + '/' + row.namespace + '/' + row.name + rowIndex)}
        <tr
          class="group/row border-t border-outline-variant/40 bg-surface transition-colors
                 duration-100 hover:bg-surface-container-low {row.name ? 'cursor-pointer' : ''}"
          onclick={() => void open(row)}
        >
          {#if isVisible('c0')}
            <td data-edge="c0" class="truncate px-3 py-1.5 text-body-medium text-on-surface">
              {row.cells?.[0] ?? ''}
            </td>
          {/if}
          {#if isVisible('kind')}
            <td class="truncate px-3 py-1.5 text-body-medium text-on-surface-variant">
              {session.combinedKindTitle(row.source)}
            </td>
          {/if}
          {#each printed.slice(1) as column, index (column.id)}
            {#if isVisible(column.id)}
              <td class="truncate px-3 py-1.5 text-body-medium text-on-surface-variant">
                {row.cells?.[index + 1] ?? ''}
              </td>
            {/if}
          {/each}
          <CustomCells specs={session.customColumns} {row} {isVisible} />
          <!--
            NO ACTIONS IN THIS VIEW, and the cell is still drawn because the
            row-menu column is the table's elastic one — see RowMenuCell. Every
            action is a write against ONE kind, and offering them here would
            mean resolving each row's kind before the menu could even say what
            it offers. Opening the object gets the full set, one click away.
          -->
          <RowMenuCell actions={[]} label={row.name ?? ''} />
        </tr>
      {/each}
    {/snippet}
  </DataTable>
{/if}
