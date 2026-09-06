<!--
  What a detail row can do, behind one control.

  A row's trailing controls used to be a cluster of icons — a reveal, an
  information note, an expander — which had to be learnt one at a time and
  changed shape from row to row. Anything that is not "show me the rest of
  this" is now one menu: the reader sees two controls on every row, always the
  same two, and finds out what a particular row offers by opening it.

  Shown when the pointer is on its row, and kept shown while it is OPEN —
  otherwise moving the pointer off the row to reach the menu would fade the
  control the menu is hanging from. The popover is a descendant of the row, so
  hovering it keeps the row hovered; the override covers the case where the
  menu was opened and the pointer then left entirely.

  Closed by an outside pointer or by Escape, like every other menu here. It
  does NOT close on scroll: the panel scrolls under the pointer while somebody
  reads the menu, and a menu that vanishes when the list moves is a menu that
  cannot be used with a trackpad.
-->
<script module lang="ts">
  /**
   * Which row's menu is open, across every list in the application.
   *
   * ONE VALUE, NOT ONE PER MENU. Each menu used to keep its own, and the
   * outside-click handler asked only whether the pointer had landed in *a*
   * row menu — so opening a second one left the first standing, and a panel
   * could end up with four open at once. A single value cannot hold two
   * answers, which is the whole fix: opening one closes the last by
   * construction rather than by every menu remembering to.
   */
  let openMenu = $state<symbol | null>(null)
</script>

