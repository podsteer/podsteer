<!--
  The application's only dropdown.

  A custom trigger and panel rather than a native <select>, matching the
  console's `.sct-custom-select-*` component so the two products present the
  same control: an 8px-radius trigger, a panel offset 4px below it, options
  with a 4px leading bar that colours in on the selected one.

  A native select was here before and is genuinely good at some things —
  type-ahead, the platform popup, hundreds of entries — so the keyboard
  handling below exists to pay that back rather than to decorate: arrows and
  Home/End move, typing jumps, Enter commits, Escape abandons.

  The panel is positioned FIXED against the trigger. Both places this is used
  sit inside a scrolling ancestor, and an absolutely positioned panel cannot
  escape one — it would be clipped by the very thing that makes the list
  scrollable. It flips above when there is no room below.
-->
<script lang="ts">
  import { ChevronDown } from '@lucide/svelte'
  import { tick } from 'svelte'

  interface Option {
    value: string
    label: string
    /** Optional trailing detail, e.g. a count. */
    hint?: string
  }

  interface Props {
    /** Describes the field. Rendered above, or as the accessible name when compact. */
    label: string
    value: string
    options: Option[]
    disabled?: boolean
    /**
     * Toolbar sizing: no visible border until hovered, tighter padding.
     * The console's `is-compact` variant, for pagination and toolbars.
     */
    compact?: boolean
    /** Called with the newly chosen value. */
    onchange?: (value: string) => void
    class?: string
  }

  let {
    label,
    value,
    options,
    disabled = false,
    compact = false,
    onchange,
    class: className = '',
  }: Props = $props()

  let open = $state(false)
  let anchor = $state<DOMRect | null>(null)
  /** Which option the keyboard is on, which is not always the chosen one. */
  let active = $state(-1)
  let trigger = $state<HTMLButtonElement | null>(null)
  let panel = $state<HTMLDivElement | null>(null)

  const selected = $derived(options.find((option) => option.value === value))
  const selectedIndex = $derived(options.findIndex((option) => option.value === value))

  /** Type-ahead buffer, cleared when typing pauses. */
  let typed = ''
  let typedAt = 0

  async function show(): Promise<void> {
    if (disabled) return
    anchor = trigger?.getBoundingClientRect() ?? null
    active = selectedIndex >= 0 ? selectedIndex : 0
    open = true
    await tick()
    scrollActiveIntoView()
  }

  function hide(focusTrigger = true): void {
    open = false
    anchor = null
    if (focusTrigger) trigger?.focus()
  }

  function choose(index: number): void {
    const option = options[index]
    if (!option) return
    if (option.value !== value) onchange?.(option.value)
    hide()
  }

  function move(delta: number): void {
    if (options.length === 0) return
    const next = Math.min(options.length - 1, Math.max(0, active + delta))
    active = next
    scrollActiveIntoView()
  }

  function scrollActiveIntoView(): void {
    panel?.querySelector<HTMLElement>(`[data-index="${active}"]`)?.scrollIntoView({ block: 'nearest' })
  }

  /** Jumps to the next option starting with what was typed. */
  function typeAhead(key: string): void {
    const now = Date.now()
    typed = now - typedAt > 800 ? key : typed + key
    typedAt = now

    const from = typed.length === 1 ? active + 1 : active
    for (let step = 0; step < options.length; step += 1) {
      const index = (from + step) % options.length
      if (options[index].label.toLowerCase().startsWith(typed.toLowerCase())) {
        active = index
        scrollActiveIntoView()
        return
      }
    }
  }

  function onTriggerKeydown(event: KeyboardEvent): void {
    if (open) return
    if (['ArrowDown', 'ArrowUp', 'Enter', ' '].includes(event.key)) {
      event.preventDefault()
      void show()
    }
  }

  function onPanelKeydown(event: KeyboardEvent): void {
    switch (event.key) {
      case 'ArrowDown':
        event.preventDefault()
        move(1)
        break
      case 'ArrowUp':
        event.preventDefault()
        move(-1)
        break
      case 'Home':
        event.preventDefault()
        active = 0
        scrollActiveIntoView()
        break
      case 'End':
        event.preventDefault()
        active = options.length - 1
        scrollActiveIntoView()
        break
      case 'Enter':
      case ' ':
        event.preventDefault()
        choose(active)
        break
      case 'Escape':
        event.preventDefault()
        hide()
        break
      case 'Tab':
        hide(false)
        break
      default:
        if (event.key.length === 1 && !event.metaKey && !event.ctrlKey) typeAhead(event.key)
    }
  }

  /** Dismisses on a click elsewhere, without a backdrop that covers the app. */
  function onPointerDown(event: MouseEvent): void {
    if (!open) return
    const target = event.target as HTMLElement | null
    if (target?.closest('[data-select-root]')) return
    hide(false)
  }

  /** Focuses the panel so the keyboard handlers below receive keys at once. */
  function claimFocus(node: HTMLElement): void {
    node.focus()
  }

  /** Places the panel under its trigger, then pulls it inside the viewport. */
  function place(node: HTMLElement, rect: DOMRect | null): void {
    if (!rect) return
    const margin = 8
    const height = node.getBoundingClientRect().height

    node.style.width = `${rect.width}px`
    node.style.left = `${Math.max(margin, Math.min(rect.left, window.innerWidth - rect.width - margin))}px`

    let top = rect.bottom + 4
    if (top + height > window.innerHeight - margin) {
      const above = rect.top - height - 4
      top = above >= margin ? above : Math.max(margin, window.innerHeight - height - margin)
    }
    node.style.top = `${top}px`
  }
