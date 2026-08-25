<!--
  One utilisation bar, with the two thresholds drawn on it.

  Every bar in the application that measures how full something is renders
  through here, which is the point. Three colours and two markers, meaning the
  same thing everywhere: blue while there is room, amber past the first
  threshold, red past the second — and a mark on the track at each, so the
  colour is never the only way to read the reading. That matters for anybody
  who cannot separate amber from red, and it is why the marks are drawn even
  when the bar has not reached them.

  The thresholds are the operator's, not ours. A cluster deliberately run hot
  should not glow red at 80% because we chose that number, so both lines move
  together from one place in Settings.
-->
<script lang="ts">
  import { preferences } from '$stores/preferences.svelte'

  interface Props {
    /** Percentage full. Negative means nothing could be measured. */
    value: number
    /** Track thickness. */
    height?: string
    /** Names the reading for anyone who cannot see it. */
    label: string
  }

  let { value, height = 'h-2', label }: Props = $props()

  const warn = $derived(preferences.warnThreshold)
  const critical = $derived(preferences.criticalThreshold)

  const width = $derived(Math.max(0, Math.min(100, value)))

  /**
   * The fill colour, from the fixed gauge palette rather than the theme.
   *
   * See app.css: these three do not follow light and dark, because they are
   * the reading itself rather than chrome around it.
   */
  const fill = $derived(
    value >= critical
      ? 'bg-gauge-critical'
      : value >= warn
        ? 'bg-gauge-warn'
        : 'bg-gauge-normal',
  )
</script>

<div
  class="relative {height} min-w-0 flex-1 overflow-hidden rounded-full bg-surface-container-highest"
  role="img"
  aria-label="{label}: {value < 0 ? 'not measured' : `${Math.round(value)} per cent`}"
>
  {#if value >= 0}
    <span
      class="absolute inset-y-0 left-0 rounded-full transition-all duration-300 ease-standard {fill}"
      style="width: {width}%"
    ></span>
  {/if}

  <!-- Drawn over the fill, so a bar that has passed a threshold still shows
       where it was. Both are always present: a marker that appeared only once
       it had been crossed would be telling somebody what they can already
       see. -->
  <span
    class="absolute inset-y-0 w-px bg-on-surface/45"
    style="left: {warn}%"
    title="{warn}%"
  ></span>
  <span
    class="absolute inset-y-0 w-px bg-on-surface/45"
    style="left: {critical}%"
    title="{critical}%"
  ></span>
</div>
