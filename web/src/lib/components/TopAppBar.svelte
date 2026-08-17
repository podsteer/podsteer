<!--
  MD3 small top app bar, doubling as the window's drag region.

  With a hidden inset title bar on macOS the OS draws no chrome of its own, so
  this element is the only thing the operator can move the window by — hence
  the `drag-region` utility here and `no-drag` on every control inside it.
-->
<script lang="ts">
  import type { Snippet } from 'svelte'
  import { isMac } from '$lib/platform'

  interface Props {
    title: string
    /** Secondary line, e.g. the connected cluster. */
    subtitle?: string
    /** Trailing controls. */
    actions?: Snippet
  }

  let { title, subtitle, actions }: Props = $props()
</script>

<header
  class="drag-region flex h-16 shrink-0 items-center gap-4 border-b border-outline-variant
         bg-surface-container px-4 {isMac ? 'pl-20' : ''}"
>
  <div class="min-w-0 flex-1">
    <h1 class="truncate text-title-medium text-on-surface">{title}</h1>
    {#if subtitle}
      <p class="truncate text-body-small text-on-surface-variant">{subtitle}</p>
    {/if}
  </div>

  {#if actions}
    <div class="flex shrink-0 items-center gap-2">
      {@render actions()}
    </div>
  {/if}
</header>
