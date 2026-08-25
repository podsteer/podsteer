<!--
  The status of one row, as a single mark.

  Shape carries the meaning and colour grades it: a triangle means something
  wants attention, a filled disc means it does not — in the same blue, amber
  and red every bar in the application uses, so the palette has to be learnt
  once.

  That ordering matters: colour alone would leave amber and red
  indistinguishable to a reader who cannot separate them, whereas a triangle
  is a triangle at any setting, and the full text is always on the element as
  its accessible name and tooltip.

  Where the caller passes the kind's own icon, that is drawn instead and only
  the colour varies. It is the better trade in a table: the glyph then marks
  where the row begins AND carries its state, which is one mark doing the work
  of the two the row used to carry. The cost is that colour becomes the only
  channel for those rows, so the status text stays on the element as its
  accessible name and tooltip.

  Without one, a disc rather than a tick for the healthy state. A check mark
  is the symbol of a control somebody operates — a box they tick — and in a
  column of clickable rows it invited being read as one.

  No label and no badge beside it. A status column repeating "Running" down
  two hundred rows is two hundred words nobody reads, and the whole value of
  the column is that the exceptions are visible without reading any of it.
-->
<script lang="ts">
  import type { Component } from 'svelte'
  import type { Tone } from '$lib/format'
  import { AlertTriangle, Circle } from '@lucide/svelte'

  interface Props {
    /** Semantic tone driving the colour. */
    tone?: Tone
    /** The status text. Not drawn, but named for assistive technology. */
    label: string
    /**
     * The kind's own icon, drawn in the tone's colour.
     *
     * A table lists one kind, so an icon repeated beside every name said
     * nothing and cost a glyph. Moved into the status column it does two jobs
     * at once: it marks where the row begins, and its colour is the row's
     * state. Without one, the mark falls back to a shape.
     */
    icon?: Component
    /** Animates the mark, for states that are actively changing. */
    pulse?: boolean
    class?: string
  }

  let { tone = 'neutral', label, icon, pulse = false, class: className = '' }: Props = $props()

  /** Attention is a triangle; everything else is a disc. */
  const NEEDS_ATTENTION: Tone[] = ['warning', 'error']

  /**
   * The same three colours the bars use, and no others.
   *
   * Green went with them. It was the only place in the application asserting
   * a fourth meaning, and "fine" is already what blue says on every gauge —
   * a reader who has learnt the palette once should not meet a new colour in
   * a table column.
   *
   * They are the fixed gauge tokens rather than the theme's semantic roles,
   * so a mark means the same thing in light and dark. See app.css.
   */
  const COLOUR: Record<Tone, string> = {
    success: 'text-gauge-normal',
    warning: 'text-gauge-warn',
    error: 'text-gauge-critical',
    info: 'text-gauge-normal',
    // Nothing to report, and not a claim that anything is well: a Normal
    // event is a record, not a verdict.
    neutral: 'text-on-surface-variant/40',
  }

  const attention = $derived(NEEDS_ATTENTION.includes(tone))
  const StatusIcon = $derived(icon ?? (attention ? AlertTriangle : Circle))
  /** Only the fallback disc is filled; a kind's icon is drawn as itself. */
  const filled = $derived(!icon && !attention)
</script>

<span class="inline-flex {className}" title={label} aria-label={label} role="img">
  <StatusIcon
    class="size-4 shrink-0 {COLOUR[tone]} {pulse ? 'animate-pulse' : ''}"
    strokeWidth={2}
    fill={filled ? 'currentColor' : 'none'}
  />
</span>
