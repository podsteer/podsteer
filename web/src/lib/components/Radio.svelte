<!--
  One option in a radio group.

  The application had raw `<input type="radio">` in three places, each styled
  by `accent-primary` or by nothing at all — which is the platform's own radio
  in the browser's own blue, the one control on screen that did not follow the
  design system.

  THE NATIVE INPUT IS STILL HERE, hidden under the drawing rather than
  replaced by it, and that is the decision worth keeping. A set of native
  radios sharing a `name` is ONE tab stop, the arrow keys move between the
  options and check as they go, the wrapping label makes the whole row a
  target, and the checked state is the browser's to expose. A div with
  `role="radio"` and a hand-written roving tabindex has to re-earn every one
  of those, and usually earns most of them.

  What is drawn: a ring that fills with a centre dot, with a state layer
  behind it for hover, focus and press. The ring, dot and layer are three
  SIBLINGS of the input rather than pseudo-elements on it, because an
  `<input>` is a replaced element and whether it grows pseudo-elements at all
  has never been something to rely on.

  Reduced motion is handled once, globally, at the foot of app.css — do not
  add a second media query here. Two of them drift, and the one inside a
  component is the one that gets forgotten.
-->
<script lang="ts">
  import type { Snippet } from 'svelte'

  /** What a group's options are keyed by. */
  type Value = string | number

  interface Props {
    /**
     * The native group name, and it is REQUIRED.
     *
     * This is what makes a set of these one tab stop with the arrows moving
     * inside it: the browser groups radios by name, and radios without one
     * are each their own group — every option a separate tab stop, the arrow
     * keys doing nothing. Two unrelated groups sharing a name on one screen
     * merge into a single group instead, so the name has to be unique per
     * group rather than per component.
     */
    name: string
    /** This option's value. */
    value: Value
    /**
     * The group's current value, for `bind:group`.
     *
     * Two ways to drive the control, because the call sites genuinely differ:
     * bind this where the choice is local state, or pass `checked` and
     * `onchange` where the truth lives in a store and the change has to be
     * written through it. Leaving this undefined is what selects the second
     * mode — so a caller binding a value that starts out `undefined` would
     * silently get the `checked` prop instead, which is why every group here
     * starts from a real value.
     */
    group?: Value
    /** Whether this option is chosen, when `group` is not bound. */
    checked?: boolean
    disabled?: boolean
    /**
     * The accessible name, when the row's own content is not one.
     *
     * The wrapping label already names the input from whatever is rendered
     * beside it, so this is only for a row whose visible content is a bare
     * figure or an icon that does not read as a choice on its own.
     */
    ariaLabel?: string
    /**
     * Aligns the control against a row of more than one line.
     *
     * `center` for a one-line option; `start` puts the ring on the first line
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
    /** Called with this option's value when it is chosen. */
    onchange?: (value: Value) => void
    class?: string
    children?: Snippet
  }

  let {
    name,
    value,
    group = $bindable(),
    checked = false,
    disabled = false,
    ariaLabel,
    align = 'center',
    dense = false,
    onchange,
    class: className = '',
    children,
  }: Props = $props()

  const isChecked = $derived(group === undefined ? checked : group === value)

  function select(): void {
    if (group !== undefined) group = value
    onchange?.(value)
  }
</script>

<label
  class="inline-flex text-body-medium text-on-surface
         {align === 'start' ? 'items-start' : 'items-center'}
         {dense ? 'gap-2' : 'gap-3'}
         {disabled ? 'cursor-default' : 'cursor-pointer'}
         {className}"
