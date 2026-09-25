<!--
  Rebinding the keyboard shortcuts.

  ONE ROW PER SHORTCUT, and pressing Change puts that row into CAPTURE: the
  next keystroke becomes the binding. A field somebody types "Ctrl+Shift+P"
  into as text would be a field that accepts "Ctlr+P", and a dropdown of every
  key on a keyboard is not a control anybody wants to use — the keyboard is
  already the right input device for this.

  WHAT CAPTURE REFUSES, and why each one:

    - A bare letter. It would fire while somebody typed into the search box,
      and no handler can tell those apart without knowing what has focus.
      A function key needs no modifier: nobody types F5 into a field.
    - Escape, Tab, Enter, the arrows. Escape unwinds every dialog and drawer
      in the application through one layered handler; the others move through
      a list. Neither is this table's to take.
    - A combination another shortcut already has. Refused by naming the other
      one, because two handlers on one key is not a preference — whichever
      fired first would look like the other being broken.

  Two shortcuts are not offered at all, and say so rather than being left out:
  switching to the Nth tab is nine keys behind one id, and the sheet's own
  ⌘/ has a bare "?" alternative that depends on what has focus. A rebinding
  field can express neither.
-->
<script lang="ts">
  import { preferences } from '$stores/preferences.svelte'
  import { shortcuts } from '$stores/shortcuts.svelte'
  import { bindableEvent, bindingFromEvent, formatBinding } from '$lib/shortcutBinding'
  import type { ShortcutScope } from '$lib/shortcuts'
  import Button from './Button.svelte'
  import HelpButton from './HelpButton.svelte'
  import { RotateCcw, TriangleAlert } from '@lucide/svelte'

  /** The row currently listening for a keystroke, if any. */
  let capturing = $state<string | null>(null)
  /** Why the last keystroke was refused, and for which row. */
  let refusal = $state<{ id: string; reason: string } | null>(null)

  const GROUPS: { scope: ShortcutScope; label: string }[] = [
    { scope: 'global', label: 'Anywhere' },
    { scope: 'cluster', label: 'In a cluster tab' },
  ]

  const changed = $derived(Object.keys(preferences.shortcutBindings).length)

  function startCapture(id: string): void {
    capturing = id
    refusal = null
  }

  /**
   * The keystroke that lands while a row is capturing.
   *
   * Swallowed whatever it is — including a combination this refuses — so the
   * shortcut being rebound cannot fire while it is being rebound, which is
   * how pressing ⌘R over the Refresh row would otherwise reload the view
   * instead of binding to it.
   */
  function onCapture(event: KeyboardEvent): void {
    if (!capturing) return

    // A modifier on its own is somebody mid-combination, not a refusal.
    if (['Control', 'Meta', 'Shift', 'Alt'].includes(event.key)) return

    event.preventDefault()
    event.stopPropagation()

    const id = capturing

    if (event.key === 'Escape') {
      // The one key that cancels rather than binds — which is also why it can
      // never BE a binding.
      capturing = null
      return
    }

    if (!bindableEvent(event)) {
      refusal = {
        id,
        reason: 'That needs a modifier, or it would fire while you were typing.',
      }
      return
    }

    const binding = bindingFromEvent(event)
    const clash = shortcuts.conflict(id, binding)
    if (clash) {
      refusal = { id, reason: `${formatBinding(binding)} is already ${clash.description.toLowerCase()}.` }
      return
    }

    preferences.setShortcutBinding(id, binding)
    capturing = null
    refusal = null
  }
</script>

<svelte:window onkeydown={onCapture} />

<section>
  <div class="flex items-start justify-between gap-3">
    <h3 class="text-title-medium text-on-surface">Keyboard</h3>
    <HelpButton topic="keyboard" about="keyboard shortcuts" />
  </div>
  <p class="mt-0.5 text-body-medium text-on-surface-variant">
    Press Change and then the keys you want. Escape cancels.
  </p>

  {#each GROUPS as group (group.scope)}
    {@const entries = shortcuts.all.filter((entry) => entry.scope === group.scope)}
    <h4 class="mt-5 text-title-small text-on-surface">{group.label}</h4>

    <ul class="mt-2 flex flex-col">
      {#each entries as entry (entry.id)}
        {@const custom = preferences.shortcutBindings[entry.id] !== undefined}
        <li
          class="flex items-center gap-3 border-b border-outline-variant/30 py-2 last:border-b-0"
        >
          <span class="min-w-0 flex-1 text-body-medium text-on-surface">
            {entry.description}
            {#if !entry.rebindable}
              <span class="text-body-small text-on-surface-variant/60">
                · more than one combination, so it is fixed
              </span>
            {/if}
          </span>

          <kbd
            class="shrink-0 rounded-xs border px-1.5 py-0.5 font-mono text-label-medium
                   whitespace-nowrap
                   {capturing === entry.id
              ? 'border-primary text-primary'
              : 'border-outline-variant bg-surface-container text-on-surface'}"
          >
            {capturing === entry.id ? 'Press keys…' : entry.keys}
          </kbd>

          {#if entry.rebindable}
            <Button variant="text" onclick={() => startCapture(entry.id)}>
              {capturing === entry.id ? 'Listening' : 'Change'}
            </Button>
            <!-- Reset is per row and appears only where there is something to
                 undo: a button that resets a shortcut nobody changed is a
                 button that does nothing, on every row. -->
            <button
              type="button"
              onclick={() => preferences.clearShortcutBinding(entry.id)}
              disabled={!custom}
              aria-label="Reset {entry.description}"
              title={custom ? 'Back to the default' : 'This is the default'}
              class="state-layer grid size-7 shrink-0 place-items-center rounded-full
                     text-on-surface-variant transition-colors duration-100
                     hover:bg-surface-container hover:text-on-surface
                     disabled:pointer-events-none disabled:opacity-25"
            >
              <RotateCcw class="size-3.5" strokeWidth={1.8} />
            </button>
          {/if}
        </li>

        {#if refusal?.id === entry.id}
          <li class="flex items-start gap-1.5 pb-2 text-body-small text-error" role="alert">
            <TriangleAlert class="mt-0.5 size-3.5 shrink-0" strokeWidth={2} />
            {refusal.reason}
          </li>
        {/if}
      {/each}
    </ul>
  {/each}

  <div class="mt-5 flex items-center gap-3">
    <Button variant="outlined" disabled={changed === 0} onclick={preferences.resetShortcutBindings}>
      Reset all
    </Button>
    <span class="text-body-medium text-on-surface-variant">
      {changed === 0
        ? 'Every shortcut is at its default.'
        : `${changed} shortcut${changed === 1 ? '' : 's'} changed.`}
    </span>
  </div>
</section>
