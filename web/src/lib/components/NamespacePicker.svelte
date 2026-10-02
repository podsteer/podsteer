<!--
  The namespace filter: All, or a set of namespaces, chosen with checkboxes.

  ONE PICKER FOR THE WHOLE APPLICATION. The sidebar's filter is a set (see
  $lib/namespaceScope), every list reads over it, and this is the control
  that chooses it — extracted from the Topology page's own scope picker, the
  first place the set existed.

  A DRAFT AND AN APPLY, not a change per tick. Ticking three namespaces one
  at a time would be three refreshes of whatever is on screen, the first two
  of them answers to questions nobody meant to ask. Cancel, Escape or a click
  elsewhere leave the filter exactly as it was. Apply is disabled while the
  draft is empty and not All: there is no "nothing selected", because a list
  of nothing is not a filter anybody wants.

  A NAME THE CLUSTER NO LONGER LISTS STAYS VISIBLE, marked "not found" — a
  namespace deleted while the tab was open, one remembered from before, or an
  account that may list objects in a namespace but not list namespaces. The
  trigger would otherwise describe a filter the list could not show, with the
  whole tree below still scoped to it.

  Keyboard: Enter, Space or ↓ on the trigger open it with focus in the filter;
  ↑/↓ move through "All namespaces" and the rows; Space ticks the row; Enter
  applies; Escape cancels.
-->
<script lang="ts">
  import { ChevronDown } from '@lucide/svelte'
  import { escapeLayer, type EscapeClaim } from '$lib/escape'
  import { namespaceLabelOf, scopeOf, type NamespaceScope } from '$lib/namespaceScope'

  interface Choice {
    name: string
    /** Trailing detail: a phase such as "terminating". */
    hint?: string
  }

  interface Props {
    /** The filter as it stands. */
    value: NamespaceScope
    /** The namespaces the cluster lists. */
    choices: Choice[]
    disabled?: boolean
    /** The trigger's tooltip, when it should say something other than the
        selection — why it is disabled, say. */
    title?: string
    /** The accessible name of the control and its panel. */
    label?: string
    applyLabel?: string
    /** Called with the chosen scope when Apply is pressed. */
    onapply: (scope: NamespaceScope) => void
    /** Called as the panel opens, for a list that can go stale. */
    onopen?: () => void
    class?: string
  }

  let {
    value,
    choices,
    disabled = false,
    title,
    label = 'Namespaces',
    applyLabel = 'Apply',
    onapply,
    onopen,
    class: className = '',
  }: Props = $props()

  let open = $state(false)
  let draftAll = $state(false)
  let draftNamespaces = $state<string[]>([])
  let filter = $state('')
  /** The keyboard's row: 0 is "All namespaces", 1… the shown namespaces. */
  let active = $state(0)
  let trigger = $state<HTMLButtonElement | null>(null)
  let claim: EscapeClaim | null = null

  const shown = $derived(namespaceLabelOf(value))

  /** Every choice, plus any selected name the cluster no longer lists. */
  const rows = $derived.by<Choice[]>(() => {
    const listed = new Set(choices.map((choice) => choice.name))
    const missing = value.namespaces
      .filter((name) => !listed.has(name))
      .map((name) => ({ name, hint: 'not found' }))
    return [...choices, ...missing]
  })

  const visible = $derived(
    rows.filter((row) => !filter || row.name.toLowerCase().includes(filter.trim().toLowerCase())),
  )

  const canApply = $derived(draftAll || draftNamespaces.length > 0)

  /** `active`, kept on a row that exists when the filter shows fewer. */
  const current = $derived(Math.min(active, visible.length))

  function show(): void {
    if (disabled) return
    draftAll = value.all
    draftNamespaces = [...value.namespaces]
    filter = ''
    active = 0
    open = true
    claim = escapeLayer()
    onopen?.()
  }

  function hide(focusTrigger = true): void {
    open = false
    claim?.release()
    claim = null
    if (focusTrigger) trigger?.focus()
  }

  function apply(): void {
    if (!canApply) return
    const next = scopeOf(draftAll ? [] : draftNamespaces)
    hide()
    onapply(next)
  }

  function toggle(name: string): void {
    if (draftAll) return
    draftNamespaces = draftNamespaces.includes(name)
      ? draftNamespaces.filter((entry) => entry !== name)
      : [...draftNamespaces, name]
  }

  function toggleActive(): void {
    if (current === 0) draftAll = !draftAll
    else {
      const row = visible[current - 1]
      if (row) toggle(row.name)
    }
  }

  function onTriggerKeydown(event: KeyboardEvent): void {
    if (open) return
    if (['ArrowDown', 'Enter', ' '].includes(event.key)) {
      event.preventDefault()
      show()
    }
  }

  function onPanelKeydown(event: KeyboardEvent): void {
    switch (event.key) {
      case 'ArrowDown':
        event.preventDefault()
        active = Math.min(visible.length, current + 1)
        break
      case 'ArrowUp':
        event.preventDefault()
        active = Math.max(0, current - 1)
        break
      case ' ':
        // A namespace name never holds a space, so in the filter it means
        // "tick this row" rather than a character.
        event.preventDefault()
        toggleActive()
        break
      case 'Enter':
        event.preventDefault()
        apply()
        break
      case 'Escape':
        event.preventDefault()
        event.stopPropagation()
        hide()
        break
    }
  }

  /**
   * Escape cancels wherever focus is while the picker is open — the panel
   * holds the escape layer, so no other layer will act on it — but only
   * while it is the innermost layer.
   */
  function onWindowKeydown(event: KeyboardEvent): void {
    if (!open || event.key !== 'Escape' || !claim?.owns()) return
    event.preventDefault()
    event.stopPropagation()
    hide()
  }

  /** Cancels when focus leaves for something outside the picker — Tab past
      the buttons, say. A blur to nothing (the window losing focus) is not
      leaving. */
  function onFocusOut(event: FocusEvent): void {
    if (!open) return
    const next = event.relatedTarget as HTMLElement | null
    if (!next || next.closest('[data-namespace-picker]')) return
    hide(false)
  }

  /** Cancels on a click elsewhere, without a backdrop over the app. */
  function onPointerDown(event: PointerEvent): void {
    if (!open) return
    const target = event.target as HTMLElement | null
    if (target?.closest('[data-namespace-picker]')) return
    hide(false)
  }

  function autofocus(node: HTMLInputElement): void {
    node.focus()
  }

  $effect(() => () => claim?.release())
