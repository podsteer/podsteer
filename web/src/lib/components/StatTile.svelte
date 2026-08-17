<!--
  A single figure from the cluster summary.

  Deliberately flat rather than a card: a row of these sits directly on the
  surface as a compact readout, and giving each one its own container would
  make the header heavier than the table it summarises.
-->
<script lang="ts">
  import type { Tone } from '$lib/format'

  interface Props {
    label: string
    value: string | number
    /** Tone of the value, for figures that carry a warning. */
    tone?: Tone
    class?: string
  }

  let { label, value, tone = 'neutral', class: className = '' }: Props = $props()

  const VALUE_CLASSES: Record<Tone, string> = {
    success: 'text-success',
    warning: 'text-warning',
    error: 'text-error',
    info: 'text-primary',
    neutral: 'text-on-surface',
  }
</script>

<div class="flex flex-col gap-0.5 {className}">
  <span class="text-label-medium text-on-surface-variant uppercase">{label}</span>
  <span class="text-headline-small tabular-nums {VALUE_CLASSES[tone]}">{value}</span>
</div>
