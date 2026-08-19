<!--
  The shell every resource list shares: sticky header with resizable and
  hideable columns. Pagination lives in the toolbar above, not here — see
  Pagination.svelte — so the table gets the full remaining height.

  Column widths and visibility persist per kind (see preferences.svelte.ts),
  because "the name column is too narrow" is a complaint about Pods, not about
  every table in the application.

  `table-layout: fixed` is what makes both resizing and truncation work. Under
  the default `auto` layout the browser sizes columns from their content, so a
  single long pod name stretches the table past the viewport and no explicit
  width is honoured.
-->
<script lang="ts" module>
  /** One column of a resource table. */
  export interface Column {
    /** Stable key. Used to persist width and visibility, so never rename it
        casually — a rename silently resets the operator's adjustments. */
    id: string
    /** Heading text. */
    label: string
    /** Default width in pixels, before any user resize. */
    width: number
    /** Right-aligns the cells, for numeric values. */
    numeric?: boolean
    /** Cannot be hidden. The name column, essentially. */
    pinned?: boolean
    /** Starts hidden; the operator opts in from the column menu. */
    defaultHidden?: boolean
  }
</script>

<script lang="ts">
  import type { Snippet } from 'svelte'
  import type { SortState } from '$lib/sort'
  import { preferences } from '$stores/preferences.svelte'
  import ColumnMenu from './ColumnMenu.svelte'
  import { ChevronUp, ChevronDown, ChevronsUpDown } from '@lucide/svelte'

  interface Props {
    /** Identifies the kind, for persisting column preferences. */
    kindId: string
    columns: Column[]
    /** Rendered once per row. Receives a visibility test so each view can skip
        the cells whose columns are hidden. */
    rows: Snippet<[(columnId: string) => boolean]>
    /** Shown instead of rows when there are none. */
    empty?: Snippet
    isEmpty?: boolean
    /** The sort in effect, or null for server order. */
    sort?: SortState | null
    /** Header click: cycles the column ascending, descending, unsorted. */
    onsort?: (columnId: string) => void
  }

  let { kindId, columns, rows, empty, isEmpty = false, sort = null, onsort }: Props = $props()

  /** Columns the operator has not hidden. */
  const visible = $derived(
    columns.filter((column) => {
      if (column.pinned) return true
      const stored = preferences.columns[kindId]?.[column.id]?.hidden
      return stored === undefined ? !column.defaultHidden : !stored
    }),
  )

  const visibleIds = $derived(new Set(visible.map((column) => column.id)))

  function isVisible(columnId: string): boolean {
    return visibleIds.has(columnId)
  }

  /** Effective width: the operator's, else the column's default. */
  function widthOf(column: Column): number {
    return preferences.columnWidth(kindId, column.id) ?? column.width
  }

  // --- Resizing -------------------------------------------------------------

  /** The column being dragged, if any. */
  let dragging = $state<{ id: string; startX: number; startWidth: number } | null>(null)

  /** Narrower than this and a column shows nothing useful, only an ellipsis. */
  const MIN_WIDTH = 56

  function startResize(event: PointerEvent, column: Column): void {
    // Stop the pointerdown reaching the header, which would otherwise be
    // interpreted as a click on the column itself.
    event.preventDefault()
    event.stopPropagation()

    dragging = { id: column.id, startX: event.clientX, startWidth: widthOf(column) }
    ;(event.currentTarget as HTMLElement).setPointerCapture(event.pointerId)
  }

  function onResizeMove(event: PointerEvent): void {
    if (!dragging) return
    const width = Math.max(MIN_WIDTH, dragging.startWidth + (event.clientX - dragging.startX))
    preferences.setColumnWidth(kindId, dragging.id, width)
  }

  function endResize(): void {
    dragging = null
  }

  /** Double-clicking a divider restores that column's default width. */
  function resetWidth(column: Column): void {
    preferences.setColumnWidth(kindId, column.id, column.width)
  }
</script>

<div class="flex min-h-0 flex-1 flex-col">
  <div class="min-h-0 flex-1 overflow-auto">
    {#if isEmpty}
      {#if empty}{@render empty()}{/if}
    {:else}
      <table class="w-full table-fixed border-collapse text-body-medium">
        <colgroup>
          {#each visible as column (column.id)}
            <col style="width: {widthOf(column)}px" />
          {/each}
          <!-- A final elastic column absorbs the leftover width so the fixed
               layout does not stretch the last real column to fill it. -->
          <col />
        </colgroup>

        <thead class="sticky top-0 z-20 bg-surface-container/95 backdrop-blur-sm">
          <tr class="text-left text-label-medium text-on-surface-variant">
            {#each visible as column, index (column.id)}
              {@const active = sort?.columnId === column.id}
              <th
                scope="col"
                aria-sort={active
                  ? sort?.direction === 'asc'
                    ? 'ascending'
                    : 'descending'
                  : undefined}
                class="relative p-0 font-medium"
              >
                <button
                  type="button"
                  onclick={() => onsort?.(column.id)}
                  title="Sort by {column.label}"
                  class="group flex w-full items-center gap-1 px-3 py-2 text-left
                         transition-colors duration-100 ease-standard
                         {index === 0 ? 'pl-5' : ''}
                         {column.numeric ? 'flex-row-reverse' : ''}
                         {active ? 'text-primary' : 'hover:text-on-surface'}"
                >
                  <span class="truncate">{column.label}</span>
                  {#if active}
                    {#if sort?.direction === 'asc'}
                      <ChevronUp class="size-3.5 shrink-0 text-primary" strokeWidth={2.5} />
                    {:else}
                      <ChevronDown class="size-3.5 shrink-0 text-primary" strokeWidth={2.5} />
                    {/if}
                  {:else}
                    <ChevronsUpDown
                      class="size-3.5 shrink-0 opacity-0 transition-opacity duration-100
                             group-hover:opacity-50"
                      strokeWidth={2}
                    />
                  {/if}
                </button>

                <!-- The drag handle. Sits over the cell boundary and is wider
                     than it looks, because a 1px target is unhittable. -->
                <span
                  role="separator"
                  aria-orientation="vertical"
                  aria-label="Resize {column.label}"
                  tabindex="-1"
                  class="absolute top-0 -right-1 z-10 h-full w-2 cursor-col-resize
                         after:absolute after:top-1/2 after:left-1/2 after:h-1/2 after:w-px
                         after:-translate-x-1/2 after:-translate-y-1/2 after:bg-outline-variant
                         hover:after:bg-primary hover:after:w-0.5
                         {dragging?.id === column.id ? 'after:bg-primary after:w-0.5' : ''}"
                  onpointerdown={(event) => startResize(event, column)}
                  onpointermove={onResizeMove}
                  onpointerup={endResize}
                  onpointercancel={endResize}
                  ondblclick={() => resetWidth(column)}
                ></span>
              </th>
            {/each}

            <th scope="col" class="relative w-10 px-2 py-2.5 text-right">
              <ColumnMenu {kindId} {columns} />
            </th>
          </tr>
        </thead>

        <tbody>
          {@render rows(isVisible)}
        </tbody>
      </table>
    {/if}
  </div>
</div>
