<!--
  A tick box.

  The last native control in the application, and it was drawn by the browser
  in the browser's own blue through `accent-primary` — a platform checkbox
  sitting beside a radio, a switch and a menu that are all drawn by us.
  Like Radio.svelte beside it, THE NATIVE INPUT IS STILL HERE, hidden under
  the drawing rather than replaced by it: space toggles it, tab reaches it,
  the wrapping label names it and makes the whole row a target, and
  `indeterminate` is a real tri-state the browser announces as mixed. A div
  with `role="checkbox"` has to re-earn all of that and usually earns some.

  WHY THE DRAWING IS NOT THE INPUT'S `:checked` STATE, which is the whole
  reason this component exists rather than a class on the native box.

  A row's tick box CANCELS the browser's own toggle (see RowSelect): a
  shift-click on an already ticked row adds a range and must leave that row
  ticked, so the box has to be a view of the selection and never a thing the
  browser flips. Cancelling is load-bearing and stays.

  What that collides with is Svelte's `set_checked`, which caches the value it
  last wrote and returns early when the new one matches — it cannot know the
  browser changed the DOM in between. A cancelled click goes: the browser
  ticks the box during pre-click activation, the handler cancels and the store
  updates, Svelte flushes and records `true` against a DOM that is already
  `true` so it writes nothing, and THEN the browser's canceled activation
  steps put the DOM back to `false`. The cache says ticked, the DOM says not,
  and every later write short-circuits — so the box never shows a tick again
  for the life of the page, however many times the row is selected. The
  table's select-all box escaped it only because it uses change and cancels
  nothing.

  So the tick is DOM STRUCTURE instead: an `{#if}` that inserts or removes the
  mark. Svelte applies that unconditionally, there is no value to compare, and
  the browser has no way to revert an element it did not create. Do not
  "simplify" this back to an `input:checked ~ .box` rule — that is the bug,
  written in CSS instead of JavaScript.

  The `data-state` attribute beside it is safe for the same reason inverted:
  it lives on a `<span>`, and the browser writes attributes on no span
  anywhere. A cache can only go stale for something two parties write.

  Reduced motion is handled once, globally, at the foot of app.css — it
  neutralises `animation-duration` as well as `transition-duration`, so do not
  add a second media query here. Two of them drift, and the one inside a
  component is the one that gets forgotten.
-->
<script lang="ts">
  import type { Snippet } from 'svelte'

  interface Props {
    /**
     * Whether the box is ticked.
     *
     * A plain input, never bindable: the drawn state is a pure function of
     * this prop, and a component that could also decide it for itself would
     * be able to disagree with the caller that cancels the browser — which is
     * the disagreement this whole file exists to make impossible. Callers
     * holding the state locally pass it back through `onchange`.
     */
    checked?: boolean
    /**
     * Some but not all of what this box stands for — the table header's
     * select-all when part of a page is ticked.
     *
     * Its own state rather than a shade of checked, and it OUTRANKS `checked`
     * the way the platform's does: a box that is both is drawn and announced
     * as mixed, because "some" is the more specific claim and the one an
     * operator has to act on.
     */
    indeterminate?: boolean
    disabled?: boolean
    /**
     * The accessible name, when the row's own content is not one.
     *
     * The wrapping label already names the input from whatever is rendered
     * beside it, so this is only for a box that stands alone in a cell.
     */
    ariaLabel?: string
    /** A tooltip on the whole row, control and content alike. */
    title?: string
    /**
     * Aligns the control against a row of more than one line.
     *
     * `center` for a one-line option; `start` puts the box on the first line
     * of an option that carries a description under its title, where centring
     * against the whole block leaves it floating between the two.
     */
    align?: 'center' | 'start'
    /**
     * Tightens the gap between the control and its content.
     *
     * A prop rather than a class the caller passes, because two `gap-*`
     * utilities on one element do not resolve by attribute order — Tailwind
     * emits them in its own order and the larger one wins whatever the caller
     * wrote. One class, chosen here, is the only way a caller reliably gets
     * the spacing it asked for.
     */
    dense?: boolean
    /**
     * Makes the label fill its line, for a whole menu row that is one target.
     *
     * A prop rather than `class="flex w-full"` from the caller, because
     * Tailwind emits `inline-flex` after `flex` in its own canonical order and
     * the later rule wins whatever the caller wrote — a caller "overriding"
     * the display would silently get the default and a row that only reacts
     * where the words are.
     */
    full?: boolean
    /**
     * The browser toggled the box and reports what it now holds.
     *
     * The ordinary path, and the one to reach for: it is a change event, so
     * it fires for the keyboard exactly as it does for the pointer.
     */
    onchange?: (checked: boolean) => void
    /**
     * The raw click, for a caller that must read a modifier key or cancel the
     * browser's toggle.
     *
     * Change events carry no `shiftKey`, which is the whole reason this is
     * exposed — a range select cannot be built on `onchange`. A caller that
     * calls `preventDefault()` here gets no `onchange`, by the browser's own
     * rules, and owns the state itself from then on.
     */
    onclick?: (event: MouseEvent & { currentTarget: HTMLInputElement }) => void
    class?: string
    children?: Snippet
    /**
     * `data-*` markers ride through to the native input.
     *
     * DataTable's Space key finds a row's box by `input[data-row-select]`, so
     * the marker has to land on the input and not on the label around it.
     * Deliberately only `data-*`: everything else stays a typo the checker
     * catches.
     */
    [key: `data-${string}`]: unknown
  }

  let {
    checked = false,
    indeterminate = false,
    disabled = false,
    ariaLabel,
    title,
    align = 'center',
    dense = false,
    full = false,
    onchange,
    onclick,
    class: className = '',
    children,
    ...markers
  }: Props = $props()

  /**
   * The one thing the drawing reads.
   *
   * NOT named `state`, which is what it was called for about an hour: a
   * variable of that name in a Svelte 5 component turns every later `$state`
   * into a store subscription on it, and the compiler's complaint names
   * neither this line nor the collision. `drawn` also says what it is for —
   * the input's own properties are set separately, below.
   */
  const drawn = $derived(indeterminate ? 'indeterminate' : checked ? 'checked' : 'unchecked')

  let input = $state<HTMLInputElement | null>(null)

  /**
   * Puts the native properties back in step with the props.
   *
   * These are written HERE and never in the markup, so that Svelte keeps no
   * cached copy of either one to go stale — see the note at the top of this
   * file. Writing unconditionally costs nothing: the browser ignores a
   * property set to what it already holds.
   */
  function syncNative(): void {
    const node = input
    if (!node) return
    node.checked = checked
    node.indeterminate = indeterminate
  }

  $effect(syncNative)
