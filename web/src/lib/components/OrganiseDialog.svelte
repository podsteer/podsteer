<!--
  Projects and groups.

  The whole lifecycle of both levels lives in one place: creating, renaming,
  reordering and deleting. Nothing here ever strands a cluster — deleting a
  group drops its members into that project's Default, and deleting a project
  drops them into the Default project — which is why the two Defaults have no
  actions of their own.

  Renaming edits in place: the row becomes the field. Deleting is a two-step
  inline confirm rather than a native confirm(), which the webview cannot be
  relied on to show.

  Reordering is two buttons rather than a drag. The lists are short, the
  dialog is a list rather than a canvas, and adjacent-swap buttons work from a
  keyboard — where the picker's drag does not.
-->
<script lang="ts">
  import Button from './Button.svelte'
  import {
    DEFAULT_GROUP_ID,
    DEFAULT_GROUP_NAME,
    DEFAULT_PROJECT_ID,
    DEFAULT_PROJECT_NAME,
    organisation,
  } from '$stores/organisation.svelte'
  import { workspace } from '$stores/workspace.svelte'
  import { ChevronUp, ChevronDown, Pencil, Trash2, Plus } from '@lucide/svelte'

  interface Props {
    open: boolean
    onclose: () => void
  }

  let { open, onclose }: Props = $props()

  type Kind = 'project' | 'group'

  let newProjectName = $state('')
  let newProjectError = $state<string | null>(null)

  /** The project a new group is being typed into, if any. */
  let addingGroupIn = $state<string | null>(null)
  let newGroupName = $state('')
  let newGroupError = $state<string | null>(null)

  /** What is being renamed, plus the working copy of its name. */
  let renaming = $state<{ kind: Kind; id: string } | null>(null)
  let renameValue = $state('')
  let renameError = $state<string | null>(null)

  /** What is one click away from deletion, if anything. */
  let confirmDelete = $state<{ kind: Kind; id: string } | null>(null)

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

  function startRename(kind: Kind, id: string, name: string): void {
    renaming = { kind, id }
    renameValue = name
    renameError = null
  }

  function commitRename(): void {
    if (!renaming) return
    const problem =
      renaming.kind === 'project'
        ? organisation.renameProject(renaming.id, renameValue)
        : organisation.renameGroup(renaming.id, renameValue)

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

  function remove(kind: Kind, id: string): void {
    if (kind === 'project') organisation.removeProject(id)
    else organisation.removeGroup(id)
    confirmDelete = null
  }

  function isRenaming(kind: Kind, id: string): boolean {
    return renaming?.kind === kind && renaming.id === id
  }

  function isConfirming(kind: Kind, id: string): boolean {
    return confirmDelete?.kind === kind && confirmDelete.id === id
  }

  function onKeydown(event: KeyboardEvent): void {
    // Escape closes the dialog only when it is not busy cancelling something
    // smaller — an open rename field owns the key first.
    if (event.key === 'Escape' && open && renaming === null) onclose()
  }

  /** Focuses a freshly mounted field and selects what is in it. */
  function claimFocus(node: HTMLInputElement): void {
    node.focus()
    node.select()
  }
</script>

<svelte:window onkeydown={onKeydown} />

{#if open}
  <button
    type="button"
    aria-label="Close organiser"
    tabindex="-1"
    class="fixed inset-0 z-40 cursor-default bg-scrim/40"
    onclick={onclose}
  ></button>

  <div
    class="fixed top-1/2 left-1/2 z-50 flex max-h-[85vh] w-[34rem] max-w-[92vw] -translate-x-1/2
           -translate-y-1/2 flex-col rounded-xl border border-outline-variant
           bg-surface-container-high p-6 shadow-level-3"
    role="dialog"
    aria-modal="true"
    aria-label="Projects and groups"
  >
    <h2 class="text-headline-small text-on-surface">Projects and groups</h2>
    <p class="mt-1 text-body-small text-on-surface-variant">
      A project is a system; a group inside it is usually an environment. Every context starts in
      {DEFAULT_PROJECT_NAME} › {DEFAULT_GROUP_NAME} — move clusters from each cluster's card in the
      picker.
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
        <section class="mb-1">
          <!-- Project row -->
          <div
            class="flex min-h-11 items-center gap-2 rounded-sm px-2 py-1.5
                   transition-colors duration-150 ease-standard hover:bg-surface-container"
          >
            {#if isRenaming('project', project.id)}
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
              <span class="min-w-0 flex-1 truncate text-title-small font-semibold text-on-surface">
                {project.name}
              </span>
              <span class="shrink-0 text-body-small tabular-nums text-on-surface-variant">
                {project.count}
                {project.count === 1 ? 'cluster' : 'clusters'}
              </span>

              {#if isConfirming('project', project.id)}
                <button type="button" onclick={() => remove('project', project.id)}
                  class="shrink-0 text-label-large text-error hover:underline">Confirm delete</button>
                <button type="button" onclick={() => (confirmDelete = null)}
                  class="shrink-0 text-label-large text-on-surface-variant hover:text-on-surface">Keep</button>
              {:else if !project.isDefault}
                <!-- projectIndex counts the Default at 0, so the first movable
                     project is at 1 and can only go down. -->
                <button
                  type="button"
                  onclick={() => organisation.moveProject(project.id, -1)}
                  disabled={projectIndex <= 1}
                  aria-label="Move {project.name} up"
                  title="Move up"
                  class="state-layer grid size-8 shrink-0 place-items-center rounded-full
                         text-on-surface-variant hover:text-on-surface
                         disabled:pointer-events-none disabled:opacity-25"
                >
                  <ChevronUp class="size-4" strokeWidth={2} />
                </button>
                <button
                  type="button"
                  onclick={() => organisation.moveProject(project.id, 1)}
                  disabled={projectIndex >= tree.length - 1}
                  aria-label="Move {project.name} down"
                  title="Move down"
                  class="state-layer grid size-8 shrink-0 place-items-center rounded-full
                         text-on-surface-variant hover:text-on-surface
                         disabled:pointer-events-none disabled:opacity-25"
                >
                  <ChevronDown class="size-4" strokeWidth={2} />
                </button>
                <button
                  type="button"
                  onclick={() => startRename('project', project.id, project.name)}
                  aria-label="Rename {project.name}"
                  title="Rename"
                  class="state-layer grid size-8 shrink-0 place-items-center rounded-full
                         text-on-surface-variant hover:text-on-surface"
                >
                  <Pencil class="size-4" strokeWidth={1.8} />
                </button>
                <button
                  type="button"
                  onclick={() => (confirmDelete = { kind: 'project', id: project.id })}
                  aria-label="Delete {project.name}"
                  title="Delete"
                  class="state-layer grid size-8 shrink-0 place-items-center rounded-full
                         text-on-surface-variant hover:text-error"
                >
                  <Trash2 class="size-4" strokeWidth={1.8} />
                </button>
              {/if}
            {/if}
          </div>

          <!-- Groups, indented under their project -->
          <ul class="ml-3 border-l border-outline-variant/60 pl-3">
            {#each project.groups as group, groupIndex (group.id)}
              <li
                class="flex min-h-10 items-center gap-2 rounded-sm px-2 py-1
                       transition-colors duration-150 ease-standard hover:bg-surface-container"
              >
                {#if isRenaming('group', group.id)}
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
                  <span class="min-w-0 flex-1 truncate text-body-medium text-on-surface">
                    {group.name}
                  </span>
                  <span class="shrink-0 text-body-small tabular-nums text-on-surface-variant/70">
                    {group.count}
                  </span>

                  {#if isConfirming('group', group.id)}
                    <button type="button" onclick={() => remove('group', group.id)}
                      class="shrink-0 text-label-large text-error hover:underline">Confirm delete</button>
                    <button type="button" onclick={() => (confirmDelete = null)}
                      class="shrink-0 text-label-large text-on-surface-variant hover:text-on-surface">Keep</button>
                  {:else if !group.isDefault}
                    <button
                      type="button"
                      onclick={() => organisation.moveGroup(group.id, -1)}
                      disabled={groupIndex <= 1}
                      aria-label="Move {group.name} up"
                      title="Move up"
                      class="state-layer grid size-7 shrink-0 place-items-center rounded-full
                             text-on-surface-variant hover:text-on-surface
                             disabled:pointer-events-none disabled:opacity-25"
                    >
                      <ChevronUp class="size-3.5" strokeWidth={2} />
                    </button>
                    <button
                      type="button"
                      onclick={() => organisation.moveGroup(group.id, 1)}
                      disabled={groupIndex >= project.groups.length - 1}
                      aria-label="Move {group.name} down"
                      title="Move down"
                      class="state-layer grid size-7 shrink-0 place-items-center rounded-full
                             text-on-surface-variant hover:text-on-surface
                             disabled:pointer-events-none disabled:opacity-25"
                    >
                      <ChevronDown class="size-3.5" strokeWidth={2} />
                    </button>
                    <button
                      type="button"
                      onclick={() => startRename('group', group.id, group.name)}
                      aria-label="Rename {group.name}"
                      title="Rename"
                      class="state-layer grid size-7 shrink-0 place-items-center rounded-full
                             text-on-surface-variant hover:text-on-surface"
                    >
                      <Pencil class="size-3.5" strokeWidth={1.8} />
                    </button>
                    <button
                      type="button"
                      onclick={() => (confirmDelete = { kind: 'group', id: group.id })}
                      aria-label="Delete {group.name}"
                      title="Delete"
                      class="state-layer grid size-7 shrink-0 place-items-center rounded-full
                             text-on-surface-variant hover:text-error"
                    >
                      <Trash2 class="size-3.5" strokeWidth={1.8} />
                    </button>
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
{/if}
