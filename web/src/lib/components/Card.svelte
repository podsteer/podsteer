<!--
  MD3 card.

  The three variants differ only in how they separate themselves from the
  background: a filled card raises the surface tone, an outlined card draws a
  hairline, an elevated card casts a shadow. Nesting a filled card inside a
  filled card is what the container ladder in app.css exists for.
-->
<script lang="ts">
  import type { Snippet } from 'svelte'

  type Variant = 'filled' | 'outlined' | 'elevated'

  interface Props {
    variant?: Variant
    /** Renders the card as an interactive element with a state layer. */
    interactive?: boolean
    /** Marks the card as the currently chosen option. */
    selected?: boolean
    onclick?: (event: MouseEvent) => void
    class?: string
    children: Snippet
  }

  let {
    variant = 'filled',
    interactive = false,
    selected = false,
    onclick,
    class: className = '',
    children,
  }: Props = $props()

  const VARIANT_CLASSES: Record<Variant, string> = {
    filled: 'bg-surface-container-low',
    outlined: 'bg-surface border border-outline-variant',
    elevated: 'bg-surface-container-low shadow-level-1',
  }

  const base = $derived(
    `rounded-sm text-on-surface transition-[background-color,box-shadow] duration-150 ease-standard ${VARIANT_CLASSES[variant]}`,
  )
  const selection = $derived(selected ? 'ring-2 ring-primary' : '')
</script>

{#if interactive}
  <button
    type="button"
    {onclick}
    aria-pressed={selected}
    class="state-layer w-full text-left {base} {selection} hover:shadow-level-1 {className}"
  >
    {@render children()}
  </button>
{:else}
  <div class="{base} {selection} {className}">
    {@render children()}
  </div>
{/if}
