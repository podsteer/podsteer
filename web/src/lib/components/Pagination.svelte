<!--
  Pagination controls: page size, range readout, and page navigation.

  Lives in the toolbar rather than under the table — consolidating it there
  with search and the other per-view controls means the table itself is free
  to use its full height, and every control an operator reaches for while
  looking at a list sits in one row instead of split across the top and
  bottom of the screen.
-->
<script lang="ts">
  import Select from './Select.svelte'
  import { PAGE_SIZES, preferences, type PageSize } from '$stores/preferences.svelte'
  import { ChevronsLeft, ChevronLeft, ChevronRight, ChevronsRight } from '@lucide/svelte'

  interface Props {
    /** Total rows after filtering, across all pages. */
    totalCount: number
    /** Index of the first row on this page. */
    pageStart: number
    currentPage: number
    pageCount: number
    onpage: (page: number) => void
    class?: string
  }

  let { totalCount, pageStart, currentPage, pageCount, onpage, class: className = '' }: Props = $props()

  const rangeEnd = $derived(Math.min(pageStart + preferences.pageSize, totalCount))
  const rangeStart = $derived(totalCount === 0 ? 0 : pageStart + 1)
</script>

<div class="flex shrink-0 items-center gap-2 {className}">
  <Select
    compact
    label="Rows per page"
    value={String(preferences.pageSize)}
    options={PAGE_SIZES.map((size) => ({ value: String(size), label: `${size} / page` }))}
    onchange={(next) => preferences.setPageSize(Number(next) as PageSize)}
  />

  <span class="whitespace-nowrap text-body-medium tabular-nums text-on-surface-variant/70">
    {rangeStart}–{rangeEnd} of {totalCount}
  </span>

  <div class="flex items-center gap-0.5">
    <button
      type="button"
      onclick={() => onpage(1)}
      disabled={currentPage <= 1}
      aria-label="First page"
      class="state-layer grid size-8 shrink-0 place-items-center rounded-md
             text-on-surface-variant transition-colors duration-100 hover:bg-surface-container hover:text-on-surface
             disabled:pointer-events-none disabled:opacity-30"
    >
      <ChevronsLeft class="size-4" strokeWidth={1.8} />
    </button>
    <button
      type="button"
      onclick={() => onpage(currentPage - 1)}
      disabled={currentPage <= 1}
      aria-label="Previous page"
      class="state-layer grid size-8 shrink-0 place-items-center rounded-md
             text-on-surface-variant transition-colors duration-100 hover:bg-surface-container hover:text-on-surface
             disabled:pointer-events-none disabled:opacity-30"
    >
      <ChevronLeft class="size-4" strokeWidth={1.8} />
    </button>

    <span class="min-w-14 px-1 text-center whitespace-nowrap text-body-medium tabular-nums text-on-surface-variant/70">
      {currentPage} / {pageCount}
    </span>

    <button
      type="button"
      onclick={() => onpage(currentPage + 1)}
      disabled={currentPage >= pageCount}
      aria-label="Next page"
      class="state-layer grid size-8 shrink-0 place-items-center rounded-md
             text-on-surface-variant transition-colors duration-100 hover:bg-surface-container hover:text-on-surface
             disabled:pointer-events-none disabled:opacity-30"
    >
      <ChevronRight class="size-4" strokeWidth={1.8} />
    </button>
    <button
      type="button"
      onclick={() => onpage(pageCount)}
      disabled={currentPage >= pageCount}
      aria-label="Last page"
      class="state-layer grid size-8 shrink-0 place-items-center rounded-md
             text-on-surface-variant transition-colors duration-100 hover:bg-surface-container hover:text-on-surface
             disabled:pointer-events-none disabled:opacity-30"
    >
      <ChevronsRight class="size-4" strokeWidth={1.8} />
    </button>
  </div>
</div>
