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
  import GaugeTrack from './GaugeTrack.svelte'
  import { preferences } from '$stores/preferences.svelte'
  import InfoHint from './InfoHint.svelte'

  interface Props {
    capacity: PodCapacity
  }

  let { capacity }: Props = $props()

  /**
   * Everything below is printed, not computed.
   *
   * Every count and share arrives formatted from the Go side, which is where
   * the thresholds and the rounding are tested. The only arithmetic left is
   * clamping a bar to its track, which is a drawing concern rather than a
   * fact about the cluster.
   */
  const width = $derived(Math.max(0, Math.min(100, capacity.usedPercentValue)))

  /**
   * Past the operator's own warning line.
   *
   * This used to warn at 85, a number nothing else in the application used.
   * Slots genuinely behave differently from CPU — there is no bursting past a
   * node's cap, the pods that do not fit simply stay Pending — but that is an
   * argument for the reader choosing one line, not for this card keeping a
   * private one.
   */
  const crowded = $derived(capacity.usedPercentValue >= preferences.warnThreshold)

  /**
   * Four figures in two pairs. The top pair is the slots — taken and free —
   * and the bottom pair is what became of the pods: working, or still waiting
   * for somewhere to run.
   *
   * Healthy earns its own row because Scheduled says a slot is occupied and
   * nothing whatever about the workload in it. A cluster can be comfortably
   * within its cap with a third of those pods crash-looping.
   */
  const figures = $derived<Figure[]>([
    {
      label: 'Scheduled',
      value: capacity.scheduledLabel,
      percent: capacity.usedPercent,
      tone: crowded ? 'text-gauge-warn' : undefined,
    },
    {
      label: 'Schedulable',
      value: capacity.freeLabel,
      percent: capacity.freePercent,
      title: 'Slots on ready, uncordoned, untainted nodes that nothing occupies',
    },
    {
      label: 'Healthy',
      value: capacity.healthyLabel,
      percent: capacity.healthyPercent,
      tone: capacity.healthy < capacity.scheduled ? 'text-warning' : undefined,
      title: 'Scheduled pods that are actually doing their job',
    },
    {
      label: 'Waiting',
      value: capacity.unschedulableLabel,
      percent: capacity.waitingPercent,
      tone: capacity.unschedulable > 0 ? 'text-warning' : undefined,
      title: 'Pods the scheduler has not placed on any node',
    },
  ])

  /**
   * Behind an icon rather than printed under the figures.
   *
   * It qualifies the total without belonging beside it permanently: inline it
   * pushed the card taller than its neighbour and competed with the figures,
   * and dropped entirely it would leave a capacity that looks wrong to anyone
   * checking it against kubectl.
   *
   * A control-plane node advertises its hundred-odd slots like any other and
   * will never accept a pod that does not tolerate its taint. Counting them
   * as headroom is the error; excluding them without saying so would be a
   * different one.
   */
  const reservedNote = $derived(
    capacity.reserved > 0
      ? `${capacity.reservedLabel} more slots on ${capacity.reservedNodes} tainted ` +
        `${capacity.reservedNodes === 1 ? 'node' : 'nodes'}, for pods that tolerate them.`
      : '',
  )
</script>

<div class="flex min-w-0 flex-col gap-2">
  <div class="flex items-baseline justify-between gap-3">
    <span class="flex items-center gap-1.5 text-label-large text-on-surface">
      Pod slots
      {#if reservedNote}
        <InfoHint text={reservedNote} label="Why some slots are not counted" />
      {/if}
    </span>
    <span class="text-body-medium tabular-nums text-on-surface-variant">
      {capacity.scheduledLabel} / {capacity.capacityLabel}
    </span>
  </div>

  <GaugeTrack value={capacity.usedPercentValue} height="h-3" label="Pod slots" />

  <CapacityFigures {figures} />
</div>
