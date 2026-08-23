<!--
  The figures beneath a capacity track.

  Four columns — label, value, label, value — with the labels on a left edge
  and the values on a right one. Shared rather than written twice so every
  track's figures land in the same place: the point of the alignment is that a
  column can be read straight down across the whole card, and that only holds
  while every track agrees where the columns are.
-->
<script lang="ts">
  export interface Figure {
    label: string
    /** Already formatted. An em dash is the right value for "not known". */
    value: string
    /** Optional smaller aside after the value, e.g. a percentage. */
    aside?: string
    /** Colours the value when it is worth noticing. */
    tone?: string
    /** Greys the whole pair when the figure is unavailable rather than zero. */
    muted?: boolean
    title?: string
  }

  interface Props {
    figures: Figure[]
  }

  let { figures }: Props = $props()
</script>

<dl class="grid grid-cols-[auto_1fr_auto_1fr] items-baseline gap-x-3 gap-y-1.5 text-body-medium">
  {#each figures as figure, index (figure.label)}
    <!-- Every second pair is the right-hand one, and takes a little padding so
         its label does not crowd the value ending beside it. -->
    <dt
      class="{index % 2 === 1 ? 'pl-2' : ''} {figure.muted
        ? 'text-on-surface-variant/50'
        : 'text-on-surface-variant'}"
      title={figure.title}
    >
      {figure.label}
    </dt>
    <dd
      class="text-right tabular-nums {figure.muted
        ? 'text-on-surface-variant/50'
        : (figure.tone ?? 'text-on-surface')}"
    >
      {figure.value}
      {#if figure.aside}
        <span class="text-on-surface-variant/70">{figure.aside}</span>
      {/if}
    </dd>
  {/each}
</dl>
