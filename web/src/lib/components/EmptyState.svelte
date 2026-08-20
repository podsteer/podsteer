<!--
  Placeholder for a view with nothing to show.

  An empty list and a failed load look identical if both render as blank space,
  so every list in PodSteer distinguishes them explicitly — this component is
  the "nothing here, and that is fine" half.
-->
<script lang="ts">
  import type { Snippet } from 'svelte'
  import { Inbox } from '@lucide/svelte'

  interface Props {
    title: string
    description?: string
    /** Optional action, e.g. a button that changes the filter. */
    action?: Snippet
    class?: string
  }

  let { title, description, action, class: className = '' }: Props = $props()
</script>

<div
  class="flex flex-col items-center justify-center gap-3 px-6 py-16 text-center {className}"
>
  <div class="grid size-14 place-items-center rounded-2xl bg-surface-container">
    <Inbox class="size-7 text-on-surface-variant/50" strokeWidth={1.3} />
  </div>

  <h2 class="text-title-medium font-semibold text-on-surface">{title}</h2>

  {#if description}
    <p class="max-w-md text-body-medium text-on-surface-variant/70">{description}</p>
  {/if}

  {#if action}
    <div class="mt-2">{@render action()}</div>
  {/if}
</div>
