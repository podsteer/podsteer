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
    /**
     * Narrowest this column may be dragged, when the shared floor is too
     * generous for what the cell has to fit.
     *
     * The floor exists because most cells degrade gracefully — a truncated
     * name is still a name, and an operator who narrows that column has
     * decided they can live with it. A cell built from several fixed-width
     * parts does not degrade, it collapses: the meter columns hold a value, a
     * bar and a percentage, and below their combined width the bar is what
     * silently disappears, which is the one part that cannot be inferred from
     * anything else on the row.
     */
    minWidth?: number
    /** Right-aligns the cells, for numeric values. */
    numeric?: boolean
    /** Cannot be hidden. The name column, essentially. */
    pinned?: boolean
    /** Starts hidden; the operator opts in from the column menu. */
    defaultHidden?: boolean
    /**
     * Drawn instead of the heading text.
     *
     * For a column whose cells are marks rather than words: the status
     * columns are as wide as one glyph, and "Status" spelt out was wider than
     * everything it was labelling. The label is still what the sort control
     * and the column menu announce, so nothing is lost to anyone reading by
     * name.
     */
    icon?: Component
  }

  /**
   * Hands keyboard focus to the table that is currently on screen.
   *
   * A module-level registration rather than a DOM query from the toolbar or a
   * binding threaded through five views: exactly one table is mounted at a
   * time, the toolbar has no reference to it, and `document.querySelector`
   * from another component is the kind of coupling that survives right up
   * until somebody renders a second table.
   *
   * Returns false when there is nothing to focus, so the caller can leave the
   * keystroke alone rather than swallowing it.
   */
  let focusHandler: (() => boolean) | null = null

  export function focusFirstRow(): boolean {
    return focusHandler?.() ?? false
  }
</script>

