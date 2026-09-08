<!--
  The views somebody named and kept, and the field for keeping another.

  IN THE TOOLBAR, BESIDE THE THINGS IT CAPTURES. A view is the kind, the
  namespace, the search and the chips — everything else in this row — so the
  control that saves them belongs in the same row rather than in Settings,
  where it would be a page away from the state it is about.

  ONE MENU RATHER THAN A SAVE BUTTON AND A LIST. The two halves are the same
  act seen from either end: what is on screen going in, and what was saved
  coming back out. Split across two controls, the second would be a list with
  no obvious way to add to it.

  A saved view is NOT keyed by cluster, so this menu is the same on every tab.
  See $lib/savedViews for that decision, and for what a view deliberately does
  not carry — the sort, the page and the columns each have a memory of their
  own, and a view that also set them would quietly change every other visit.
-->
<script lang="ts">
  import { escapeLayer, type EscapeClaim } from '$lib/escape'
  import { preferences } from '$stores/preferences.svelte'
  import { describeView, viewMatches, MAX_SAVED_VIEWS, type SavedView, type ViewState } from '$lib/savedViews'
  import HelpButton from './HelpButton.svelte'
  import { Bookmark, BookmarkCheck, Check, Plus, X } from '@lucide/svelte'

  interface Props {
    /** What is on screen right now, for saving and for the tick. */
    current: ViewState
    /** The current kind's title, for the line under each name. */
    kindTitle: (kindId: string) => string
    /** The all-namespaces sentinel, so the summary can name it in words. */
    allNamespaces: string
    /** Opens a view. */
    onapply: (view: SavedView) => void
  }

  let { current, kindTitle, allNamespaces, onapply }: Props = $props()

  let open = $state(false)
  let name = $state('')
  let field = $state<HTMLInputElement | null>(null)

  const views = $derived(preferences.savedViews)
  const applied = $derived(views.find((view) => viewMatches(view, current)) ?? null)
  const full = $derived(views.length >= MAX_SAVED_VIEWS)

  /**
   * Whether saving under this name would replace a view rather than add one.
   *
   * Said on the button before it is pressed, because overwriting is the
   * right behaviour and a silent one would be a surprise: typing a name that
   * already exists is somebody correcting that view, not asking for a second
   * one with the same name.
   */
  const replacing = $derived(
    name.trim() !== '' &&
      views.some((view) => view.name.toLowerCase() === name.trim().toLowerCase()),
  )
  const canSave = $derived(name.trim() !== '' && (replacing || !full))

  function toggle(): void {
    open = !open
    if (open) {
      // Pre-filled with the applied view's name, so pressing Save again after
      // adjusting a filter updates the view somebody is plainly working on
      // rather than making a near-duplicate they then have to tidy up.
      name = applied?.name ?? ''
      queueMicrotask(() => field?.focus())
    }
  }

  function save(): void {
    if (!canSave) return
    if (preferences.saveView(name, current)) name = ''
  }

  function apply(view: SavedView): void {
    onapply(view)
    open = false
  }

  function onFieldKeydown(event: KeyboardEvent): void {
    if (event.key !== 'Enter') return
    event.preventDefault()
    save()
  }

  function onWindowPointerDown(event: PointerEvent): void {
    if (!open) return
    const target = event.target as HTMLElement | null
    if (!target?.closest('[data-saved-views]')) open = false
  }

  function onKeydown(event: KeyboardEvent): void {
    if (event.key !== 'Escape') return
    // One Escape, one layer. See $lib/escape.
    if (!escape?.owns()) return
    open = false
  }

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

<svelte:window onpointerdown={onWindowPointerDown} onkeydown={onKeydown} />

