<!--
  One dimension of cluster capacity, drawn as a single track.

  The track is the point. Requests, limits and usage are three different
  numbers that every other dashboard shows as three separate gauges, which
  leaves the operator to work out the relationship between them — and the
  relationship is the whole story:

    • Requests decide what can be SCHEDULED. When this reaches the end of the
      track the cluster refuses new pods, however idle the nodes are.
    • Usage is what is actually happening. The gap between usage and requests
      is capacity that is paid for and not used.
    • Limits are what could happen if everything peaked at once, and routinely
      exceed the cluster — so the limits marker is allowed to sit past the end.

  Drawing them on one track makes "93% requested, 8% used" visible as the
  single fact it is.
-->
<script lang="ts">
  import type { ResourceUsage } from '$lib/api/client'

  interface Props {
    label: string
    usage: ResourceUsage
    /** Shown under the bar as the unit, e.g. "cores" or "memory". */
    unit?: string
  }

  let { label, usage, unit = '' }: Props = $props()

  const requestWidth = $derived(Math.max(0, Math.min(100, usage.requestPercent)))
  const usageWidth = $derived(usage.measured ? Math.max(0, Math.min(100, usage.usagePercent)) : 0)

  /** Limits may exceed allocatable, so the marker is clamped to the track. */
  const limitOffset = $derived(Math.max(0, Math.min(100, usage.limitPercent)))
  const limitsOvercommitted = $derived(usage.limitPercent > 100)

  /**
   * Reservations crossing 90% is the point at which scheduling failures start,
   * so the track changes colour there rather than at an arbitrary "full".
   */
  const requestTone = $derived(
    usage.requestPercent >= 90
      ? 'bg-error/70'
      : usage.requestPercent >= 75
        ? 'bg-warning/70'
        : 'bg-primary/45',
  )

  /** Efficiency is -1 when nothing was measured. */
  const efficiency = $derived(usage.efficiency >= 0 ? Math.round(usage.efficiency) : null)
</script>

<div class="flex flex-col gap-2">
  <div class="flex items-baseline justify-between gap-3">
    <span class="text-label-large text-on-surface">{label}</span>
    <span class="text-body-small tabular-nums text-on-surface-variant">
      {usage.requests} / {usage.allocatable}
      {#if unit}<span class="text-on-surface-variant/60">{unit}</span>{/if}
    </span>
  </div>

  <div
    class="relative h-3 w-full overflow-hidden rounded-full bg-surface-container-highest"
    role="img"
    aria-label="{label}: {usage.requests} requested of {usage.allocatable} allocatable"
  >
    <!-- Requests: the band that decides schedulability. -->
    <span
      class="absolute inset-y-0 left-0 rounded-full transition-all duration-300 ease-standard {requestTone}"
      style="width: {requestWidth}%"
    ></span>

    <!-- Usage: drawn on top and narrower, so it reads as "of that band, this
         much is real" rather than as a competing measurement. -->
    {#if usage.measured}
      <span
        class="absolute inset-y-[3px] left-0 rounded-full bg-primary transition-all duration-300 ease-standard"
        style="width: {usageWidth}%"
      ></span>
    {/if}

    <!-- Limits marker. -->
    {#if usage.limitPercent > 0}
      <span
        class="absolute inset-y-0 w-0.5 {limitsOvercommitted ? 'bg-error' : 'bg-on-surface/40'}"
        style="left: {limitOffset}%"
        title="Limits: {usage.limits}{limitsOvercommitted ? ' — more than the cluster has' : ''}"
      ></span>
    {/if}
  </div>

  <div class="flex flex-wrap items-baseline gap-x-4 gap-y-1 text-body-small text-on-surface-variant/80">
    <span class="tabular-nums">
      <span class="text-on-surface-variant">Requested</span>
      {Math.round(usage.requestPercent)}%
    </span>

    {#if usage.measured}
      <span class="tabular-nums">
        <span class="text-on-surface-variant">Used</span>
        {usage.usage} ({Math.round(usage.usagePercent)}%)
      </span>
    {/if}

    <span class="tabular-nums">
      <span class="text-on-surface-variant">Free to schedule</span>
      {usage.schedulable}
    </span>

    <!-- The number nobody else prints: how much of the reservation is real. -->
    {#if efficiency !== null}
      <span
        class="tabular-nums {efficiency < 25 ? 'text-warning' : ''}"
        title="Measured usage as a share of what was requested"
      >
        <span class="text-on-surface-variant">Efficiency</span>
        {efficiency}%
      </span>
    {/if}
  </div>
</div>