>
  <span class="control" data-align={align}>
    <!--
      The input carries no visual of its own: transparent, borderless, sized
      to the ring exactly. It stays fully opaque rather than `opacity: 0`
      because the keyboard focus ring is drawn by the browser as an outline on
      THIS element, and an outline on a transparent element shows while an
      outline on an invisible one does not.
    -->
    <input
      type="radio"
      {name}
      {disabled}
      value={String(value)}
      checked={isChecked}
      aria-label={ariaLabel}
      onchange={select}
    />
    <span class="state" aria-hidden="true"></span>
    <span class="ring" aria-hidden="true"></span>
    <span class="dot" aria-hidden="true"></span>
  </span>

  {#if children}
    <span class="min-w-0">{@render children()}</span>
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
   * One 20px cell with everything stacked in it. `place-items: center` is what
   * lets the state layer be larger than that cell and stay concentric with the
   * ring anyway, so the layer's size can change with nothing repositioned.
   */
  .control {
    position: relative;
    display: inline-grid;
    place-items: center;
    /* The ring's size, and it has to stay the ring's size: this box is what
       the row centres, so a control larger than what it draws puts the
       drawing off-centre by half the difference. */
    inline-size: 16px;
    block-size: 16px;
    flex: none;

    /* The state layer sits BEHIND the ring (`z-index: -1` below), and without
       a new stacking context here that negative index escapes upwards and
       paints behind the row's own background instead of behind the ring. */
    isolation: isolate;
  }

  /* A control beside a two-line option lines up with the cap height of the
     first line, not with the top of its box. */
  .control[data-align='start'] {
    margin-block-start: 1px;
  }

  input {
    appearance: none;
    margin: 0;
    grid-area: 1 / 1;
    /* The ring's size, and the reason to keep saying so: this is a grid item,
       so anything here larger than the control sizes the track and pushes the
       drawing off centre — which is exactly the fault the state layer caused
       before it was taken out of flow. The input carries the focus ring, so
       it has to be the control's size rather than merely inside it. */
    inline-size: 16px;
    block-size: 16px;
    border: 0;
    border-radius: 9999px;
    background: transparent;
    cursor: inherit;
    z-index: 1;
  }

  /*
   * 28px, not the spec's 40dp touch target.
   *
   * This is a dense desktop client: option rows sit six pixels apart, and a
   * 40px circle around a 20px control would reach into the rows above and
   * below far enough to read as a smear rather than as a highlight. 28px
   * still reads as a layer around the ring and stays inside its own row.
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

  .ring {
    grid-area: 1 / 1;
    /* SIXTEEN, matching the checkbox and the 16px icons the rest of the
       interface is built from. Twenty is the size the design language names,
       and it is the right size in a form of its own; sitting beside a
       sixteen-pixel checkbox in the same pane it simply read as bigger. One
       size for both is worth more here than either number is. */
    inline-size: 16px;
    block-size: 16px;
    border: 1.5px solid var(--color-on-surface-variant);
    border-radius: 9999px;
    pointer-events: none;
    transition: border-color var(--duration-short) var(--ease-standard);
  }

  /*
   * The dot is always in the DOM, scaled to nothing when unchecked, so that
   * choosing an option is a transition rather than an element appearing.
   * Animating `transform` also keeps the ring's geometry out of it: growing
   * the dot's width instead would move its own box on every frame, and a
   * centred grid cell would chase it.
   */
  .dot {
    grid-area: 1 / 1;
    /* Half the ring, which is the proportion the design language uses and the
       thing that has to hold when the ring changes size — not the 10px it
       happened to be when the ring was twenty. */
    inline-size: 8px;
    block-size: 8px;
    border-radius: 9999px;
    background-color: var(--color-primary);
    transform: scale(0);
    pointer-events: none;
    transition: transform var(--duration-short) var(--ease-standard);
  }

  input:checked ~ .ring {
    border-color: var(--color-primary);
  }

  input:checked ~ .dot {
    transform: scale(1);
  }

  input:checked ~ .state {
    background-color: var(--color-primary);
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
   * the `:checked` rules above, so source order is the only thing deciding
   * what colour a disabled-and-chosen option ends up: move this block upwards
   * and a disabled selected radio stays in full primary blue, telling an
   * operator it can be changed when it cannot.
   *
   * MD3's disabled state is the content colour at 38%, ring and dot alike.
   */
  input:disabled ~ .ring {
    border-color: var(--color-on-surface);
    opacity: 0.38;
  }

  input:disabled ~ .dot {
    background-color: var(--color-on-surface);
    opacity: 0.38;
  }
</style>
