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
  import GaugeTrack from './GaugeTrack.svelte'
  import { preferences, type ThresholdScope } from '$stores/preferences.svelte'

  interface Props {
    /** The measured value, already formatted, e.g. "0.012" or "18.4 MiB". */
    label: string
    /**
     * What is being measured — "CPU", "Memory" — for the screen reader.
     *
     * The column heading says it once for sighted readers, so the cell does
     * not repeat it visually; anyone arriving at a single cell out of context
     * still needs to be told which of the two they landed on.
     */
    name: string
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
     * changes from row to row cannot be compared down its own length.
     */
    absent?: string
    /** False when nothing measured it: no metrics-server, or a pod it did not reach. */
    measured?: boolean
    /**
     * Whether the denominator is a CAPACITY, so that a high reading is a
     * problem and the operator's gauge thresholds apply.
     *
     * TRUE for a node, where usage is divided by allocatable: 90% of a node's
     * memory is genuinely worth somebody's attention, and the colour is how
     * they notice it without reading every row. Such a bar is drawn by
     * GaugeTrack, so it carries the thresholds set in Settings — and the
     * marker ticks that say where they fall — exactly as the same reading
     * does on the overview.
     *
     * FALSE for a pod against its own request, where it emphatically is not.
     * A request is a reservation, not a ceiling; a pod sitting at 95% of what
     * it asked for is a pod that was sized correctly, and one above 100% is
     * a Burstable pod doing exactly what Burstable means. Colouring those
     * amber and red would light up most of a healthy pod list, which is how a
     * signal stops being read. That bar stays one flat colour.
     */
    thresholds?: boolean
    /**
     * A SECOND percentage, which decides the colour when `thresholds` is off.
     *
     * For a pod the bar's length and the bar's colour answer different
     * questions, and both are worth asking. The length is usage against the
     * REQUEST — am I sized right — where a full bar is a success. The colour
     * is usage against the LIMIT — am I about to be stopped — where a high
     * reading is the warning it looks like.
     *
     * The two cannot contradict each other, because Kubernetes requires
     * request ≤ limit: anything near its limit is at least as near its
     * request, so a coloured bar is always an already-full one. Amber on a
     * full bar therefore reads exactly as it should — "using more than you
     * reserved, AND close to your ceiling" — while a full blue bar is the
     * common, harmless case of a Burstable pod using its headroom.
     *
     * Null leaves the bar the plain fill colour, which is what a pod with no
     * limit declared gets: there is no ceiling, so there is no proximity to
     * one, and inventing a colour for it would be inventing a denominator.
     *
     * No marker ticks are drawn for this, unlike a node's. A tick shows WHERE
     * on the track a threshold falls, and these thresholds do not fall
     * anywhere on this track — they belong to the other denominator.
     */
    severity?: number | null
    /** Tooltip for the whole meter, naming what the proportion is of. */
    title?: string
    /** Which surface's threshold lines apply — the list this cell is in. */
    scope: ThresholdScope
    /**
     * How much room to reserve for the value, as a CSS length.
     *
     * Fixed per COLUMN, because that is what keeps the bars on a common left
     * edge — a box that hugged each row's own text would start every bar at a
     * different x and there would be nothing to compare down the column.
     *
     * But fixed per column, NOT one width for all of them. A single figure
     * shared by CPU and memory has to be sized for the longer of the two, and
     * the shorter column then carries the difference as dead space on its
     * left for every row it will ever draw: `0.015` in a box cut for
     * `1023.9MiB` is a third of the cell reserved for characters that cannot
     * appear in it.
     *
     * Expressed in `ch` so it tracks the font rather than a pixel guess about
     * it. With `tabular-nums` every digit is exactly 1ch, so the width can be
     * counted off the widest string the formatter can actually produce.
     */
    valueWidth?: string
    class?: string
  }

  let {
    label,
    name,
    percent,
    measured = true,
    thresholds = true,
    title,
    absent = '',
    severity = null,
    scope,
    valueWidth = '9ch',
    class: className = '',
  }: Props = $props()

  /**
   * The flat bar's width, which IS capped at 100 — a bar cannot draw past its
   * own track. The printed figure below is not capped, so a pod at three
   * times its request reads as 300% beside a full bar rather than being
   * quietly rounded down to a comfortable-looking 100%.
   *
   * Only the thresholds={false} branch needs this; GaugeTrack clamps its own.
   */
  const width = $derived(Math.max(0, Math.min(100, percent ?? 0)))

  /**
   * The flat bar's colour, from `severity` and the operator's own lines.
   *
   * Read from Settings rather than fixed here, so a team running deliberately
   * tight limits and a team running generous ones each get a bar that agrees
   * with the number they chose. A threshold switched off simply never fires,
   * the same as everywhere else.
   */
  const flatTone = $derived.by(() => {
    if (severity === null) return 'bg-primary'

    const lines = preferences.thresholdsFor(scope)
    if (lines.criticalEnabled && severity >= lines.critical) return 'bg-gauge-critical'
    if (lines.warnEnabled && severity >= lines.warn) return 'bg-gauge-warn'
    return 'bg-primary'
  })
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
    class="shrink-0 truncate text-right tabular-nums {measured
      ? ''
      : 'text-on-surface-variant/40'}"
    style="width: {valueWidth}"
  >
    {label}
  </span>

  {#if measured && percent !== null}
    {#if thresholds}
      <!--
        Delegated, rather than a second bar with its own opinion. This used to
        hard-code amber at 75 and red at 90 and tint the whole length — three
        disagreements with the rest of the application at once: an operator
        who set 95/99 for a deliberately hot cluster still got amber at 75, a
        switched-off threshold was drawn anyway, and whole-bar tinting is the
        design GaugeTrack's own notes record as tried and rejected because it
        makes 81% and 99% look identical.
      -->
      <GaugeTrack value={percent} height="h-1.5" width="min-w-6 flex-1" label={name} {scope} />
    {:else}
      <span class="h-1.5 min-w-6 flex-1 overflow-hidden rounded-full bg-surface-container-highest">
        <span
          class="block h-full rounded-full transition-all duration-300 ease-standard {flatTone}"
          style="width: {width}%"
        ></span>
      </span>
    {/if}
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
