<!--
  Projects and groups.

  The whole lifecycle of both levels lives in one place: creating, renaming,
  reordering, moving groups between projects, and deleting. Nothing here ever
  strands a cluster — deleting a group drops its members into that project's
  Default, and deleting a project drops them into the Default project — which
  is why the two Defaults have no actions of their own.

  Every row carries ONE control, an overflow menu, rather than a row of icon
  buttons. Four tiny glyphs at the end of a line were both noisy at two levels
  of nesting and easy to miss entirely: the first version of this dialog looked
  like it offered nothing at all, because the only rows with actions were the
  ones the operator had not created yet.

  Dragging a row is the fast path for the same operations. A project reorders
  among projects; a group reorders among its own project's groups, or moves to
  another project by being dropped on that project's row. Everything a drag can
  do, the menu can also do — a drag is unusable from a keyboard and invisible
  to a screen reader, so nothing may be reachable by drag alone.

  Renaming edits in place: the row becomes the field. Deleting is a two-step
  inline confirm rather than a native confirm(), which the webview cannot be
  relied on to show.
-->
<script lang="ts">
  import { escapeLayer, type EscapeClaim } from '$lib/escape'
  import { modal } from '$lib/modal'
  import Button from './Button.svelte'
  import { DEFAULT_PROJECT_ID, organisation } from '$stores/organisation.svelte'
  import { workspace } from '$stores/workspace.svelte'
  import {
    ChevronUp,
    ChevronDown,
    EllipsisVertical,
    FolderInput,
    GripVertical,
    Pencil,
    Plus,
    Trash2,
  } from '@lucide/svelte'

  interface Props {
    open: boolean
    onclose: () => void
  }

  let { open, onclose }: Props = $props()

  type Kind = 'project' | 'group'
  interface Row {
    kind: Kind
    id: string
    /** The owning project, for a group row. */
    projectId?: string
  }

  let newProjectName = $state('')
  let newProjectError = $state<string | null>(null)

  /** The project a new group is being typed into, if any. */
  let addingGroupIn = $state<string | null>(null)
  let newGroupName = $state('')
  let newGroupError = $state<string | null>(null)

  /** Whose overflow menu is open, if any. */
  let menuFor = $state<Row | null>(null)
  /**
   * Where that menu should appear, in viewport coordinates.
   *
   * The menus are positioned `fixed` against the button that opened them
   * rather than absolutely inside the row. The row sits in the scrolling list,
   * and an absolutely positioned child cannot escape a scroll container — so
   * a menu on the last visible row was clipped in half by the very element
   * that makes the list scrollable.
   */
  let menuAnchor = $state<DOMRect | null>(null)
  /** Whose "move to project" list is showing inside that menu. */
  let movingGroup = $state<string | null>(null)

  /** What is being renamed, plus the working copy of its name. */
  let renaming = $state<Row | null>(null)
  let renameValue = $state('')
  let renameError = $state<string | null>(null)

  /** What is one click away from deletion, if anything. */
  let confirmDelete = $state<Row | null>(null)

  /** The row being dragged, and the row the pointer is over. */
  let dragging = $state<Row | null>(null)
  let dropOn = $state<Row | null>(null)

  // Deriving from both stores keeps the counts honest while someone
  // reorganises with the dialog open.
  const tree = $derived(
    organisation.allProjects().map((project) => ({
      ...project,
      count: organisation.countIn(workspace.clusters, project.id),
      groups: organisation.groupsIn(project.id).map((group) => ({
        ...group,
        count: organisation.countIn(workspace.clusters, project.id, group.id),
      })),
    })),
  )

  function addProject(): void {
    const problem = organisation.createProject(newProjectName)
    newProjectError = problem
    if (!problem) newProjectName = ''
  }

  function addGroup(projectId: string): void {
    const problem = organisation.createGroup(newGroupName, projectId)
    newGroupError = problem
    if (!problem) {
      newGroupName = ''
      addingGroupIn = null
    }
  }

  function startRename(row: Row, name: string): void {
    renaming = row
    renameValue = name
    renameError = null
    closeMenu()
  }

  function commitRename(): void {
    if (!renaming) return
    const problem =
      renaming.kind === 'project'
        ? organisation.renameProject(renaming.id, renameValue)
        : organisation.renameGroup(renaming.id, renameValue, renaming.projectId ?? '')

    renameError = problem
    if (!problem) renaming = null
  }

  function cancelRename(): void {
    renaming = null
    renameError = null
  }

  function onRenameKeydown(event: KeyboardEvent): void {
    if (event.key === 'Enter') commitRename()
    if (event.key === 'Escape') cancelRename()
  }

  function remove(row: Row): void {
    if (row.kind === 'project') organisation.removeProject(row.id)
    else organisation.removeGroup(row.id)
    confirmDelete = null
  }

  function openMenu(row: Row, event: MouseEvent, alreadyOpen: boolean): void {
    if (alreadyOpen) {
      closeMenu()
      return
    }
    menuAnchor = (event.currentTarget as HTMLElement).getBoundingClientRect()
    menuFor = row
    movingGroup = null
  }

  function closeMenu(): void {
    menuFor = null
    menuAnchor = null
    movingGroup = null
  }

  /**
   * Places a menu under its button, then pulls it back inside the viewport.
   *
   * Measured after render rather than predicted: the group menu changes height
   * when it switches to the project list, and guessing from an item count
   * would be wrong the moment an item is added. `deps` exists only so the
   * action re-runs when that content changes.
   */
  function anchorMenu(node: HTMLElement, deps: { rect: DOMRect | null; key: unknown }): {
    update: (next: { rect: DOMRect | null; key: unknown }) => void
  } {
    const place = ({ rect }: { rect: DOMRect | null }): void => {
      if (!rect) return
      const margin = 8
      const { width, height } = node.getBoundingClientRect()

      // Right-aligned to the button, because the button sits at the right end
      // of its row and a left-aligned menu would hang off the dialog.
      const left = Math.max(margin, Math.min(rect.right - width, window.innerWidth - width - margin))

      // Below by preference; above when that would overflow; clamped when
      // neither fits, which is the case on a short window.
      let top = rect.bottom + 4
      if (top + height > window.innerHeight - margin) {
        const above = rect.top - height - 4
        top = above >= margin ? above : Math.max(margin, window.innerHeight - height - margin)
      }

      node.style.left = `${left}px`
      node.style.top = `${top}px`
    }

    place(deps)
    return { update: place }
  }

  /**
   * Whether `a` names this exact row.
   *
   * The project has to be compared as well as the id. Every project's default
   * group is `DEFAULT_GROUP_ID`, so matching on the id alone opened all three
   * default menus at once — and would have put the rename field on all three
   * rows too. Real groups have unique ids, so this only ever matters for the
   * defaults, which is exactly why it was easy to miss.
   */
  function isRow(a: Row | null, kind: Kind, id: string, projectId?: string): boolean {
    if (a?.kind !== kind || a.id !== id) return false
    return projectId === undefined || a.projectId === projectId
  }

  // --- Dragging -------------------------------------------------------------

  function startDrag(event: DragEvent, row: Row): void {
    if (!event.dataTransfer) return
    dragging = row
    event.dataTransfer.effectAllowed = 'move'
    event.dataTransfer.setData('text/plain', row.id)
    closeMenu()
  }

  function endDrag(): void {
    dragging = null
    dropOn = null
  }

  /** True when the row being dragged could legally land on `target`. */
  function accepts(target: Row): boolean {
    if (!dragging || (dragging.kind === target.kind && dragging.id === target.id)) return false
    // A project only ever reorders among projects.
    if (dragging.kind === 'project') return target.kind === 'project'
    // A group reorders among its own project's groups, or moves to a project.
    if (target.kind === 'project') return true
    return dragging.projectId === target.projectId
  }

  function dragOver(event: DragEvent, target: Row): void {
    if (!accepts(target)) return
    // Preventing the default is what marks this a valid drop target; without
    // it the browser refuses the drop and the row springs back.
    event.preventDefault()
    if (event.dataTransfer) event.dataTransfer.dropEffect = 'move'
    dropOn = target
  }

  function drop(event: DragEvent, target: Row): void {
    if (!dragging || !accepts(target)) return
    event.preventDefault()

    if (dragging.kind === 'project') {
      organisation.placeProjectBefore(dragging.id, target.id)
    } else if (target.kind === 'project') {
      organisation.moveGroupToProject(dragging.id, target.id)
    } else {
      organisation.placeGroupBefore(dragging.id, target.id)
    }
    endDrag()
  }

  /**
   * Dismisses an open menu on a click elsewhere.
   *
   * A window listener rather than a full-screen backdrop element. The dialog
   * sits in its own stacking context, so a backdrop outside it is above the
   * menus nested within it however the z-indexes are written — it covered the
   * menu and swallowed every click on an item. Asking what was clicked has no
   * such problem, and the toggle buttons are excluded so the click that opens
   * a menu does not immediately close it again.
   */
  function onPointerDown(event: MouseEvent): void {
    if (!menuFor) return
    const target = event.target as HTMLElement | null
    if (target?.closest('[role="menu"], [data-menu-toggle]')) return
    closeMenu()
  }

  function onKeydown(event: KeyboardEvent): void {
    if (event.key !== 'Escape' || !open) return
    // Only when nothing nearer holds it — this dialog already layers its own
    // Escape internally, and the same rule applies between components.
    if (!escape?.owns()) return
    // Escape unwinds the innermost thing first: a menu, then a rename, then
    // the dialog itself.
    if (menuFor) closeMenu()
    else if (renaming) cancelRename()
    else onclose()
  }

  /** Focuses a freshly mounted field and selects what is in it. */
  function claimFocus(node: HTMLInputElement): void {
    node.focus()
    node.select()
  }

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
</script>

