<!--
  Pod slots, drawn as a peer of the resource tracks.

  Its own component rather than a mode of CapacityBar, because the thing being
  counted is genuinely different: a slot has no requests, no limits and no
  usage — a pod either occupies one or it does not. Bending a track built for
  three overlapping quantities into one that has a single band would have cost
  more in conditionals than this costs in lines.

  It is a peer nonetheless, because it is the limit that catches people out.
  A node refuses pods at its cap however much CPU and memory are free, and a
  cluster can be 9% committed on every other dimension and still be full.
-->
<script lang="ts">
  import type { PodCapacity } from '$lib/api/client'
  import CapacityFigures, { type Figure } from './CapacityFigures.svelte'

  interface Props {
    capacity: PodCapacity
  }

  let { capacity }: Props = $props()

  const width = $derived(Math.max(0, Math.min(100, capacity.usedPercent)))

  /** Free slots, floored: a cluster past its cap has none rather than fewer. */
  const free = $derived(Math.max(0, capacity.capacity - capacity.scheduled))

  /**
   * 85% is where this starts to matter.
   *
   * Earlier than the resource tracks warn, because slots cannot be
   * overcommitted the way CPU can: there is no burst past the cap, and the
   * pods that do not fit simply stay Pending.
   */
  const tone = $derived(capacity.usedPercent >= 85 ? 'bg-error/70' : 'bg-primary/45')

  /** The free share, floored for the same reason as the count above it. */
  const freePercent = $derived(Math.max(0, 100 - capacity.usedPercent))

  /**
   * Three figures, not four.
   *
   * A slot has no requested-versus-used distinction to fill a fourth: a pod
   * occupies one or it does not, so Scheduled already carries the share that
   * a resource track spends two rows saying. Padding the grid to four would
   * mean inventing a figure, and the empty cell is the honest shape.
   */
  const figures = $derived<Figure[]>([
    {
      label: 'Scheduled',
      value: capacity.scheduled.toLocaleString(),
      percent: `${Math.round(capacity.usedPercent)}%`,
      tone: capacity.usedPercent >= 85 ? 'text-warning' : undefined,
    },
    {
      label: 'Schedulable',
      value: free.toLocaleString(),
      percent: `${Math.round(freePercent)}%`,
      title: 'Slots on ready, uncordoned nodes that nothing occupies',
    },
    capacity.unschedulable > 0
      ? {
          label: 'Waiting',
          value: capacity.unschedulable.toLocaleString(),
          tone: 'text-warning',
          title: 'Pods the scheduler has not placed on any node',
        }
      : { label: 'Waiting', value: '0' },
  ])
</script>

<div class="flex min-w-0 flex-col gap-2">
  <div class="flex items-baseline justify-between gap-3">
    <span class="text-label-large text-on-surface">Pod slots</span>
    <span class="text-body-medium tabular-nums text-on-surface-variant">
      {capacity.scheduled.toLocaleString()} / {capacity.capacity.toLocaleString()}
    </span>
  </div>

  <div
    class="relative h-3 w-full overflow-hidden rounded-full bg-surface-container-highest"
    role="img"
    aria-label="Pod slots: {capacity.scheduled} of {capacity.capacity} occupied"
  >
    <span
      class="absolute inset-y-0 left-0 rounded-full transition-all duration-300 ease-standard {tone}"
      style="width: {width}%"
    ></span>
  </div>

  <CapacityFigures {figures} />
</div>
