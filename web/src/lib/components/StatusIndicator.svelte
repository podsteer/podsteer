<!--
  A tonal status dot with its label — the MD3 "assist chip" reduced to what a
  dense table row can afford.

  Colour is never the only carrier of meaning: the label is always rendered
  alongside, so the status survives a monochrome display and a colour-blind
  reader.
-->
<script lang="ts">
  import type { Tone } from '$lib/format'

  interface Props {
    /** Semantic tone driving the colour. */
    tone?: Tone
    /** The status text. */
    label: string
    /** Renders the dot only, with the label as an accessible name. */
    compact?: boolean
    /** Animates the dot, for states that are actively changing. */
    pulse?: boolean
    class?: string
  }

  let { tone = 'neutral', label, compact = false, pulse = false, class: className = '' }: Props =
    $props()

  const DOT_CLASSES: Record<Tone, string> = {
    success: 'bg-success',
    warning: 'bg-warning',
    error: 'bg-error',
    info: 'bg-primary',
    neutral: 'bg-outline',
  }

  const TEXT_CLASSES: Record<Tone, string> = {
    success: 'text-success',
    warning: 'text-warning',
    error: 'text-error',
    info: 'text-primary',
    neutral: 'text-on-surface-variant',
  }
</script>

<span
  class="inline-flex items-center gap-2 {className}"
  title={compact ? label : undefined}
  aria-label={compact ? label : undefined}
>
  <span
    class="size-2 shrink-0 rounded-full {DOT_CLASSES[tone]} {pulse ? 'animate-pulse' : ''}"
    aria-hidden="true"
  ></span>
  {#if !compact}
    <span class="text-label-large {TEXT_CLASSES[tone]}">{label}</span>
  {/if}
</span>
