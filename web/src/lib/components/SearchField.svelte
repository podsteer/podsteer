<!--
  The list search box.

  Filtering happens client-side against rows already fetched, which is why it
  is instant and why it makes no request. Cmd+K / Ctrl+K focuses it.

  Browser autocomplete is disabled (`type="text"` plus `autocomplete="off"`)
  so the native suggestion list and its built-in clear button never appear
  alongside our own.

  The value is not only ever a plain substring any more — see `$lib/query` —
  so this also carries an invalid state for a pattern that failed to parse,
  and a description for what the box currently accepts.
-->
<script lang="ts">
  import { Search, AlertCircle, X, Command } from '@lucide/svelte'
  import { isMac } from '$lib/platform'

  interface Props {
    value: string
    placeholder?: string
    onchange: (value: string) => void
    /**
     * Asked to move on from the field, by pressing Down. Returns true when
     * something took focus, so an empty result list leaves the key alone
     * rather than swallowing it.
     */
    onnext?: () => boolean
    class?: string
    /**
     * True when the query does not parse — an unclosed regex, typically.
     * Swaps the leading icon for an alert and recolours the border, the same
     * vocabulary ErrorBanner uses for a failure, scaled down to a field.
     */
    invalid?: boolean
    /**
     * The field's `title` and accessible description: the parse error while
     * `invalid`, otherwise a one-line summary of the syntax currently in
     * effect (`describeQuery`). Omit to leave both unset.
     */
    description?: string
  }

  let {
    value,
    placeholder = 'Search…',
    onchange,
    onnext,
    class: className = '',
    invalid = false,
    description,
  }: Props = $props()

  // `aria-describedby` needs an id to point at, and this field can appear
  // more than once — the Cmd+K box today, potentially another table's
  // tomorrow — so the id is generated per instance rather than hard-coded.
  const uid = $props.id()
  const descriptionId = `${uid}-description`

  let inputEl = $state<HTMLInputElement | null>(null)

  /** Focus the search field. Called from outside via Cmd+K. */
  export function focus(): void {
    inputEl?.focus()
    inputEl?.select()
  }

  function handleKeydown(event: KeyboardEvent): void {
    // Enter has nothing to confirm — every keystroke already filters the
    // table live — so it just gets the cursor out of the field.
    if (event.key === 'Escape' || event.key === 'Enter') {
      inputEl?.blur()
      return
    }

    // Down goes to what you were searching FOR. Typing a few characters and
    // reaching for the results is the whole motion; making it a mouse trip
    // wastes the filtering.
    if (event.key === 'ArrowDown' && onnext?.()) {
      event.preventDefault()
      inputEl?.blur()
    }
  }
</script>

<label class="relative flex items-center {className}">
  {#if invalid}
    <AlertCircle class="pointer-events-none absolute left-3 size-3.5 text-error" strokeWidth={2} />
  {:else}
    <Search
      class="pointer-events-none absolute left-3 size-3.5 text-on-surface-variant/60"
      strokeWidth={2}
    />
  {/if}
  <!--
    NAMED EXPLICITLY, because the label wrapping this has no text of its own —
    only the search icon, the shortcut chip and the clear button. A screen
    reader therefore built the name out of those: empty, this box announced
    itself as "Ctrl K, edit text", and once you typed in it the chip was
    replaced by the clear button and the name became "Clear search". Wrong in
    both states, and it changed as you typed.
  -->
  <input
    bind:this={inputEl}
    type="text"
    autocomplete="off"
    autocorrect="off"
    autocapitalize="off"
    spellcheck="false"
    {value}
    {placeholder}
    aria-label={placeholder}
    aria-invalid={invalid || undefined}
    aria-describedby={description ? descriptionId : undefined}
    title={description}
    oninput={(event) => onchange(event.currentTarget.value)}
    onkeydown={handleKeydown}
    style:border-color={invalid ? 'var(--color-error)' : undefined}
    class="field h-8 w-full pl-9 text-body-medium {value ? 'pr-8' : 'pr-16'}"
  />

  <!-- `aria-describedby` needs an element, not a string — unlike the draft
       `aria-description` attribute this targets, which svelte-check's a11y
       lint does not yet recognise on an input. Visually hidden: `title`
       already carries this to a sighted pointer user via hover. -->
  {#if description}
    <span id={descriptionId} class="sr-only">{description}</span>
  {/if}

  {#if value}
    <button
      type="button"
      onclick={() => {
        onchange('')
        inputEl?.focus()
      }}
      aria-label="Clear search"
      class="state-layer absolute right-1.5 grid size-5 place-items-center rounded-full
             text-on-surface-variant/60 transition-colors duration-100 hover:text-on-surface"
    >
      <X class="size-3" strokeWidth={2.5} />
    </button>
  {:else}
    <span
      class="pointer-events-none absolute right-2 flex items-center gap-0.5 rounded border
             border-outline-variant/40 bg-surface-container px-1.5 py-0.5 text-label-small
             text-on-surface-variant/50"
    >
      {#if isMac}
        <Command class="size-2.5" strokeWidth={2} />
      {:else}
        <span class="text-[10px]">Ctrl</span>
      {/if}
      <span class="text-[10px]">K</span>
    </span>
  {/if}
</label>
