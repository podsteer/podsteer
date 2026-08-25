<!--
  The status of one row, as a single mark.

  Shape carries the meaning and colour grades it: a triangle means something
  wants attention, a filled disc means it does not. That ordering matters —
  colour alone would leave amber and red indistinguishable to a reader who
  cannot separate them, whereas a triangle is a triangle at any setting, and
  the full text is always on the element as its accessible name and tooltip.

  A disc rather than a tick for the healthy state. A check mark is the symbol
  of a control somebody operates — a box they tick — and in a column of
  clickable rows it invited being read as one. A disc asserts a state and
  offers nothing to press.

  No label and no badge beside it. A status column repeating "Running" down
  two hundred rows is two hundred words nobody reads, and the whole value of
  the column is that the exceptions are visible without reading any of it.
-->
<script lang="ts">
  import type { Tone } from '$lib/format'
  import { AlertTriangle, Circle } from '@lucide/svelte'

  interface Props {
    /** Semantic tone driving shape and colour. */
    tone?: Tone
    /** The status text. Not drawn, but named for assistive technology. */
    label: string
    /** Animates the mark, for states that are actively changing. */
    pulse?: boolean
    class?: string
  }

  let { tone = 'neutral', label, pulse = false, class: className = '' }: Props = $props()

  /** Attention is a triangle; everything else is a disc. */
  const NEEDS_ATTENTION: Tone[] = ['warning', 'error']

  const COLOUR: Record<Tone, string> = {
    success: 'text-success',
    warning: 'text-warning',
    error: 'text-error',
    info: 'text-primary',
    // Informational rather than healthy — a Normal event is not a claim that
    // anything is well, so it does not get the colour that would say so.
    neutral: 'text-on-surface-variant/50',
  }

  const attention = $derived(NEEDS_ATTENTION.includes(tone))
  const StatusIcon = $derived(attention ? AlertTriangle : Circle)
</script>

<span class="inline-flex {className}" title={label} aria-label={label} role="img">
  <StatusIcon
    class="size-4 shrink-0 {COLOUR[tone]} {pulse ? 'animate-pulse' : ''}"
    strokeWidth={2}
    fill={attention ? 'none' : 'currentColor'}
  />
</span>