<div class="relative" data-saved-views>
  <!-- The icon says whether what is on screen is one of the saved ones, which
       is the question somebody glancing at it has. -->
  <button
    type="button"
    onclick={toggle}
    aria-expanded={open}
    aria-label="Saved views"
    title={applied ? `Saved view: ${applied.name}` : 'Saved views'}
    class="state-layer grid size-8 shrink-0 place-items-center rounded-full
           transition-colors duration-100 hover:bg-surface-container hover:text-on-surface
           {applied ? 'text-gauge-normal' : 'text-on-surface-variant'}
           {open ? 'bg-surface-container' : ''}"
  >
    {#if applied}
      <BookmarkCheck class="size-4" strokeWidth={1.8} />
    {:else}
      <Bookmark class="size-4" strokeWidth={1.8} />
    {/if}
  </button>

  {#if open}
    <div
      class="absolute top-full right-0 z-30 mt-1 w-80 rounded-sm border border-outline-variant/60
             bg-surface-container-high py-1.5 shadow-level-3"
      role="group"
      aria-label="Saved views"
    >
      <div class="flex items-center justify-between gap-2 px-3 pt-1 pb-2">
        <span class="text-body-medium text-on-surface">Saved views</span>
        <HelpButton topic="saved-views" about="saved views" />
      </div>

      <!-- Saving, at the top, because the menu is opened to save far more
           often than to prune. -->
      <div class="flex items-center gap-1.5 px-3 pb-2">
        <input
          bind:this={field}
          bind:value={name}
          onkeydown={onFieldKeydown}
          type="text"
          placeholder="Name this view"
          aria-label="Name this view"
          class="h-8 min-w-0 flex-1 rounded-sm border border-outline-variant bg-transparent px-2
                 text-body-medium text-on-surface placeholder:text-on-surface-variant/50
                 focus:border-primary focus:outline-none"
        />
        <button
          type="button"
          onclick={save}
          disabled={!canSave}
          class="state-layer inline-flex h-8 shrink-0 items-center gap-1.5 rounded-sm border
                 border-outline-variant px-2 text-label-large text-on-surface-variant
                 transition-colors duration-100 hover:bg-surface-container hover:text-on-surface
                 disabled:opacity-50"
        >
          <Plus class="size-3.5" strokeWidth={1.8} />
          {replacing ? 'Replace' : 'Save'}
        </button>
      </div>

      {#if full && !replacing}
        <p class="px-3 pb-2 text-body-small text-on-surface-variant/60">
          {MAX_SAVED_VIEWS} views is the limit. Delete one to save another, or save over a name you already have.
        </p>
      {/if}

      {#if views.length > 0}
        <ul class="max-h-80 overflow-y-auto border-t border-outline-variant/40 pt-1">
          {#each views as view (view.id)}
            <li class="flex items-center gap-1 px-1">
              <button
                type="button"
                onclick={() => apply(view)}
                class="state-layer flex min-w-0 flex-1 items-center gap-2 rounded-sm px-2 py-1.5
                       text-left transition-colors duration-100 hover:bg-surface-container"
              >
                <span class="grid size-4 shrink-0 place-items-center">
                  {#if applied?.id === view.id}
                    <Check class="size-3.5 text-gauge-normal" strokeWidth={2} />
                  {/if}
                </span>
                <span class="min-w-0">
                  <span class="block truncate text-body-medium text-on-surface">{view.name}</span>
                  <!-- What it selects, so a name nobody remembers writing is
                       still readable without applying it. -->
                  <span class="block truncate text-body-small text-on-surface-variant/70">
                    {describeView(view, kindTitle(view.kindId), allNamespaces)}
                  </span>
                </span>
              </button>

              <button
                type="button"
                onclick={() => preferences.deleteView(view.id)}
                aria-label="Delete {view.name}"
                title="Delete this view"
                class="state-layer grid size-7 shrink-0 place-items-center rounded-sm
                       text-on-surface-variant transition-colors duration-100
                       hover:bg-surface-container hover:text-on-surface"
              >
                <X class="size-3.5" strokeWidth={1.8} />
              </button>
            </li>
          {/each}
        </ul>
      {:else}
        <p class="border-t border-outline-variant/40 px-3 pt-2 text-body-small text-on-surface-variant/60">
          Nothing saved yet. A view keeps the kind, the namespace, the search and the status
          chips — not the sort or the columns, which are remembered on their own.
        </p>
      {/if}
    </div>
  {/if}
</div>
