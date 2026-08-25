<!--
  A manifest with its toolbar: the whole YAML surface, in one piece.

  Both places that show YAML render this rather than assembling a toolbar and
  an editor themselves. The search box has to reach into the editor to move
  the selection, so something has to own both — and once one component owns
  them, the drawer's tab and the edit dialog cannot drift apart in what they
  offer or where it sits.

  Reading left to right: what you are looking for, then how it is displayed,
  then what you can do to it. Search takes the free space because it is the
  only control that is typed into rather than clicked, and the two icon groups
  are separated by a rule so that a display preference is never mistaken for
  an action against the cluster.
-->
<script lang="ts">
  import type { Snippet } from 'svelte'
  import { Search, X } from '@lucide/svelte'
  import PaneToolbar from './PaneToolbar.svelte'
  import WrapLinesToggle from './WrapLinesToggle.svelte'
  import ManagedFieldsToggle from './ManagedFieldsToggle.svelte'
  import YamlEditor, { type EditorApi } from './YamlEditor.svelte'
  import { findMatches } from '$lib/textSearch'

  interface Props {
    /** The manifest to show. */
    content: string
    readonly?: boolean
    onchange?: (value: string) => void
    /** Whether to offer the managed-fields control. */
    managedFields?: boolean
    /** Disables that control, for when the view cannot safely be re-seeded. */
    managedFieldsDisabled?: boolean
    managedFieldsDisabledReason?: string
    /** The actions for this pane — edit, copy — at the trailing edge. */
    actions?: Snippet
  }

  let {
    content,
    readonly = false,
    onchange,
    managedFields = true,
    managedFieldsDisabled = false,
    managedFieldsDisabledReason,
    actions,
  }: Props = $props()

  let query = $state('')
  let api = $state<EditorApi | null>(null)
  let input = $state<HTMLInputElement | null>(null)

  /**
   * Counted from the text directly, not asked of the editor.
   *
   * Going through CodeMirror would mean reading a state field the query has
   * not necessarily reached yet, so the number would trail the typing by a
   * keystroke. Derived from `content` as well as `query` so it also follows
   * an edit, which matters in the dialog where the document changes under
   * the search.
   */
  const matches = $derived(findMatches(content, query).length)

  function onKeydown(event: KeyboardEvent): void {
    if (event.key === 'Enter') {
      event.preventDefault()
      // Shift steps backwards, the convention every find box shares.
      if (event.shiftKey) api?.findPrevious()
      else api?.findNext()
    } else if (event.key === 'Escape' && query) {
      // Clears the search rather than closing the drawer behind it: while
      // there is something in the box, Escape belongs to the box.
      event.stopPropagation()
      query = ''
    }
  }
</script>

<div class="flex h-full flex-col">
  <PaneToolbar>
    {#snippet children()}
      <!-- The one control that is typed into, so it takes the free space. -->
      <div class="relative flex min-w-0 flex-1 items-center">
        <Search
          class="pointer-events-none absolute left-2 size-3.5 text-on-surface-variant/50"
          strokeWidth={1.8}
        />
        <input
          bind:this={input}
          bind:value={query}
          onkeydown={onKeydown}
          type="text"
          placeholder="Search"
          aria-label="Search the manifest"
          class="field h-7 w-full min-w-0 py-0 pr-16 pl-7 text-body-small"
        />
        {#if query}
          <!-- The count sits inside the field, where it answers the question
               the typing just asked without moving anything. -->
          <span
            class="pointer-events-none absolute right-7 text-body-small tabular-nums
                   {matches === 0 ? 'text-on-surface-variant/50' : 'text-on-surface-variant'}"
          >
            {matches}
          </span>
          <button
            type="button"
            onclick={() => {
              query = ''
              input?.focus()
            }}
            aria-label="Clear search"
            title="Clear search"
            class="absolute right-1.5 grid size-5 place-items-center rounded-full
                   text-on-surface-variant/60 transition-colors hover:text-on-surface"
          >
            <X class="size-3.5" strokeWidth={2} />
          </button>
        {/if}
      </div>
    {/snippet}

    {#snippet trailing()}
      <WrapLinesToggle />
      {#if managedFields}
        <ManagedFieldsToggle
          disabled={managedFieldsDisabled}
          disabledReason={managedFieldsDisabledReason}
        />
      {/if}

      {#if actions}
        <!-- A rule between how it is shown and what is done to it. The
             controls to its left change this view; the ones to its right
             change the cluster. -->
        <div class="mx-1 h-5 w-px bg-outline-variant/40"></div>
        {@render actions()}
      {/if}
    {/snippet}
  </PaneToolbar>

  <div class="min-h-0 flex-1">
    <YamlEditor {content} {readonly} {onchange} {query} onready={(a) => (api = a)} />
  </div>
</div>
