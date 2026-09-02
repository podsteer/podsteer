<!--
  The label-and-value list every detail pane states its facts in.

  ONE GRID FOR THE WHOLE DRAWER — see `detail-grid` in app.css. Every section
  of every panel puts its labels in the same column at the same width, so a
  panel reads as one thing from top to bottom rather than as a stack of lists
  that each chose their own proportions. That width is a share of the panel,
  so dragging the panel's edge widens both columns rather than only the values
  — which is what the clipping below responds to, since a wider label column
  is fewer rows that need opening at all.

  THE LABEL CARRIES THE EMPHASIS AND THE VALUE RECEDES. The labels are the
  same on every object of a kind, so they are what the eye navigates by:
  somebody looking for "Node" is looking for the word, not for the name beside
  it. Emphasising the values instead made every pane a wall of equally loud
  text with the signposts greyed out.

  BOTH COLUMNS CLIP TO ONE LINE, AND A ROW THAT IS CLIPPED CAN BE OPENED.
  This reverses what this list used to do — values wrapped by default, on the
  reasoning that a value nobody can read in full is a value nobody can act on.
  That is still true, and is why the expander exists; what wrapping cost was
  the shape of the pane. A `last-applied-configuration` annotation wrapped to
  fourteen lines and a probe string to three, so the rows a panel is scanned
  for were separated by paragraphs of reference material nobody was reading.
  Clipped, every row is one line and the list is scannable; expanded, the
  value is there in full. Nothing is hidden that cannot be asked for.

  A real <dl>, so the pairing is in the document and not only in the grid.
