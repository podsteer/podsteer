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

<span class="relative inline-flex items-center">
  <button
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

  {#if open}
    <!-- Above the icon and growing rightwards from it.
         Centring reads better in the abstract and is wrong here: these icons
         follow a short label at the left of a card, so a centred panel hung
         half of itself over the card's edge and lost its first few words.
         Anchoring the left edge to the icon keeps the whole note inside. -->
    <span
      role="tooltip"
      class="pointer-events-none absolute bottom-full left-0 z-30 mb-1.5 w-64
             rounded-sm border border-outline-variant bg-surface-container-high px-3 py-2
             text-body-small leading-relaxed font-normal text-on-surface shadow-level-2"
    >
      {text}
    </span>
  {/if}
</span>
