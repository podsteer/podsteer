<!--
  The command palette: global search across kinds, objects and open
  clusters, plus every action the toolbar and the tab bar already offer.

  Opened by ⌘⇧P / Ctrl+Shift+P and ⌘P — registered in $lib/shortcuts, listed
  in ShortcutSheet.svelte the same way every other combo is. ⌘K is left
  alone: it keeps meaning "focus the table search", which SearchField.svelte
  already owns, so this palette does not steal a muscle-memory shortcut for
  a DIFFERENT search box.

  All the ranking, grouping and on-demand kind search live in
  $stores/palette — this component is wiring: it keeps that store pointed at
  the live workspace (`sync`, below), renders whatever `palette.groups`
  says, and turns a keystroke into a call on the store. See that module's
  own doc comment for the "search memory first, one request per scoped
  kind" discipline this whole feature exists to honour.
-->
<script lang="ts">
  import { escapeLayer, type EscapeClaim } from '$lib/escape'
  import { modal } from '$lib/modal'
  import { categoryMeta, iconForKind, type LucideIcon } from '$lib/kindIcons'
  import { palette, type PaletteEntry, type PaletteGroupName } from '$stores/palette.svelte'
  import { workspace } from '$stores/workspace.svelte'
  import { APPLICATIONS_KIND_ID, OVERVIEW_KIND_ID } from '$stores/session.svelte'
  import {
    Blocks,
    Command,
    FolderOpen,
    History,
    LayoutDashboard,
    Search,
    Server,
    X,
  } from '@lucide/svelte'

  interface Props {
    open: boolean
    onclose: () => void
  }

  let { open, onclose }: Props = $props()

  let inputEl = $state<HTMLInputElement | null>(null)
  let listEl = $state<HTMLDivElement | null>(null)

  /**
   * Keeps $stores/palette pointed at the live workspace.
   *
   * Runs for as long as this component is mounted, not only at `open` —
   * $lib/shortcuts opens the palette from ClusterTabs and the picker alike,
   * so the tab it should act on can change while it sits closed, and a tab
   * can also be switched or closed WHILE the palette is open, which must
   * not leave a command pointed at a session that just went away.
   */
  $effect(() => {
    palette.sync(
      workspace.active ?? null,
      workspace.sessions.map((session) => ({ id: session.cluster.id })),
      workspace.focus,
    )
  })

  $effect(() => {
    if (!open) return
    palette.show()
    // Focus follows the dialog opening, same as every other dialog here —
    // `use:modal` below does the actual focus MANAGEMENT (trap, restore);
    // this just makes sure the field itself, not the dialog shell, is what
    // receives the first keystroke.
    inputEl?.focus()
  })

  /** Escape belongs to the innermost open layer. See $lib/escape. */
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

  function onWindowKeydown(event: KeyboardEvent): void {
    if (event.key !== 'Escape' || !open) return
    if (!escape?.owns()) return
    onclose()
  }

  /** Rebuilds the full query string with `newText` as the free-text tail,
      keeping whatever pills are already accepted. The store re-parses the
      result, so typing a NEW `kind:`/`ns:`/`cluster:` token by hand into
      this free-text box is absorbed into its own chip the moment it is
      recognised — see removePill below for the reverse. */
  function rebuildQuery(newText: string): string {
    const parsed = palette.parsed
    const parts: string[] = []
    if (parsed.commandsOnly) parts.push('>')
    if (parsed.kind !== undefined) parts.push(`kind:${parsed.kind}`)
    if (parsed.namespace !== undefined) parts.push(`ns:${parsed.namespace}`)
    if (parsed.cluster !== undefined) parts.push(`cluster:${parsed.cluster}`)
    if (newText) parts.push(newText)
    return parts.join(' ')
  }

  function onInput(event: Event & { currentTarget: HTMLInputElement }): void {
    palette.setQuery(rebuildQuery(event.currentTarget.value))
  }

  /** Drops one pill, keeping the others and the free text. */
  function removePill(which: 'kind' | 'namespace' | 'cluster'): void {
    const parsed = palette.parsed
    const parts: string[] = []
    if (parsed.commandsOnly) parts.push('>')
    if (which !== 'kind' && parsed.kind !== undefined) parts.push(`kind:${parsed.kind}`)
    if (which !== 'namespace' && parsed.namespace !== undefined) parts.push(`ns:${parsed.namespace}`)
    if (which !== 'cluster' && parsed.cluster !== undefined) parts.push(`cluster:${parsed.cluster}`)
    if (parsed.text) parts.push(parsed.text)
    palette.setQuery(parts.join(' '))
    inputEl?.focus()
  }

  const hasAnyPill = $derived(
    palette.parsed.kind !== undefined ||
      palette.parsed.namespace !== undefined ||
      palette.parsed.cluster !== undefined,
  )

  /** Backspace at the very start of the field removes the most recently
      typed pill instead of doing nothing — the same "chip swallows the
      cursor" convention a tag input uses, so an operator does not have to
      hunt for the small × on each chip just to change their mind. */
  function removeLastPill(): void {
    const parsed = palette.parsed
    if (parsed.cluster !== undefined) removePill('cluster')
    else if (parsed.namespace !== undefined) removePill('namespace')
    else if (parsed.kind !== undefined) removePill('kind')
  }

  function onInputKeydown(event: KeyboardEvent): void {
    switch (event.key) {
      case 'ArrowDown':
        event.preventDefault()
        palette.moveSelection(1)
        scrollActiveIntoView()
        break
      case 'ArrowUp':
        event.preventDefault()
        palette.moveSelection(-1)
        scrollActiveIntoView()
        break
      case 'Enter':
        event.preventDefault()
        palette.runSelected()
        break
      case 'Tab':
        // Nothing else in this dialog is worth tabbing to — the focus trap
        // keeps focus on the field regardless — so Tab is free to mean
        // "accept the top kind suggestion" instead, same as it does nowhere
        // else in the application but reads naturally here.
        if (palette.parsed.kind === undefined) {
          event.preventDefault()
          palette.acceptKindPill()
        }
        break
      case 'Backspace':
        if (
          inputEl &&
          inputEl.selectionStart === 0 &&
          inputEl.selectionEnd === 0 &&
          hasAnyPill
        ) {
          event.preventDefault()
          removeLastPill()
        }
        break
    }
  }

  function scrollActiveIntoView(): void {
    listEl?.querySelector<HTMLElement>('[data-active="true"]')?.scrollIntoView({ block: 'nearest' })
  }

  /** A stable id per rendered row, for aria-activedescendant — see
      Select.svelte for the same pattern over a listbox. */
  const instance = $props.id()
  function entryId(entry: PaletteEntry): string {
    return `${instance}-${entry.id}`
  }

  /** The group headings, in the same voice Navigator's own section labels
      use (see kindIcons.ts's CATEGORY_META). */
  const GROUP_META: Record<PaletteGroupName, { label: string; icon: LucideIcon }> = {
    Commands: { label: 'Commands', icon: Command },
    Kinds: { label: 'Go to', icon: LayoutDashboard },
    Objects: { label: 'Objects', icon: Search },
    Recents: { label: 'Recent', icon: History },
    Clusters: { label: 'Clusters', icon: Server },
    Namespaces: { label: 'Namespaces', icon: FolderOpen },
  }

  /** The navigator's own glyph for a kind id, so a Kinds/Objects/Recents row
      reads exactly the way the sidebar already does — see kindIcons.ts's
      own comment on why the two must never drift apart. */
  function glyphFor(entry: PaletteEntry): LucideIcon {
    if (!entry.kindId) return GROUP_META[entry.group].icon
    if (entry.kindId === OVERVIEW_KIND_ID) return LayoutDashboard
    if (entry.kindId === APPLICATIONS_KIND_ID) return Blocks
    const kind = workspace.active?.kinds.find((candidate) => candidate.id === entry.kindId)
    return kind ? iconForKind(kind) : categoryMeta('').icon
  }

  /** The kind: pill's display label — the catalogue's own title when the
      typed or accepted id resolves to one, otherwise whatever was typed, so
      a pill mid-edit still shows something rather than going blank. */
  const kindPillLabel = $derived.by((): string | undefined => {
    const id = palette.parsed.kind
    if (id === undefined) return undefined
    return workspace.active?.kinds.find((kind) => kind.id === id)?.title ?? id
  })
