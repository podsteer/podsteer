<!--
  The help drawer, on the right, holding whatever topic was last asked for.

  MOUNTED ONCE, AT THE TOP OF THE APPLICATION, and driven by $stores/help — so
  a dialog's (?) is a one-line call rather than a panel of its own. Twenty
  dialogs each with their own drawer would be twenty answers to where it sits,
  how it closes and what Escape means.

  IT IS A DIALOG, AND KEEPS A DIALOG'S OBLIGATIONS. It opens over another
  dialog, which is the whole point — the operator is mid-decision and wants to
  know what the button does — so focus moves into it, Tab stays in it, Escape
  closes the innermost layer (this one) and leaves the dialog underneath open,
  and focus returns to the (?) that opened it. `use:modal` does all four; the
  escape claim is what stops one keystroke closing both.

  ON THE RIGHT, AND NOT CENTRED. It sits beside the dialog rather than on top
  of it, so what somebody is reading about stays visible while they read.

  ITS TYPE IS THE OVERVIEW'S TYPE. A title at `title-large`, headings at
  `title-medium` and prose at `body-medium` — the same three sizes the
  overview's cards use for the same three jobs. This panel opened at
  `body-small` throughout, which is the size for a caption under a figure, not
  for the paragraphs somebody came here to read.
-->
<script lang="ts">
  import { escapeLayer, type EscapeClaim } from '$lib/escape'
  import { modal } from '$lib/modal'
  import { help } from '$stores/help.svelte'
  import { helpTopic } from '$lib/help'
  import { CircleHelp, X } from '@lucide/svelte'

  const topic = $derived(helpTopic(help.topic))

  function onKeydown(event: KeyboardEvent): void {
    if (event.key !== 'Escape') return
    if (!escape?.owns()) return
    help.close()
  }

  let escape = $state<EscapeClaim | null>(null)
  $effect(() => {
    if (topic === null) return
    const held = escapeLayer()
    escape = held
    return () => {
      held.release()
      escape = null
    }
  })
</script>

<svelte:window onkeydown={onKeydown} />

{#if topic}
  <!-- A scrim thin enough to read the dialog through. It is here to be
       clicked, not to dim: the thing being explained has to stay legible. -->
  <button
    type="button"
    aria-label="Close help"
    tabindex="-1"
    class="fixed inset-0 z-[90] cursor-default bg-scrim/20"
    onclick={() => help.close()}
  ></button>

  <div
    class="fixed top-0 right-0 bottom-0 z-[95] flex w-[28rem] max-w-[92vw] flex-col
           border-l border-outline-variant bg-surface-container-high shadow-level-3"
    role="dialog"
    aria-modal="true"
    use:modal
    aria-label="Help: {topic.title}"
  >
    <header
      class="flex shrink-0 items-start justify-between gap-3 border-b border-outline-variant/60
             px-5 pt-4 pb-3"
    >
      <div class="min-w-0">
        <h2 class="flex items-center gap-2 text-title-large text-on-surface">
          <CircleHelp
            class="size-5 shrink-0 text-on-surface-variant"
            strokeWidth={2}
            aria-hidden="true"
          />
          {topic.title}
        </h2>
        <p class="mt-1.5 text-body-medium leading-relaxed text-on-surface-variant">{topic.lede}</p>
      </div>
      <button
        type="button"
        onclick={() => help.close()}
        aria-label="Close help"
        class="state-layer grid size-8 shrink-0 place-items-center rounded-full
               text-on-surface-variant transition-colors duration-100
               hover:bg-surface-container hover:text-on-surface"
      >
        <X class="size-4" strokeWidth={2} />
      </button>
    </header>

    <div class="min-h-0 flex-1 overflow-y-auto px-5 py-4">
      {#each topic.sections as section (section.heading)}
        <section class="mt-6 first:mt-0">
          <h3 class="text-title-medium font-semibold text-on-surface">{section.heading}</h3>
          {#each section.body as paragraph, index (index)}
            <p class="mt-2 text-body-medium leading-relaxed text-on-surface-variant">
              {paragraph}
            </p>
          {/each}
        </section>
      {/each}
    </div>
  </div>
{/if}
