<!--
  A note that only appears when asked for.

  Some facts qualify a figure without belonging beside it permanently — that a
  cluster has slots on tainted nodes, that a number is measured somewhere
  unexpected. Printed inline they push the card around and compete with the
  figures for attention; dropped entirely they leave a number that looks wrong
  to anybody who checks it against kubectl.

  Both hover and click open it. Hover alone is a rule that excludes anybody on
  a touch screen and anybody navigating by keyboard, and a click that had to be
  aimed at a 14px target would be worse than the inline sentence it replaced.

  THE PANEL IS POSITIONED AGAINST THE WINDOW, NOT AGAINST THE ICON, and that is
  a fix rather than a preference. It used to hang above the icon on
  `absolute bottom-full`, which works for a hint on a card halfway down the
  page and fails for the one beside the search field: that icon sits a few
  pixels below the top of `<main>`, which is `overflow-hidden`, so the note was
  drawn above the toolbar and clipped off. Nothing about the icon's own box can
  escape an ancestor that clips, so the panel is `position: fixed` and placed
  from the button's measured rectangle — below it by default, above only when
  below would run off the bottom, and never past either side.
-->
<script lang="ts">
  import { Info } from '@lucide/svelte'

  interface Props {
    /** The note itself. */
    text: string
    /** Names what the note is about, for anyone who cannot see the icon. */
    label: string
  }

  let { text, label }: Props = $props()

  let clicked = $state(false)
  let pointed = $state(false)
  let focused = $state(false)

  const open = $derived(clicked || pointed || focused)

  /** How far the panel sits from the icon, and from the window's edges. */
  const GAP = 6
  const MARGIN = 8
  /** Matches the `w-64` below. Read here so the clamp cannot drift from it. */
  const WIDTH = 256

  let anchor = $state<HTMLButtonElement | null>(null)
  let panelHeight = $state(0)
  /** Bumped to re-measure while the panel is open — see the effect below. */
  let moved = $state(0)

  /**
   * Where the panel goes, in window coordinates.
   *
   * Depends on `moved` as well as on `open` so that a scroll or a resize while
   * the note is showing recomputes it. On the first frame `panelHeight` is
   * still 0, which resolves to the below-the-icon case — the same answer the
   * measurement gives in all but the rare flip, so there is nothing to see.
   */
  const position = $derived.by(() => {
    void moved
    if (!open || !anchor) return null

    const rect = anchor.getBoundingClientRect()
    const below = rect.bottom + GAP
    const fitsBelow = below + panelHeight <= window.innerHeight - MARGIN
    const top = fitsBelow ? below : Math.max(MARGIN, rect.top - GAP - panelHeight)

    const left = Math.min(
      Math.max(MARGIN, rect.left),
      Math.max(MARGIN, window.innerWidth - WIDTH - MARGIN),
    )

    return { top, left }
  })

  /**
   * Re-measure while it is open.
   *
   * The toolbar does not scroll, but a hint on the overview does, and a note
   * left behind by the thing it describes is worse than one that never opened.
   */
  $effect(() => {
    if (!open) return

    const remeasure = (): void => {
      moved += 1
    }
    window.addEventListener('scroll', remeasure, true)
    window.addEventListener('resize', remeasure)
    return () => {
      window.removeEventListener('scroll', remeasure, true)
      window.removeEventListener('resize', remeasure)
    }
  })

  /**
   * Escape closes it, because a panel opened by a click needs a way out that
   * is not "find the icon again".
   */
  function onKeydown(event: KeyboardEvent): void {
    if (event.key === 'Escape' && clicked) {
      event.stopPropagation()
      clicked = false
    }
  }
</script>

<span class="inline-flex items-center">
  <button
    bind:this={anchor}
    type="button"
    aria-label={label}
    aria-expanded={open}
    onclick={() => (clicked = !clicked)}
    onpointerenter={() => (pointed = true)}
    onpointerleave={() => (pointed = false)}
    onfocus={() => (focused = true)}
    onblur={() => {
      focused = false
      clicked = false
    }}
    onkeydown={onKeydown}
    class="state-layer flex size-5 shrink-0 items-center justify-center rounded-full
           text-on-surface-variant/60 transition-colors duration-100
           hover:bg-surface-container hover:text-on-surface-variant"
  >
    <Info class="size-3.5" strokeWidth={2} />
  </button>

  {#if open && position}
    <!-- The left edge is anchored to the icon rather than centred on it.
         Centring reads better in the abstract and is wrong here: these icons
         follow a short label at the left of a card, so a centred panel hung
         half of itself over the card's edge and lost its first few words. -->
    <span
      role="tooltip"
      bind:clientHeight={panelHeight}
      style="top: {position.top}px; left: {position.left}px;"
      class="pointer-events-none fixed z-50 w-64 rounded-sm border border-outline-variant
             bg-surface-container-high px-3 py-2 text-body-small leading-relaxed
             font-normal text-on-surface shadow-level-2"
    >
      {text}
    </span>
  {/if}
</span>
