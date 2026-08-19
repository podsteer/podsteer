<!--
  The list search box.

  Filtering happens client-side against rows already fetched, which is why it
  is instant and why it makes no request. Cmd+K / Ctrl+K focuses it.

  Browser autocomplete is disabled (`type="text"` plus `autocomplete="off"`)
  so the native suggestion list and its built-in clear button never appear
  alongside our own.
-->
<script lang="ts">
  import { Search, X, Command } from '@lucide/svelte'
  import { isMac } from '$lib/platform'

  interface Props {
    value: string
    placeholder?: string
    onchange: (value: string) => void
    class?: string
  }

  let { value, placeholder = 'Search…', onchange, class: className = '' }: Props = $props()

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
    }
  }
</script>

<label class="relative flex items-center {className}">
  <Search
    class="pointer-events-none absolute left-3 size-3.5 text-on-surface-variant/60"
    strokeWidth={2}
  />
  <input
    bind:this={inputEl}
    type="text"
    autocomplete="off"
    autocorrect="off"
    autocapitalize="off"
    spellcheck="false"
    {value}
    {placeholder}
    oninput={(event) => onchange(event.currentTarget.value)}
    onkeydown={handleKeydown}
    class="no-drag h-8 w-full rounded-lg border border-outline-variant/60 bg-surface-container-low
           pl-9 text-body-medium text-on-surface placeholder:text-on-surface-variant/50
           transition-all duration-150 ease-standard
           hover:border-outline focus:border-primary focus:bg-surface focus:shadow-sm focus:outline-none
           {value ? 'pr-8' : 'pr-16'}"
  />

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