-->
<script lang="ts">
  import type { Snippet } from 'svelte'
  import { ChevronDown } from '@lucide/svelte'
  import {
    preferences,
    detailLabelBounds,
    DEFAULT_DETAIL_LABEL_SHARE,
  } from '$stores/preferences.svelte'

  export interface DetailRow {
    label: string
    /**
     * Already formatted, and already given its own em dash if it is missing.
     * Callers know what an absent value means for their field; this does not.
     *
     * On a `control` row this is the value in words rather than what is
     * rendered — it becomes the cell's tooltip, so a masked value still says
     * where it comes from.
     */
    value: string
    /**
     * Makes the value a link to the object it names.
     *
     * Offered rather than assumed: a Node row is worth following, and the
     * same row on a cluster whose nodes this account cannot list is not. The
     * caller decides, because only it knows whether there is anywhere to go.
     * See $lib/reference.
     */
    onclick?: () => void
    /**
     * Colours the value when it carries a verdict rather than a fact.
     *
     * A pod condition is the case this exists for: PodScheduled=False is not
     * the same kind of statement as Node=node-1, and a list that renders them
     * identically loses the only thing anybody scans a condition list for.
     * Left off for everything else, because a list where most rows are
     * coloured is a list where none of them are.
     */
    tone?: 'warn' | 'critical'
    /**
     * The value cell holds a control rather than text, and is rendered by the
     * list's `value` snippet.
     *
     * Never clipped: clipping text loses a few characters somebody can ask
     * for back, and clipping a control loses the button. An environment
     * variable read from a Secret is the case — the cell is a reveal button,
     * not a string.
     */
    control?: boolean
  }

  interface Props {
    rows: DetailRow[]
    /**
     * Renders the value cell of every `control` row.
     *
     * Optional, because most lists are text throughout. A row marked
     * `control` with no snippet to render falls back to its text, which is
     * the safe direction to fail in.
     */
    value?: Snippet<[DetailRow, number]>
  }

  let { rows, value }: Props = $props()

  let list = $state<HTMLElement | null>(null)
  /**
   * The cells themselves, for asking the browser what did not fit.
   *
   * Reactive, because `bind:this` into a plain array assigns without telling
   * anything — the measurement would then run against whatever was bound on
   * the previous render.
   */
  let labelCells = $state<HTMLElement[]>([])
  let valueCells = $state<HTMLElement[]>([])

  let expanded = $state<number[]>([])
  /** Which rows lost something to the clip, and so are worth opening. */
  let clipped = $state<boolean[]>([])
  /**
   * The same, held outside the reactive graph.
   *
   * `measure` needs the previous answer for a row that is currently open —
   * an open row wraps, so the browser reports it as fitting, and measuring it
   * would remove the control that closes it again. Reading the $state version
   * would make the effect depend on what it writes.
   */
  let measured: boolean[] = []

  /**
   * The same value, laid out, when it is JSON.
   *
   * Annotations are the reason: `last-applied-configuration` and most of what
   * operators write is a single line of minified JSON, and a single line of
   * minified JSON opened to its full width is no more readable than it was
   * clipped — it is just longer. Indented, it is a document.
   *
   * Objects and arrays only. `"true"`, `"3"` and `"null"` are all valid JSON
   * and formatting them changes nothing, so a value that merely parses is not
   * enough; it has to have structure to lay out.
   *
   * PARSED AND RE-PRINTED, which is worth being explicit about: the text
   * shown is this application's rendering of the value, not the value's own
   * bytes. Key order is preserved and nothing is dropped, but whitespace is
   * ours. The YAML tab remains the place the object is quoted exactly.
   */
  function laidOut(value: string): string | null {
    const text = value.trim()
    if (!text.startsWith('{') && !text.startsWith('[')) return null

    try {
      const parsed: unknown = JSON.parse(text)
      if (parsed === null || typeof parsed !== 'object') return null
      return JSON.stringify(parsed, null, 2)
    } catch {
      // Not JSON, or JSON this browser will not parse. Either way it is text.
      return null
    }
  }

  /** Whether the browser had to cut something off to fit the column. */
  function cut(cell: HTMLElement | undefined): boolean {
    return !!cell && cell.scrollWidth > cell.clientWidth + 1
  }

  function measure(): void {
    measured = rows.map((_, index) =>
      expanded.includes(index)
        ? (measured[index] ?? true)
        : cut(labelCells[index]) || cut(valueCells[index]),
    )
    clipped = measured
  }

  // --- The divider between the columns -------------------------------------
  //
  // ONE DIVIDER, SHOWN ONCE PER LIST. Dragging the one in Identity moves the
  // one in Labels and in Annotations and in every container card, because
  // there is only one column width and every section reads it. That is the
  // point: a panel whose sections could disagree about where the columns
  // divide would stop being a panel and start being a stack of tables.
  //
  // What is dragged is a SHARE of the pane rather than a pixel width, so a
  // list inside a container card — which is narrower than the sections around
  // it — stays in proportion while the pointer moves rather than jumping into
  // proportion when it is released.

  let dividing = $state(false)

  /** The gap between the columns, which the divider sits in the middle of. */
  function gap(): number {
    const size = parseFloat(getComputedStyle(document.documentElement).fontSize)
    return Number.isFinite(size) && size > 0 ? size : 16
  }

  function startDivide(event: PointerEvent): void {
    event.preventDefault()
    dividing = true
    ;(event.currentTarget as HTMLElement).setPointerCapture(event.pointerId)
  }

  function onDivideMove(event: PointerEvent): void {
    if (!dividing || !list) return

    const pane = list.getBoundingClientRect()
    if (pane.width <= 0) return

    const rootFontSize = gap()
    const { min, max } = detailLabelBounds(pane.width, rootFontSize)
    // The divider sits in the middle of the gap, so the column ends half a
    // gap before the pointer. Measured from the pane's own left edge rather
    // than from where the drag started: the two diverge the moment a bound
    // bites, and the divider then stops following the pointer until it has
    // been dragged all the way back.
    const width = Math.min(max, Math.max(min, event.clientX - pane.left - rootFontSize / 2))
    preferences.labelShareDrag = width / pane.width
  }

  function endDivide(): void {
    if (preferences.labelShareDrag !== null) {
      preferences.setDetailLabelShare(preferences.labelShareDrag)
    }
    dividing = false
  }

  function toggle(index: number): void {
    expanded = expanded.includes(index)
      ? expanded.filter((open) => open !== index)
      : [...expanded, index]
  }

  // Re-measured when the content changes, when a row is opened or closed, and
  // when the drawer is resized — all three change what fits, and only the
  // first is visible to Svelte on its own.
  $effect(() => {
    rows
    expanded
    measure()

    if (!list) return
    const observer = new ResizeObserver(() => measure())
    observer.observe(list)
    return () => observer.disconnect()
  })
</script>

