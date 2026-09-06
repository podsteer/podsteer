<!--
  What a detail row can do, behind one control.

  A row's trailing controls used to be a cluster of icons — a reveal, an
  information note, an expander — which had to be learnt one at a time and
  changed shape from row to row. Anything that is not "show me the rest of
  this" is now one menu: the reader sees two controls on every row, always the
  same two, and finds out what a particular row offers by opening it.

  Shown when the pointer is on its row, and kept shown while it is OPEN —
  otherwise moving the pointer off the row to reach the menu would fade the
  control the menu is hanging from. The popover is a descendant of the row, so
  hovering it keeps the row hovered; the override covers the case where the
  menu was opened and the pointer then left entirely.

  Closed by an outside pointer or by Escape, like every other menu here. It
  does NOT close on scroll: the panel scrolls under the pointer while somebody
  reads the menu, and a menu that vanishes when the list moves is a menu that
  cannot be used with a trackpad.
-->
<script module lang="ts">
  /**
   * Which row's menu is open, across every list in the application.
   *
   * ONE VALUE, NOT ONE PER MENU. Each menu used to keep its own, and the
   * outside-click handler asked only whether the pointer had landed in *a*
   * row menu — so opening a second one left the first standing, and a panel
   * could end up with four open at once. A single value cannot hold two
   * answers, which is the whole fix: opening one closes the last by
   * construction rather than by every menu remembering to.
   */
  let openMenu = $state<symbol | null>(null)
</script>

<script lang="ts">
  import { flash } from '$lib/flash.svelte'
  import { menuKeys } from '$lib/menuKeys'
  import { escapeLayer, type EscapeClaim } from '$lib/escape'
  import type { Component } from 'svelte'
  import { Check, Copy, Eye, EyeOff, Link2, MoreVertical, Pencil } from '@lucide/svelte'

  export interface RowAction {
    label: string
    /**
     * What sort of thing this does.
     *
     * Chooses the icon, and singles out the copy — which confirms itself in
     * place before the menu closes, the way the status bar's share menu does.
     */
    kind?: 'reference' | 'copy' | 'reveal' | 'hide' | 'edit'
    onclick: () => void
  }

  const ICONS: Record<string, Component<{ class?: string; strokeWidth?: number }>> = {
    reference: Link2,
    copy: Copy,
    reveal: Eye,
    hide: EyeOff,
    edit: Pencil,
  }

  interface Props {
    /** What this row offers. An empty list renders no control at all. */
    actions: RowAction[]
    /** Names the row, for the control's accessible label. */
    label: string
    /**
     * Whether the control shows without the row being hovered.
     *
     * TRUE IN A TABLE, where it is a column of its own: a column whose
     * contents appear only under the pointer reads as an empty column, and
     * somebody looking for the control cannot find it without sweeping the
     * table. FALSE in a detail pane, where the control sits beside a value
     * rather than in a column, and one dot per row would be noise on every
     * row of every pane.
     */
    persistent?: boolean
  }

  let { actions, label, persistent = false }: Props = $props()

  /**
   * Where the menu goes, in WINDOW coordinates.
   *
   * IT HAS TO ESCAPE THE TABLE, which is why this is measured rather than
   * left to `position: absolute`. A list row sits inside the table's
   * horizontal scrollport, and a scrollport clips: anchored to the cell, the
   * menu was cut off at the table's right edge — the one place it always
   * opens, since its column is pinned there. No z-index helps, because
   * clipping is not stacking.
   *
   * Right-aligned to the control, because it opens at the right edge of a row
   * and a menu growing rightwards from there would leave the window. Flipped
   * above only when below would not fit, and clamped so neither end can land
   * off screen.
   */
  const MENU_WIDTH = 192
  const MENU_GAP = 4
  const MENU_MARGIN = 8

  let trigger = $state<HTMLButtonElement | null>(null)
  let menuHeight = $state(0)
  /** Bumped while open so a scroll or a resize re-measures. */
  let moved = $state(0)

  const placement = $derived.by(() => {
    void moved
    if (!open || !trigger) return null

    const rect = trigger.getBoundingClientRect()
    const below = rect.bottom + MENU_GAP
    const fitsBelow = below + menuHeight <= window.innerHeight - MENU_MARGIN
    const top = fitsBelow ? below : Math.max(MENU_MARGIN, rect.top - MENU_GAP - menuHeight)

    const left = Math.min(
      Math.max(MENU_MARGIN, rect.right - MENU_WIDTH),
      Math.max(MENU_MARGIN, window.innerWidth - MENU_WIDTH - MENU_MARGIN),
    )

    return { top, left }
  })

  /**
   * A menu positioned against the window does not travel with the row it
   * belongs to, so a scroll would leave it behind. Re-measuring is cheaper
   * than the alternative and only ever runs while one menu is open.
   */
  $effect(() => {
    if (!open) return

    const remeasure = (): void => {
      moved += 1
    }
    window.addEventListener('scroll', remeasure, true)
    window.addEventListener('resize', remeasure)
    return () => {
      window.removeEventListener('scroll', remeasure, true)
      window.removeEventListener('resize', remeasure)
    }
  })

  /** This menu's identity, for the one open at a time. */
  const id = Symbol('row-menu')
  const open = $derived(openMenu === id)

  let node = $state<HTMLElement | null>(null)
  const copied = flash(900)

  function choose(action: RowAction): void {
    action.onclick()

    // Copying gives nothing back on its own — the clipboard is silent and the
    // row looks identical — so it says so before closing. Everything else has
    // a visible result: a panel changes, or a value appears.
    if (action.kind === 'copy') {
      copied.show(() => {
        if (openMenu === id) openMenu = null
      })
      return
    }
    openMenu = null
  }

  function onWindowPointerDown(event: PointerEvent): void {
    if (!open) return
    // THIS menu's element, not any row menu. Asking whether the pointer
    // landed in a row menu was what let a click on another row's control
    // leave this one open.
    if (!node?.contains(event.target as Node)) openMenu = null
  }

  function onKeydown(event: KeyboardEvent): void {
    if (event.key !== 'Escape' || !open) return
    // ONE ESCAPE, ONE LAYER. A menu open inside the detail drawer used to see
    // one keystroke close the menu AND the drawer — and the drawer's Escape
    // discards an unsaved YAML draft, so a keystroke aimed at a menu could
    // throw somebody's work away. stopPropagation cannot help: every one of
    // these listeners is on the window, so nothing propagates between them.
    if (!escape?.owns()) return
    openMenu = null
  }

  /**
   * Window listeners, ONLY WHILE THIS MENU IS OPEN.
   *
   * They were attached unconditionally, and there is one of these per detail
   * ROW: a sixty-row pod pane installed a hundred and twenty window
   * listeners, and every keystroke anywhere in the application was dispatched
   * to sixty handlers that immediately returned. Only one row menu can be
   * open at a time, so at most two of these ever exist now.
   *
   * Attached from an effect rather than a conditional `<svelte:window>`,
   * which Svelte does not allow inside a block.
   */
  $effect(() => {
    if (!open) return

    window.addEventListener('pointerdown', onWindowPointerDown)
    window.addEventListener('keydown', onKeydown)
    return () => {
      window.removeEventListener('pointerdown', onWindowPointerDown)
      window.removeEventListener('keydown', onKeydown)
    }
  })

  /**
   * Escape belongs to the innermost open layer. See $lib/escape.
   */
  let escape = $state<EscapeClaim | null>(null)
  $effect(() => {
    if (!open) return
    const held = escapeLayer()
    escape = held
    return () => {
      held.release()
      escape = null
    }
  })

  // Nothing left running behind a component that has gone away.
  $effect(() => () => copied.cancel())
