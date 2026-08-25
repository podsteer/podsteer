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
  import { preferences } from '$stores/preferences.svelte'
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
    /**
     * Replaces the Efficiency figure for a track that cannot have one.
     *
     * Ephemeral storage is the case: efficiency compares what pods USE with
     * what they RESERVED, and nothing anywhere reports per-pod disk use. The
     * slot is better spent on a figure that exists than on a dash explaining
     * that one does not.
     */
    fourth?: Figure
  }

  let { label, usage, note = '', fourth }: Props = $props()

  const requestWidth = $derived(Math.max(0, Math.min(100, usage.requestPercent)))
  const usageWidth = $derived(usage.measured ? Math.max(0, Math.min(100, usage.usagePercent)) : 0)

  /** Limits may exceed allocatable, so the marker is clamped to the track. */
  const limitOffset = $derived(Math.max(0, Math.min(100, usage.limitPercent)))
  const limitsOvercommitted = $derived(usage.limitPercent > 100)

  const warn = $derived(preferences.warnThreshold)
  const critical = $derived(preferences.criticalThreshold)

  /**
   * The requests band, in the application's three gauge colours.
   *
   * It used to have thresholds of its own — 75 and 90 — which meant this bar
   * turned amber at a number no other bar did. One pair of lines now, set by
   * the operator, applied everywhere.
   */
  const requestTone = $derived(
    usage.requestPercent >= critical
      ? 'bg-gauge-critical'
      : usage.requestPercent >= warn
        ? 'bg-gauge-warn'
        : 'bg-gauge-normal',
  )

  /**
   * Efficiency as a number, for the threshold only — the printed figure comes
   * formatted from the Go side like every other. -1 means nothing was
   * measured, which is not the same as nothing being used.
   */
  const efficiency = $derived(usage.efficiency >= 0 ? usage.efficiency : null)

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
      percent: usage.requestPercentLabel,
    },
    usage.measured
      ? {
          label: 'Used',
          value: usage.usage,
          percent: usage.usagePercentLabel,
        }
      : { label: 'Used', value: '—', muted: true },
    {
      label: 'Schedulable',
      value: usage.schedulable,
      percent: usage.schedulablePercentLabel,
      title: 'Allocatable not already requested',
    },
    // Efficiency has no amount of its own: it IS the ratio between the two
    // figures above it, so it occupies the share column and leaves the
    // amount empty, which puts it under the other percentages where it can
    // be compared with them.
    fourth ??
      (efficiency !== null
        ? {
            label: 'Efficiency',
            percent: usage.efficiencyLabel,
            tone: efficiency < 25 ? 'text-gauge-warn' : undefined,
            title: 'Measured usage as a share of what was requested',
          }
        : { label: 'Efficiency', value: '—', muted: true }),
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
    <!-- Requests: the band that decides schedulability, in the gauge palette
         so its colour means the same here as on every other bar. -->
    <span
      class="absolute inset-y-0 left-0 rounded-full transition-all duration-300 ease-standard {requestTone}"
      style="width: {requestWidth}%"
    ></span>

    <!-- Usage: drawn on top and narrower, so it reads as "of that band, this
         much is real" rather than as a competing measurement. -->
    {#if usage.measured}
      <span
        class="absolute inset-y-[3px] left-0 rounded-full bg-on-surface/70 transition-all duration-300 ease-standard"
        style="width: {usageWidth}%"
      ></span>
    {/if}

    <!-- The two thresholds, on every bar in the application at the same
         values. Thin, because this track already carries a limits marker
         that has to stay the louder of the three. -->
    <span class="absolute inset-y-0 w-px bg-on-surface/45" style="left: {warn}%" title="{warn}%"
    ></span>
    <span
      class="absolute inset-y-0 w-px bg-on-surface/45"
      style="left: {critical}%"
      title="{critical}%"
    ></span>

    <!-- Limits marker: what could happen if everything peaked at once. -->
    {#if usage.limitPercent > 0}
      <span
        class="absolute inset-y-0 w-0.5 {limitsOvercommitted ? 'bg-gauge-critical' : 'bg-on-surface'}"
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