<script lang="ts">
  import { flash } from '$lib/flash.svelte'
  import { menuKeys } from '$lib/menuKeys'
  import { escapeLayer, type EscapeClaim } from '$lib/escape'
  import type { Component } from 'svelte'
  import {
    Ban,
    Check,
    CirclePause,
    CirclePlay,
    Copy,
    Eye,
    EyeOff,
    Info,
    Link2,
    LogOut,
    MoreVertical,
    Pencil,
    Play,
    RotateCcw,
    Scale,
    ScrollText,
    SquareTerminal,
    TerminalSquare,
    Trash2,
    TriangleAlert,
  } from '@lucide/svelte'

  export interface RowAction {
    label: string
    /**
     * What sort of thing this does.
     *
     * Chooses the icon, and singles out the copy — which confirms itself in
     * place before the menu closes, the way the status bar's share menu does.
     *
     * The verbs were added when the row menus stopped offering one item
     * each; every one of them names the SAME act as the drawer's toolbar
     * button for it, and uses that button's icon, so the two surfaces cannot
     * come to look like two different features.
     */
    kind?:
      | 'reference'
      | 'overview'
      | 'copy'
      | 'reveal'
      | 'hide'
      | 'edit'
      | 'logs'
      | 'terminal'
      | 'shell'
      | 'restart'
      | 'scale'
      | 'trigger'
      | 'suspend'
      | 'resume'
      | 'cordon'
      | 'evict'
      | 'drain'
      | 'delete'
    /**
     * Whether this item removes something, or takes something out of
     * service.
     *
     * NOT A COLOUR ANY MORE. Delete, Evict and Drain were drawn in the error
     * token; they are drawn like every other item now. Red on the two or
     * three most dangerous rows of a menu made the whole menu read as a
     * warning instead of as a list of what a row offers, and it separated
     * nothing for anybody who cannot see it — a colour is not in the
     * accessibility tree, carries no ARIA and is announced by nothing. What
     * DOES separate them is the item's own name and icon, and those are read
     * out: "Delete" with a bin, "Evict" with a door, "Drain…" with the same
     * door and an ellipsis promising a dialog. That is unchanged.
     *
     * The flag itself stays, as a fact rather than a hook: it is published as
     * `data-destructive` on the item so a test can name these three without
     * matching on their labels.
     */
    destructive?: boolean
    /**
     * Whether this item does NOT act on the cluster.
     *
     * WHERE THE SEPARATOR GOES, decided once here rather than by each view
     * placing a divider. See the rule at the render below: a rule is drawn
     * wherever two consecutive items disagree about this, so nothing has to
     * remember that "Copy as kubectl" is last and nothing can put a second
     * line in by hand. A caller that sets it on no item (the detail pane's
     * menus) gets no separator, which is right — those menus are all reads.
     */
    local?: boolean
    /**
     * Whether the item is refused, and why — the read-only cluster case.
     *
     * DISABLED, NEVER ABSENT, which is CLAUDE.md's rule for the whole
     * read-only surface: a disabled control with its reason is a feature
     * somebody can find and understand, while a missing one reads as a
     * feature that does not exist. The reason belongs in `hint`; a disabled
     * item shows no tooltip of its own, so it is carried on the row wrapper.
     */
    disabled?: boolean
    /** The item's tooltip — the refusal's reason when disabled. */
    hint?: string
    /**
     * What pressing it does.
     *
     * A COPY MUST REPORT WHETHER IT WORKED, which is why this may return a
     * promise of a boolean rather than nothing. The menu used to flash
     * "Copied!" the instant the handler returned, so the confirmation stood
     * for the handler having been CALLED and not for any text reaching the
     * clipboard — and in the shipped webview `navigator.clipboard` is
     * undefined (no secure context), so the copy silently did nothing while
     * the menu said it had. An operator who believed it pasted whatever was
     * on their clipboard before. Every `kind: 'copy'` handler therefore
     * returns `$lib/clipboard`'s answer, and the menu says "Copied!" only for
     * `true` and "Copy failed" for `false`.
     *
     * Everything else returns nothing: those items open the drawer, whose own
     * appearance is the confirmation.
     */
    onclick: () => void | Promise<boolean>
  }

  const ICONS: Record<string, Component<{ class?: string; strokeWidth?: number }>> = {
    reference: Link2,
    // The drawer's own Overview tab icon, so the item and the tab it opens
    // are visibly the same thing rather than two features that happen to
    // agree — the same rule every other verb here follows.
    overview: Info,
    copy: Copy,
    reveal: Eye,
    hide: EyeOff,
    edit: Pencil,
    logs: ScrollText,
    terminal: TerminalSquare,
    shell: SquareTerminal,
    restart: RotateCcw,
    scale: Scale,
    trigger: Play,
    suspend: CirclePause,
    resume: CirclePlay,
    cordon: Ban,
    evict: LogOut,
    drain: LogOut,
    delete: Trash2,
  }

  interface Props {
    /** What this row offers. An empty list renders no control at all. */
    actions: RowAction[]
    /** Names the row, for the control's accessible label. */
    label: string
    /**
     * Whether the control shows without the row being hovered.
     *
     * TRUE IN A TABLE, where it is a column of its own: a column whose
     * contents appear only under the pointer reads as an empty column, and
     * somebody looking for the control cannot find it without sweeping the
     * table. FALSE in a detail pane, where the control sits beside a value
     * rather than in a column, and one dot per row would be noise on every
     * row of every pane.
     */
    persistent?: boolean
  }

  let { actions, label, persistent = false }: Props = $props()

  /**
   * Where the menu goes, in WINDOW coordinates.
   *
   * IT HAS TO ESCAPE THE TABLE, which is why this is measured rather than
   * left to `position: absolute`. A list row sits inside the table's
   * horizontal scrollport, and a scrollport clips: anchored to the cell, the
   * menu was cut off at the table's right edge — the one place it always
   * opens, since its column is pinned there. No z-index helps, because
   * clipping is not stacking.
   *
   * Right-aligned to the control, because it opens at the right edge of a row
   * and a menu growing rightwards from there would leave the window. Flipped
   * above only when below would not fit, and clamped so neither end can land
   * off screen.
   */
  const MENU_WIDTH = 192
  const MENU_GAP = 4
  const MENU_MARGIN = 8

  /**
   * Moves the menu out of the table and onto the body.
   *
   * POSITION: FIXED WAS NOT ENOUGH, and the reason is worth stating because
   * the two failures look identical from a screenshot. Fixed positioning
   * escaped the table's scrollport, which was the CLIPPING. It did nothing
   * about the STACKING: a pinned column cell is `position: sticky` with a
   * z-index, which makes it a stacking context, so the menu's own z-index is
   * only ever weighed against its siblings inside that one cell. Every row
   * below has a sticky cell at the same level and comes later in the
   * document, so all of them paint over this menu whatever number it carries.
   *
   * Nothing about z-index can fix that from inside the cell — the menu has to
   * leave it. On the body it has no ancestor that positions it and no
   * stacking context above it, so its z-index means what it says.
   */
  function portal(node: HTMLElement): { destroy: () => void } {
    document.body.appendChild(node)
    return {
      destroy: () => node.remove(),
    }
  }

  /**
   * The portalled menu, held because it is no longer a descendant of `node`.
   *
   * The outside-click check asks whether the pointer landed inside THIS menu,
   * and moving the element to the body made every click on one of its own
   * items land "outside" — closing the menu before the item could run. Both
   * elements have to be asked now.
   */
  let menu = $state<HTMLElement | null>(null)

  let trigger = $state<HTMLButtonElement | null>(null)
  let menuHeight = $state(0)
  /** Bumped while open so a scroll or a resize re-measures. */
  let moved = $state(0)

  const placement = $derived.by(() => {
    void moved
    if (!open || !trigger) return null

    const rect = trigger.getBoundingClientRect()
    const below = rect.bottom + MENU_GAP
    const fitsBelow = below + menuHeight <= window.innerHeight - MENU_MARGIN
    const top = fitsBelow ? below : Math.max(MENU_MARGIN, rect.top - MENU_GAP - menuHeight)

    const left = Math.min(
      Math.max(MENU_MARGIN, rect.right - MENU_WIDTH),
      Math.max(MENU_MARGIN, window.innerWidth - MENU_WIDTH - MENU_MARGIN),
    )

    return { top, left }
  })

  /**
   * A menu positioned against the window does not travel with the row it
   * belongs to, so a scroll would leave it behind. Re-measuring is cheaper
   * than the alternative and only ever runs while one menu is open.
   */
  $effect(() => {
    if (!open) return

    const remeasure = (): void => {
      moved += 1
    }
    window.addEventListener('scroll', remeasure, true)
    window.addEventListener('resize', remeasure)
    return () => {
      window.removeEventListener('scroll', remeasure, true)
      window.removeEventListener('resize', remeasure)
    }
  })

  /** This menu's identity, for the one open at a time. */
  const id = Symbol('row-menu')
  const open = $derived(openMenu === id)

  let node = $state<HTMLElement | null>(null)
  const copied = flash(900)
  /**
   * Held longer than the confirmation, because it has to be READ.
   *
   * "Copied!" only confirms what somebody already expected, so it can go by
   * in under a second; "Copy failed" is news, and news that arrives and
   * leaves before it is read is the same as a menu that lied. It is also the
   * one outcome where doing nothing is not the right next step — the operator
   * has to select the text by hand instead — so the menu stays put long
   * enough to say so.
   */
  const copyFailed = flash(2400)

  /**
   * Runs an item, and closes the menu when its result is known.
   *
   * A COPY IS AWAITED AND EVERYTHING ELSE IS NOT, and that asymmetry is the
   * whole fix. `copied.show()` used to run unconditionally after the handler
   * returned, so the confirmation was evidence that a function had been
   * called and nothing more. A copy goes through the Go process or the DOM
   * (see $lib/clipboard) and both are asynchronous, so the only honest place
   * to decide what to say is after the answer: `true` confirms, `false` says
   * it failed, and neither is guessed.
   *
   * The other items are not awaited because there is nothing to wait for:
   * each sets a request on the session and opens the drawer, whose appearing
   * IS the feedback. Awaiting them would hold the menu open over the panel
   * that just opened underneath it.
   */
  async function choose(action: RowAction): Promise<void> {
    if (action.disabled) return
    const result = action.onclick()

    if (action.kind !== 'copy') {
      openMenu = null
      return
    }

    // A handler that returned nothing has not been converted to report its
    // outcome, and the menu must not invent one on its behalf: silence is
    // read as failure, which is the safe direction. `=== true` rather than a
    // truthiness test says that in one character.
    const copiedOk = (await result) === true

    // Closing is deferred to the flash's own expiry so the message is on
    // screen for its full span — the menu closing early would take the
    // sentence with it, which is what a bare setTimeout used to do here when
    // a second click restarted the clock. See $lib/flash.
    const close = (): void => {
      if (openMenu === id) openMenu = null
    }
    if (copiedOk) copied.show(close)
    else copyFailed.show(close)
  }

  function onWindowPointerDown(event: PointerEvent): void {
    if (!open) return
    // THIS menu's element, not any row menu. Asking whether the pointer
    // landed in a row menu was what let a click on another row's control
    // leave this one open.
    const target = event.target as Node
    // BOTH, because the menu is portalled onto the body and is therefore not
    // inside `node` any more. Asking only the wrapper closed the menu on the
    // pointerdown of a click aimed at one of its own items.
    if (!node?.contains(target) && !menu?.contains(target)) openMenu = null
  }

  function onKeydown(event: KeyboardEvent): void {
    if (event.key !== 'Escape' || !open) return
    // ONE ESCAPE, ONE LAYER. A menu open inside the detail drawer used to see
    // one keystroke close the menu AND the drawer — and the drawer's Escape
    // discards an unsaved YAML draft, so a keystroke aimed at a menu could
    // throw somebody's work away. stopPropagation cannot help: every one of
    // these listeners is on the window, so nothing propagates between them.
    if (!escape?.owns()) return
    openMenu = null
  }

  /**
   * Window listeners, ONLY WHILE THIS MENU IS OPEN.
   *
   * They were attached unconditionally, and there is one of these per detail
   * ROW: a sixty-row pod pane installed a hundred and twenty window
   * listeners, and every keystroke anywhere in the application was dispatched
   * to sixty handlers that immediately returned. Only one row menu can be
   * open at a time, so at most two of these ever exist now.
   *
   * Attached from an effect rather than a conditional `<svelte:window>`,
   * which Svelte does not allow inside a block.
   */
  $effect(() => {
    if (!open) return

    window.addEventListener('pointerdown', onWindowPointerDown)
    window.addEventListener('keydown', onKeydown)
    return () => {
      window.removeEventListener('pointerdown', onWindowPointerDown)
      window.removeEventListener('keydown', onKeydown)
    }
  })

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

  // Nothing left running behind a component that has gone away.
  $effect(() => () => {
    copied.cancel()
    copyFailed.cancel()
  })
