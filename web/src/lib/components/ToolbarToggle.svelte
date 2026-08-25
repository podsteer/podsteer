<!--
  One icon control in a PaneToolbar, either on or off.

  A toggle rather than a button, and it says so: `aria-pressed` is what tells
  a screen reader that this is a state and which state it is in, and the title
  names the state it is in rather than the one it would move to. Both matter
  more than usual here, because the difference between wrapped and unwrapped
  text is invisible until a line happens to be long enough to show it.

  Pressed is drawn as a tinted container rather than a filled block of primary.
  These sit directly above the text they govern, and a saturated button that
  close to a wall of monospace pulls the eye away from the thing being read —
  the toolbar should be findable, not loud.
-->
<script lang="ts">
  import type { Component } from 'svelte'

  interface Props {
    /** The control's icon. */
    icon: Component
    /** Names the control. Used as the accessible name. */
    label: string
    /** Whether the control is currently on. */
    pressed: boolean
    /**
     * Describes the CURRENT state, not the action.
     *
     * "Wrapping lines" tells somebody what they are looking at; "Wrap lines"
     * beside an already-wrapped pane reads as an offer to do what has been
     * done.
     */
    title?: string
    onclick: () => void
    disabled?: boolean
  }

  let { icon: Icon, label, pressed, title, onclick, disabled = false }: Props = $props()
</script>

<button
  type="button"
  {onclick}
  {disabled}
  aria-pressed={pressed}
  aria-label={label}
  title={title ?? label}
  class="state-layer grid size-7 shrink-0 place-items-center rounded-sm
         transition-colors duration-100
         {pressed
    ? 'bg-primary/14 text-primary'
    : 'text-on-surface-variant hover:bg-surface-container hover:text-on-surface'}
         disabled:pointer-events-none disabled:opacity-30"
>
  <Icon class="size-4" strokeWidth={1.8} />
</button>
