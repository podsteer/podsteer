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

  function closeMenu(): void {
    menuFor = null
    movingGroup = null
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

  function onKeydown(event: KeyboardEvent): void {
    if (event.key !== 'Escape' || !open) return
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
</script>

<svelte:window onkeydown={onKeydown} ondragend={endDrag} />

{#if open}
  <button
    type="button"
    aria-label="Close organiser"
    tabindex="-1"
    class="fixed inset-0 z-40 cursor-default bg-scrim/40"
    onclick={onclose}
  ></button>

  <div
    class="fixed top-1/2 left-1/2 z-50 flex max-h-[85vh] w-[36rem] max-w-[92vw] -translate-x-1/2
           -translate-y-1/2 flex-col rounded-xl border border-outline-variant
           bg-surface-container-high p-6 shadow-level-3"
    role="dialog"
    aria-modal="true"
    aria-label="Projects and groups"
  >
    <h2 class="text-headline-small text-on-surface">Projects and groups</h2>
    <p class="mt-1 text-body-small text-on-surface-variant">
      A project is a system; a group inside it is usually an environment. Drag a row to reorder it,
      or drop a group on a project to move it there. Every context starts in
      {organisation.defaultProjectName} › {organisation.defaultGroupNameFor(DEFAULT_PROJECT_ID)}.
    </p>

    <!-- New project -->
    <div class="mt-5 flex items-start gap-2">
      <label class="relative block flex-1">
        <span
          class="absolute -top-2 left-3 z-10 bg-surface-container-high px-1 text-body-small text-on-surface-variant"
        >
          Project name
        </span>
        <input
          type="text"
          bind:value={newProjectName}
          placeholder="e.g. Checkout"
          onkeydown={(event) => event.key === 'Enter' && addProject()}
          class="h-12 w-full rounded-xs border border-outline bg-transparent px-4 text-body-large
                 text-on-surface transition-colors duration-150 ease-standard
                 hover:border-on-surface focus:border-primary focus:outline-none"
        />
      </label>
      <Button variant="tonal" class="mt-2" onclick={addProject}>Add</Button>
    </div>
    {#if newProjectError}
      <p class="mt-1.5 text-body-small text-error">{newProjectError}</p>
    {/if}

    <!-- The tree -->
    <div class="mt-4 min-h-0 flex-1 overflow-y-auto">
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
                class="h-9 min-w-0 flex-1 rounded-xs border border-primary bg-transparent px-3
                       text-body-medium text-on-surface focus:outline-none"
              />
              <button type="button" onclick={commitRename}
                class="shrink-0 text-label-large text-primary hover:underline">Save</button>
              <button type="button" onclick={cancelRename}
                class="shrink-0 text-label-large text-on-surface-variant hover:text-on-surface">Cancel</button>
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
                <button type="button" onclick={() => remove(row)}
                  class="shrink-0 text-label-large text-error hover:underline">Delete</button>
                <button type="button" onclick={() => (confirmDelete = null)}
                  class="shrink-0 text-label-large text-on-surface-variant hover:text-on-surface">Keep</button>
              {:else}
                <button
                  type="button"
                  onclick={() => (menuFor = isRow(menuFor, 'project', project.id) ? null : row)}
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
                  class="absolute right-2 top-full z-50 mt-1 w-52 overflow-hidden rounded-md border
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
                    class="h-8 min-w-0 flex-1 rounded-xs border border-primary bg-transparent px-3
                           text-body-medium text-on-surface focus:outline-none"
                  />
                  <button type="button" onclick={commitRename}
                    class="shrink-0 text-label-large text-primary hover:underline">Save</button>
                  <button type="button" onclick={cancelRename}
                    class="shrink-0 text-label-large text-on-surface-variant hover:text-on-surface">Cancel</button>
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
                    <button type="button" onclick={() => remove(grow)}
                      class="shrink-0 text-label-large text-error hover:underline">Delete</button>
                    <button type="button" onclick={() => (confirmDelete = null)}
                      class="shrink-0 text-label-large text-on-surface-variant hover:text-on-surface">Keep</button>
                  {:else}
                    <button
                      type="button"
                      onclick={() => {
                        menuFor = isRow(menuFor, 'group', group.id, project.id) ? null : grow
                        movingGroup = null
                      }}
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
                      class="absolute right-2 top-full z-50 mt-1 w-56 overflow-hidden rounded-md border
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
                    class="h-8 min-w-0 flex-1 rounded-xs border border-primary bg-transparent px-3
                           text-body-medium text-on-surface focus:outline-none"
                  />
                  <button type="button" onclick={() => addGroup(project.id)}
                    class="shrink-0 text-label-large text-primary hover:underline">Add</button>
                  <button type="button" onclick={() => (addingGroupIn = null)}
                    class="shrink-0 text-label-large text-on-surface-variant hover:text-on-surface">Cancel</button>
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

  <!-- Closes an open row menu on any outside click, without swallowing the
       click that opened it. Sits under the menus and above everything else. -->
  {#if menuFor}
    <button
      type="button"
      tabindex="-1"
      aria-label="Close menu"
      class="fixed inset-0 z-40 cursor-default"
      onclick={closeMenu}
    ></button>
  {/if}
{/if}
