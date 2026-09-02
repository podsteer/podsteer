<!--
  The draggable boundary between a detail pane's two columns.

  ONE DIVIDER, SHOWN ONCE PER PANE, MOVING ALL OF THEM. Dragging the one in
  Identity moves the one in Labels, in Annotations and in every container card,
  because there is only one column width and every section reads it. A panel
  whose sections could disagree about where the columns divide would stop being
  a panel and start being a stack of tables.

  A component rather than markup repeated in each list, because the panes are
  not all built the same way: most are DetailList, but a node's pods and a
  namespace's contents are their own grids on the same `detail-grid`. They had
  the columns and not the handle, which read as the divider being broken on
  exactly those two sections.

  What is dragged is a SHARE of the pane rather than a pixel width, so a pane
  nested inside a container card — narrower than the sections around it —
  stays in proportion while the pointer moves rather than jumping into
  proportion when it is released.
-->
<script lang="ts">
  import {
    preferences,
    detailLabelBounds,
    DEFAULT_DETAIL_LABEL_SHARE,
  } from '$stores/preferences.svelte'

  interface Props {
    /**
     * The grid this divides, for measuring against.
     *
     * The caller's own element, because only it knows which of its children
     * is the grid — and the divider has to be positioned against a `relative`
     * ancestor the caller provides too.
     */
    pane: HTMLElement | null
  }

  let { pane }: Props = $props()

  let dividing = $state(false)

  /** The gap between the columns, in the middle of which the divider sits. */
  function rootFontSize(): number {
    const size = parseFloat(getComputedStyle(document.documentElement).fontSize)
    return Number.isFinite(size) && size > 0 ? size : 16
  }

  function start(event: PointerEvent): void {
    event.preventDefault()
    dividing = true
    ;(event.currentTarget as HTMLElement).setPointerCapture(event.pointerId)
  }

  function move(event: PointerEvent): void {
    if (!dividing || !pane) return

    const box = pane.getBoundingClientRect()
    if (box.width <= 0) return

    const gap = rootFontSize()
    const { min, max } = detailLabelBounds(box.width, gap)
    // The divider sits in the middle of the gap, so the column ends half a gap
    // before the pointer. Measured from the pane's own left edge rather than
    // from where the drag started: the two diverge the moment a bound bites,
    // and the divider then stops following the pointer until it has been
    // dragged all the way back.
    const width = Math.min(max, Math.max(min, event.clientX - box.left - gap / 2))
    preferences.labelShareDrag = width / box.width
  }

  function end(): void {
    if (preferences.labelShareDrag !== null) {
      preferences.setDetailLabelShare(preferences.labelShareDrag)
    }
    dividing = false
  }
</script>

<!--
  Invisible until the pointer is near it, like the navigator's edge and the
  panel's own — a permanent rule between every label and every value would
  draw a line down a pane whose whole job is the text.

  Positioned from the column width rather than measured, so it stays on the
  boundary as the panel is resized with nothing listening for it.
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
  onpointerdown={start}
  onpointermove={move}
  onpointerup={end}
  onpointercancel={end}
  ondblclick={() => preferences.setDetailLabelShare(DEFAULT_DETAIL_LABEL_SHARE)}
></span>
