<!--
  A compact usage meter.

  Colour crosses to warning at 75% and error at 90% — the thresholds at which
  an operator should start caring about a node, chosen so that a healthy
  cluster's meters are uniformly calm and any colour at all means something.
-->
<script lang="ts">
  interface Props {
    /** Percentage 0–100. */
    percent: number
    /** The value to print alongside, already formatted. */
    label: string
    /** False when nothing was measured, e.g. no metrics-server. */
    measured?: boolean
    class?: string
  }

  let { percent, label, measured = true, class: className = '' }: Props = $props()

  const clamped = $derived(Math.max(0, Math.min(100, percent)))
  const tone = $derived(clamped >= 90 ? 'bg-error' : clamped >= 75 ? 'bg-warning' : 'bg-primary')
</script>

<div class="flex items-center gap-2 {className}">
  <span class="w-14 shrink-0 text-right text-body-small tabular-nums">{label}</span>
  {#if measured}
    <span class="h-1.5 w-16 shrink-0 overflow-hidden rounded-full bg-surface-container-highest">
      <span
        class="block h-full rounded-full transition-all duration-300 ease-standard {tone}"
        style="width: {clamped}%"
      ></span>
    </span>
    <span class="w-8 shrink-0 text-right text-[11px] tabular-nums text-on-surface-variant/70">
      {Math.round(clamped)}%
    </span>
  {:else}
    <span class="text-body-small italic text-on-surface-variant/50">no metrics</span>
  {/if}
</div>
