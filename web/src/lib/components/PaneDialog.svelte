<!--
  One pane of the drawer, given the whole window.

  The drawer is a column beside a table, which is the right shape for glancing
  at an object and the wrong one for reading a five-hundred-line manifest or
  following a busy log. Maximising moves the same pane into a dialog with room
  to work in — the same toolbar, the same controls, the same state — so it is
  the same surface at a different size rather than a second implementation of
  it.

  This began as the edit dialog, which was already a large modal containing a
  YAML pane. Making it generic cost almost nothing and removed the oddity that
  editing was the only thing worth a bigger window.

  It renders whatever it is handed and owns none of it: the state stays in the
  drawer, so maximising mid-edit carries the draft across and restoring puts
  it back.
-->
<script lang="ts">
  import type { Snippet } from 'svelte'
  import type { Component } from 'svelte'
  import { Minimize2, X } from '@lucide/svelte'

  interface Props {
    open: boolean
    /** The object's kind icon, matching the drawer's own header. */
    icon?: Component
    /** The kind, as the first part of the path. */
    kind?: string
    /** The object's name. */
    name?: string
    /** Names the dialog for assistive technology. */
    label: string
    onclose: () => void
    children: Snippet
    /** Actions along the bottom, for a pane that has any. */
    footer?: Snippet
  }

  let { open, icon: Icon, kind, name, label, onclose, children, footer }: Props = $props()

  function onKeydown(event: KeyboardEvent): void {
    // Only when nothing nearer has claimed it — a search box with something in
    // it stops the event before it reaches here.
    if (event.key === 'Escape' && open) onclose()
  }
</script>

<svelte:window onkeydown={onKeydown} />

{#if open}
  <!-- Dimmed, not blurred, for the same reason the drawer's scrim is: what is
       behind is the context this was opened from. -->
  <button
    type="button"
    aria-label="Close"
    tabindex="-1"
    class="fixed inset-0 z-[60] cursor-default bg-scrim/40"
    onclick={onclose}
  ></button>

  <div
    class="fixed inset-6 z-[70] flex flex-col overflow-hidden rounded-sm border
           border-outline-variant bg-surface-container-high shadow-level-3"
    role="dialog"
    aria-modal="true"
    aria-label={label}
  >
    <!-- The same identity the drawer shows, so it is obvious this is that
         object enlarged rather than a new window about something else. -->
    <header class="flex shrink-0 items-center gap-3 border-b border-outline-variant/60 px-4 py-3">
      {#if Icon}
        <Icon class="size-5 shrink-0 text-on-surface-variant" strokeWidth={1.8} />
      {/if}
      <div class="min-w-0">
        {#if name}
          <h2 class="truncate text-title-medium font-semibold text-on-surface">{name}</h2>
        {/if}
        {#if kind}
          <p class="text-body-small text-on-surface-variant/70">{kind}</p>
        {/if}
      </div>

      <div class="ml-auto flex items-center gap-0.5">
        <button
          type="button"
          onclick={onclose}
          aria-label="Restore"
          title="Restore to the side panel"
          class="state-layer grid size-8 shrink-0 place-items-center rounded-full
                 text-on-surface-variant transition-colors duration-100
                 hover:bg-surface-container hover:text-on-surface"
        >
          <Minimize2 class="size-4" strokeWidth={1.8} />
        </button>
        <button
          type="button"
          onclick={onclose}
          aria-label="Close"
          title="Close"
          class="state-layer grid size-8 shrink-0 place-items-center rounded-full
                 text-on-surface-variant transition-colors duration-100
                 hover:bg-surface-container hover:text-on-surface"
        >
          <X class="size-4" strokeWidth={1.8} />
        </button>
      </div>
    </header>

    <div class="min-h-0 flex-1 bg-surface-container-lowest">
      {@render children()}
    </div>

    {#if footer}
      <div
        class="flex shrink-0 items-center justify-end gap-3 border-t border-outline-variant/60 px-4 py-3"
      >
        {@render footer()}
      </div>
    {/if}
  </div>
{/if}
