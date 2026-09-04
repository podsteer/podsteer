<!--
  Two manifests, compared — side by side by default, folding to a single
  unified column when the pane is too narrow for two to be readable.

  Renders the same `diffLines` edit script three ways depending on what is
  asked of it: as a two-column grid here, as `unified()` text for the copy
  button, and — via the same `foldSegments` this reads — as the fold that
  collapses a long run of unchanged lines to a count. All three come from
  `$lib/diff`, so "3 changes" here and what Copy puts on the clipboard can
  never disagree about what changed.

  Colours are the app's own success/error/warning containers, not a new
  palette invented for diffing: green already means "this is fine" and red
  already means "this needs attention" everywhere else in PodSteer, and a
  diff view that picked its own shade of green would be the one surface in
  the application where that stopped being true. `changed` — a line replaced
  in place, which is what most edits to a manifest actually are — gets
  warning's amber rather than being drawn as an unrelated removal-then-
  addition pair, because that IS what it is: neither wrong on its own.
-->
<script lang="ts">
  import {
    diffLines,
    foldSegments,
    hunks,
    splitLines,
    unified,
    zipOpsToRows,
    type DiffLine,
    type DiffOp,
    isCoarseDiff,
  } from '$lib/diff'
  import { flash } from '$lib/flash.svelte'
  import { isTypingTarget } from '$lib/shortcuts'
  import { ChevronDown, ChevronUp, Check, Copy, FoldVertical } from '@lucide/svelte'

  interface Props {
    left: string
    right: string
    /** Names the two sides — "This object" / "billing/web on staging", say —
        shown as column headers and folded into the copy button's title. */
    leftLabel?: string
    rightLabel?: string
    /** Context lines kept around a change. Matches `unified()`'s own default. */
    context?: number
  }

  let { left, right, leftLabel = 'Left', rightLabel = 'Right', context = 3 }: Props = $props()

  const ops = $derived(diffLines(splitLines(left), splitLines(right)))
  const hunkList = $derived(hunks(ops, context))
  const changedLines = $derived(ops.filter((op) => op.kind !== 'equal').length)
  const identical = $derived(hunkList.length === 0)
  /** True when the differing region was too large for a line-by-line answer
      and diffLines reported it as one block replacement — said out loud
      below, because a coarse diff passed off as a fine one misleads. */
  const coarse = $derived(isCoarseDiff(splitLines(left), splitLines(right)))

  /** Folds unchanged runs behind a "N unchanged lines hidden" divider. On by
      default: the point of a diff is the part that differs, and a manifest
      that agrees on 400 lines and disagrees on three should not need 400
      lines of scrolling to find them. */
  let hideUnchanged = $state(true)
  const segments = $derived(foldSegments(ops, context))

  // --- Responsive layout -----------------------------------------------------
  //
  // Measured against THIS component's own width, not the window's — the same
  // reasoning DetailList.svelte's own ResizeObserver follows: this pane can
  // be the drawer's YAML tab (narrow), the maximised dialog (wide), or a
  // side panel of CompareDialog, and only its own box says which.

  let root = $state<HTMLDivElement | null>(null)
  /** Below this, two columns of manifest are narrower than is worth reading —
      about what two `field`-width panes need side by side with a gutter and
      line numbers each. */
  const NARROW_THRESHOLD_PX = 640
  let narrow = $state(false)

  $effect(() => {
    if (!root) return
    const observer = new ResizeObserver((entries) => {
      const width = entries[0]?.contentRect.width ?? root!.clientWidth
      narrow = width < NARROW_THRESHOLD_PX
    })
    observer.observe(root)
    return () => observer.disconnect()
  })

  // --- Row models --------------------------------------------------------

  type SideRow =
    | { type: 'gap'; hidden: number }
    | { type: 'line'; hunk: number; hunkStart: boolean; left: DiffLine; right: DiffLine }

  type UnifiedRow =
    | { type: 'gap'; hidden: number }
    | { type: 'line'; hunk: number; hunkStart: boolean; op: DiffOp }

  /**
   * Both derivations below walk the SAME gap/hunk segments unified() and the
   * fold agree on, building one row per rendered line or one divider row per
   * folded gap. `hunk` numbers a row by which REAL hunk (from `hunkList`) it
   * belongs to, whether or not folding is currently hiding anything, so n/p
   * navigation works identically either way; a row inside a shown gap
   * carries the PREVIOUS hunk's number, since there is no divider there to
   * jump to.
   *
   * Two copies rather than one generic walk over `DiffSegment[]`: the side
   * view zips each hunk's ops into paired left/right lines first (see
   * `zipOpsToRows`) while the unified view renders the ops directly, so the
   * per-item shape genuinely differs and a shared walker would need to take
   * the zipping as a parameter anyway — at which point it is naming a
   * function to avoid writing the loop twice, not sharing behaviour.
   */
  const sideRows = $derived.by((): SideRow[] => {
    const rows: SideRow[] = []
    let hunkIndex = -1
    for (const seg of segments) {
      if (seg.kind === 'gap') {
        if (hideUnchanged) {
          if (seg.ops.length > 0) rows.push({ type: 'gap', hidden: seg.ops.length })
          continue
        }
        const { left: l, right: r } = zipOpsToRows(seg.ops)
        for (let i = 0; i < l.length; i++) {
          rows.push({ type: 'line', hunk: Math.max(hunkIndex, 0), hunkStart: false, left: l[i], right: r[i] })
        }
        continue
      }
      hunkIndex++
      const { left: l, right: r } = zipOpsToRows(seg.ops)
      for (let i = 0; i < l.length; i++) {
        rows.push({ type: 'line', hunk: hunkIndex, hunkStart: i === 0, left: l[i], right: r[i] })
      }
    }
    return rows
  })

  const unifiedRows = $derived.by((): UnifiedRow[] => {
    const rows: UnifiedRow[] = []
    let hunkIndex = -1
    for (const seg of segments) {
      if (seg.kind === 'gap') {
        if (hideUnchanged) {
          if (seg.ops.length > 0) rows.push({ type: 'gap', hidden: seg.ops.length })
          continue
        }
        for (const op of seg.ops) rows.push({ type: 'line', hunk: Math.max(hunkIndex, 0), hunkStart: false, op })
        continue
      }
      hunkIndex++
      seg.ops.forEach((op, i) => rows.push({ type: 'line', hunk: hunkIndex, hunkStart: i === 0, op }))
    }
    return rows
  })

  // --- Row styling ---------------------------------------------------------
  //
  // From the app's own success/error/warning tokens — see this file's own
  // header comment for why no new colour exists here.

  const ROW_BG: Record<DiffLine['kind'], string> = {
    same: '',
    changed: 'bg-warning-container/20',
    added: 'bg-success-container/20',
    removed: 'bg-error-container/20',
    empty: 'bg-surface-container-low/60',
  }

  const GUTTER_TEXT: Record<DiffLine['kind'], string> = {
    same: 'text-on-surface-variant/50',
    changed: 'text-on-warning-container/80',
    added: 'text-on-success-container/80',
    removed: 'text-on-error-container/80',
    empty: 'text-on-surface-variant/30',
  }

  const OP_BG: Record<DiffOp['kind'], string> = {
    equal: '',
    delete: 'bg-error-container/20',
    insert: 'bg-success-container/20',
  }

  const OP_GUTTER: Record<DiffOp['kind'], string> = {
    equal: 'text-on-surface-variant/50',
    delete: 'text-on-error-container/80',
    insert: 'text-on-success-container/80',
  }

  function marker(kind: DiffLine['kind'], side: 'left' | 'right'): string {
    if (kind === 'added') return '+'
    if (kind === 'removed') return '-'
    if (kind === 'changed') return side === 'left' ? '-' : '+'
    return ' '
  }

  function opMarker(kind: DiffOp['kind']): string {
    return kind === 'insert' ? '+' : kind === 'delete' ? '-' : ' '
  }

  // --- Hunk navigation (n / p) ---------------------------------------------

  function scrollToHunk(index: number): void {
    if (!root || hunkList.length === 0) return
    const clamped = ((index % hunkList.length) + hunkList.length) % hunkList.length
    const target = root.querySelector<HTMLElement>(`[data-hunk-start="${clamped}"]`)
    target?.scrollIntoView({ block: 'center' })
    target?.focus()
  }

  /** The hunk nearest the top of the current scroll position — n/p starts
      counting from HERE rather than from hunk 0 every time, so repeatedly
      pressing n after scrolling by hand continues from where the pane
      actually is. */
  function currentHunk(): number {
    if (!root) return -1
    const rows = [...root.querySelectorAll<HTMLElement>('[data-hunk-start]')]
    const top = root.getBoundingClientRect().top
    let current = -1
    for (const row of rows) {
      if (row.getBoundingClientRect().top - top > 4) break
      current = Number(row.dataset.hunkStart)
    }
    return current
  }

  function onKeydown(event: KeyboardEvent): void {
    if (isTypingTarget(event.target) || event.metaKey || event.ctrlKey || event.altKey) return
    if (event.key !== 'n' && event.key !== 'p') return
    if (hunkList.length === 0) return
    // Only when the pointer or focus is actually over this pane — n/p are
    // bare letters, and stealing them while an operator is typing a name
    // into CompareDialog's picker beside this view would be exactly the
    // "seventeen listeners on one target" mistake escape.ts exists to avoid
    // for Escape. This view has no such stack to join, so it settles for the
    // narrower guard: react only while the pointer is over it.
    if (!root?.matches(':hover') && document.activeElement !== root && !root?.contains(document.activeElement)) {
      return
    }
    event.preventDefault()
    const from = currentHunk()
    scrollToHunk(event.key === 'n' ? from + 1 : from - 1)
  }

  // --- Copy ------------------------------------------------------------------

  const copied = flash(1500)

  async function copyUnified(): Promise<void> {
    const text = unified(left, right, context)
    await navigator.clipboard.writeText(text)
    copied.show()
  }

  $effect(() => () => copied.cancel())