</script>

<svelte:window onpointerdown={onPointerDown} onkeydown={onWindowKeydown} />

<div data-namespace-picker class="relative {className}" onfocusout={onFocusOut}>
  <button
    bind:this={trigger}
    type="button"
    {disabled}
    aria-haspopup="dialog"
    aria-expanded={open}
    title={title ?? shown.title}
    onclick={() => (open ? hide() : show())}
    onkeydown={onTriggerKeydown}
    class="field flex h-8 w-full items-center justify-between gap-2 px-3 text-left text-body-medium whitespace-nowrap
           disabled:opacity-38"
  >
    <span class="sr-only">{label}</span>
    <span class="truncate">{shown.label}</span>
    <ChevronDown
      class="size-4 shrink-0 text-on-surface-variant/70 transition-transform duration-150 {open ? 'rotate-180' : ''}"
      strokeWidth={2}
    />
  </button>

  {#if open}
    <div
      role="dialog"
      aria-label={label}
      tabindex="-1"
      onkeydown={onPanelKeydown}
      class="absolute left-0 top-9 z-[70] flex max-h-96 w-full min-w-64 flex-col gap-2 rounded-xs border
             border-outline-variant bg-surface-container p-3 shadow-level-2"
    >
      <label
        class="flex items-center gap-2 rounded-xs px-1 text-body-medium text-on-surface
               {current === 0 ? 'state-layer-active' : ''}"
      >
        <input type="checkbox" bind:checked={draftAll} />
        All namespaces
      </label>
      <input
        use:autofocus
        type="search"
        bind:value={filter}
        placeholder="Filter namespaces…"
        aria-label="Filter namespaces"
        class="field h-8 px-2 text-body-small"
      />
      <ul class="min-h-0 flex-1 overflow-y-auto" aria-label="Namespaces">
        {#each visible as row, index (row.name)}
          <li>
            <label
              class="flex items-center gap-2 rounded-xs px-1 py-0.5 text-body-small text-on-surface
                     {draftAll ? 'opacity-50' : ''} {current === index + 1 ? 'state-layer-active' : ''}"
            >
              <input
                type="checkbox"
                disabled={draftAll}
                checked={draftAll || draftNamespaces.includes(row.name)}
                onchange={() => toggle(row.name)}
              />
              <span class="truncate">{row.name}</span>
              {#if row.hint}
                <span class="shrink-0 text-on-surface-variant/70">— {row.hint}</span>
              {/if}
            </label>
          </li>
        {:else}
          <li class="text-body-small text-on-surface-variant/70">
            {rows.length === 0 ? 'No namespaces to choose from.' : 'No namespace matches.'}
          </li>
        {/each}
      </ul>
      <div class="flex justify-end gap-2">
        <button
          type="button"
          class="state-layer rounded-sm px-3 py-1 text-label-large text-on-surface-variant hover:bg-surface-container-high"
          onclick={() => hide()}
        >
          Cancel
        </button>
        <button
          type="button"
          disabled={!canApply}
          class="rounded-sm bg-primary px-3 py-1 text-label-large text-on-primary disabled:opacity-50"
          onclick={apply}
        >
          {applyLabel}
        </button>
      </div>
    </div>
  {/if}
</div>
