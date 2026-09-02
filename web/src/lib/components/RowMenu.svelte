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
  import type { Component } from 'svelte'
  import { Check, Copy, Eye, EyeOff, Link2, MoreVertical } from '@lucide/svelte'

  export interface RowAction {
    label: string
    /**
     * What sort of thing this does.
     *
     * Chooses the icon, and singles out the copy — which confirms itself in
     * place before the menu closes, the way the status bar's share menu does.
     */
    kind?: 'reference' | 'copy' | 'reveal' | 'hide'
    onclick: () => void
  }

  const ICONS: Record<string, Component<{ class?: string; strokeWidth?: number }>> = {
    reference: Link2,
    copy: Copy,
    reveal: Eye,
    hide: EyeOff,
  }

  interface Props {
    /** What this row offers. An empty list renders no control at all. */
    actions: RowAction[]
    /** Names the row, for the control's accessible label. */
    label: string
  }

  let { actions, label }: Props = $props()

  /** This menu's identity, for the one open at a time. */
  const id = Symbol('row-menu')
  const open = $derived(openMenu === id)

  let node = $state<HTMLElement | null>(null)
  let copied = $state(false)

  function choose(action: RowAction): void {
    action.onclick()

    // Copying gives nothing back on its own — the clipboard is silent and the
    // row looks identical — so it says so before closing. Everything else has
    // a visible result: a panel changes, or a value appears.
    if (action.kind === 'copy') {
      copied = true
      setTimeout(() => {
        copied = false
        if (openMenu === id) openMenu = null
      }, 900)
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
    if (event.key === 'Escape' && open) openMenu = null
  }
</script>

<svelte:window onpointerdown={onWindowPointerDown} onkeydown={onKeydown} />

{#if actions.length > 0}
  <div class="relative" data-row-menu bind:this={node}>
    <button
      type="button"
      onclick={() => (openMenu = open ? null : id)}
      aria-expanded={open}
      aria-label="More for {label}"
      title="More"
      class="grid size-5 shrink-0 cursor-pointer place-items-center rounded-full
             transition-all duration-100 hover:text-on-surface
             group-hover/row:opacity-100 group-focus-within/row:opacity-100
             focus-visible:opacity-100
             {open ? 'text-on-surface opacity-100' : 'text-on-surface-variant/60 opacity-0'}"
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
        class="absolute top-full right-0 z-30 mt-1 w-48 overflow-hidden rounded-sm
               border border-outline-variant/60 bg-surface-container-high py-1.5 shadow-level-2"
        role="menu"
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
                   {copied && action.kind === 'copy' ? 'text-success' : 'text-on-surface'}"
          >
            {#if copied && action.kind === 'copy'}
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
