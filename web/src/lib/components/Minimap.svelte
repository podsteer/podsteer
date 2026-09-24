<!--
  A minimap down the right edge of a maximized text pane — the manifest or
  the logs — in the manner of Sublime Text's and VS Code's.

  Each line is drawn as a thin bar shaped by its own text: indented where the
  line is indented, as long as the line is long. That shape is what makes it
  a MAP rather than a scrollbar — a manifest's blocks and a stack trace's
  indentation are recognisable in it — and the matches the search box found
  are drawn over it, full width, so "where are they" and "what is around
  them" are answered in the same glance.

  THE HOST OWNS THE GEOMETRY. Lines are counted by index here, never by pixel,
  because the two hosts measure differently — CodeMirror wraps and folds, the
  log list virtualises with measured row heights — and each already knows how
  to turn a line into a scroll position and back. So a host passes which
  lines are in view and is asked to show one; this component never touches
  its scroller.

  Scaled to fit rather than scrolled: the whole document is always visible,
  which is the point of it next to a search. A short document is drawn at
  most MAX_LINE_PX per line and simply ends partway down.

  On a CANVAS rather than as elements: five thousand log lines are five
  thousand divs, which is the cost the log list's own virtualisation exists
  to avoid.
-->
<script lang="ts">
  interface Props {
    /** The document, one entry per line, in the order the pane shows them. */
    lines: readonly string[]
    /** Indexes into `lines` that match the current search. */
    marks?: readonly number[]
    /** The match Enter last moved to, drawn stronger than the rest. */
    current?: number
    /** The first and last line in view, inclusive. */
    viewport: { first: number; last: number }
    /** Asked to bring a line into view, centred. */
    onjump: (line: number) => void
  }

  let { lines, marks = [], current = -1, viewport, onjump }: Props = $props()

  /** Tallest a line is drawn, so a twenty-line manifest is not twenty slabs. */
  const MAX_LINE_PX = 3
  /** The text width the map's width stands for, in characters. */
  const COLUMNS = 120

  let canvas = $state<HTMLCanvasElement | null>(null)
  let width = $state(0)
  let height = $state(0)
  /** Bumped when the theme changes, so the colours are read again. */
  let themeVersion = $state(0)

  const lineHeight = $derived(lines.length > 0 ? Math.min(MAX_LINE_PX, height / lines.length) : 0)

  $effect(() => {
    // The palette lives on the root and changes with it; a MutationObserver
    // is the one signal that covers the explicit toggle AND the "system"
    // setting, which the preferences store resolves onto that attribute.
    const observer = new MutationObserver(() => themeVersion++)
    observer.observe(document.documentElement, { attributes: true, attributeFilter: ['data-theme', 'class'] })
    return () => observer.disconnect()
  })

  $effect(() => {
    void themeVersion
    const element = canvas
    if (!element || width === 0 || height === 0) return

    const ratio = window.devicePixelRatio || 1
    element.width = Math.round(width * ratio)
    element.height = Math.round(height * ratio)
    const context = element.getContext('2d')
    if (!context) return
    context.setTransform(ratio, 0, 0, ratio, 0, 0)
    context.clearRect(0, 0, width, height)

    const style = getComputedStyle(element)
    const ink = style.getPropertyValue('--on-surface-variant').trim() || '#888'
    const match = style.getPropertyValue('--gauge-warn').trim() || '#e0a030'
    const lens = style.getPropertyValue('--on-surface').trim() || '#fff'

    const step = lineHeight
    const bar = Math.max(step * 0.7, 0.5)
    const column = width / COLUMNS

    // The text. At fewer than a pixel per line many lines share a row of
    // pixels, and drawing each at low alpha lets a dense block read darker
    // than a sparse one, which is the texture that makes it a map.
    context.globalAlpha = step >= 1 ? 0.35 : 0.2
    context.fillStyle = ink
    for (let i = 0; i < lines.length; i++) {
      const text = lines[i]
      const indent = text.length - text.trimStart().length
      const length = text.trim().length
      if (length === 0) continue
      const x = Math.min(indent, COLUMNS) * column
      context.fillRect(x, i * step, Math.min(length * column, width - x), bar)
    }

    // The matches, full width so a single hit in five thousand lines is
    // still a visible stroke rather than a speck at one line's indent.
    context.fillStyle = match
    const markHeight = Math.max(step, 2)
    for (const index of marks) {
      context.globalAlpha = index === current ? 1 : 0.6
      context.fillRect(0, index * step, width, markHeight)
    }

    // The part of the document on screen.
    context.globalAlpha = 0.12
    context.fillStyle = lens
    const top = viewport.first * step
    const bottom = (viewport.last + 1) * step
    context.fillRect(0, top, width, Math.max(bottom - top, 4))
    context.globalAlpha = 1
  })

  /** The line under a pointer, clamped to the document. */
  function lineAt(event: PointerEvent): number {
    if (!canvas || lineHeight === 0) return 0
    const y = event.clientY - canvas.getBoundingClientRect().top
    return Math.max(0, Math.min(lines.length - 1, Math.floor(y / lineHeight)))
  }

  let dragging = false

  function onpointerdown(event: PointerEvent): void {
    if (event.button !== 0 || lines.length === 0) return
    dragging = true
    ;(event.currentTarget as HTMLElement).setPointerCapture(event.pointerId)
    onjump(lineAt(event))
  }

  function onpointermove(event: PointerEvent): void {
    if (dragging) onjump(lineAt(event))
  }

  function onpointerup(event: PointerEvent): void {
    dragging = false
    ;(event.currentTarget as HTMLElement).releasePointerCapture(event.pointerId)
  }
</script>

<!-- Hidden from assistive technology: it is a picture of text that is fully
     present, and navigable, in the pane beside it. -->
<div
  class="relative h-full w-24 shrink-0 cursor-pointer border-l border-outline-variant/40
         bg-surface-container-lowest"
  bind:clientWidth={width}
  bind:clientHeight={height}
  aria-hidden="true"
  data-minimap
>
  <canvas
    bind:this={canvas}
    class="absolute inset-0 h-full w-full touch-none"
    {onpointerdown}
    {onpointermove}
    {onpointerup}
    onpointercancel={onpointerup}
  ></canvas>
</div>
