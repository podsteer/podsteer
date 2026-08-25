<!--
  One utilisation bar, with the two thresholds drawn on it.

  Every bar in the application that measures how full something is renders
  through here, which is the point. The fill is SEGMENTED rather than tinted:
  the first eighty per cent stays blue however full the bar gets, what spills
  past the first threshold is amber, and what spills past the second is red.
  A bar at 95% therefore shows all three, and the amber and red segments are
  exactly the overshoot — how far past comfortable it has gone, drawn to
  scale, rather than a colour swapped on the whole length.

  Recolouring the entire bar was the earlier version and it threw away that
  reading: 81% and 99% looked identical, both simply amber-then-red, and the
  eye had no way to judge the distance between them.

  Both thresholds are marked on the track whether or not the fill has reached
  them, so colour is never the only channel carrying the reading — which is
  what makes this work for anybody who cannot separate amber from red.

  The thresholds are the operator's, not ours. A cluster deliberately run hot
  should not glow red at 80% because we chose that number, so both lines move
  together from one place in Settings.
-->
<script lang="ts">
  import type { Snippet } from 'svelte'
  import { preferences } from '$stores/preferences.svelte'

  interface Props {
    /** Percentage full. Negative means nothing could be measured. */
    value: number
    /** Track thickness. */
    height?: string
    /**
     * How the track takes its width from whatever contains it.
     *
     * It cannot decide for itself. `flex-1` sets flex-basis on the MAIN axis,
     * so in the flex COLUMN a capacity card lays its label, bar and figures
     * out in, it sets the basis of the HEIGHT to zero and collapses the track
     * to nothing — while in the flex row a node's track sits in, it is
     * exactly right. The default suits a column or a plain block; a caller in
     * a row passes "min-w-0 flex-1".
     */
    width?: string
    /** Names the reading for anyone who cannot see it. */
    label: string
    /**
     * Drawn inside the track, above the fill.
     *
     * For the one caller that has more to say on the same line: the capacity
     * bars overlay measured usage on their requested band and mark where
     * limits fall.
     */
    children?: Snippet
  }

  let { value, height = 'h-2', width = 'w-full', label, children }: Props = $props()

  const warn = $derived(preferences.warnThreshold)
  const critical = $derived(preferences.criticalThreshold)

  const filled = $derived(Math.max(0, Math.min(100, value)))

  /**
   * The three segments, each the width of its own band.
   *
   * Clamped against both ends so a bar stops contributing to a segment it has
   * not reached: at 85% the amber runs from the first threshold to 85 and the
   * red is not drawn at all.
   */
  const normal = $derived(Math.min(filled, warn))
  const over = $derived(Math.max(0, Math.min(filled, critical) - warn))
  const severe = $derived(Math.max(0, filled - critical))
</script>

<div
  class="relative {height} {width} overflow-hidden rounded-full bg-surface-container-highest"
  role="img"
  aria-label="{label}: {value < 0 ? 'not measured' : `${Math.round(value)} per cent`}"
>
  {#if value >= 0}
    <!-- Square inner edges, round outer ones: the container clips the left,
         and only the last segment drawn needs its right end rounded, or the
         joins between bands show as notches. -->
    <span
      class="absolute inset-y-0 left-0 rounded-l-full bg-gauge-normal transition-all duration-300
             ease-standard {over > 0 || severe > 0 ? '' : 'rounded-r-full'}"
      style="width: {normal}%"
    ></span>

    {#if over > 0}
      <span
        class="absolute inset-y-0 bg-gauge-warn transition-all duration-300 ease-standard
               {severe > 0 ? '' : 'rounded-r-full'}"
        style="left: {warn}%; width: {over}%"
      ></span>
    {/if}

    {#if severe > 0}
      <span
        class="absolute inset-y-0 rounded-r-full bg-gauge-critical transition-all duration-300
               ease-standard"
        style="left: {critical}%; width: {severe}%"
      ></span>
    {/if}
  {/if}

  {@render children?.()}

  <!-- Drawn over the fill, so a bar that has passed a threshold still shows
       where it was. Both are always present: a marker that appeared only once
       it had been crossed would be telling somebody what they can already
       see. -->
  <span class="absolute inset-y-0 w-px bg-on-surface/45" style="left: {warn}%" title="{warn}%"
  ></span>
  <span
    class="absolute inset-y-0 w-px bg-on-surface/45"
    style="left: {critical}%"
    title="{critical}%"
  ></span>
</div>