</script>

<svelte:window onkeydown={onKeydown} />

<div bind:this={root} class="flex h-full flex-col" role="region" aria-label="Diff between {leftLabel} and {rightLabel}">
  <!-- Toolbar: hunk summary and navigation, folding, copy. -->
  {#if coarse}
    <p class="shrink-0 border-b border-warning/30 bg-warning-container/40 px-3 py-1.5 text-body-small text-on-warning-container" role="status">
      The differing region is too large for a line-by-line comparison, so it is shown as one block removed and one block added.
    </p>
  {/if}
  <div class="flex h-10 shrink-0 items-center gap-1 border-b border-outline-variant/60 bg-surface-container-low/50 px-2">
    <p class="px-1 text-body-small text-on-surface-variant/70">
      {#if identical}
        No differences
      {:else}
        {hunkList.length} {hunkList.length === 1 ? 'hunk' : 'hunks'}, {changedLines}
        {changedLines === 1 ? 'line' : 'lines'} changed
      {/if}
    </p>

    {#if !identical}
      <div class="flex items-center">
        <button
          type="button"
          onclick={() => scrollToHunk(currentHunk() - 1)}
          aria-label="Previous change (p)"
          title="Previous change (p)"
          class="state-layer grid size-7 shrink-0 place-items-center rounded-sm text-on-surface-variant
                 transition-colors duration-100 hover:bg-surface-container hover:text-on-surface"
        >
          <ChevronUp class="size-4" strokeWidth={1.8} />
        </button>
        <button
          type="button"
          onclick={() => scrollToHunk(currentHunk() + 1)}
          aria-label="Next change (n)"
          title="Next change (n)"
          class="state-layer grid size-7 shrink-0 place-items-center rounded-sm text-on-surface-variant
                 transition-colors duration-100 hover:bg-surface-container hover:text-on-surface"
        >
          <ChevronDown class="size-4" strokeWidth={1.8} />
        </button>
      </div>
    {/if}

    <div class="ml-auto flex items-center gap-1.5">
      <button
        type="button"
        onclick={() => (hideUnchanged = !hideUnchanged)}
        aria-pressed={hideUnchanged}
        aria-label="Hide unchanged lines"
        title={hideUnchanged ? 'Hiding unchanged lines — click to show everything' : 'Show unchanged lines folded'}
        class="state-layer grid size-7 shrink-0 place-items-center rounded-sm transition-colors duration-100
               {hideUnchanged
          ? 'bg-primary/14 text-primary'
          : 'text-on-surface-variant hover:bg-surface-container hover:text-on-surface'}"
      >
        <FoldVertical class="size-4" strokeWidth={1.8} />
      </button>

      <div class="mx-1 h-5 w-px bg-outline-variant/40"></div>

      <button
        type="button"
        onclick={copyUnified}
        disabled={identical}
        aria-label="Copy unified diff"
        title={copied.on ? 'Copied' : 'Copy unified diff'}
        class="state-layer grid size-7 shrink-0 place-items-center rounded-sm transition-colors duration-100
               {copied.on ? 'text-gauge-normal' : 'text-on-surface-variant hover:bg-surface-container hover:text-on-surface'}
               disabled:pointer-events-none disabled:opacity-30"
      >
        {#if copied.on}
          <Check class="size-4" strokeWidth={1.8} />
        {:else}
          <Copy class="size-4" strokeWidth={1.8} />
        {/if}
      </button>
    </div>
  </div>

  <!-- Body -->
  <div class="min-h-0 flex-1 overflow-auto font-mono text-body-small">
    {#if identical}
      <div class="flex h-full flex-col items-center justify-center gap-2 p-4 text-on-surface-variant/60">
        <Check class="size-8" strokeWidth={1.2} />
        <p class="text-body-medium">These manifests are identical.</p>
      </div>
    {:else if narrow}
      <!-- Unified: one column, line numbers for both sides side by side in
           the gutter since a removed line has no `right` number and an
           added line has no `left` one. -->
      <div class="grid" style="grid-template-columns: 3rem 3rem 1.5rem 1fr;">
        {#each unifiedRows as row, index (index)}
          {#if row.type === 'gap'}
            <button
              type="button"
              onclick={() => (hideUnchanged = false)}
              class="col-span-4 flex items-center gap-2 border-y border-outline-variant/40
                     bg-surface-container-low px-3 py-1 text-left text-body-small text-on-surface-variant/70
                     hover:bg-surface-container"
            >
              <span class="h-px flex-1 bg-outline-variant/40"></span>
              {row.hidden} unchanged {row.hidden === 1 ? 'line' : 'lines'} hidden
              <span class="h-px flex-1 bg-outline-variant/40"></span>
            </button>
          {:else}
            {@const bg = OP_BG[row.op.kind]}
            {@const gutter = OP_GUTTER[row.op.kind]}
            <!--
              svelte-ignore a11y_no_noninteractive_tabindex

              The tabindex here is always -1 or absent, never 0 — the same
              programmatic-focus-without-a-tab-stop pattern $lib/modal.ts uses
              on the dialog panel itself. scrollToHunk() needs an element it
              CAN call .focus() on; a plain <div> cannot be focused at all
              without one. The linter cannot see through the ternary that
              keeps it out of the tab order, which is what it is actually
              flagging.
            -->
            <div
              data-hunk-start={row.hunkStart ? row.hunk : undefined}
              tabindex={row.hunkStart ? -1 : undefined}
              class="col-span-4 grid grid-cols-subgrid {bg}"
            >
              <span class="select-none border-r border-outline-variant/30 px-2 text-right {gutter}">
                {row.op.aLine !== null ? row.op.aLine + 1 : ''}
              </span>
              <span class="select-none border-r border-outline-variant/30 px-2 text-right {gutter}">
                {row.op.bLine !== null ? row.op.bLine + 1 : ''}
              </span>
              <span class="select-none border-r border-outline-variant/30 text-center {gutter}">
                {opMarker(row.op.kind)}
              </span>
              <span class="min-w-0 px-2 whitespace-pre" data-selectable>{row.op.text}</span>
            </div>
          {/if}
        {/each}
      </div>
    {:else}
      <!-- Side by side: independent line numbers per column, a synced grid
           because sideRows() padded the shorter column to match. -->
      <div class="grid grid-cols-2">
        <p class="border-r border-b border-outline-variant/40 bg-surface-container-low px-3 py-1 text-body-small font-semibold text-on-surface">
          {leftLabel}
        </p>
        <p class="border-b border-outline-variant/40 bg-surface-container-low px-3 py-1 text-body-small font-semibold text-on-surface">
          {rightLabel}
        </p>

        {#each sideRows as row, index (index)}
          {#if row.type === 'gap'}
            <button
              type="button"
              onclick={() => (hideUnchanged = false)}
              class="col-span-2 flex items-center gap-2 border-y border-outline-variant/40
                     bg-surface-container-low px-3 py-1 text-left text-body-small text-on-surface-variant/70
                     hover:bg-surface-container"
            >
              <span class="h-px flex-1 bg-outline-variant/40"></span>
              {row.hidden} unchanged {row.hidden === 1 ? 'line' : 'lines'} hidden — click to show
              <span class="h-px flex-1 bg-outline-variant/40"></span>
            </button>
          {:else}
            <!-- svelte-ignore a11y_no_noninteractive_tabindex — see the
                 identical case in the unified branch above. -->
            <div
              data-hunk-start={row.hunkStart ? row.hunk : undefined}
              tabindex={row.hunkStart ? -1 : undefined}
              class="grid grid-cols-[3rem_1.5rem_1fr] border-r border-outline-variant/30 {ROW_BG[row.left.kind]}"
            >
              <span class="select-none border-r border-outline-variant/30 px-2 text-right {GUTTER_TEXT[row.left.kind]}">
                {row.left.lineNumber ?? ''}
              </span>
              <span class="select-none border-r border-outline-variant/30 text-center {GUTTER_TEXT[row.left.kind]}">
                {marker(row.left.kind, 'left')}
              </span>
              <span class="min-w-0 px-2 whitespace-pre" data-selectable>{row.left.text}</span>
            </div>
            <div class="grid grid-cols-[3rem_1.5rem_1fr] {ROW_BG[row.right.kind]}">
              <span class="select-none border-r border-outline-variant/30 px-2 text-right {GUTTER_TEXT[row.right.kind]}">
                {row.right.lineNumber ?? ''}
              </span>
              <span class="select-none border-r border-outline-variant/30 text-center {GUTTER_TEXT[row.right.kind]}">
                {marker(row.right.kind, 'right')}
              </span>
              <span class="min-w-0 px-2 whitespace-pre" data-selectable>{row.right.text}</span>
            </div>
          {/if}
        {/each}
      </div>
    {/if}
  </div>
</div>
