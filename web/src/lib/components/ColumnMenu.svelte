<!--
  The per-table column chooser.

  Lives in the toolbar, after the pager. It was in the header's trailing cell,
  which was reachable only until somebody scrolled a wide table sideways — and
  a wide table is exactly when an operator goes looking for it. In the toolbar
  it sits still, in the row where every other per-view control already is.

  Pinned columns are listed but not toggleable — hiding the name column would
  leave rows nothing can identify them by.
-->
<script lang="ts">
  import { preferences } from '$stores/preferences.svelte'
  import type { Column } from './DataTable.svelte'
  import { Columns3, RotateCcw, Pin } from '@lucide/svelte'

  interface Props {
    kindId: string
    columns: Column[]
  }

  let { kindId, columns }: Props = $props()

  let open = $state(false)

  function isHidden(column: Column): boolean {
    if (column.pinned) return false
    const stored = preferences.columns[kindId]?.[column.id]?.hidden
    return stored === undefined ? Boolean(column.defaultHidden) : stored
  }

  function onWindowPointerDown(event: PointerEvent): void {
    if (!open) return
    const target = event.target as HTMLElement | null
    if (!target?.closest('[data-column-menu]')) open = false
  }

  function onKeydown(event: KeyboardEvent): void {
    if (event.key === 'Escape') open = false
  }
</script>

<svelte:window onpointerdown={onWindowPointerDown} onkeydown={onKeydown} />

<div class="relative" data-column-menu>
  <button
    type="button"
    onclick={() => (open = !open)}
    aria-expanded={open}
    aria-label="Choose columns"
    title="Choose columns"
    class="state-layer grid size-8 shrink-0 place-items-center rounded-full text-on-surface-variant
           transition-colors duration-100 hover:bg-surface-container hover:text-on-surface
           {open ? 'bg-surface-container text-on-surface' : ''}"
  >
    <!-- Sized to the pager's buttons rather than the header cell's, since
         that is what it now sits beside. -->
    <Columns3 class="size-4" strokeWidth={1.8} />
  </button>

  {#if open}
    <!-- NOT role="menu". A menu's children must be menu items, and these are a
         <ul> of <label><input type=checkbox>. Assistive technology in menu
         mode exposes only menu items, so it found none at all and "Choose
         columns" opened an empty menu. A labelled group of checkboxes is what
         this actually is. -->
    <div
      class="absolute top-full right-0 z-30 mt-1 w-56 rounded-sm border border-outline-variant/60
             bg-surface-container-high py-1.5 shadow-level-3"
      role="group"
      aria-label="Columns"
    >
      <p class="px-3 py-1 text-[10px] font-semibold uppercase tracking-wider text-on-surface-variant/60">
        Columns
      </p>

      <ul class="max-h-80 overflow-auto py-0.5">
        {#each columns as column (column.id)}
          <li>
            <label
              class="flex cursor-pointer items-center gap-2.5 rounded-sm px-3 py-1.5 text-body-small
                     text-on-surface transition-colors duration-75 hover:bg-surface-container-highest
                     {column.pinned ? 'cursor-default opacity-50' : ''}"
            >
              <input
                type="checkbox"
                checked={!isHidden(column)}
                disabled={column.pinned}
                onchange={() => preferences.toggleColumn(kindId, column.id)}
                class="size-3.5 accent-primary"
              />
              <span class="flex-1 truncate">{column.label}</span>
              {#if column.pinned}
                <Pin class="size-3 text-on-surface-variant/50" strokeWidth={2} />
              {/if}
            </label>
          </li>
        {/each}
      </ul>

      <div class="mt-1 border-t border-outline-variant/30 pt-1">
        <button
          type="button"
          onclick={() => preferences.resetColumns(kindId)}
          class="state-layer flex w-full items-center gap-2 px-3 py-1.5 text-left text-body-small text-primary"
        >
          <RotateCcw class="size-3.5" strokeWidth={1.8} />
          Reset columns
        </button>
      </div>
    </div>
  {/if}
</div>
