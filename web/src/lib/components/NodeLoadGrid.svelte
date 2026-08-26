<!--
  Every node's load, in the dimensions that decide whether more will fit.

  A cluster total hides the shape of the problem. "46% requested" across
  eighteen nodes is equally consistent with every node at 46% and with half of
  them at 90% while the rest idle — and only the second explains why a pod will
  not schedule on a cluster that looks half empty. That difference is the whole
  reason this exists, and it is visible in one glance here and in no number
  anywhere else on the page.

  Built from the same tracks as the capacity card rather than as a chart. A
  canvas gave this its own font, its own palette and its own idea of a
  threshold marker, and every one of them had to be talked back into matching
  the page. These are the page's own bars: the theme applies to them because
  they are made of it, the 80% rule is the same rule the cards draw, and the
  node's full name can sit above its tracks instead of being truncated into an
  axis gutter.
-->
<script lang="ts">
  import type { NodeLoad } from '$lib/api/client'
  import { preferences } from '$stores/preferences.svelte'
  import GaugeTrack from './GaugeTrack.svelte'

  interface Props {
    loads: NodeLoad[]
    /** Called when a node is chosen, to open it. */
    onselect?: (name: string) => void
  }

  let { loads, onselect }: Props = $props()

  /**
   * How many nodes this card draws. All of them, as far as it is concerned.
   *
   * Six, which is three rows of two — enough to fill the card without
   * becoming the page, and the same shape every other metric on the overview
   * takes: a top few, not a browsable list. The list arrives sorted busiest
   * first, so these are the six worth looking at; the Nodes page is where
   * somebody goes to see the rest.
   *
   * There was an expansion control here. It made this card the one thing on
   * the page that could grow to hundreds of rows, which is a different kind
   * of surface from its neighbours, and the answer to "show me every node"
   * was always the navigator rather than a card on a dashboard.
   */
  const SHOWN = 6

  /**
   * The rows to draw, with their gauges built once.
   *
   * `tracks(load)` used to be called inside the `{#each}` body, so it
   * allocated an array and four objects per node on EVERY render — with
   * fresh identities each time, which made the keyed inner block re-diff on
   * every ten-second refresh even though nothing about the node had changed.
   */
  const rows = $derived(
    loads.slice(0, SHOWN).map((load) => ({ load, tracks: tracks(load) })),
  )

  interface Track {
    label: string
    /** Percentage, or -1 when the dimension could not be measured. */
    value: number
    /** The quantity behind the share, already formatted. */
    amount: string
    /** The share, already rounded. Empty when nothing was measured. */
    share: string
    title: string
  }

  function tracks(load: NodeLoad): Track[] {
    return [
      {
        label: 'CPU',
        value: load.cpuPercent,
        amount: load.cpuAmount,
        share: load.cpuShare,
        title: 'Requested against allocatable',
      },
      {
        label: 'Memory',
        value: load.memoryPercent,
        amount: load.memoryAmount,
        share: load.memoryShare,
        title: 'Requested against allocatable',
      },
      {
        label: 'Pods',
        value: load.podPercent,
        amount: load.podAmount,
        share: load.podShare,
        title: 'Scheduled against this node’s cap',
      },
      {
        label: 'Disk',
        value: load.diskPercent,
        amount: load.diskAmount,
        share: load.diskShare,
        title: load.diskMeasured
          ? 'The fuller of the node’s filesystems'
          : 'No kubelet answered for this node',
      },
    ]
  }

  /**
   * Figures stay one colour whatever the reading.
   *
   * The bar beside them already says how full the node is, in bands, against
   * marks. Colouring the number as well said it twice and made a row of four
   * tracks look like four separate alarms — and a figure that changes colour
   * is one somebody reads as a state rather than as the quantity it is.
   * Unmeasured is the one exception, because that is not a reading at all.
   */
  function figureTone(value: number): string {
    return value < 0 ? 'text-on-surface-variant/40' : 'text-on-surface-variant'
  }
</script>

<div class="flex flex-col gap-3">
  <!-- Two columns, so six nodes occupy the width the card already has. One
       column left most of it empty and made the card twice as tall for the
       same information. -->
  <div class="grid gap-x-10 gap-y-5 lg:grid-cols-2">
    {#each rows as { load, tracks: nodeTracks } (load.name)}
      <div class="flex min-w-0 flex-col gap-2">
        <!-- The name in full and in the weight a capacity track's label
             carries, above its own bars. An axis gutter could only ever hold
             a truncated version, and node names differ at the end. -->
        <div class="flex items-baseline justify-between gap-3">
          {#if onselect}
            <button
              type="button"
              onclick={() => onselect?.(load.name)}
              class="resource-link min-w-0 truncate text-left text-label-large"
              title="Open {load.name}"
            >
              {load.name}
            </button>
          {:else}
            <span class="min-w-0 truncate text-label-large text-on-surface">{load.name}</span>
          {/if}

          <!-- Only what is exceptional. The pod count used to sit here and
               said the same thing as the Pods track below it.

               Two independent facts, so two chips: what state the node is in,
               and who is allowed on it. A tainted node's shares are measured
               against its own capacity, which is correct — a taint does not
               shrink a node — but without this marker "5% of pod slots used"
               reads as 95% of headroom anybody can schedule into, and for
               most workloads it is none. -->
          <span class="flex shrink-0 items-baseline gap-2 text-label-small uppercase">
            {#if !load.ready}
              <span class="text-error">Not ready</span>
            {:else if !load.schedulable}
              <span class="text-warning">Cordoned</span>
            {/if}

            {#if load.controlPlane}
              <span class="text-on-surface-variant/60" title="Reserved by its control-plane taint">
                Control plane
              </span>
            {:else if load.reserved}
              <span
                class="text-on-surface-variant/60"
                title="Tainted — only pods that tolerate it can be scheduled here"
              >
                Tainted
              </span>
            {/if}
          </span>
        </div>

        {#each nodeTracks as track (track.label)}
          <!-- Label, bar, then amount and share in the columns the capacity
               card uses: the quantity right-aligned against a rule that never
               moves, and the share in a slot wide enough for its longest
               value. -->
          <div class="flex items-center gap-3">
            <span class="w-14 shrink-0 text-body-medium text-on-surface" title={track.title}>
              {track.label}
            </span>

            <GaugeTrack
              value={track.value}
              height="h-1.5"
              width="min-w-0 flex-1"
              label={track.label}
            />

            <span
              class="flex shrink-0 items-baseline gap-2 text-body-medium tabular-nums {figureTone(
                track.value,
              )}"
            >
              <span class="w-16 text-right">{track.amount}</span>
              <span
                aria-hidden="true"
                class="text-outline-variant {track.share ? '' : 'invisible'}">|</span
              >
              <span class="w-[4.5ch] text-right">{track.share}</span>
            </span>
          </div>
        {/each}
      </div>
    {/each}
  </div>

</div>