</script>

<svelte:window onkeydown={onWindowKeydown} />

{#if open}
  <button
    type="button"
    aria-label="Close the command palette"
    tabindex="-1"
    class="fixed inset-0 z-40 cursor-default bg-scrim/40"
    onclick={onclose}
  ></button>

  <!-- Anchored a third of the way down rather than dead centre — a command
       palette is read top-down like a menu, and centring it vertically
       leaves as much dead space below the last row as there is chrome
       above the field, which is not where the eye expects either. -->
  <div
    class="fixed top-[16vh] right-0 left-0 z-50 mx-auto flex max-h-[68vh] w-[38rem]
           max-w-[92vw] flex-col overflow-hidden rounded-sm border border-outline-variant
           bg-surface-container-high shadow-level-3"
    role="dialog"
    aria-modal="true"
    aria-label="Command palette"
    use:modal
  >
    <!-- Pills first, then the field — a chip for each accepted `kind:`,
         `ns:` or `cluster:` pill, with its own × plus the whole-field
         Backspace shortcut above. -->
    <div class="flex flex-wrap items-center gap-1.5 border-b border-outline-variant/60 px-4 py-3">
      <Search class="size-4 shrink-0 text-on-surface-variant/60" strokeWidth={2} />

      {#if palette.parsed.commandsOnly}
        <span
          class="flex items-center gap-1 rounded-full bg-primary/12 px-2 py-0.5
                 text-label-medium font-medium text-primary"
        >
          <Command class="size-3" strokeWidth={2.2} />
          Commands only
        </span>
      {/if}

      {#if kindPillLabel !== undefined}
        <span
          class="flex items-center gap-1 rounded-full bg-surface-container px-2 py-0.5
                 text-label-medium text-on-surface"
        >
          kind: {kindPillLabel}
          <button
            type="button"
            onclick={() => removePill('kind')}
            aria-label="Remove kind filter"
            class="state-layer grid size-3.5 place-items-center rounded-full text-on-surface-variant/70
                   hover:text-on-surface"
          >
            <X class="size-2.5" strokeWidth={2.5} />
          </button>
        </span>
      {/if}

      {#if palette.parsed.namespace !== undefined}
        <span
          class="flex items-center gap-1 rounded-full bg-surface-container px-2 py-0.5
                 text-label-medium text-on-surface"
        >
          ns: {palette.parsed.namespace}
          <button
            type="button"
            onclick={() => removePill('namespace')}
            aria-label="Remove namespace filter"
            class="state-layer grid size-3.5 place-items-center rounded-full text-on-surface-variant/70
                   hover:text-on-surface"
          >
            <X class="size-2.5" strokeWidth={2.5} />
          </button>
        </span>
      {/if}

      {#if palette.parsed.cluster !== undefined}
        <span
          class="flex items-center gap-1 rounded-full bg-surface-container px-2 py-0.5
                 text-label-medium text-on-surface"
        >
          cluster: {palette.parsed.cluster}
          <button
            type="button"
            onclick={() => removePill('cluster')}
            aria-label="Remove cluster filter"
            class="state-layer grid size-3.5 place-items-center rounded-full text-on-surface-variant/70
                   hover:text-on-surface"
          >
            <X class="size-2.5" strokeWidth={2.5} />
          </button>
        </span>
      {/if}

      <!--
        A combobox, not a listbox: focus STAYS here the whole time and the
        arrow keys move `aria-activedescendant` instead of moving DOM focus
        row to row — the same pattern VS Code's own Quick Open uses, and
        what lets Backspace-to-remove-a-pill above work at all.
      -->
      <input
        bind:this={inputEl}
        type="text"
        autocomplete="off"
        autocorrect="off"
        autocapitalize="off"
        spellcheck="false"
        role="combobox"
        aria-expanded="true"
        aria-controls="{instance}-listbox"
        aria-activedescendant={palette.flatEntries[palette.selectedIndex]
          ? entryId(palette.flatEntries[palette.selectedIndex])
          : undefined}
        value={palette.parsed.text}
        placeholder={hasAnyPill || palette.parsed.commandsOnly
          ? 'Search…'
          : 'Search kinds, objects, clusters… try "kind:pods" or ">"'}
        aria-label="Command palette search"
        oninput={onInput}
        onkeydown={onInputKeydown}
        class="min-w-32 flex-1 border-none bg-transparent px-1 py-0.5 text-body-large
               text-on-surface outline-none placeholder:text-on-surface-variant/50"
      />
    </div>

    <!-- Results -->
    <div
      bind:this={listEl}
      id="{instance}-listbox"
      role="listbox"
      aria-label="Command palette results"
      class="min-h-0 flex-1 overflow-y-auto py-1.5"
    >
      {#if palette.groups.length === 0}
        <p class="px-4 py-10 text-center text-body-medium text-on-surface-variant/60">
          Nothing found for “{palette.query}”.
        </p>
      {/if}

      {#each palette.groups as group (group.name)}
        <div class="px-2 py-1">
          <p
            class="px-2 py-1 text-body-small font-semibold tracking-wider text-on-surface-variant/70 uppercase"
          >
            {GROUP_META[group.name].label}
          </p>
          <ul>
            {#each group.entries as entry (entry.id)}
              {@const active = palette.flatEntries[palette.selectedIndex]?.id === entry.id}
              {@const Glyph = glyphFor(entry)}
              <li>
                <button
                  type="button"
                  id={entryId(entry)}
                  role="option"
                  aria-selected={active}
                  data-active={active}
                  onmouseenter={() => {
                    const index = palette.flatEntries.findIndex((candidate) => candidate.id === entry.id)
                    if (index >= 0) palette.selectedIndex = index
                  }}
                  onclick={palette.runSelected}
                  class="flex w-full items-center gap-2.5 rounded-sm px-2.5 py-2 text-left
                         transition-colors duration-75
                         {active
                    ? 'bg-primary/12 text-primary'
                    : 'text-on-surface hover:bg-surface-container'}"
                >
                  <Glyph
                    class="size-4 shrink-0 {active ? 'text-primary' : 'text-on-surface-variant/60'}"
                    strokeWidth={1.8}
                  />
                  <span class="min-w-0 flex-1 truncate text-body-medium">{entry.title}</span>
                  {#if entry.detail}
                    <span class="shrink-0 truncate text-label-small text-on-surface-variant/50">
                      {entry.detail}
                    </span>
                  {/if}
                </button>
              </li>
            {/each}
          </ul>
        </div>
      {/each}
    </div>

    <!-- Footer: the key hints, matching ShortcutSheet's own <kbd> styling. -->
    <div
      class="flex shrink-0 items-center gap-3 border-t border-outline-variant/60 px-4 py-2
             text-label-small text-on-surface-variant/70"
    >
      <span class="flex items-center gap-1">
        <kbd class="rounded-xs border border-outline-variant bg-surface-container px-1 py-px font-mono">↑↓</kbd>
        navigate
      </span>
      <span class="flex items-center gap-1">
        <kbd class="rounded-xs border border-outline-variant bg-surface-container px-1 py-px font-mono">↵</kbd>
        select
      </span>
      <span class="flex items-center gap-1">
        <kbd class="rounded-xs border border-outline-variant bg-surface-container px-1 py-px font-mono">Tab</kbd>
        scope to kind
      </span>
      <span class="flex items-center gap-1">
        <kbd class="rounded-xs border border-outline-variant bg-surface-container px-1 py-px font-mono">&gt;</kbd>
        commands only
      </span>
      <span class="ml-auto flex items-center gap-1">
        <kbd class="rounded-xs border border-outline-variant bg-surface-container px-1 py-px font-mono">esc</kbd>
        close
      </span>
    </div>
  </div>
{/if}