<svelte:window onkeydown={onKeydown} ondragend={endDrag} onpointerdown={onPointerDown} />

{#if open}
  <button
    type="button"
    aria-label="Close organiser"
    tabindex="-1"
    class="fixed inset-0 z-40 cursor-default bg-scrim/40"
    onclick={onclose}
  ></button>

  <!-- Centred by a grid layer rather than by -translate-x/y-1/2.
       A CSS transform makes an element the containing block for `position:
       fixed` descendants, so the row menus below — which are fixed, in order
       to escape the list's scroll container — resolved their viewport
       coordinates against the dialog instead and landed a couple of hundred
       pixels off. Centring without a transform is the fix; compensating for
       the offset in the maths would only have hidden it.

       The layer ignores pointer events so the backdrop underneath still
       receives the click that dismisses the dialog. -->
  <div class="pointer-events-none fixed inset-0 z-50 grid place-items-center p-4">
    <div
      class="pointer-events-auto flex max-h-[85vh] w-[44rem] max-w-[94vw] flex-col rounded-sm
             border border-outline-variant bg-surface-container-high p-6 shadow-level-3"
      role="dialog"
      aria-modal="true"
      use:modal
      aria-label="Projects and groups"
    >
    <h2 class="text-headline-small text-on-surface">Projects and groups</h2>
    <p class="mt-1 text-body-small text-on-surface-variant">
      A project is a system; a group inside it is usually an environment. Drag a row to reorder it,
      or drop a group on a project to move it there. Every context starts in
      {organisation.defaultProjectName} › {organisation.defaultGroupNameFor(DEFAULT_PROJECT_ID)}.
    </p>

    <!-- New project.
         The label sits above rather than notching the border. A floating
         label needs a field tall enough to hold it, which is how this ended
         up 48px high beside a 32px button — every control in the application
         is h-8, and this one was half again as tall as the thing next to it. -->
    <div class="mt-5">
      <label for="new-project-name" class="text-body-small text-on-surface-variant">
        Project name
      </label>
      <div class="mt-1.5 flex items-center gap-2">
        <input
          id="new-project-name"
          type="text"
          bind:value={newProjectName}
          placeholder="e.g. Checkout"
          onkeydown={(event) => event.key === 'Enter' && addProject()}
          class="field h-8 min-w-0 flex-1 px-3 text-body-medium"
        />
        <Button variant="tonal" onclick={addProject}>Add</Button>
      </div>
    </div>
    {#if newProjectError}
      <p class="mt-1.5 text-body-small text-error">{newProjectError}</p>
    {/if}

    <!-- The tree -->
    <!-- A fixed-positioned menu does not travel with its row, so scrolling
         the list out from under one would leave it stranded. Closing is both
         simpler and what every other menu in the application does. -->
    <div class="mt-4 min-h-[9rem] flex-1 overflow-y-auto" onscroll={closeMenu}>
      {#each tree as project, projectIndex (project.id)}
        {@const row = { kind: 'project' as Kind, id: project.id }}
        {@const isDropTarget = isRow(dropOn, 'project', project.id)}
        <section class="mb-1">
          <!-- Project row -->
          <div
            role="listitem"
            draggable={!project.isDefault && renaming === null}
            ondragstart={(event) => startDrag(event, row)}
            ondragover={(event) => dragOver(event, row)}
            ondragleave={() => (dropOn = null)}
            ondrop={(event) => drop(event, row)}
            class="group/row relative flex min-h-11 items-center gap-2 rounded-sm px-2 py-1.5
                   transition-colors duration-150 ease-standard hover:bg-surface-container
                   {isRow(dragging, 'project', project.id) ? 'opacity-40' : ''}
                   {isDropTarget ? 'outline outline-2 outline-dashed outline-primary/50' : ''}"
          >
            {#if isRow(renaming, 'project', project.id)}
              <input
                type="text"
                bind:value={renameValue}
                onkeydown={onRenameKeydown}
                aria-label="Rename {project.name}"
                use:claimFocus
                class="field h-8 min-w-0 flex-1 px-3 text-body-medium"
              />
              <Button variant="outlined" onclick={cancelRename}>Cancel</Button>
              <Button variant="filled" onclick={commitRename}>Save</Button>
            {:else}
              <span class="grid w-4 shrink-0 place-items-center text-on-surface-variant/30">
                {#if !project.isDefault}
                  <GripVertical
                    class="size-4 cursor-grab opacity-0 transition-opacity duration-150
                           group-hover/row:opacity-100"
                    strokeWidth={1.8}
                  />
                {/if}
              </span>

              <span class="min-w-0 flex-1 truncate text-title-small font-semibold text-on-surface">
                {project.name}
              </span>

              {#if project.isDefault}
                <!-- Said rather than left blank: an empty slot reads as
                     something missing, not as something fixed. It can be
                     renamed — it cannot be moved or deleted, because it is
                     where everything falls back to. -->
                <span class="shrink-0 text-body-small text-on-surface-variant/50">
                  fallback · always first
                </span>
              {/if}

              <span class="shrink-0 text-body-small tabular-nums text-on-surface-variant">
                {project.count}
                {project.count === 1 ? 'cluster' : 'clusters'}
              </span>

              {#if isRow(confirmDelete, 'project', project.id)}
                <Button variant="outlined" onclick={() => (confirmDelete = null)}>Keep</Button>
                <Button variant="filled" onclick={() => remove(row)}>Delete</Button>
              {:else}
                <button
                  type="button"
                  onclick={(event) => openMenu(row, event, isRow(menuFor, 'project', project.id))}
                  data-menu-toggle
                  aria-label="Actions for {project.name}"
                  aria-expanded={isRow(menuFor, 'project', project.id)}
                  class="state-layer grid size-8 shrink-0 place-items-center rounded-full
                         text-on-surface-variant hover:text-on-surface"
                >
                  <EllipsisVertical class="size-4" strokeWidth={2} />
                </button>
              {/if}

              {#if isRow(menuFor, 'project', project.id)}
                <div
                  role="menu"
                  aria-label="{project.name} actions"
                  use:anchorMenu={{ rect: menuAnchor, key: null }}
                  class="fixed z-[60] w-52 overflow-hidden rounded-sm border
                         border-outline-variant bg-surface-container-highest py-1 shadow-level-2"
                >
                  <button type="button" role="menuitem"
                    onclick={() => startRename(row, project.name)}
                    class="state-layer flex w-full items-center gap-2.5 px-3 py-2 text-left
                           text-body-medium text-on-surface-variant">
                    <Pencil class="size-4 shrink-0" strokeWidth={1.8} /> Rename
                  </button>
                  {#if !project.isDefault}
                  <button type="button" role="menuitem"
                    disabled={projectIndex <= 1}
                    onclick={() => { organisation.moveProject(project.id, -1); closeMenu() }}
                    class="state-layer flex w-full items-center gap-2.5 px-3 py-2 text-left
                           text-body-medium text-on-surface-variant
                           disabled:pointer-events-none disabled:opacity-35">
                    <ChevronUp class="size-4 shrink-0" strokeWidth={2} /> Move up
                  </button>
                  <button type="button" role="menuitem"
                    disabled={projectIndex >= tree.length - 1}
                    onclick={() => { organisation.moveProject(project.id, 1); closeMenu() }}
                    class="state-layer flex w-full items-center gap-2.5 px-3 py-2 text-left
                           text-body-medium text-on-surface-variant
                           disabled:pointer-events-none disabled:opacity-35">
                    <ChevronDown class="size-4 shrink-0" strokeWidth={2} /> Move down
                  </button>
                  <button type="button" role="menuitem"
                    onclick={() => { confirmDelete = row; closeMenu() }}
                    class="state-layer flex w-full items-center gap-2.5 px-3 py-2 text-left
                           text-body-medium text-error">
                    <Trash2 class="size-4 shrink-0" strokeWidth={1.8} /> Delete
                  </button>
                  {/if}
                </div>
              {/if}
            {/if}
          </div>

          <!-- Groups, indented under their project -->
          <ul class="ml-4 border-l border-outline-variant/60 pl-3">
            {#each project.groups as group, groupIndex (group.id)}
              {@const grow = { kind: 'group' as Kind, id: group.id, projectId: project.id }}
              <li
                draggable={!group.isDefault && renaming === null}
                ondragstart={(event) => startDrag(event, grow)}
                ondragover={(event) => dragOver(event, grow)}
                ondragleave={() => (dropOn = null)}
                ondrop={(event) => drop(event, grow)}
                class="group/row relative flex min-h-10 items-center gap-2 rounded-sm px-2 py-1
                       transition-colors duration-150 ease-standard hover:bg-surface-container
                       {isRow(dragging, 'group', group.id, project.id) ? 'opacity-40' : ''}
                       {isRow(dropOn, 'group', group.id, project.id)
                         ? 'outline outline-2 outline-dashed outline-primary/50'
                         : ''}"
              >
                {#if isRow(renaming, 'group', group.id, project.id)}
                  <input
                    type="text"
                    bind:value={renameValue}
                    onkeydown={onRenameKeydown}
                    aria-label="Rename {group.name}"
                    use:claimFocus
                    class="field h-8 min-w-0 flex-1 px-3 text-body-medium"
                  />
                  <Button variant="outlined" onclick={cancelRename}>Cancel</Button>
                  <Button variant="filled" onclick={commitRename}>Save</Button>
                {:else}
                  <span class="grid w-4 shrink-0 place-items-center text-on-surface-variant/30">
                    {#if !group.isDefault}
                      <GripVertical
                        class="size-3.5 cursor-grab opacity-0 transition-opacity duration-150
                               group-hover/row:opacity-100"
                        strokeWidth={1.8}
                      />
                    {/if}
                  </span>

                  <span class="min-w-0 flex-1 truncate text-body-medium text-on-surface">
                    {group.name}
                  </span>

                  {#if group.isDefault}
                    <span class="shrink-0 text-body-small text-on-surface-variant/50">
                      fallback · always first
                    </span>
                  {/if}

                  <span class="shrink-0 text-body-small tabular-nums text-on-surface-variant/70">
                    {group.count}
                  </span>

                  {#if isRow(confirmDelete, 'group', group.id, project.id)}
                    <Button variant="outlined" onclick={() => (confirmDelete = null)}>Keep</Button>
                    <Button variant="filled" onclick={() => remove(grow)}>Delete</Button>
                  {:else}
                    <button
                      type="button"
                      onclick={(event) =>
                        openMenu(grow, event, isRow(menuFor, 'group', group.id, project.id))}
                      data-menu-toggle
                      aria-label="Actions for {group.name}"
                      aria-expanded={isRow(menuFor, 'group', group.id, project.id)}
                      class="state-layer grid size-7 shrink-0 place-items-center rounded-full
                             text-on-surface-variant hover:text-on-surface"
                    >
                      <EllipsisVertical class="size-3.5" strokeWidth={2} />
                    </button>
                  {/if}

                  {#if isRow(menuFor, 'group', group.id, project.id)}
                    <div
                      role="menu"
                      aria-label="{group.name} actions"
                      use:anchorMenu={{ rect: menuAnchor, key: movingGroup }}
                      class="fixed z-[60] max-h-[60vh] w-56 overflow-y-auto rounded-sm border
                             border-outline-variant bg-surface-container-highest py-1 shadow-level-2"
                    >
                      {#if movingGroup === group.id && !group.isDefault}
                        <p class="px-3 pb-1 pt-2 text-[11px] font-semibold uppercase tracking-wider
                                  text-on-surface-variant/60">
                          Move to project
                        </p>
                        {#each tree as target (target.id)}
                          <button type="button" role="menuitem"
                            disabled={target.id === project.id}
                            onclick={() => {
                              organisation.moveGroupToProject(group.id, target.id)
                              closeMenu()
                            }}
                            class="state-layer flex w-full items-center gap-2.5 px-3 py-2 text-left
                                   text-body-medium text-on-surface-variant
                                   disabled:pointer-events-none disabled:opacity-35">
                            <span class="truncate">{target.name}</span>
                            {#if target.id === project.id}
                              <span class="ml-auto shrink-0 text-body-small text-on-surface-variant/50">
                                current
                              </span>
                            {/if}
                          </button>
                        {/each}
                      {:else}
                        <button type="button" role="menuitem"
                          onclick={() => startRename(grow, group.name)}
                          class="state-layer flex w-full items-center gap-2.5 px-3 py-2 text-left
                                 text-body-medium text-on-surface-variant">
                          <Pencil class="size-4 shrink-0" strokeWidth={1.8} /> Rename
                        </button>
                        {#if !group.isDefault}
                        <button type="button" role="menuitem"
                          onclick={() => (movingGroup = group.id)}
                          class="state-layer flex w-full items-center gap-2.5 px-3 py-2 text-left
                                 text-body-medium text-on-surface-variant">
                          <FolderInput class="size-4 shrink-0" strokeWidth={1.8} /> Move to project…
                        </button>
                        <button type="button" role="menuitem"
                          disabled={groupIndex <= 1}
                          onclick={() => { organisation.moveGroup(group.id, -1); closeMenu() }}
                          class="state-layer flex w-full items-center gap-2.5 px-3 py-2 text-left
                                 text-body-medium text-on-surface-variant
                                 disabled:pointer-events-none disabled:opacity-35">
                          <ChevronUp class="size-4 shrink-0" strokeWidth={2} /> Move up
                        </button>
                        <button type="button" role="menuitem"
                          disabled={groupIndex >= project.groups.length - 1}
                          onclick={() => { organisation.moveGroup(group.id, 1); closeMenu() }}
                          class="state-layer flex w-full items-center gap-2.5 px-3 py-2 text-left
                                 text-body-medium text-on-surface-variant
                                 disabled:pointer-events-none disabled:opacity-35">
                          <ChevronDown class="size-4 shrink-0" strokeWidth={2} /> Move down
                        </button>
                        <button type="button" role="menuitem"
                          onclick={() => { confirmDelete = grow; closeMenu() }}
                          class="state-layer flex w-full items-center gap-2.5 px-3 py-2 text-left
                                 text-body-medium text-error">
                          <Trash2 class="size-4 shrink-0" strokeWidth={1.8} /> Delete
                        </button>
                        {/if}
                      {/if}
                    </div>
                  {/if}
                {/if}
              </li>
            {/each}

            <!-- Add a group to this project -->
            <li class="py-1">
              {#if addingGroupIn === project.id}
                <div class="flex items-center gap-2">
                  <input
                    type="text"
                    bind:value={newGroupName}
                    placeholder="e.g. Staging"
                    use:claimFocus
                    onkeydown={(event) => {
                      if (event.key === 'Enter') addGroup(project.id)
                      if (event.key === 'Escape') addingGroupIn = null
                    }}
                    class="field h-8 min-w-0 flex-1 px-3 text-body-medium"
                  />
                  <Button variant="outlined" onclick={() => (addingGroupIn = null)}>Cancel</Button>
                  <Button variant="filled" onclick={() => addGroup(project.id)}>Add</Button>
                </div>
                {#if newGroupError}
                  <p class="mt-1 text-body-small text-error">{newGroupError}</p>
                {/if}
              {:else}
                <button
                  type="button"
                  onclick={() => {
                    addingGroupIn = project.id
                    newGroupName = ''
                    newGroupError = null
                  }}
                  class="state-layer flex items-center gap-1.5 rounded-sm px-2 py-1
                         text-label-large text-on-surface-variant hover:text-on-surface"
                >
                  <Plus class="size-3.5" strokeWidth={2} />
                  Add group
                </button>
              {/if}
            </li>
          </ul>
        </section>
      {/each}
    </div>

    {#if renameError}
      <p class="mt-1.5 text-body-small text-error">{renameError}</p>
    {/if}

    <div class="mt-5 flex shrink-0 justify-end">
      <Button onclick={onclose}>Done</Button>
    </div>
    </div>
  </div>

{/if}
