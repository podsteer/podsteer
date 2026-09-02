<!--
  What a detail row can do, behind one control.

  A row's trailing controls used to be a cluster of icons — a reveal, an
  information note, an expander — which had to be learnt one at a time and
  changed shape from row to row. Anything that is not "show me the rest of
  this" is now one menu: the reader sees two controls on every row, always the
  same two, and finds out what a particular row offers by opening it.

  Closed by an outside pointer or by Escape, like every other menu here. It
  does NOT close on scroll: the panel scrolls under the pointer while somebody
  reads the menu, and a menu that vanishes when the list moves is a menu that
  cannot be used with a trackpad.
-->
<script lang="ts">
  import { MoreVertical } from '@lucide/svelte'

  export interface RowAction {
    label: string
    /** Named so the menu can mark the one that reads a Secret. */
    kind?: 'reference' | 'copy' | 'reveal'
    onclick: () => void
  }

  interface Props {
    /** What this row offers. An empty list renders no control at all. */
    actions: RowAction[]
    /** Names the row, for the control's accessible label. */
    label: string
  }

  let { actions, label }: Props = $props()

  let open = $state(false)

  function choose(action: RowAction): void {
    open = false
    action.onclick()
  }

  function onWindowPointerDown(event: PointerEvent): void {
    if (!open) return
    const target = event.target as HTMLElement | null
    if (!target?.closest('[data-row-menu]')) open = false
  }

  function onKeydown(event: KeyboardEvent): void {
    if (event.key === 'Escape') open = false
  }
</script>

<svelte:window onpointerdown={onWindowPointerDown} onkeydown={onKeydown} />

{#if actions.length > 0}
  <div class="relative" data-row-menu>
    <button
      type="button"
      onclick={() => (open = !open)}
      aria-expanded={open}
      aria-label="More for {label}"
      title="More"
      class="state-layer grid size-5 shrink-0 place-items-center rounded-full
             text-on-surface-variant/60 transition-colors duration-100
             hover:bg-surface-container hover:text-on-surface
             {open ? 'bg-surface-container text-on-surface' : ''}"
    >
      <MoreVertical class="size-3.5" strokeWidth={2} />
    </button>

    {#if open}
      <!-- Anchored to the right, because the control sits at the right edge
           of a panel and a menu opening leftward from it stays inside. -->
      <div
        class="absolute top-full right-0 z-30 mt-1 w-44 rounded-sm border border-outline-variant/60
               bg-surface-container-high py-1 shadow-level-3"
        role="menu"
      >
        {#each actions as action (action.label)}
          <button
            type="button"
            role="menuitem"
            onclick={() => choose(action)}
            class="state-layer flex w-full items-center px-3 py-1.5 text-left text-body-small
                   text-on-surface transition-colors duration-75 hover:bg-surface-container-highest"
          >
            {action.label}
          </button>
        {/each}
      </div>
    {/if}
  </div>
{/if}
