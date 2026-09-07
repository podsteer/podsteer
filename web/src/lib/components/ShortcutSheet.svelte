<!--
  Every keyboard shortcut the application has, grouped by where it applies.

  Reads SHORTCUTS directly (see $lib/shortcuts) rather than keeping its own
  copy — this list and the handlers that act on ⌘B, ⌘R and the rest are two
  views of the same table, so they cannot silently disagree about what a key
  does or how it is spelled on this platform.
-->
<script lang="ts">
  import { escapeLayer, type EscapeClaim } from '$lib/escape'
  import { modal } from '$lib/modal'
  import { SHORTCUTS, type ShortcutScope } from '$lib/shortcuts'
  import { X } from '@lucide/svelte'

  interface Props {
    open: boolean
    onclose: () => void
  }

  let { open, onclose }: Props = $props()

  /**
   * What each scope means, in the operator's terms — matching the voice
   * SettingsDialog's own SCOPE_META uses for the threshold surfaces.
   */
  const GROUPS: { scope: ShortcutScope; label: string; detail: string }[] = [
    {
      scope: 'global',
      label: 'Anywhere',
      detail: 'Works from any tab, including the cluster picker.',
    },
    {
      scope: 'cluster',
      label: 'In a cluster',
      detail: 'Acts on whichever cluster tab is currently in front.',
    },
  ]

  function onKeydown(event: KeyboardEvent): void {
    if (event.key !== 'Escape' || !open) return
    if (!escape?.owns()) return
    onclose()
  }

  /** Escape belongs to the innermost open layer. See $lib/escape. */
  let escape = $state<EscapeClaim | null>(null)
  $effect(() => {
    if (!open) return
    const held = escapeLayer()
    escape = held
    return () => {
      held.release()
      escape = null
    }
  })
</script>

<svelte:window onkeydown={onKeydown} />

{#if open}
  <button
    type="button"
    aria-label="Close keyboard shortcuts"
    tabindex="-1"
    class="fixed inset-0 z-40 cursor-default bg-scrim/40"
    onclick={onclose}
  ></button>

  <!-- Centred by a flex wrapper, and it has to be, because this sheet is as
       tall as its content.

       SettingsDialog centres with `inset-0 m-auto` and the note there is right
       about why — a transform makes the element a containing block for every
       `position: fixed` descendant, which put a dropdown inside it in the
       corner of the dialog instead of the window. What that note does not say
       is that it works there because that dialog PINS a height (`h-[36rem]`).
       This one used the same classes with `h-fit`, and a content height does
       not resolve against a box told to be both top:0 and bottom:0: the sheet
       opened as a stub showing its header and the first group heading, with
       the shortcut list scrolled away inside it.

       A wrapper that centres by flex fixes it without giving the transform
       back: the sheet is an ordinary flex item, so it takes its content's
       height, `max-h-[85vh]` bounds it, and the list scrolls only when there
       is genuinely more than fits. The wrapper sets no transform, so fixed
       descendants still resolve against the window. It passes pointer events
       through so the backdrop behind it still closes on a click. -->
  <div class="pointer-events-none fixed inset-0 z-50 flex items-center justify-center p-4">
  <div
    class="pointer-events-auto flex max-h-full w-[34rem] max-w-[92vw]
           flex-col overflow-hidden rounded-sm border border-outline-variant
           bg-surface-container-high shadow-level-3"
    role="dialog"
    aria-modal="true"
    use:modal
    aria-label="Keyboard shortcuts"
  >
    <header class="flex shrink-0 items-center justify-between px-5 pt-4 pb-2">
      <h2 class="text-title-medium text-on-surface">Keyboard shortcuts</h2>
      <button
        type="button"
        onclick={onclose}
        aria-label="Close"
        class="state-layer grid size-8 shrink-0 place-items-center rounded-full
               text-on-surface-variant transition-colors duration-100
               hover:bg-surface-container hover:text-on-surface"
      >
        <X class="size-4" strokeWidth={2} />
      </button>
    </header>

    <div class="min-h-0 flex-1 overflow-y-auto px-5 pb-5">
      {#each GROUPS as group (group.scope)}
        {@const entries = SHORTCUTS.filter((entry) => entry.scope === group.scope)}
        {#if entries.length > 0}
          <section class="mt-3 first:mt-0">
            <h3 class="text-title-small text-on-surface">{group.label}</h3>
            <p class="mt-0.5 text-body-medium text-on-surface-variant">{group.detail}</p>

            <!-- Two columns: the keys, then what they do. Keys are a fixed
                 column rather than inline text so every description in the
                 group starts at the same place, which is what makes a list
                 like this scannable at a glance. -->
            <ul class="mt-3 grid grid-cols-[auto_1fr] items-baseline gap-x-4 gap-y-2">
              {#each entries as entry (entry.id)}
                <li class="contents">
                  <kbd
                    class="justify-self-start rounded-xs border border-outline-variant
                           bg-surface-container px-1.5 py-0.5 font-mono text-label-medium
                           whitespace-nowrap text-on-surface"
                  >
                    {entry.keys}
                  </kbd>
                  <span class="text-body-medium text-on-surface-variant">{entry.description}</span>
                </li>
              {/each}
            </ul>
          </section>
        {/if}
      {/each}
    </div>
  </div>
  </div>
{/if}
