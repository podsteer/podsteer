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
    /**
     * The amount, already formatted and without a unit — the label carries
     * that. Empty for a figure that is only ever a proportion.
     */
    value?: string
    /** The share, e.g. "47%". Empty for a figure that has no denominator. */
    percent?: string
    /** Colours the figure when it is worth noticing. */
    tone?: string
    /** Greys the whole row when the figure is unavailable rather than zero. */
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
    <!-- The label carries the emphasis and the figures recede, which is the
         opposite of the usual arrangement and right here: the four labels are
         identical on every track, so they are what the eye navigates by, and
         somebody scanning for Efficiency should find the word before the
         number.

         The right-hand pair takes a wider indent than the gap between a label
         and its own value. Without it "47% Used" ran together as one phrase;
         the space is what separates the end of one figure from the start of
         the next. -->
    <dt
      class="{index % 2 === 1 ? 'pl-6' : ''} {figure.muted
        ? 'text-on-surface-variant/50'
        : 'text-on-surface'}"
      title={figure.title}
    >
      {figure.label}
    </dt>
    <!-- Amount, rule, share. The share sits in a slot wide enough for its
         longest possible value, so the rule between them lands in the same
         place on every row of every track — which is what makes it a guide
         the eye can follow down the card rather than punctuation that drifts
         with whatever number happens to be there.

         Both parts are kept and both are hidden rather than dropped when a
         figure has only one of them: a row that omits its cells would pull
         its amount out to the edge and break the very alignment the slot
         exists to hold. -->
    <!-- Amount and share take one colour, set on the row rather than on each
         span: they are two halves of one reading, and giving the number more
         weight than its proportion made the pair look like two facts. -->
    <dd
      class="flex items-baseline justify-end gap-2 tabular-nums {figure.muted
        ? 'text-on-surface-variant/50'
        : (figure.tone ?? 'text-on-surface-variant')}"
    >
      <span>{figure.value ?? ''}</span>
      <span
        aria-hidden="true"
        class="text-outline-variant {figure.value && figure.percent ? '' : 'invisible'}"
      >
        |
      </span>
      <span class="w-[4.5ch] text-right">{figure.percent ?? ''}</span>
    </dd>
  {/each}
</dl>
