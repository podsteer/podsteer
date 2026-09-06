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
    /**
     * A selection column, for bulk actions.
     *
     * Its header is the "select all on this page" checkbox rather than a
     * sort control, it cannot be resized, and the column chooser does not
     * list it — there is nothing to sort by, widen or hide in a tick box.
     * Always paired with `pinned`, since a view that offers selection
     * offers it on every row.
     */
    select?: boolean
    /**
     * The row's own menu, for what a row can do.
     *
     * A REAL COLUMN, which it was not until 2026-09-06. The menu cell used to
     * fall into the trailing elastic column by arithmetic — the header had one
     * cell fewer than every row, and the control's width was whatever slack
     * the table happened to have. That made the column count dishonest to
     * anything counting cells, and left the menu neither reliably visible nor
     * a fixed size to aim at.
     *
     * Declared by the view rather than added here, exactly as `select` is,
     * because DataTable renders no cells: the view owns the row markup, so
     * only the view knows whether its rows carry a menu at all. Must be LAST
     * in the column list, and paired with `pinned` — there is nothing to hide
     * in a control that is the only way to reach half a row's actions.
     *
     * See $lib/fixedColumns for the edge it is pinned to.
     */
    menu?: boolean
  }

  /**
   * The row menu's column, shared by every view that draws one.
   *
   * ONE OBJECT RATHER THAN SIX COPIES, because three of its four fields have
   * to agree with something elsewhere or the column misbehaves quietly: the
   * id is what the fixed-edge CSS and the operator's stored width are keyed
   * on, `pinned` is what keeps it out of the column chooser's reach, and the
   * width is sized to the control rather than to its heading, which is not
   * drawn. Append it LAST — after the operator's own columns — since it is
   * the end of the row.
   */
  export const ROW_MENU_COLUMN: Column = {
    id: 'menu',
    label: 'Row menu',
    // As narrow as this table makes a column: MIN_WIDTH is the floor every
    // column is clamped up to, and there is no narrower honest number to
    // write. The tick box column declares 40 and is silently widened to the
    // same 56, which is why the two edges match on screen — declaring 40 here
    // would render identically and describe something that never happens.
    width: 56,
    pinned: true,
    menu: true,
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
  import { activeTable, type CSVExport } from '$stores/activeTable.svelte'
  import { edgeBoundaries, fixedPlacements } from '$lib/fixedColumns'
  import { ChevronUp, ChevronDown, ChevronsUpDown } from '@lucide/svelte'
  import Checkbox from './Checkbox.svelte'

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
    /**
     * Produces this table's CSV export.
     *
     * DataTable has no idea what a row IS — it renders whatever markup the
     * `rows` snippet hands it — so it cannot build this itself. It only
     * carries the reference from whichever view supplied it to the toolbar's
     * Export CSV control, the same way it already carries `columns` there.
     */
    exportRows?: () => CSVExport
    /**
     * The state of a `select` column's header checkbox, and what clicking
     * it does. Supplied by a view whose rows carry a RowSelect cell; a
     * select column with none draws a disabled box.
     */
    selectAll?: { checked: boolean; indeterminate: boolean; ontoggle: () => void }
  }

  let {
    kindId,
    columns,
    rows,
    empty,
    isEmpty = false,
    sort = null,
    onsort,
    exportRows,
    selectAll,
  }: Props = $props()

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
   * Arrow keys walk the rows; Enter opens one; Space ticks one; Escape lets
   * go.
   *
   * Enter clicks the row rather than calling a handler of its own, so the
   * keyboard and the mouse can never open different things — whatever a click
   * does today is what Enter does. Space goes the same way to the row's tick
   * box (see RowSelect), for the same reason: one path, owned by the cell,
   * and shift carries across so a keyboard range reads like a shift-click.
   * A view with no tick boxes keeps Space as a second Enter, so the key does
   * something everywhere.
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
        event.preventDefault()
        current.click()
        break
      case ' ': {
        event.preventDefault()
        const box = current.querySelector<HTMLInputElement>('input[data-row-select]')
        if (box) {
          box.dispatchEvent(
            new MouseEvent('click', { bubbles: true, cancelable: true, shiftKey: event.shiftKey }),
          )
        } else {
          current.click()
        }
        break
      }
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
   * Publishes this table's columns for the toolbar's column chooser.
   *
   * The chooser used to sit in the header's trailing cell, which put it off
   * screen the moment a wide table was scrolled sideways — and the tables it
   * matters most on are precisely the wide ones. It now lives in the toolbar
   * beside the pager, where it cannot be scrolled away from, so the columns
   * have to reach a component that is not an ancestor of this one.
   *
   * Re-runs when `columns` changes, which is what keeps the menu correct for
   * a generic table whose columns are whatever the API server just described.
   */
  $effect(() => {
    const token = activeTable.claim(kindId, columns, exportRows)
    return () => activeTable.release(token)
  })

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

  /**
   * Arrow keys resize a column, which is the only way a keyboard can.
   *
   * It was pointer-only, so a keyboard operator could not widen a column
   * whose values were being cut off — and could not undo it either, because
   * the reset was a double-click. Enter is the reset now.
   */
  function onResizeKeydown(event: KeyboardEvent, column: Column): void {
    const STEP = 16
    let width: number
    switch (event.key) {
      case 'ArrowLeft':
        width = widthOf(column) - STEP
        break
      case 'ArrowRight':
        width = widthOf(column) + STEP
        break
      case 'Enter':
        width = column.width
        break
      default:
        return
    }
    event.preventDefault()
    preferences.setColumnWidth(kindId, column.id, Math.max(minWidthOf(column), width))
  }

  // --- Fixed edge columns ---------------------------------------------------

  /**
   * The row-menu column, and everything before it.
   *
   * They are drawn separately because the ELASTIC COLUMN HAS TO SIT BETWEEN
   * THEM. The fixed layout needs one column with no width to absorb the
   * leftover, or the surplus is shared out over the real columns and the last
   * one stretches; and the menu has to be at the right-hand end of the table
   * whether or not there is any surplus, which it is not if the elastic
   * column follows it. So the order is: every other column, the elastic one,
   * then the menu — in the colgroup, in the header, and in each view's row
   * (see RowMenuCell, which draws both of the last two cells for exactly this
   * reason).
   */
  const menuColumn = $derived(visible.find((column) => column.menu))
  const bodyColumns = $derived(visible.filter((column) => !column.menu))

  /**
   * Where each fixed column sits, from the widths in effect right now.
   *
   * Derived rather than measured: a column's width is already known here —
   * the colgroup is written from it — and reading it back out of the DOM
   * would be a second source of truth that disagrees for one frame after
   * every resize.
   */
  const placements = $derived(
    fixedPlacements(
      visible.map((column) => ({
        id: column.id,
        width: widthOf(column),
        select: column.select,
        menu: column.menu,
      })),
      preferences.fixedEdges,
    ),
  )

  const fixedSelect = $derived(placements.find((placement) => placement.kind === 'select'))
  const fixedMenu = $derived(placements.find((placement) => placement.kind === 'menu'))

  /** The scrollport, for how far it has been scrolled sideways. */
  let scroller = $state<HTMLElement | null>(null)
  /** The table itself, because it is what changes width when a column does. */
  let grid = $state<HTMLTableElement | null>(null)

  let boundaries = $state({ left: false, right: false })

  function measureEdges(): void {
    const node = scroller
    if (!node) return
    boundaries = edgeBoundaries(node.scrollLeft, node.scrollWidth, node.clientWidth)
  }

  /**
   * Keeps the hairlines in step with the scroll position.
   *
   * THREE sources, and dropping any one of them leaves a hairline drawn over
   * a table that no longer scrolls, or missing from one that does. Scrolling
   * is the obvious one. The scrollport resizes when the window or the detail
   * drawer does. And the TABLE resizes when a column is dragged, a column is
   * hidden, or a custom column is added — none of which changes the
   * scrollport at all, so an observer watching only that would miss every one
   * of them.
   *
   * The listener is passive: it reads geometry and never prevents the scroll.
   */
  $effect(() => {
    const port = scroller
    if (!port) return

    measureEdges()
    port.addEventListener('scroll', measureEdges, { passive: true })

    const observer = new ResizeObserver(measureEdges)
    observer.observe(port)
    if (grid) observer.observe(grid)

    return () => {
      port.removeEventListener('scroll', measureEdges)
      observer.disconnect()
    }
  })
