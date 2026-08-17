<!--
  Placeholder for a view with nothing to show.

  An empty list and a failed load look identical if both render as blank space,
  so every list in K8Sense distinguishes them explicitly — this component is
  the "nothing here, and that is fine" half.
-->
<script lang="ts">
  import type { Snippet } from 'svelte'

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
  <svg
    class="size-10 text-on-surface-variant opacity-60"
    viewBox="0 0 24 24"
    fill="none"
    stroke="currentColor"
    stroke-width="1.5"
    stroke-linecap="round"
    stroke-linejoin="round"
    aria-hidden="true"
  >
    <path d="M21 8 12 3 3 8v8l9 5 9-5V8Z" />
    <path d="m3 8 9 5 9-5M12 13v8" />
  </svg>

  <h2 class="text-title-medium text-on-surface">{title}</h2>

  {#if description}
    <p class="max-w-md text-body-medium text-on-surface-variant">{description}</p>
  {/if}

  {#if action}
    <div class="mt-2">{@render action()}</div>
  {/if}
</div>
