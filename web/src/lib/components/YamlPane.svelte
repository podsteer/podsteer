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
  import PaneToolbar from './PaneToolbar.svelte'
  import ToolbarSearch from './ToolbarSearch.svelte'
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
    /** Hands the caller the editor's own controls — see YamlEditor's
        `EditorApi`. Only a fresh document being seeded needs this; the
        drawer's own tab has never used it. */
    onready?: (api: EditorApi) => void
  }

  let {
    content,
    readonly = false,
    onchange,
    managedFields = true,
    managedFieldsDisabled = false,
    managedFieldsDisabledReason,
    actions,
    onready,
  }: Props = $props()

  let query = $state('')
  let api = $state<EditorApi | null>(null)

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

</script>

<div class="flex h-full flex-col">
  <PaneToolbar>
    {#snippet children()}
      <ToolbarSearch
        value={query}
        label="Search the manifest"
        count={String(matches)}
        empty={matches === 0}
        onchange={(value) => (query = value)}
        onnext={() => api?.findNext()}
        onprevious={() => api?.findPrevious()}
      />
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
    <YamlEditor
      {content}
      {readonly}
      {onchange}
      {query}
      onready={(a) => {
        api = a
        onready?.(a)
      }}
    />
  </div>
</div>