</script>



{#if actions.length > 0}
  <div class="relative" data-row-menu bind:this={node}>
    <!--
      TWO WAYS IN TO THE SAME "row/row" GROUP. `group-data-[row-hover]/row` is
      for a caller like DetailList, where the row is two grid siblings (a dt
      and a dd) with no element wrapping both — hover has to be tracked in
      script and published as a `data-row-hover` attribute, because CSS
      `:hover` has nothing to attach to that covers the whole row. A table
      `<tr>` has no such problem: it is one element, so `group-hover/row`
      answers the same question for free. Both are on the button so either
      caller works without this component needing to know which kind of row
      it is in.
    -->
    <!--
      PERSISTENT DRAWS ITSELF WITH COLOUR, NOT OPACITY. Hidden entirely until
      the row is hovered, the control is unfindable in a column of its own —
      the column reads as empty and somebody has to sweep the table to
      discover there was anything in it. So in a table it is always there, dim
      at rest and brighter under the pointer, which says "you can press this"
      without competing with the row's own text. In a detail pane it keeps the
      old behaviour, because there it sits beside a value rather than in a
      column and one dot per row would be noise.
    -->
    <button
      bind:this={trigger}
      type="button"
      onclick={() => (openMenu = open ? null : id)}
      aria-expanded={open}
      aria-label="More for {label}"
      title="More"
      class="grid size-5 shrink-0 cursor-pointer place-items-center rounded-full
             transition-all duration-100 hover:text-on-surface
             group-data-[row-hover]/row:opacity-100 group-hover/row:opacity-100
             group-focus-within/row:opacity-100 focus-visible:opacity-100
             {persistent ? 'opacity-100' : ''}
             {persistent && !open ? 'text-on-surface-variant/45 group-hover/row:text-on-surface-variant' : ''}
             {open ? 'text-on-surface opacity-100' : persistent ? '' : 'text-on-surface-variant/60 opacity-0'}"
    >
      <MoreVertical class="size-3.5" strokeWidth={2} />
    </button>

    {#if open}
      <!-- Anchored to the right, because the control sits at the right edge
           of a panel and a menu opening leftward from it stays inside. -->
      <!-- The same menu the status bar's "Share on…" opens: same width, same
           ground, same item metrics and the same muted leading icon. Two
           dropdowns in one application should not be two designs. -->
      <div
        bind:clientHeight={menuHeight}
        style={placement ? `top: ${placement.top}px; left: ${placement.left}px;` : 'visibility: hidden;'}
        class="fixed z-50 w-48 overflow-hidden rounded-sm
               border border-outline-variant/60 bg-surface-container-high py-1.5 shadow-level-2"
        role="menu"
        aria-label="More for {label}"
        use:menuKeys={{ onclose: () => (openMenu = null) }}
      >
        {#each actions as action (action.label)}
          {@const Icon = ICONS[action.kind ?? 'reference']}
          <button
            type="button"
            role="menuitem"
            onclick={() => choose(action)}
            class="state-layer flex w-full cursor-pointer items-center gap-2.5 px-3 py-1.5
                   text-left text-body-medium transition-colors duration-75
                   hover:bg-surface-container-highest
                   {copied.on && action.kind === 'copy' ? 'text-success' : 'text-on-surface'}"
          >
            {#if copied.on && action.kind === 'copy'}
              <Check class="size-3.5 shrink-0" strokeWidth={2.5} />
              Copied!
            {:else}
              <Icon class="size-3.5 shrink-0 text-on-surface-variant/70" />
              {action.label}
            {/if}
          </button>
        {/each}
      </div>
    {/if}
  </div>
{/if}