</script>



{#if actions.length > 0}
  <div class="relative" data-row-menu bind:this={node}>
    <!--
      TWO WAYS IN TO THE SAME "row/row" GROUP. `group-data-[row-hover]/row` is
      for a caller like DetailList, where the row is two grid siblings (a dt
      and a dd) with no element wrapping both — hover has to be tracked in
      script and published as a `data-row-hover` attribute, because CSS
      `:hover` has nothing to attach to that covers the whole row. A table
      `<tr>` has no such problem: it is one element, so `group-hover/row`
      answers the same question for free. Both are on the button so either
      caller works without this component needing to know which kind of row
      it is in.
    -->
    <!--
      PERSISTENT DRAWS ITSELF WITH COLOUR, NOT OPACITY. Hidden entirely until
      the row is hovered, the control is unfindable in a column of its own —
      the column reads as empty and somebody has to sweep the table to
      discover there was anything in it. So in a table it is always there, dim
      at rest and brighter under the pointer, which says "you can press this"
      without competing with the row's own text. In a detail pane it keeps the
      old behaviour, because there it sits beside a value rather than in a
      column and one dot per row would be noise.
    -->
    <button
      bind:this={trigger}
      type="button"
      onclick={() => (openMenu = open ? null : id)}
      aria-expanded={open}
      aria-label="More for {label}"
      title="More"
      class="grid size-5 shrink-0 cursor-pointer place-items-center rounded-full
             transition-all duration-100 hover:text-on-surface
             group-data-[row-hover]/row:opacity-100 group-hover/row:opacity-100
             group-focus-within/row:opacity-100 focus-visible:opacity-100
             {persistent ? 'opacity-100' : ''}
             {persistent && !open ? 'text-on-surface-variant/45 group-hover/row:text-on-surface-variant' : ''}
             {open ? 'text-on-surface opacity-100' : persistent ? '' : 'text-on-surface-variant/60 opacity-0'}"
    >
      <MoreVertical class="size-3.5" strokeWidth={2} />
    </button>

    {#if open}
      <!-- Anchored to the right, because the control sits at the right edge
           of a panel and a menu opening leftward from it stays inside. -->
      <!-- The same menu the status bar's "Share on…" opens: same width, same
           ground, same item metrics and the same muted leading icon. Two
           dropdowns in one application should not be two designs. -->
      <div
        use:portal
        bind:this={menu}
        bind:clientHeight={menuHeight}
        style={placement ? `top: ${placement.top}px; left: ${placement.left}px;` : 'visibility: hidden;'}
        class="fixed z-50 w-48 overflow-hidden rounded-sm
               border border-outline-variant/60 bg-surface-container-high py-1.5 shadow-level-2"
        role="menu"
        aria-label="More for {label}"
        use:menuKeys={{ onclose: () => (openMenu = null) }}
      >
        {#each actions as action, index (action.label)}
          {@const Icon = ICONS[action.kind ?? 'reference']}
          <!--
            ONE RULE FOR THE LINE: it goes wherever the menu crosses from
            items that act on the cluster to items that do not, which is
            `RowAction.local` changing between two neighbours. Nothing here
            knows which item that is or that it comes last, and no view
            places a divider of its own — so a menu whose items vary (a
            node's, a suspended CronJob's, a read-only cluster's) cannot end
            up with the line in the wrong place, and a second local item
            added later joins the same group rather than growing a second
            rule.

            `!!` on both sides because `local` is optional: the detail pane's
            menus set it on nothing, and without the coercion an unset item
            beside an explicit `false` would differ and draw a line through a
            menu that has no such division.
          -->
          {#if index > 0 && !!action.local !== !!actions[index - 1].local}
            <div class="my-1.5 border-t border-outline-variant/50" role="separator"></div>
          {/if}
          <!-- The title sits on a wrapper, not the button: a disabled
               control shows no tooltip of its own, and the refusal's reason
               is the one thing somebody looking at a greyed-out Delete
               needs. The same wrapper the bulk bar uses for its own. -->
          <span title={action.hint} class="block">
            <!--
              `data-destructive` rather than a colour. Delete, Evict and Drain
              are no longer drawn in the error token (see RowAction above for
              why), and this is what keeps the fact addressable: a test names
              those three without matching on their labels, and nothing about
              it reaches the screen.
            -->
            <button
              type="button"
              role="menuitem"
              data-destructive={action.destructive ? '' : undefined}
              disabled={action.disabled}
              onclick={() => void choose(action)}
              class="state-layer flex w-full items-center gap-2.5 px-3 py-1.5
                     text-left text-body-medium transition-colors duration-75
                     cursor-pointer hover:bg-surface-container-highest
                     disabled:pointer-events-none disabled:opacity-38
                     {action.kind !== 'copy'
                ? 'text-on-surface'
                : copied.on
                  ? 'text-success'
                  : copyFailed.on
                    ? 'text-error'
                    : 'text-on-surface'}"
            >
              {#if action.kind === 'copy' && copied.on}
                <Check class="size-3.5 shrink-0" strokeWidth={2.5} />
                Copied!
              {:else if action.kind === 'copy' && copyFailed.on}
                <!-- Says what happened in the item's own place rather than
                     raising a banner somewhere else: the operator is looking
                     here, and the next thing they have to do — select the
                     command by hand — is here too. -->
                <TriangleAlert class="size-3.5 shrink-0" strokeWidth={2.5} />
                Copy failed
              {:else}
                <Icon class="size-3.5 shrink-0 text-on-surface-variant/70" />
                {action.label}
              {/if}
            </button>
          </span>
        {/each}
      </div>
    {/if}
  </div>
{/if}
