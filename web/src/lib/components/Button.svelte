<!--
  Action button.

  Covers the four variants PodSteer uses. Each is a full literal class string in
  the map below rather than an interpolated one, because Tailwind resolves
  classes by scanning source text — a string assembled at runtime would not be
  generated into the stylesheet.

  The shape is a compact, small-radius rectangle rather than MD3's tall pill:
  this is a desktop tool, where smaller squarer buttons read as actions rather
  than touch targets. Icon buttons stay circular.
-->
<script lang="ts">
  import type { Snippet } from 'svelte'

  /** Button emphasis levels. */
  type Variant = 'filled' | 'tonal' | 'outlined' | 'text'

  interface Props {
    /** Emphasis level. Filled is the single most important action on a view. */
    variant?: Variant
    /** Native button type. */
    type?: 'button' | 'submit' | 'reset'
    disabled?: boolean
    /** Renders a progress state and blocks interaction. */
    loading?: boolean
    /** Accessible label, required when the content is not descriptive text. */
    label?: string
    onclick?: (event: MouseEvent) => void
    class?: string
    children: Snippet
  }

  let {
    variant = 'filled',
    type = 'button',
    disabled = false,
    loading = false,
    label,
    onclick,
    class: className = '',
    children,
  }: Props = $props()

  const VARIANT_CLASSES: Record<Variant, string> = {
    filled: 'bg-primary text-on-primary shadow-none hover:shadow-level-1',
    tonal: 'bg-secondary-container text-on-secondary-container',
    outlined: 'border border-outline text-primary bg-transparent',
    text: 'text-primary bg-transparent px-3',
  }

  const isInert = $derived(disabled || loading)
</script>

<button
  {type}
  {onclick}
  disabled={isInert}
  aria-label={label}
  aria-busy={loading}
  class="state-layer no-drag inline-flex h-8 shrink-0 items-center justify-center gap-2
         rounded-sm px-4 text-label-large whitespace-nowrap
         transition-[box-shadow,opacity] duration-150 ease-standard
         disabled:pointer-events-none disabled:opacity-38
         {VARIANT_CLASSES[variant]} {className}"
>
  {#if loading}
    <span
      class="size-4 shrink-0 animate-spin rounded-full border-2 border-current border-t-transparent"
      aria-hidden="true"
    ></span>
  {/if}
  {@render children()}
</button>
