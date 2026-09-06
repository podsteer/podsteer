<!--
  The application's only dropdown.

  A custom trigger and panel rather than a native <select>, drawn the way
  Material Design 3 draws an exposed dropdown menu: the application's one
  field shape for the trigger, and a menu surface below it at the menu corner
  radius, carrying a level-2 elevation and marking its chosen row with the
  secondary container tone and a trailing check.

  A native select was here before and is genuinely good at some things —
  type-ahead, the platform popup, hundreds of entries — so the keyboard
  handling below exists to pay that back rather than to decorate: arrows and
  Home/End move, typing jumps, Enter commits, Escape abandons.

  The panel is positioned FIXED against the trigger, which is what lets it
  escape a scrolling ancestor that would otherwise clip the very list that
  makes it scrollable. Fixed is not quite absolute, though: it resolves
  against the nearest ancestor carrying a transform, a filter or containment
  rather than against the viewport, and a dialog centred with `-translate-1/2`
  was enough to send a panel to the corner of the window. So the placement
  measures where it actually landed and corrects for the difference, which
  works whatever the ancestor did. It flips above when there is no room below.
-->
<script lang="ts">
  import { Check, ChevronDown } from '@lucide/svelte'
  import { tick, untrack } from 'svelte'

  interface Option {
    value: string
    label: string
    /** Optional trailing detail, e.g. a count. */
    hint?: string
  }

  interface Props {
    /**
     * Describes the field. Rendered as the panel's heading, and as the
     * accessible name when compact unless `accessibleName` overrides it.
     *
     * Keep it short: it is a heading, and a long one is what a panel has to
     * stretch to fit.
     */
    label: string
    /**
     * The accessible name and tooltip, when they should say more than the
     * heading does.
     *
     * A per-row control needs to name the row it belongs to for anyone
     * navigating by keyboard or screen reader, while the visible heading has
     * to stay short enough not to widen the panel past the object it names.
     */
    accessibleName?: string
    value: string
    options: Option[]
    disabled?: boolean
    /**
     * Toolbar sizing: no visible border until hovered, tighter padding.
     * The console's `is-compact` variant, for pagination and toolbars.
     */
    compact?: boolean
    /**
     * Shown on the trigger when nothing is selected.
     *
     * Lets the same control act as a menu of one-shot choices — Snooze for an
     * hour, a day — where there is no standing value to display afterwards.
     */
    placeholder?: string
    /** Called with the newly chosen value. */
    onchange?: (value: string) => void
    /**
     * Called as the panel opens, for options that can go stale.
     *
     * A list read once when a screen loaded is wrong by the time somebody
     * looks at it, and opening the panel is exactly that moment — so this is
     * where a caller re-reads it, instead of polling to keep fresh a list
     * nobody is looking at.
     *
     * Fired without being awaited: the panel opens NOW, on the click, showing
     * what is already known. Whatever arrives lands in `options` and Svelte
     * re-renders the panel around it. Blocking the open on a network round
     * trip would trade a stale entry for a dropdown that feels broken.
     */
    onopen?: () => void
    class?: string
  }

  let {
    label,
    accessibleName,
    value,
    options,
    disabled = false,
    compact = false,
    placeholder = '',
    onchange,
    onopen,
    class: className = '',
  }: Props = $props()

  let open = $state(false)
  let anchor = $state<DOMRect | null>(null)
  /** Which option the keyboard is on, which is not always the chosen one. */
  let active = $state(-1)

  /**
   * A stable id per option, for aria-activedescendant.
   *
   * Instance-scoped, because several of these are on screen at once — the
   * namespace filter and the settings panes — and two listboxes numbering
   * their options the same way would have the browser resolve the wrong one.
   */
  const instance = $props.id()
  const optionId = (index: number): string => `${instance}-option-${index}`
  /** The panel's own id, so the trigger can name what it opened. */
  const listboxId = `${instance}-listbox`
  /**
   * The VALUE that index points at, kept alongside it.
   *
   * `active` is an index into a list that a caller's `onopen` can replace a
   * moment after the panel opened — a namespace created since the tab
   * connected arrives in sorted position and shifts everything after it down
   * one. The index alone would keep pointing at the same slot, so the
   * highlight would slide onto a neighbour and Enter would choose it. The
   * value is what survives the list being rebuilt.
   *
   * Plain, not `$state`: it is only ever read to re-find a row, and making it
   * reactive would re-run the effect below that maintains it.
   */
  let activeValue = ''
  let trigger = $state<HTMLButtonElement | null>(null)
  let panel = $state<HTMLDivElement | null>(null)

  const selected = $derived(options.find((option) => option.value === value))
  const selectedIndex = $derived(options.findIndex((option) => option.value === value))

  /** Moves the keyboard's position, remembering WHICH option it landed on. */
  function setActive(index: number): void {
    active = index
    activeValue = options[index]?.value ?? ''
  }

  /**
   * Re-finds the highlighted option after the list is replaced beneath it.
   *
   * Only while the panel is open, and only by value — an option that has gone
   * away entirely leaves the highlight where it was rather than jumping it
   * somewhere arbitrary.
   */
  $effect(() => {
    const list = options
    if (!open || activeValue === '') return

    untrack(() => {
      const index = list.findIndex((option) => option.value === activeValue)
      if (index >= 0 && index !== active) active = index
    })
  })

  /** Type-ahead buffer, cleared when typing pauses. */
  let typed = ''
  let typedAt = 0

  async function show(): Promise<void> {
    if (disabled) return
    anchor = trigger?.getBoundingClientRect() ?? null
    setActive(selectedIndex >= 0 ? selectedIndex : 0)
    open = true

    // Deliberately not awaited — see the prop's own note. The panel is already
    // opening with what is known; a fresher list arrives into it.
    onopen?.()

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
    setActive(Math.min(options.length - 1, Math.max(0, active + delta)))
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
        setActive(index)
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
        setActive(0)
        scrollActiveIntoView()
        break
      case 'End':
        event.preventDefault()
        setActive(options.length - 1)
        scrollActiveIntoView()
        break
      case 'Enter':
      case ' ':
        event.preventDefault()
        choose(active)
        break
      case 'Escape':
        // Stopped here as well as defaulted: a dialog listening on the window
        // for Escape would otherwise close along with the panel, which is one
        // key press doing two things nobody asked for.
        event.preventDefault()
        event.stopPropagation()
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

  /**
   * Places the panel under its trigger, then pulls it inside the viewport.
   *
   * A full-width select gets a panel matching its trigger, because the two
   * read as one control. A compact one gets a panel sized to its own
   * contents, floored at the trigger's width: "10" through "100" under a
   * "Rows" heading needs about seven characters, and stretching that to a
   * fixed minimum left most of the panel empty.
   *
   * IT ALSO STARTS THE ENTRANCE, and only on the last line, by writing the
   * placement it settled on. Everything above measures the panel with
   * `getBoundingClientRect()`, which reports the box a `transform` has moved
   * it to rather than the box it was laid out in — so an entrance animating
   * any property that shifts the box has to be held until the measuring is
   * finished, or a menu opened inside a transformed dialog is corrected
   * against its own animation and lands somewhere arbitrary. The keyframes
   * live in app.css and wait for this attribute; a menu that never reaches
   * here (no anchor to measure against) simply appears, which is the right
   * failure.
   */
  function place(node: HTMLElement, rect: DOMRect | null): void {
    if (!rect) return
    const margin = 8

    if (compact) {
      node.style.minWidth = `${rect.width}px`
      node.style.width = 'max-content'
    } else {
      node.style.width = `${rect.width}px`
    }

    const width = node.getBoundingClientRect().width
    const height = node.getBoundingClientRect().height

    // Right-aligned to the trigger when it grew past it, so a compact control
    // near the right edge does not push its panel off screen.
    const preferredLeft = compact ? rect.right - width : rect.left
    const left = Math.max(margin, Math.min(preferredLeft, window.innerWidth - width - margin))

    let flipped = false
    let top = rect.bottom + 4
    if (top + height > window.innerHeight - margin) {
      const above = rect.top - height - 4
      top = above >= margin ? above : Math.max(margin, window.innerHeight - height - margin)
      flipped = true
    }

    node.style.left = `${left}px`
    node.style.top = `${top}px`

    // Where it actually landed, which is not always where it was put: a
    // transformed ancestor makes these coordinates relative to itself. The
    // difference is measured and subtracted rather than the ancestor being
    // hunted for, so this stays correct whatever a future dialog does to
    // centre itself.
    const landed = node.getBoundingClientRect()
    if (Math.abs(landed.left - left) > 0.5 || Math.abs(landed.top - top) > 0.5) {
      node.style.left = `${left - (landed.left - left)}px`
      node.style.top = `${top - (landed.top - top)}px`
    }

    // Measuring is over: the menu may now move. Actions run before the
    // browser paints, so nothing is on screen at the un-animated position
    // first — there is no flash to avoid here, only a measurement to protect.
    // The direction follows the flip, so a menu forced upwards rises from
    // below instead of descending through the trigger it belongs to.
    node.dataset.placement = flipped ? 'above' : 'below'
  }
</script>

<svelte:window onpointerdown={onPointerDown} onresize={() => open && hide(false)} />

<div data-select-root class="relative {compact ? 'inline-block' : 'block'} {className}">
  <button
    bind:this={trigger}
    type="button"
    {disabled}
    data-select-trigger
    aria-haspopup="listbox"
    aria-expanded={open}
    aria-controls={open ? listboxId : undefined}
    title={compact ? (accessibleName ?? label) : undefined}
    onclick={() => (open ? hide() : show())}
    onkeydown={onTriggerKeydown}
    class="flex w-full items-center justify-between gap-2 text-left text-body-medium
           whitespace-nowrap
           {compact
      ? 'no-drag h-8 rounded-sm border border-transparent px-2 text-on-surface hover:border-outline-variant/60 disabled:pointer-events-none disabled:opacity-38'
      : 'field h-8 px-3'}
           {open && compact ? 'border-outline-variant/60' : ''}"
  >
    <!--
      What the control IS, said to assistive technology only.

      This used to be an `aria-label`, and only on the compact variant. Both
      halves were wrong. `aria-label` REPLACES an element's content in the
      accessible name, so a compact trigger announced "Rows per page" and
      never the number it was showing; and a full-width trigger, which had no
      label at all, announced its current value with nothing saying what the
      value was of — "production, button", no mention of namespaces.

      A hidden span is CONCATENATED with the visible text instead, so both
      variants now read as "<what it is>, <what it is set to>". The visible
      label stays where it was, inside the panel: see the note there for why
      it is not sitting over the field.
    -->
    <span class="sr-only">{accessibleName ?? label}</span>
    <span class="truncate {selected ? '' : 'text-on-surface-variant'}">
      {selected ? selected.label : placeholder}
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
    <!--
      aria-activedescendant says WHICH OPTION IS ACTIVE. Focus stays on the
      panel and the arrow keys move a highlight, which is the right design for
      a listbox — but the highlight was only drawn, never announced, so a
      screen reader following the arrow keys heard nothing move.
    -->
    <div
      bind:this={panel}
      id={listboxId}
      data-select-root
      data-select-menu
      role="listbox"
      aria-label={accessibleName ?? label}
      aria-activedescendant={active >= 0 && options[active] ? optionId(active) : undefined}
      tabindex="-1"
      use:place={anchor}
      onkeydown={onPanelKeydown}
      use:claimFocus
      class="fixed z-[70] max-h-[300px] overflow-y-auto overflow-x-hidden rounded-xs border
             border-outline-variant bg-surface-container pb-1 shadow-level-2"
    >
      <!--
        The menu corner radius, deliberately tighter than the field's 8px: MD3
        gives a menu its own shape token rather than borrowing its trigger's,
        and matching the two made the panel read as a second field stacked
        under the first rather than as a surface floating over it.

        The outline is NOT part of that language — a menu is meant to separate
        itself from what it covers by elevation alone. It is kept because half
        of that mechanism does not survive this application's dark theme: a
        shadow cast onto a near-black surface is invisible, and without the
        outline the panel's edge disappears into whatever is behind it.
      -->
      <!-- What the dropdown is FOR, said where it is being used rather than
           above the trigger. A label sitting over the field spent a line of
           chrome saying something only relevant once the list is open — and
           when the list is long, this is the thing worth keeping in view, so
           it sticks rather than scrolling away.

           Hidden from assistive technology because the listbox already
           carries the same string as its accessible name, and announcing it
           twice is worse than not showing it. -->
      <p
        aria-hidden="true"
        class="sticky top-0 z-10 border-b border-outline-variant/60 bg-surface-container px-3 py-2
               text-label-medium text-on-surface-variant"
      >
        {label}
      </p>

      {#each options as option, index (option.value)}
        {@const isSelected = option.value === value}
        <!--
          The highlight is ONE class driven by `active`, which both the arrow
          keys and `onmouseenter` set — so the pointer and the keyboard cannot
          disagree about which row is lit, and cannot light two at once. That
          is why it is `state-layer-active` and not the `state-layer` utility
          sitting beside it in app.css: `state-layer` waits for `:hover`,
          which a keyboard never delivers to an option, because focus stays on
          the panel.

          Selection is the secondary container tone plus a trailing check,
          rather than the leading bar this control used to draw. The check is
          rendered on EVERY row and only made visible on the chosen one: an
          icon that came and went would change the width left to the label
          beside it, so every row would re-truncate as the selection moved.
        -->
        <button
          type="button"
          role="option"
          id={optionId(index)}
          data-index={index}
          aria-selected={isSelected}
          onclick={() => choose(index)}
          onmouseenter={() => (active = index)}
          class="flex w-full items-center gap-2 px-3 py-2 text-left text-body-medium
                 {isSelected
            ? 'bg-secondary-container text-on-secondary-container'
            : 'text-on-surface'}
                 {active === index ? 'state-layer-active' : ''}"
        >
          <span class="truncate">{option.label}</span>
          {#if option.hint}
            <span class="ml-auto shrink-0 text-body-small opacity-70">
              {option.hint}
            </span>
          {/if}
          <Check
            class="size-4 shrink-0 {option.hint ? '' : 'ml-auto'} {isSelected ? '' : 'invisible'}"
            strokeWidth={2}
            aria-hidden="true"
          />
        </button>
      {/each}
    </div>
  {/if}
</div>