</script>

<div class="flex min-h-0 flex-1 flex-col">
  <div class="min-h-0 flex-1 overflow-auto" bind:this={scroller}>
    {#if isEmpty}
      {#if empty}{@render empty()}{/if}
    {:else}
      <!--
        The two data-fixed-* attributes are what switch stickiness on, and the
        two data-scrolled-* ones are what draw the hairline. Both pairs are on
        the TABLE rather than on the cells, because the cells are rendered by
        each view and this component is the only thing that knows the answer.
      -->
      <table
        bind:this={grid}
        class="w-full table-fixed border-collapse text-body-medium"
        style="--fixed-select-offset: {fixedSelect?.offset ?? 0}px;
               --fixed-menu-offset: {fixedMenu?.offset ?? 0}px"
        data-fixed-select={fixedSelect ? '' : undefined}
        data-fixed-menu={fixedMenu ? '' : undefined}
        data-scrolled-start={boundaries.left ? '' : undefined}
        data-scrolled-end={boundaries.right ? '' : undefined}
      >
        <colgroup>
          {#each bodyColumns as column (column.id)}
            <col style="width: {widthOf(column)}px" />
          {/each}
          <!-- The elastic column absorbs the leftover width so the fixed
               layout does not stretch the last real column to fill it. It
               sits BEFORE the menu column so the menu is at the right-hand
               end of the table however much slack there is. -->
          <col />
          {#if menuColumn}
            <col style="width: {widthOf(menuColumn)}px" />
          {/if}
        </colgroup>

        <thead class="sticky top-0 z-20 bg-surface-container/95 backdrop-blur-sm">
          <tr class="text-left text-label-medium text-on-surface-variant">
            {#each bodyColumns as column, index (column.id)}
              {@const active = sort?.columnId === column.id}
              <th
                scope="col"
                data-edge={column.select ? 'select' : undefined}
                aria-sort={active
                  ? sort?.direction === 'asc'
                    ? 'ascending'
                    : 'descending'
                  : undefined}
                class="relative p-0 font-medium"
              >
                {#if column.select}
                  <!-- The page's tick box: all, some (indeterminate) or none
                       of the rows on screen. It describes THIS page and acts
                       on this page — see RowSelection.toggleAllVisible. -->
                  <span class="flex items-center py-2 {index === 0 ? 'pl-5' : 'px-3'}">
                    <Checkbox
                      checked={selectAll?.checked ?? false}
                      indeterminate={selectAll?.indeterminate ?? false}
                      disabled={!selectAll}
                      ariaLabel="Select all rows on this page"
                      title="Select all rows on this page"
                      onchange={() => selectAll?.ontoggle()}
                    />
                  </span>
                {:else}
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
                <!--
                  svelte-ignore a11y_no_noninteractive_tabindex, a11y_no_noninteractive_element_interactions

                  Both warnings are false here — see ColumnDivider.svelte. A
                  focusable separator is the window-splitter pattern.
                -->
                <span
                  role="separator"
                  aria-orientation="vertical"
                  aria-label="Resize {column.label}"
                  aria-valuenow={widthOf(column)}
                  aria-valuetext="{widthOf(column)} pixels"
                  tabindex="0"
                  onkeydown={(event) => onResizeKeydown(event, column)}
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
                {/if}
              </th>
            {/each}

            <!-- The elastic column's own header cell. A <td> rather than a
                 <th>, because it heads nothing: it is the slack. It exists so
                 the header row holds one cell per column, which is what makes
                 the count something a reader — or a test — can rely on. -->
            <td class="p-0"></td>

            {#if menuColumn}
              <!-- The row menu's heading. Empty on screen, because a word over
                   a column of three dots labels nothing anybody needs, and
                   named for anyone reading the table by its headings. -->
              <th scope="col" data-edge="menu" class="p-0 font-medium">
                <span class="sr-only">{menuColumn.label}</span>
              </th>
            {/if}
          </tr>
        </thead>

        <tbody bind:this={body}>
          {@render rows(isVisible)}
        </tbody>
      </table>
    {/if}
  </div>
</div>

<style>
  /*
    The columns that stay put, in CSS because the cells are not this
    component's markup: every view draws its own <tr>, so these rules reach
    them through :global and one data attribute rather than through classes
    threaded down five levels. What DataTable owns is the pair of switches on
    the <table> and the offsets beside them.

    AN OPAQUE BACKGROUND IS NOT DECORATION HERE. A sticky cell paints only
    what it was given, and a row's state used to be a translucent tint — so
    without this the columns scrolling underneath show through the pinned tick
    box and it reads as two things at once. `inherit` is what keeps that
    colour right in every state a row can be in, hover included: it takes
    whatever the row's own class resolved to, so a view that grows a sixth
    state needs no change here. It works only because those tints are now
    opaque, which is the other half of this and lives in app.css.

    A header cell cannot inherit anything: a <tr> has no background of its own
    and the band belongs to the <thead>, which scrolls sideways out from under
    a cell that does not move with it. So they name the header ground.
  */
  :global(table[data-fixed-select] :is(th, td)[data-edge='select']) {
    position: sticky;
    left: var(--fixed-select-offset, 0px);
  }

  :global(table[data-fixed-menu] :is(th, td)[data-edge='menu']) {
    position: sticky;
    right: var(--fixed-menu-offset, 0px);
  }

  :global(table tbody td[data-edge]) {
    background-color: inherit;
    /* Above the cells scrolling past, below the header. The <thead> carries
       its own z-index and establishes a stacking context, so this number is
       only ever weighed against the ordinary cells beside it. */
    z-index: 2;
  }

  :global(table thead :is(th, td)[data-edge]) {
    background-color: var(--table-header);
    /* A corner cell is sticky in BOTH directions, so it has to win against
       the header cells it slides over as well as the body. This decides the
       first; the <thead>'s own z-index decides the second for every cell in
       it at once, which is why the two numbers do not need to agree. */
    z-index: 10;
  }

  /*
    The boundary, drawn only while something is passing underneath it.

    An inset shadow rather than a border, because the table is
    `border-collapse: collapse` — where a cell's border is resolved against
    its neighbours' and painted by the TABLE, which is the one thing that does
    not move with a cell that has been pinned. A shadow takes no part in that
    collapse and travels with the cell.

    The fade is the second half of not flickering, after the dead zone in
    edgeBoundaries: a scroll settling a fraction of a pixel either side of the
    threshold crosses it visibly slowly instead of blinking.
  */
  :global(table :is(th, td)[data-edge]) {
    transition: box-shadow 120ms var(--ease-standard, ease);
  }

  :global(table[data-fixed-select][data-scrolled-start] :is(th, td)[data-edge='select']) {
    box-shadow: inset -1px 0 0 var(--outline-variant);
  }

  :global(table[data-fixed-menu][data-scrolled-end] :is(th, td)[data-edge='menu']) {
    box-shadow: inset 1px 0 0 var(--outline-variant);
  }
</style>
