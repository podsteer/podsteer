<!--
  The one dialog header: a title, and the two controls every dialog should
  have had.

  (?) THEN (X), IN THAT ORDER, because that is the order they are wanted in:
  the question comes before the exit. A dialog with no topic shows only the
  close control, so the pair is never a question mark that answers nothing.

  WHY A CLOSE CONTROL AT ALL, when Escape and a click behind the dialog
  already close these. Both are invisible. Somebody who does not know a dialog
  can be dismissed by clicking behind it looks for the control that dismisses
  it, and finding nothing but Cancel beside a destructive verb is a worse
  place to be than it sounds: Cancel and Close are the same act here, but only
  one of them reads as leaving rather than as deciding.
-->
<script lang="ts">
  import type { Component } from 'svelte'
  import type { HelpTopicId } from '$lib/help'
  import HelpButton from './HelpButton.svelte'
  import { X } from '@lucide/svelte'

  interface Props {
    /** The dialog's title, naming the object where there is one. */
    title: string
    /** The icon beside it, for the dialogs that carry one. */
    icon?: Component | null
    /** Classes for that icon — dialogs colour theirs by severity. */
    iconClass?: string
    /** Which topic (?) opens; omit for a dialog with nothing to explain. */
    help?: HelpTopicId | null
    onclose: () => void
  }

  let {
    title,
    icon: Icon = null,
    iconClass = 'text-on-surface-variant',
    help: topic = null,
    onclose,
  }: Props = $props()
</script>

<header class="flex items-start justify-between gap-3">
  <h2 class="flex min-w-0 items-center gap-2 text-headline-small text-on-surface">
    {#if Icon}
      <Icon class="size-5 shrink-0 {iconClass}" strokeWidth={2} aria-hidden="true" />
    {/if}
    <!-- Selectable, because the title is routinely the only place an object's
         full name is written, and a name somebody cannot copy is a name they
         have to retype into a terminal. -->
    <span class="min-w-0" data-selectable>{title}</span>
  </h2>

  <div class="-mt-1 -mr-1 flex shrink-0 items-center gap-0.5">
    {#if topic}
      <HelpButton {topic} about={title} />
    {/if}
    <button
      type="button"
      onclick={onclose}
      aria-label="Close"
      class="state-layer grid size-8 place-items-center rounded-full text-on-surface-variant
             transition-colors duration-100 hover:bg-surface-container hover:text-on-surface"
    >
      <X class="size-4" strokeWidth={2} />
    </button>
  </div>
</header>
