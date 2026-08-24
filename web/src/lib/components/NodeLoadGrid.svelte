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

  interface Props {
    loads: NodeLoad[]
    /** Called when a node is chosen, to open it. */
    onselect?: (name: string) => void
  }

  let { loads, onselect }: Props = $props()

  /**
   * How many nodes are drawn before the reader asks for more.
   *
   * Six, which is three rows of two — enough to fill the card without
   * becoming the page. The list is sorted busiest first, so the six shown are
   * the six that matter and expanding is a choice rather than a chore.
   */
  const COLLAPSED = 6

  let expanded = $state(false)

  const shown = $derived(expanded ? loads : loads.slice(0, COLLAPSED))
  const hidden = $derived(Math.max(0, loads.length - COLLAPSED))

  /**
   * Where a track stops being comfortable.
   *
   * The same numbers the capacity card and the findings use. A cluster does
   * not have one meaning of "nearly full" per component.
   */
  const WARN = 80
  const CRITICAL = 90

  interface Track {
    label: string
    /** Percentage, or -1 when the dimension could not be measured. */
    value: number
    title: string
  }

  function tracks(load: NodeLoad): Track[] {
    return [
      { label: 'CPU', value: load.cpuPercent, title: 'Requested against allocatable' },
      { label: 'Memory', value: load.memoryPercent, title: 'Requested against allocatable' },
      { label: 'Pods', value: load.podPercent, title: 'Scheduled against this node’s cap' },
      {
        label: 'Disk',
        value: load.diskPercent,
        title:
          load.diskPercent >= 0
            ? 'The fuller of the node’s filesystems'
            : 'No kubelet answered for this node',
      },
    ]
  }

  /** Bar colour by how close the track is to refusing work. */
  function tone(value: number): string {
    if (value >= CRITICAL) return 'bg-error/70'
    if (value >= WARN) return 'bg-warning/70'
    return 'bg-primary/45'
  }

  /** Figure colour, matching the track it belongs to. */
  function figureTone(value: number): string {
    if (value < 0) return 'text-on-surface-variant/40'
    if (value >= CRITICAL) return 'text-error'
    if (value >= WARN) return 'text-warning'
    return 'text-on-surface-variant'
  }
</script>

<div class="flex flex-col gap-3">
  <!-- Two columns, so six nodes occupy the width the card already has. One
       column left most of it empty and made the card twice as tall for the
       same information. -->
  <div class="grid gap-x-8 gap-y-4 lg:grid-cols-2">
    {#each shown as load (load.name)}
      <div class="flex min-w-0 flex-col gap-1.5">
        <!-- The name in full, above its own tracks. An axis gutter could only
             ever hold a truncated version of it, and node names differ at the
             end. -->
        <div class="flex items-baseline justify-between gap-2">
          {#if onselect}
            <button
              type="button"
              onclick={() => onselect?.(load.name)}
              class="min-w-0 truncate text-left text-body-medium text-on-surface
                     transition-colors duration-100 hover:text-primary"
              title="Open {load.name}"
            >
              {load.name}
            </button>
          {:else}
            <span class="min-w-0 truncate text-body-medium text-on-surface">{load.name}</span>
          {/if}

          <span class="flex shrink-0 items-center gap-2 text-label-small">
            {#if !load.ready}
              <span class="text-error uppercase">Not ready</span>
            {:else if !load.schedulable}
              <span class="text-warning uppercase">Cordoned</span>
            {/if}
            {#if load.controlPlane}
              <span class="text-on-surface-variant/60 uppercase">Control plane</span>
            {/if}
            <span class="tabular-nums text-on-surface-variant/60">{load.pods} pods</span>
          </span>
        </div>

        {#each tracks(load) as track (track.label)}
          <div class="flex items-center gap-2">
            <span class="w-14 shrink-0 text-body-small text-on-surface-variant" title={track.title}>
              {track.label}
            </span>

            <div
              class="relative h-2 min-w-0 flex-1 overflow-hidden rounded-full bg-surface-container-highest"
              role="img"
              aria-label="{track.label}: {track.value < 0
                ? 'not measured'
                : `${Math.round(track.value)} per cent`}"
            >
              {#if track.value >= 0}
                <span
                  class="absolute inset-y-0 left-0 rounded-full transition-all duration-300
                         ease-standard {tone(track.value)}"
                  style="width: {Math.min(100, track.value)}%"
                ></span>
              {/if}

              <!-- The same marker the capacity tracks carry, at the same
                   threshold, so a reader who has learnt one has learnt both. -->
              <span
                class="absolute inset-y-0 w-0.5 bg-on-surface/40"
                style="left: {WARN}%"
                title="{WARN}%"
              ></span>
            </div>

            <span
              class="w-10 shrink-0 text-right text-body-small tabular-nums {figureTone(track.value)}"
            >
              {track.value < 0 ? '—' : `${Math.round(track.value)}%`}
            </span>
          </div>
        {/each}
      </div>
    {/each}
  </div>

  {#if hidden > 0}
    <button
      type="button"
      onclick={() => (expanded = !expanded)}
      aria-expanded={expanded}
      class="state-layer self-start rounded-xs px-1.5 py-1 text-label-medium text-primary
             transition-colors duration-100 hover:bg-primary/10"
    >
      {expanded ? `Show the busiest ${COLLAPSED}` : `Show all ${loads.length} nodes`}
    </button>
  {/if}
</div>
