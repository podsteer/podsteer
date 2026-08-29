<!--
  A compact usage meter: the measured value, a bar, and the proportion.

  Used by both the node list and the pod list, which divide by different
  things — a node's usage is a share of its allocatable capacity, a pod's is a
  share of what it requested. The component is deliberately ignorant of which:
  it is handed a percentage and prints it. What it does need to know is
  whether that percentage is one where a high reading is BAD, which is what
  `thresholds` says, because the two denominators disagree about that
  completely. See the prop.
-->
<script lang="ts">
  interface Props {
    /** The measured value, already formatted, e.g. "0.012" or "18.4 MiB". */
    label: string
    /**
     * The proportion, 0–100 and occasionally beyond.
     *
     * Null means there is no denominator to be a proportion OF — a pod that
     * declares no request — and draws `absent` in the track instead of a bar.
     * That is a different state from 0, which is a real measurement of an
     * idle workload, and the two must not render alike.
     */
    percent: number | null
    /**
     * What to say where the bar would be when there is no denominator.
     *
     * An EMPTY CELL IS NOT A STATEMENT. Leaving the track blank was accurate
     * and still read as an omission — as though the meter had failed rather
     * than as though there was nothing to meter — and the explanation was
     * reachable only by hovering, which nobody does to a cell that looks
     * broken. Naming the absence turns it into the finding it actually is: a
     * pod with no reservation is a pod the scheduler is free to place
     * anywhere and the kubelet is free to evict first.
     *
     * Deliberately words rather than a bar of some substitute denominator.
     * The obvious substitute is the node's allocatable capacity, and it fails
     * twice over: a pod is typically a fraction of one percent of its node,
     * so the bar would be an invisible sliver, and a column whose denominator
     * changes from row to row cannot be compared down its own length. See
     * docs/decisions/0002-pod-meters-name-the-absent-denominator.md.
     */
    absent?: string
    /** False when nothing measured it: no metrics-server, or a pod it did not reach. */
    measured?: boolean
    /**
     * Whether a high reading is a problem.
     *
     * TRUE for a node, where the denominator is allocatable capacity: 90% of
     * a node's memory is genuinely worth somebody's attention, and the colour
     * is how they notice it without reading every row.
     *
     * FALSE for a pod against its own request, where it emphatically is not.
     * A request is a reservation, not a ceiling; a pod sitting at 95% of what
     * it asked for is a pod that was sized correctly, and one above 100% is
     * a Burstable pod doing exactly what Burstable means. Colouring those
     * amber and red would light up most of a healthy pod list, which is how a
     * signal stops being read.
     */
    thresholds?: boolean
    /** Tooltip for the whole meter, naming what the proportion is of. */
    title?: string
    class?: string
  }

  let {
    label,
    percent,
    measured = true,
    thresholds = true,
    title,
    absent = '',
    class: className = '',
  }: Props = $props()

  /**
   * The bar's width, which IS capped at 100 — a bar cannot draw past its own
   * track. The printed figure below is not capped, so a pod at three times
   * its request reads as 300% beside a full bar rather than being quietly
   * rounded down to a comfortable-looking 100%.
   */
  const width = $derived(Math.max(0, Math.min(100, percent ?? 0)))

  const tone = $derived(
    !thresholds
      ? 'bg-primary'
      : width >= 90
        ? 'bg-error'
        : width >= 75
          ? 'bg-warning'
          : 'bg-primary',
  )
</script>

<!--
  The value and the percentage both keep the table's own text size. Only the
  colour separates them: the measurement is stated in the body colour, the
  proportion in the muted one, so the pair reads as a figure with a qualifier
  rather than a figure with a footnote.

  The two TEXT parts are fixed-width and the BAR is what flexes. Widening the
  column has to make the bar longer, because the bar is the only part of the
  cell that gets more useful with more room — the numbers are the same eight
  characters at any width, and letting them stretch would only unpick the
  decimal alignment that makes the column scannable in the first place.
-->
<div class="flex items-center gap-2 {className}" {title}>
  <span
    class="w-20 shrink-0 truncate text-right tabular-nums {measured
      ? ''
      : 'text-on-surface-variant/40'}"
  >
    {label}
  </span>

  {#if measured && percent !== null}
    <span
      class="h-1.5 min-w-6 flex-1 overflow-hidden rounded-full bg-surface-container-highest"
    >
      <span
        class="block h-full rounded-full transition-all duration-300 ease-standard {tone}"
        style="width: {width}%"
      ></span>
    </span>
    <span class="w-12 shrink-0 text-right tabular-nums text-on-surface-variant/70">
      {Math.round(percent)}%
    </span>
  {:else if measured && absent}
    <!--
      Set in the muted colour and given no track of its own, so it reads as a
      note about the row rather than as a meter reading nothing. It must not
      be mistaken for a measurement: there is no bar, no percentage, and no
      colour that appears anywhere else in this column.
    -->
    <span class="flex-1 truncate text-on-surface-variant/50">{absent}</span>
  {/if}
</div>
