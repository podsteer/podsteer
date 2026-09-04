<!--
  The search field at the leading edge of a PaneToolbar.

  Extracted from YamlPane once the log pane needed the same thing. What the
  query MEANS differs between the two — the manifest finds and jumps, the log
  stream filters — but the field itself must not: same position, same size,
  same count in the same corner, same clear button, same suppression of every
  autofill and spellcheck mechanism the webview would otherwise attach to a
  bare text input.

  It takes the free space because it is the only control in the row that is
  typed into rather than clicked.
-->
<script lang="ts">
  import { AlertCircle, Search, X } from '@lucide/svelte'

  interface Props {
    /** The current query. */
    value: string
    /** Placeholder, since "search" alone does not say what is searched. */
    placeholder?: string
    /** Names the field for assistive technology. */
    label: string
    /**
     * Drawn inside the field, at the right.
     *
     * A bare number where the pane finds matches, `shown/total` where it
     * filters — the callers mean different things and say so themselves.
     */
    count?: string
    /** True when the query matches nothing, which dims the count. */
    empty?: boolean
    /** Focuses the field as soon as it appears. */
    autofocus?: boolean
    onchange: (value: string) => void
    /** Enter, and Shift+Enter, where stepping through matches makes sense. */
    onnext?: () => void
    onprevious?: () => void
    /**
     * True when the query does not parse — an unclosed `re:`/`/…/` regex.
     * Swaps the leading icon for an alert and recolours the border, the same
     * vocabulary `SearchField` uses for the table's own search box, so a
     * broken pattern reads the same way whichever field it was typed into.
     */
    invalid?: boolean
    /** The field's `title`: the parse error while `invalid`, otherwise
        omitted. Mirrors `SearchField`'s `description`, narrowed to what this
        field needs — it has no separate syntax summary to show when valid. */
    description?: string
  }

  let {
    value,
    placeholder = 'Search',
    label,
    count,
    empty = false,
    autofocus = false,
    onchange,
    onnext,
    onprevious,
    invalid = false,
    description,
  }: Props = $props()

  let input = $state<HTMLInputElement | null>(null)

  /**
   * Focused once per mount, not on every change.
   *
   * Without the guard the effect re-runs whenever `input` is reassigned and
   * takes focus back from wherever it had moved — mid-edit, in the dialog.
   *
   * NOT SET BY THE YAML OR LOG PANES ANY MORE. Both are reached by switching
   * a tab, and switching to a pane is a request to READ it: a find box taking
   * focus meant the next arrow key filtered instead of scrolled, and a screen
   * reader was dropped into a text field rather than at the content somebody
   * had just asked for. The box is visible in the toolbar and one Tab away.
   * Keep this for a search that APPEARS because it was asked for.
   */
  let focused = false
  $effect(() => {
    if (autofocus && input && !focused) {
      focused = true
      input.focus()
    }
  })

  function onKeydown(event: KeyboardEvent): void {
    if (event.key === 'Enter' && (onnext || onprevious)) {
      event.preventDefault()
      // Shift steps backwards, the convention every find box shares.
      if (event.shiftKey) onprevious?.()
      else onnext?.()
    } else if (event.key === 'Escape' && value) {
      // While there is something in the box, Escape belongs to the box and
      // not to the drawer behind it.
      event.stopPropagation()
      onchange('')
    }
  }
</script>

<!-- The margin is the gap to the first icon: at the toolbar's own spacing the
     field's border sat almost against it and the two read as one control. -->
<div class="relative mr-3 flex min-w-0 flex-1 items-center">
  {#if invalid}
    <AlertCircle
      class="pointer-events-none absolute left-2 size-3.5 text-error"
      strokeWidth={1.8}
    />
  {:else}
    <Search
      class="pointer-events-none absolute left-2 size-3.5 text-on-surface-variant/50"
      strokeWidth={1.8}
    />
  {/if}

  <!-- Every suggestion mechanism off. This searches one document, so a
       dropdown of things typed into other search boxes is noise that covers
       the first line of the result — and the webview offers exactly that
       unless told not to. `name` is omitted for the same reason: an unnamed
       field is not one autofill recognises. The two data attributes are for
       the password managers that ignore `autocomplete` on principle. -->
  <input
    bind:this={input}
    {value}
    oninput={(event) => onchange(event.currentTarget.value)}
    onkeydown={onKeydown}
    type="text"
    {placeholder}
    aria-label={label}
    aria-invalid={invalid || undefined}
    title={description}
    autocomplete="off"
    autocorrect="off"
    autocapitalize="off"
    spellcheck="false"
    data-1p-ignore
    data-lpignore="true"
    style:border-color={invalid ? 'var(--color-error)' : undefined}
    class="field h-7 w-full min-w-0 py-0 pr-16 pl-7 text-body-small"
  />

  {#if value}
    <!-- Inside the field, where it answers the question the typing just asked
         without moving anything on the row. -->
    {#if count !== undefined}
      <span
        class="pointer-events-none absolute right-7 text-body-small tabular-nums
               {empty ? 'text-on-surface-variant/50' : 'text-on-surface-variant'}"
      >
        {count}
      </span>
    {/if}
    <button
      type="button"
      onclick={() => {
        onchange('')
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