</script>

<!--
  No type scale and no text colour here, unlike Radio.
  A dozen call sites carry their own — body-small in a menu row, body-medium in
  a dialog, on-surface-variant beside a muted label — and two utilities setting
  the same property do NOT resolve by the order the caller wrote them in:
  Tailwind emits them in its own order and whichever it emits last wins. A
  default here would therefore beat some callers and lose to others, which is
  the worst of both. The label inherits, and the caller says what it wants.
-->
<label
  {title}
  class="{full ? 'flex w-full' : 'inline-flex'}
         {align === 'start' ? 'items-start' : 'items-center'}
         {dense ? 'gap-2' : 'gap-3'}
         {disabled ? 'cursor-default' : 'cursor-pointer'}
         {className}"
>
  <span class="control" data-align={align}>
    <!--
      The input carries no visual of its own: transparent, borderless, sized
      to the box exactly. It stays fully opaque rather than `opacity: 0`
      because the keyboard focus ring is drawn by the browser as an outline on
      THIS element — the one app.css gives every control, in the themed accent
      so it reads on the dark surface and the light one — and an outline on a
      transparent element shows while an outline on an invisible one does not.
    -->
    <input
      bind:this={input}
      type="checkbox"
      {disabled}
      aria-label={ariaLabel}
      onchange={(event) => onchange?.(event.currentTarget.checked)}
      onclick={(event) => {
        onclick?.(event)
        // A cancelled click is put back by the browser AFTER every listener
        // has run, so nothing done here or in the caller can win that race —
        // only a later frame can. The drawing does not care either way; this
        // is so the state a screen reader reads off the input is still the
        // truth on a row whose box the caller drives itself.
        if (event.defaultPrevented) requestAnimationFrame(syncNative)
      }}
      {...markers}
    />
    <span class="state" data-state={drawn} aria-hidden="true"></span>
    <span class="box" data-state={drawn} aria-hidden="true">
      {#if drawn === 'checked'}
        <svg class="mark" viewBox="0 0 16 16" fill="none" aria-hidden="true">
          <path
            d="M3.5 8.4 6.4 11.3 12.5 5"
            stroke="currentColor"
            stroke-width="2"
            stroke-linecap="round"
            stroke-linejoin="round"
          />
        </svg>
      {:else if drawn === 'indeterminate'}
        <span class="dash"></span>
      {/if}
    </span>
  </span>

  <!-- `flex-1` only on a full-width row, where the content is what has to take
       up the slack; on an inline control it would stretch the label past its
       own words. What goes INSIDE is the caller's — a row that needs a
       trailing icon lays that out itself, because this component cannot know
       which of a caller's several children is the one that should truncate. -->
  {#if children}
    <span class="min-w-0 {full ? 'flex-1' : ''}">{@render children()}</span>
  {/if}
</label>

<style>
  /*
   * Scoped rather than utilities in app.css, because none of this is reusable:
   * it is four elements that only mean anything stacked on each other in this
   * one order. Every colour, radius, duration and easing below is still a
   * token from app.css — the scoping decides where the rules live, never what
   * they are allowed to say.
   *
   * 16px, where Radio's ring is 20px and the spec's box is 18dp. The select
   * column is 40px wide with 24px of padding in it (see RowSelect), so 16 is
   * what fits without widening a column that every list shares; a control an
   * operator meets in a dense table is also not the place to be the largest
   * thing in the row.
   */
  .control {
    position: relative;
    display: inline-grid;
    place-items: center;
    inline-size: 16px;
    block-size: 16px;
    flex: none;

    /* MEASURED, NOT GUESSED: the state layer used to be a grid item, and
       being larger than this box it sized the implicit track — 24px around a
       16px control. `place-items: center` then centred the box in the TRACK
       rather than in the control, putting it four pixels below everything
       else on the row. It is taken out of flow below so the track is this
       box, and centring means what it says. The radio had the same fault for
       the same reason. */

    /* The state layer sits BEHIND the box (`z-index: -1` below), and without
       a new stacking context here that negative index escapes upwards and
       paints behind the row's own background instead of behind the box. */
    isolation: isolate;
  }

  /* A control beside a two-line option lines up with the cap height of the
     first line, not with the top of its box. */
  .control[data-align='start'] {
    margin-block-start: 2px;
  }

  input {
    appearance: none;
    margin: 0;
    grid-area: 1 / 1;
    inline-size: 16px;
    block-size: 16px;
    border: 0;
    border-radius: var(--radius-xxs);
    background: transparent;
    cursor: inherit;
    z-index: 1;
  }

  /*
   * 24px, not the spec's 40dp touch target.
   *
   * This is a dense desktop client: table rows are about thirty pixels tall,
   * and a 40px circle around a 16px box would reach into the rows above and
   * below far enough to read as a smear rather than as a highlight. 24px is
   * the same four-pixel halo Radio draws around its ring, so a checkbox and a
   * radio hover identically.
   */
  .state {
    /* ABSOLUTE, so it cannot size the grid track it used to sit in. A halo
       wider than the control is the point of it; a halo that makes the
       control's own box smaller than its track is how the drawing ended up
       four pixels below the row it belongs to. */
    position: absolute;
    inline-size: 24px;
    block-size: 24px;
    border-radius: 9999px;
    background-color: var(--color-on-surface);
    opacity: 0;
    pointer-events: none;
    transition: opacity var(--duration-short) var(--ease-standard);
    z-index: -1;
  }

  .box {
    grid-area: 1 / 1;
    display: grid;
    place-items: center;
    inline-size: 16px;
    block-size: 16px;
    /* 1.5px, not 2. Every other edge in the application is a hairline, and a
       two-pixel stroke on a sixteen-pixel box is an eighth of it — which is
       what read as heavy beside the icons it sits next to. */
    border: 1.5px solid var(--color-on-surface-variant);
    border-radius: var(--radius-xxs);
    color: var(--color-on-primary);
    pointer-events: none;
    transition:
      background-color var(--duration-short) var(--ease-standard),
      border-color var(--duration-short) var(--ease-standard);
  }

  /* Ticked and mixed are the same container — filled, no separate border —
     because they differ in what they CLAIM, not in how firmly. */
  .box[data-state='checked'],
  .box[data-state='indeterminate'] {
    background-color: var(--color-primary);
    border-color: var(--color-primary);
  }

  .state[data-state='checked'],
  .state[data-state='indeterminate'] {
    background-color: var(--color-primary);
  }

  /*
   * The mark arrives rather than appears.
   *
   * An ANIMATION and not a transition, because the element is INSERTED by the
   * `{#if}` above rather than restyled — there is no previous value for a
   * transition to run from. That is the cost of drawing from structure, and
   * it is the right cost: the reduced-motion block in app.css neutralises
   * animation and transition alike, so this is honoured for free.
   */
  .mark {
    inline-size: 12px;
    block-size: 12px;
    animation: mark-in var(--duration-short) var(--ease-standard);
  }

  .dash {
    inline-size: 8px;
    block-size: 2px;
    border-radius: 9999px;
    background-color: currentColor;
    animation: mark-in var(--duration-short) var(--ease-standard);
  }

  @keyframes mark-in {
    from {
      opacity: 0;
      transform: scale(0.6);
    }
  }

  label:hover input:not(:disabled) ~ .state {
    opacity: var(--state-hover);
  }

  input:focus-visible ~ .state {
    opacity: var(--state-focus);
  }

  input:active:not(:disabled) ~ .state {
    opacity: var(--state-pressed);
  }

  /*
   * Disabled comes LAST on purpose. These match at the same specificity as
   * the `[data-state]` rules above, so source order is the only thing
   * deciding what colour a disabled-and-ticked box ends up: move this block
   * upwards and a disabled ticked checkbox stays in full primary blue,
   * telling an operator it can be changed when it cannot.
   *
   * MD3's disabled state is the content colour at 38%, and the mark inside a
   * filled one switches to the surface — on-primary is tuned to sit on the
   * accent, and on a grey that is 38% of the text colour it is not legible in
   * either theme.
   */
  input:disabled ~ .box {
    border-color: color-mix(in srgb, var(--color-on-surface) 38%, transparent);
    background-color: transparent;
    color: var(--color-surface);
  }

  input:disabled ~ .box[data-state='checked'],
  input:disabled ~ .box[data-state='indeterminate'] {
    background-color: color-mix(in srgb, var(--color-on-surface) 38%, transparent);
  }
</style>
