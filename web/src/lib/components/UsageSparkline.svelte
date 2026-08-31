<!--
  The open pod's recent usage, drawn from what the refresh already fetched.

  An inline SVG rather than the charting library the overview uses. That chart
  answers a different question — a cluster's capacity against its requests,
  over a retained history, with axes worth reading — and pulling it in here
  would put a 500KB renderer and a legend behind two dozen data points whose
  entire job is to show a shape.

  NO AXIS AND NO GRID, deliberately. The numbers that matter are printed
  beside it, and a y-axis on a series that starts wherever the drawer opened
  would invite comparisons between two pods that were watched at different
  times.
-->
<script lang="ts">
  import type { UsageSample } from '$stores/session.svelte'

  interface Props {
    samples: UsageSample[]
    /** Which series to draw. */
    metric: 'cpu' | 'memory'
    /** The current value, already formatted by Go. */
    current: string
    label: string
  }

  let { samples, metric, current, label }: Props = $props()

  const values = $derived(
    samples.map((sample) => (metric === 'cpu' ? sample.cpuCores : sample.memoryBytes)),
  )

  /**
   * The path, scaled to its own peak.
   *
   * Zero-based rather than min-based: a series scaled between its own minimum
   * and maximum turns a pod idling between 40m and 42m into a dramatic
   * mountain range. Anchoring at zero keeps the shape honest — a flat line is
   * a flat workload.
   */
  const path = $derived.by(() => {
    if (values.length < 2) return ''

    const peak = Math.max(...values, 1)
    const step = 100 / (values.length - 1)

    return values
      .map((value, index) => {
        const x = (index * step).toFixed(2)
        const y = (30 - (value / peak) * 28).toFixed(2)
        return `${index === 0 ? 'M' : 'L'}${x},${y}`
      })
      .join(' ')
  })
</script>

<div class="flex items-center gap-3">
  <span class="w-16 shrink-0 text-body-medium text-on-surface-variant">{label}</span>

  {#if path}
    <svg
      class="h-8 min-w-0 flex-1"
      viewBox="0 0 100 30"
      preserveAspectRatio="none"
      role="img"
      aria-label="{label} over the last {samples.length} refreshes"
    >
      <!-- vector-effect keeps the stroke one pixel wide despite the
           non-uniform scale that preserveAspectRatio="none" applies; without
           it the line thickens and thins with the pane's width. -->
      <path
        d={path}
        fill="none"
        stroke="currentColor"
        stroke-width="1.5"
        vector-effect="non-scaling-stroke"
        class="text-primary"
      />
    </svg>
  {:else}
    <!--
      Said plainly rather than drawn as an empty box. The series begins when
      the drawer opens because nothing retains per-pod history, and a chart
      that looked broken for its first thirty seconds would be reported as a
      bug every time.
    -->
    <span class="min-w-0 flex-1 text-body-small text-on-surface-variant/60">
      watching — the shape appears after a few refreshes
    </span>
  {/if}

  <span class="w-24 shrink-0 text-right text-body-medium tabular-nums text-on-surface">
    {current}
  </span>
</div>
