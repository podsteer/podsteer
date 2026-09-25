<!--
  The per-table column chooser.

  Lives in the toolbar, after the pager. It was in the header's trailing cell,
  which was reachable only until somebody scrolled a wide table sideways — and
  a wide table is exactly when an operator goes looking for it. In the toolbar
  it sits still, in the row where every other per-view control already is.

  Pinned columns are listed but not toggleable — hiding the name column would
  leave rows nothing can identify them by.

  It is also where an operator's OWN columns are made: one label or annotation
  key, shown verbatim, per kind — see $lib/customColumns. The keys offered are
  the ones the rows on screen actually carry, read when the menu opens, so a
  column is picked from what this cluster uses rather than typed from memory;
  free text is there for the key that is not on this page of rows. Adding an
  annotation column re-reads the list, because only the keys somebody asked
  for ever travel — see the client's listNamespaceSummaries note.
-->
<script lang="ts">
  import { escapeLayer, type EscapeClaim } from '$lib/escape'
  import { preferences } from '$stores/preferences.svelte'
  import type { Column } from './DataTable.svelte'
  import {
    LAST_APPLIED_ANNOTATION,
    customColumnId,
    isCustomColumnId,
    isValidKey,
    needsWholeObject,
    type ColumnSource,
    type CustomColumnSpec,
    type MetadataKeys,
  } from '$lib/customColumns'
  import { controlKindOf, isControlColumn, type EdgeColumn } from '$lib/fixedColumns'
  import { Columns3, RotateCcw, Pin, Plus, X, ChevronUp, ChevronDown } from '@lucide/svelte'
  import Checkbox from './Checkbox.svelte'
  import HelpButton from './HelpButton.svelte'

  interface Props {
    kindId: string
    columns: Column[]
    /**
     * The label and annotation keys the rows on screen carry, read when the
     * menu opens rather than on every refresh. Optional: without it the
     * picker offers free text alone.
     */
    keys?: () => MetadataKeys
    /**
     * Called after a custom column is added or removed, so whoever owns the
     * list can re-read it when the projection changed.
     */
    onchange?: (spec: CustomColumnSpec, change: 'added' | 'removed') => void
  }

  let { kindId, columns, keys, onchange }: Props = $props()

  let open = $state(false)

  /** The columns the view declared — everything that is not the operator's. */
  const builtIn = $derived(columns.filter((column) => !isCustomColumnId(column.id)))

  /**
   * The operator's own, in their order. From preferences rather than from
   * `columns`, so the list is right the instant one is added — the view's
   * merged column list catches up a tick later, through the same store.
   */
  const custom = $derived(preferences.customColumnsFor(kindId))

  /**
   * The control columns this table actually has, in row order.
   *
   * Read off `columns` rather than listed here, so a view with no tick boxes
   * — the event and namespace lists — is not offered a switch for a column it
   * does not draw. The Fixed choice itself is application-wide (see
   * preferences.fixedEdges): turning it off here turns it off on every list,
   * which is the point. A reading habit is not a fact about pods.
   */
  const edges = $derived(
    columns.flatMap((column) => {
      const kind = controlKindOf(column)
      return kind === null ? [] : [{ kind, label: column.label }]
    }),
  )

  function toggleFixed(edge: EdgeColumn): void {
    preferences.toggleEdgeFixed(edge)
  }

  function isHidden(column: Column): boolean {
    if (column.pinned) return false
    const stored = preferences.columns[kindId]?.[column.id]?.hidden
    return stored === undefined ? Boolean(column.defaultHidden) : stored
  }

  function isCustomHidden(spec: CustomColumnSpec): boolean {
    return preferences.columns[kindId]?.[customColumnId(spec)]?.hidden === true
  }

  // --- Adding a column --------------------------------------------------------

  let source = $state<ColumnSource>('label')
  let key = $state('')
  /** What the rows carried when the menu was opened. */
  let onScreen = $state<MetadataKeys>({ labels: [], annotations: [] })

  // A JSONPath column has nothing to suggest: the paths are the object's
  // shape, which this side never sees. The field is free text and the
  // expression is judged where it is evaluated.
  const suggestions = $derived(
    source === 'label' ? onScreen.labels : source === 'annotation' ? onScreen.annotations : [],
  )
  const trimmed = $derived(key.trim())
  const alreadyAdded = $derived(custom.some((spec) => spec.source === source && spec.key === trimmed))
  const canAdd = $derived(trimmed !== '' && isValidKey(source, trimmed) && !alreadyAdded)

  /** Why the key cannot be added, when it cannot — shown under the field. */
  const problem = $derived.by(() => {
    if (trimmed === '') return ''
    if (alreadyAdded) return 'Already a column'
    if (source === 'annotation' && trimmed === LAST_APPLIED_ANNOTATION) {
      return 'The last-applied manifest is a whole document, not a value'
    }
    if (source === 'jsonpath' && !isValidKey(source, trimmed)) {
      return 'A path starts with a dot or a brace, as in .status.phase'
    }
    if (!isValidKey(source, trimmed)) return 'A key cannot contain spaces or commas'
    return ''
  })

  /**
   * Whether this kind's list already fetches whole objects — because it has
   * a JSONPath column, or because the one being typed would add the first.
   *
   * SAID BEFORE THE COLUMN IS ADDED, not after. It is the one custom column
   * that changes what a list costs, and an operator who is about to make
   * every refresh of a five-thousand-pod list fetch whole objects should
   * know that from the control rather than from a graph later.
   */
  const wholeObjects = $derived(needsWholeObject(custom))
  const wouldFetchWholeObjects = $derived(
    !wholeObjects && source === 'jsonpath' && trimmed !== '' && problem === '',
  )

  function toggle(): void {
    open = !open
    if (open) onScreen = keys?.() ?? { labels: [], annotations: [] }
  }

  function add(): void {
    if (!canAdd) return
    const spec: CustomColumnSpec = { source, key: trimmed }
    if (preferences.addCustomColumn(kindId, spec)) onchange?.(spec, 'added')
    key = ''
  }

  function remove(spec: CustomColumnSpec): void {
    preferences.removeCustomColumn(kindId, spec)
    onchange?.(spec, 'removed')
  }

  function onKeyFieldKeydown(event: KeyboardEvent): void {
    if (event.key !== 'Enter') return
    event.preventDefault()
    add()
  }

  function onWindowPointerDown(event: PointerEvent): void {
    if (!open) return
    const target = event.target as HTMLElement | null
    if (!target?.closest('[data-column-menu]')) open = false
  }

  function onKeydown(event: KeyboardEvent): void {
    if (event.key !== 'Escape') return
    // One Escape, one layer. See $lib/escape.
    if (!escape?.owns()) return
    open = false
  }

  /**
   * Escape belongs to the innermost open layer. See $lib/escape.
   */
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