</script>

<svelte:window onpointerdown={onPointerDown} onresize={() => open && hide(false)} />

<div data-select-root class="relative {compact ? 'inline-block' : 'block'} {className}">
  {#if !compact}
    <span class="mb-1.5 block text-body-small text-on-surface-variant">{label}</span>
  {/if}

  <button
    bind:this={trigger}
    type="button"
    {disabled}
    aria-haspopup="listbox"
    aria-expanded={open}
    aria-label={compact ? label : undefined}
    title={compact ? label : undefined}
    onclick={() => (open ? hide() : show())}
    onkeydown={onTriggerKeydown}
    class="flex w-full items-center justify-between gap-2 text-left text-body-medium
           whitespace-nowrap
           {compact
      ? 'no-drag h-8 rounded-sm border border-transparent px-2 text-on-surface hover:border-outline-variant/60 disabled:pointer-events-none disabled:opacity-38'
      : 'field h-8 px-3'}
           {open && compact ? 'border-outline-variant/60' : ''}"
  >
    <span class="truncate">
      {selected ? selected.label : ''}
      {#if selected?.hint}
        <span class="text-on-surface-variant/70">— {selected.hint}</span>
      {/if}
    </span>
    <ChevronDown
      class="size-4 shrink-0 text-on-surface-variant/70 transition-transform duration-150
             {open ? 'rotate-180' : ''}"
      strokeWidth={2}
    />
  </button>

  {#if open}
    <div
      bind:this={panel}
      data-select-root
      role="listbox"
      aria-label={label}
      tabindex="-1"
      use:place={anchor}
      onkeydown={onPanelKeydown}
      use:claimFocus
      class="fixed z-[70] max-h-[300px] min-w-40 overflow-y-auto overflow-x-hidden rounded-sm border
             border-outline-variant bg-surface-container py-1 shadow-level-2"
    >
      {#each options as option, index (option.value)}
        {@const isSelected = option.value === value}
        <button
          type="button"
          role="option"
          data-index={index}
          aria-selected={isSelected}
          onclick={() => choose(index)}
          onmouseenter={() => (active = index)}
          class="flex w-full items-center gap-2 border-l-4 py-2 pr-3 pl-3 text-left text-body-medium
                 transition-colors duration-100
                 {isSelected
            ? 'border-l-primary bg-primary-container font-medium text-on-primary-container'
            : 'border-l-transparent text-on-surface'}
                 {active === index && !isSelected ? 'bg-surface-container-high' : ''}"
        >
          <span class="truncate">{option.label}</span>
          {#if option.hint}
            <span class="ml-auto shrink-0 text-body-small text-on-surface-variant/70">
              {option.hint}
            </span>
          {/if}
        </button>
      {/each}
    </div>
  {/if}
</div>