<div class="relative">
  <dl class="detail-grid" bind:this={list}>
  <!--
    KEYED BY POSITION, NOT BY LABEL. A label is not unique and was never going
    to be: kubectl prints several "Mounts:" lines for one container and several
    "Tolerations:" for one pod, and this list renders them the same way. Keying
    by label made Svelte throw on the first pod with two volume mounts — which
    is every pod, because the service-account token is one of them.

    Position is the right key here anyway. `rows` is derived and rebuilt whole
    on each change, so there is no row identity to preserve across renders.
  -->
  {#each rows as row, index (index)}
    {@const open = expanded.includes(index)}
    <!--
      break-all on an opened label, because a label column also holds
      annotation keys, and an identifier has no word boundaries to break at:
      kubectl.kubernetes.io/last-applied-configuration is one "word", so
      break-words leaves it overflowing while break-all wraps it where it
      must.

      Selectable, for labels and annotations where the key is half of what
      somebody came to copy.
    -->
    <dt
      bind:this={labelCells[index]}
      class="min-w-0 text-body-medium text-on-surface {open ? 'break-all' : 'truncate'}"
      data-selectable
    >
      {row.label}
    </dt>
    <dd
      class="flex min-w-0 items-start gap-1 text-body-medium {row.tone === 'critical'
        ? 'text-error'
        : row.tone === 'warn'
          ? 'text-gauge-warn'
          : 'text-on-surface-variant'}"
    >
      {#if row.control && value}
        <!-- The caller's own markup, at its natural size. See `control`. -->
        <span class="min-w-0" title={row.value}>{@render value(row, index)}</span>
      {:else if open && laidOut(row.value)}
        <!--
          Laid out, in the monospace face indentation needs to mean anything.
          `pre-wrap` rather than `pre`: a long string value inside the JSON
          would otherwise scroll the pane sideways, and a detail panel that
          scrolls horizontally has lost.
        -->
        <pre
          class="min-w-0 flex-1 font-mono text-body-small leading-relaxed break-words
                 whitespace-pre-wrap"
          data-selectable>{laidOut(row.value)}</pre>
      {:else}
        <span
          bind:this={valueCells[index]}
          class="min-w-0 {open ? 'break-words' : 'truncate'}"
          data-selectable
        >
          {#if row.onclick}
            <!-- A button, not an anchor: this navigates within the
                 application and has no address. Styled as a link because that
                 is what it behaves like, and because a value that is
                 followable should look different from one that is not. It
                 sets no width of its own — the span around it is what clips,
                 so a link and a plain value are cut off in the same place. -->
            <button type="button" onclick={row.onclick} class="resource-link text-left">
              {row.value}
            </button>
          {:else}
            {row.value}
          {/if}
        </span>
      {/if}

      <!--
        Only on rows that lost something. A chevron on every row is a column
        of controls that mostly do nothing, and it would be the loudest thing
        in a pane whose whole job is the text beside it.
      -->
      {#if clipped[index]}
        <button
          type="button"
          onclick={() => toggle(index)}
          aria-expanded={open}
          aria-label={open ? `Collapse ${row.label}` : `Expand ${row.label}`}
          title={open ? 'Show less' : 'Show the whole value'}
          class="state-layer grid size-5 shrink-0 place-items-center rounded-full
                 text-on-surface-variant/60 transition-colors duration-100
                 hover:bg-surface-container hover:text-on-surface"
        >
          <ChevronDown
            class="size-3.5 transition-transform duration-150 ease-standard {open
              ? 'rotate-180'
              : ''}"
            strokeWidth={2}
          />
        </button>
      {/if}
    </dd>
  {/each}
  </dl>

  <!--
    The divider. Invisible until the pointer is near it, like the navigator's
    edge and the panel's own — a permanent rule between every label and every
    value would draw a line down a pane whose whole job is the text.

    Positioned from the column width rather than measured, so it stays on the
    boundary as the panel is resized with nothing listening. Double-click
    restores the default.
  -->
  <span
    role="separator"
    aria-orientation="vertical"
    aria-label="Resize the label column"
    tabindex="-1"
    style="left: calc(var(--detail-label-width) + 0.5rem)"
    class="absolute top-0 z-10 h-full w-2 -translate-x-1/2 cursor-col-resize
           after:absolute after:top-0 after:left-1/2 after:h-full after:w-px
           after:-translate-x-1/2 after:bg-transparent after:transition-colors
           after:duration-100 hover:after:bg-primary/50 {dividing
      ? 'after:w-0.5 after:bg-primary'
      : ''}"
    onpointerdown={startDivide}
    onpointermove={onDivideMove}
    onpointerup={endDivide}
    onpointercancel={endDivide}
    ondblclick={() => preferences.setDetailLabelShare(DEFAULT_DETAIL_LABEL_SHARE)}
  ></span>
</div>