<div class="relative" data-column-menu>
  <button
    type="button"
    onclick={toggle}
    aria-expanded={open}
    aria-label="Choose columns"
    title="Choose columns"
    class="state-layer grid size-8 shrink-0 place-items-center rounded-full text-on-surface-variant
           transition-colors duration-100 hover:bg-surface-container hover:text-on-surface
           {open ? 'bg-surface-container text-on-surface' : ''}"
  >
    <!-- Sized to the pager's buttons rather than the header cell's, since
         that is what it now sits beside. -->
    <Columns3 class="size-4" strokeWidth={1.8} />
  </button>

  {#if open}
    <!-- NOT role="menu". A menu's children must be menu items, and these are a
         <ul> of <label><input type=checkbox>. Assistive technology in menu
         mode exposes only menu items, so it found none at all and "Choose
         columns" opened an empty menu. A labelled group of checkboxes is what
         this actually is. -->
    <div
      class="absolute top-full right-0 z-30 mt-1 w-72 rounded-sm border border-outline-variant/60
             bg-surface-container-high py-1.5 shadow-level-3"
      role="group"
      aria-label="Columns"
    >
      <!-- The (?) beside the heading rather than a paragraph in the menu:
           what a JSONPath column costs is worth a page, and this control is
           already dense. -->
      <div class="flex items-center justify-between gap-2 px-3 pt-1 pb-1.5">
        <span class="text-body-medium text-on-surface">Columns</span>
        <HelpButton topic="custom-columns" about="custom columns" />
      </div>

      <p class="px-3 py-1 text-label-small font-semibold uppercase tracking-wider text-on-surface-variant/60">
        Columns
      </p>

      <ul class="max-h-80 overflow-auto py-0.5">
        <!-- A control column is not a column to choose: nothing to hide,
             nothing to name, nothing to sort by. Both of them appear under
             Fixed below instead, where the only choice they offer is. -->
        {#each builtIn.filter((column) => !isControlColumn(column)) as column (column.id)}
          <li>
            <Checkbox
              checked={!isHidden(column)}
              disabled={column.pinned}
              onchange={() => preferences.toggleColumn(kindId, column.id)}
              full
              dense
              class="rounded-sm px-3 py-1.5 text-body-small text-on-surface
                     transition-colors duration-75 hover:bg-surface-container-highest
                     {column.pinned ? 'opacity-50' : ''}"
            >
              <span class="flex items-center gap-2">
                <span class="min-w-0 flex-1 truncate">{column.label}</span>
                {#if column.pinned}
                  <Pin class="size-3 shrink-0 text-on-surface-variant/50" strokeWidth={2} />
                {/if}
              </span>
            </Checkbox>
          </li>
        {/each}
      </ul>

      {#if edges.length > 0}
        <!-- The two control columns, and the one choice they offer: whether
             each stays put while the table scrolls sideways. They are not in
             the list above because there is nothing there to decide — neither
             can be hidden, sorted by or resized. -->
        <p
          class="mt-1 border-t border-outline-variant/30 px-3 pt-2 pb-1 text-label-small font-semibold
                 uppercase tracking-wider text-on-surface-variant/60"
        >
          Fixed
        </p>
        <ul class="py-0.5" aria-label="Fixed columns">
          {#each edges as edge (edge.kind)}
            <li>
              <Checkbox
                checked={preferences.isEdgeFixed(edge.kind)}
                onchange={() => toggleFixed(edge.kind)}
                full
                dense
                class="rounded-sm px-3 py-1.5 text-body-small text-on-surface
                       transition-colors duration-75 hover:bg-surface-container-highest"
              >
                <span class="block truncate">{edge.label}</span>
              </Checkbox>
            </li>
          {/each}
        </ul>
        <p class="px-3 pb-1 text-label-small text-on-surface-variant/60">
          Stays in view while the table scrolls sideways. Applies to every list.
        </p>
      {/if}

      <!-- The operator's own columns. Each is one key, shown as its heading,
           with where it reads from beside it: a label `team` and an
           annotation `team` are two different columns with one word for a
           heading, and the badge is what tells them apart here. -->
      <p
        class="mt-1 border-t border-outline-variant/30 px-3 pt-2 pb-1 text-label-small font-semibold
               uppercase tracking-wider text-on-surface-variant/60"
      >
        Your columns
      </p>

      {#if custom.length > 0}
        <ul class="max-h-48 overflow-auto py-0.5" aria-label="Custom columns">
          {#each custom as spec, index (customColumnId(spec))}
            <li
              class="flex items-center gap-2 px-3 py-1 text-body-small text-on-surface
                     transition-colors duration-75 hover:bg-surface-container-highest"
            >
              <Checkbox
                checked={!isCustomHidden(spec)}
                onchange={() => preferences.toggleColumn(kindId, customColumnId(spec))}
                ariaLabel="Show {spec.key}"
              />
              <span class="min-w-0 flex-1 truncate" title={spec.key}>{spec.key}</span>
              <span
                class="shrink-0 rounded bg-surface-container-highest px-1 text-label-small uppercase
                       tracking-wider text-on-surface-variant/70"
              >
                {spec.source}
              </span>
              <!-- Reorder by buttons rather than drag: two arrows are the
                   whole interaction, and they work from the keyboard. -->
              <button
                type="button"
                onclick={() => preferences.moveCustomColumn(kindId, index, index - 1)}
                disabled={index === 0}
                aria-label="Move {spec.key} up"
                title="Move up"
                class="state-layer grid size-6 shrink-0 place-items-center rounded-full
                       text-on-surface-variant disabled:opacity-30"
              >
                <ChevronUp class="size-3.5" strokeWidth={2} />
              </button>
              <button
                type="button"
                onclick={() => preferences.moveCustomColumn(kindId, index, index + 1)}
                disabled={index === custom.length - 1}
                aria-label="Move {spec.key} down"
                title="Move down"
                class="state-layer grid size-6 shrink-0 place-items-center rounded-full
                       text-on-surface-variant disabled:opacity-30"
              >
                <ChevronDown class="size-3.5" strokeWidth={2} />
              </button>
              <button
                type="button"
                onclick={() => remove(spec)}
                aria-label="Remove {spec.key}"
                title="Remove column"
                class="state-layer grid size-6 shrink-0 place-items-center rounded-full
                       text-on-surface-variant hover:text-error"
              >
                <X class="size-3.5" strokeWidth={2} />
              </button>
            </li>
          {/each}
        </ul>
      {:else}
        <p class="px-3 py-1 text-body-small text-on-surface-variant/60">
          None yet. Add a label or annotation key below.
        </p>
      {/if}

      <!-- The add form. The datalist carries what the rows on screen carry
           for the chosen source; anything else is typed. Enter adds. -->
      <form
        class="flex flex-col gap-1.5 px-3 pt-1.5 pb-1"
        onsubmit={(event) => {
          event.preventDefault()
          add()
        }}
      >
        <div class="flex items-center gap-1.5">
          <select
            bind:value={source}
            aria-label="Column source"
            class="h-7 shrink-0 rounded-sm border border-outline-variant/60 bg-surface-container
                   px-1.5 text-body-small text-on-surface"
          >
            <option value="label">Label</option>
            <option value="annotation">Annotation</option>
            <option value="jsonpath">JSONPath</option>
          </select>
          <input
            type="text"
            bind:value={key}
            list="column-key-suggestions"
            placeholder={source === 'jsonpath'
              ? '.status.phase'
              : 'key, e.g. app.kubernetes.io/name'}
            aria-label="{source} key"
            aria-invalid={problem !== '' ? 'true' : undefined}
            onkeydown={onKeyFieldKeydown}
            autocomplete="off"
            spellcheck="false"
            class="h-7 min-w-0 flex-1 rounded-sm border bg-surface-container px-2 text-body-small
                   text-on-surface placeholder:text-on-surface-variant/50 focus:outline-none
                   {problem !== '' ? 'border-error' : 'border-outline-variant/60 focus:border-primary'}"
          />
          <datalist id="column-key-suggestions">
            {#each suggestions as suggestion (suggestion)}
              <option value={suggestion}></option>
            {/each}
          </datalist>
          <button
            type="submit"
            disabled={!canAdd}
            aria-label="Add column"
            title="Add column"
            class="state-layer grid size-7 shrink-0 place-items-center rounded-full text-primary
                   disabled:opacity-30"
          >
            <Plus class="size-4" strokeWidth={2} />
          </button>
        </div>
        {#if problem !== ''}
          <p class="text-label-small text-error" role="alert">{problem}</p>
        {:else if wouldFetchWholeObjects}
          <p class="text-label-small text-on-surface-variant/60">
            A path reads into spec and status, so this list will fetch whole objects on every
            refresh. Labels and annotations do not.
          </p>
        {:else if source === 'jsonpath'}
          <p class="text-label-small text-on-surface-variant/60">
            The same paths kubectl takes: <span class="font-mono">.status.phase</span>,
            <span class="font-mono">.spec.containers[0].image</span>.
          </p>
        {:else if suggestions.length > 0}
          <p class="text-label-small text-on-surface-variant/60">
            {suggestions.length} {source} key{suggestions.length === 1 ? '' : 's'} on this list
          </p>
        {/if}
      </form>

      <div class="mt-1 border-t border-outline-variant/30 pt-1">
        <!-- Widths and visibility only. The operator's own columns are a
             different kind of choice and are removed one at a time above. -->
        <button
          type="button"
          onclick={() => preferences.resetColumns(kindId)}
          class="state-layer flex w-full items-center gap-2 px-3 py-1.5 text-left text-body-small text-primary"
        >
          <RotateCcw class="size-3.5" strokeWidth={1.8} />
          Reset widths and visibility
        </button>
      </div>
    </div>
  {/if}
</div>
