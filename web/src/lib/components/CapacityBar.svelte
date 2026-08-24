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
  import CapacityFigures, { type Figure } from './CapacityFigures.svelte'

  interface Props {
    label: string
    usage: ResourceUsage
    /**
     * A line beneath the figures, for a dimension whose numbers need one.
     *
     * Ephemeral storage is the case: almost nobody declares it, so its track
     * is honestly empty, and an empty track with no explanation reads as a
     * failure to measure rather than as the finding it is.
     */
    note?: string
  }

  let { label, usage, note = '' }: Props = $props()

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

  /**
   * The four figures, in the order they are read.
   *
   * Requested above Schedulable on the left, Used above Efficiency on the
   * right, in every bar whatever the values are — an unavailable figure
   * greys out rather than disappearing, because a column that shifts when a
   * number is missing cannot be read down.
   */
  const figures = $derived<Figure[]>([
    {
      label: 'Requested',
      value: usage.requests,
      percent: `${Math.round(usage.requestPercent)}%`,
    },
    usage.measured
      ? {
          label: 'Used',
          value: usage.usage,
          percent: `${Math.round(usage.usagePercent)}%`,
        }
      : { label: 'Used', value: '—', muted: true },
    {
      label: 'Schedulable',
      value: usage.schedulable,
      percent: `${Math.round(usage.schedulablePercent)}%`,
      title: 'Allocatable not already requested',
    },
    // Efficiency has no amount of its own: it IS the ratio between the two
    // figures above it, so it occupies the share column and leaves the
    // amount empty, which puts it under the other percentages where it can
    // be compared with them.
    efficiency !== null
      ? {
          label: 'Efficiency',
          percent: `${efficiency}%`,
          tone: efficiency < 25 ? 'text-warning' : undefined,
          title: 'Measured usage as a share of what was requested',
        }
      : { label: 'Efficiency', value: '—', muted: true },
  ])

</script>

<div class="flex min-w-0 flex-col gap-2">
  <div class="flex items-baseline justify-between gap-3">
    <span class="text-label-large text-on-surface">{label}</span>
    <span class="text-body-medium tabular-nums text-on-surface-variant">
      {usage.requests} / {usage.allocatable}
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

  <CapacityFigures {figures} />

  {#if note}
    <p class="text-body-small leading-relaxed text-on-surface-variant/60">{note}</p>
  {/if}
</div>
