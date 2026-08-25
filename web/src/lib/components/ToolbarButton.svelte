<!--
  One icon action in a PaneToolbar.

  The sibling of ToolbarToggle, for the things that DO something rather than
  the things that are on or off: edit, copy. Same size and spacing, no
  `aria-pressed`, because a button that reported a state it does not have
  would tell a screen reader the opposite of the truth.

  Disabled rather than hidden when an action cannot apply — the row keeps its
  shape, and the title says why.
-->
<script lang="ts">
  import type { Component } from 'svelte'

  interface Props {
    /** The action's icon. */
    icon: Component
    /** Names the action. Used as the accessible name. */
    label: string
    /** Explains the action, or why it is unavailable. */
    title?: string
    onclick: () => void
    disabled?: boolean
    /**
     * Draws the action as having just succeeded.
     *
     * For copy, which has no visible result of its own: without a moment of
     * acknowledgement there is no way to tell a click that worked from one
     * that missed.
     */
    active?: boolean
  }

  let { icon: Icon, label, title, onclick, disabled = false, active = false }: Props = $props()
</script>

<button
  type="button"
  {onclick}
  {disabled}
  aria-label={label}
  title={title ?? label}
  class="state-layer grid size-7 shrink-0 place-items-center rounded-sm
         transition-colors duration-100
         {active
    ? 'text-gauge-normal'
    : 'text-on-surface-variant hover:bg-surface-container hover:text-on-surface'}
         disabled:pointer-events-none disabled:opacity-30"
>
  <Icon class="size-4" strokeWidth={1.8} />
</button>