<script lang="ts">
  import type { Component, Snippet } from 'svelte'
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

  let body = $state<HTMLTableSectionElement | null>(null)



  /** Every row, in the order they are displayed. */
  function rowsOf(): HTMLTableRowElement[] {
    return body ? [...body.querySelectorAll('tr')] : []
  }

  /**
   * Rows are made focusable and navigable here rather than by each view.
   *
   * Five views render their own <tr>, and none of them should have to know
   * about keyboard navigation to get it.
   *
   * The rows are OBSERVED rather than derived from anything. They change on
   * search, on paging, on sorting and on every refresh, and the snippet that
   * renders them is opaque from here — a hand-written list of dependencies
   * would be wrong the first time somebody added a fourth way to change them,
   * and wrong silently, since the only symptom is that the keyboard stops
   * reaching rows that look perfectly normal.
   *
   * tabindex is -1 rather than 0: rows are reached by arrowing down from the
   * search field, not by tabbing through several hundred of them.
   *
   * The key handler is attached here rather than written on <tbody> in the
   * markup, because one listener that delegates cannot go stale — and because
   * a table section is not an interactive element, which is exactly what the
   * accessibility linter says when you put a handler on one.
   */
  $effect(() => {
    const node = body
    if (!node) return

    const number = (): void => {
      for (const row of rowsOf()) row.tabIndex = -1
    }
    number()

    const observer = new MutationObserver(number)
    observer.observe(node, { childList: true })
    node.addEventListener('keydown', onRowKeydown)

    return () => {
      observer.disconnect()
      node.removeEventListener('keydown', onRowKeydown)
    }
  })

  // Registers this table as the one the toolbar can hand focus to.
  $effect(() => {
    focusHandler = () => {
      const first = rowsOf()[0]
      if (!first) return false
      first.focus()
      first.scrollIntoView({ block: 'nearest' })
      return true
    }
    return () => {
      focusHandler = null
    }
  })

  /**
   * Arrow keys walk the rows; Enter opens one; Escape lets go.
   *
   * Enter clicks the row rather than calling a handler of its own, so the
   * keyboard and the mouse can never open different things — whatever a click
   * does today is what Enter does.
   */
  function onRowKeydown(event: KeyboardEvent): void {
    const current = (event.target as HTMLElement | null)?.closest('tr')
    if (!current) return

    const all = rowsOf()
    const index = all.indexOf(current as HTMLTableRowElement)
    if (index < 0) return

    const focus = (next: number): void => {
      const row = all[Math.min(all.length - 1, Math.max(0, next))]
      row?.focus()
      row?.scrollIntoView({ block: 'nearest' })
    }

    switch (event.key) {
      case 'ArrowDown':
        event.preventDefault()
        focus(index + 1)
        break
      case 'ArrowUp':
        event.preventDefault()
        // Off the top is a return to the search field, not a dead end.
        if (index === 0) current.blur()
        else focus(index - 1)
        break
      case 'Home':
        event.preventDefault()
        focus(0)
        break
      case 'End':
        event.preventDefault()
        focus(all.length - 1)
        break
      case 'Enter':
      case ' ':
        event.preventDefault()
        current.click()
        break
      case 'Escape':
        event.preventDefault()
        current.blur()
        break
    }
  }

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

  /**
   * Effective width: the column being dragged, else the operator's, else the
   * default.
   *
   * The live width is held here rather than pushed into preferences on every
   * pointermove, and that matters twice over. Each write serialised the whole
   * preferences payload — pruning snoozes on the way — into a synchronous
   * localStorage.setItem, sixty to a hundred and twenty times a second. And
   * it reassigned `preferences.columns`, which invalidates `visible` and
   * `visibleIds` above, which every `{#if isVisible(…)}` in every cell of
   * every row subscribes to: a hundred rows of ten columns is a thousand
   * conditional blocks re-evaluated per frame of a drag.
   */
  function widthOf(column: Column): number {
    if (dragging?.id === column.id) return dragging.width

    // The stored width is clamped on the way OUT, not just on the way in. A
    // preference is persisted per kind and outlives the code that produced
    // it, so a column that was narrow before it grew a minimum — or before
    // its cell was rebuilt to hold more — would otherwise stay at a width
    // nothing can render in, with no way to discover why except dragging it.
    const stored = preferences.columnWidth(kindId, column.id)
    return Math.max(minWidthOf(column), stored ?? column.width)
  }

  // --- Resizing -------------------------------------------------------------

  /** The column being dragged, if any. */
  let dragging = $state<{ id: string; startX: number; startWidth: number; width: number } | null>(
    null,
  )

  /** Narrower than this and a column shows nothing useful, only an ellipsis. */
  const MIN_WIDTH = 56

  /** The floor for one column: its own, when it declares one. */
  function minWidthOf(column: Column): number {
    return column.minWidth ?? MIN_WIDTH
  }

  function startResize(event: PointerEvent, column: Column): void {
    // Stop the pointerdown reaching the header, which would otherwise be
    // interpreted as a click on the column itself.
    event.preventDefault()
    event.stopPropagation()

    const startWidth = widthOf(column)
    dragging = { id: column.id, startX: event.clientX, startWidth, width: startWidth }
    ;(event.currentTarget as HTMLElement).setPointerCapture(event.pointerId)
  }

  function onResizeMove(event: PointerEvent): void {
    if (!dragging) return
    const column = columns.find((candidate) => candidate.id === dragging?.id)
    const floor = column ? minWidthOf(column) : MIN_WIDTH
    const width = Math.max(floor, dragging.startWidth + (event.clientX - dragging.startX))
    dragging = { ...dragging, width }
  }

  /** The one point at which the drag becomes a stored preference. */
  function endResize(): void {
    if (dragging) preferences.setColumnWidth(kindId, dragging.id, dragging.width)
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
                  {#if column.icon}
                    {@const HeaderIcon = column.icon}
                    <HeaderIcon
                      class="size-4 shrink-0"
                      strokeWidth={1.8}
                      aria-label={column.label}
                    />
                  {:else}
                    <span class="truncate">{column.label}</span>
                  {/if}
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

        <tbody bind:this={body}>
          {@render rows(isVisible)}
        </tbody>
      </table>
    {/if}
  </div>
</div>
