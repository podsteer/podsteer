<!--
  Manage groups.

  The whole group lifecycle lives in one place: creating, renaming and
  deleting groups. Deleting a group never strands clusters — its members fall
  back to Default, which is why the default group itself has no actions.

  Renaming edits in place: the row becomes the field. Deleting is a two-step
  inline confirm instead of a native confirm(), which the webview cannot be
  relied on to show.
-->
<script lang="ts">
  import Button from './Button.svelte'
  import { DEFAULT_GROUP_ID, DEFAULT_GROUP_NAME, groups } from '$stores/groups.svelte'
  import { workspace } from '$stores/workspace.svelte'

  interface Props {
    open: boolean
    onclose: () => void
  }

  let { open, onclose }: Props = $props()

  let newName = $state('')
  let newError = $state<string | null>(null)

  /** The group being renamed, plus the working copy of its name. */
  let renamingId = $state<string | null>(null)
  let renameValue = $state('')
  let renameError = $state<string | null>(null)

  /** The group one click away from deletion, if any. */
  let confirmDeleteId = $state<string | null>(null)

  // Deriving from both stores keeps the member counts honest while someone
  // reorganises with the dialog open.
  const sections = $derived(groups.sections(workspace.clusters))
  const membersOf = $derived(
    new Map(sections.map((section) => [section.id, section.clusters.length])),
  )

  function addGroup(): void {
    const problem = groups.create(newName)
    newError = problem
    if (!problem) newName = ''
  }

  function startRename(id: string, name: string): void {
    renamingId = id
    renameValue = name
    renameError = null
  }

  function commitRename(): void {
    if (!renamingId) return
    const problem = groups.rename(renamingId, renameValue)
    renameError = problem
    if (!problem) renamingId = null
  }

  function cancelRename(): void {
    renamingId = null
    renameError = null
  }

  function onRenameKeydown(event: KeyboardEvent): void {
    // Escape belongs to the field here, so keep it from the window handler
    // that would otherwise close the whole dialog.
    event.stopPropagation()
    if (event.key === 'Enter') commitRename()
    if (event.key === 'Escape') cancelRename()
  }

  function onKeydown(event: KeyboardEvent): void {
    if (event.key === 'Escape' && open && renamingId === null) onclose()
  }

  /** Focuses a freshly mounted rename field and selects the current name. */
  function claimFocus(node: HTMLInputElement): void {
    node.focus()
    node.select()
  }
</script>

<svelte:window onkeydown={onKeydown} />

{#if open}
  <button
    type="button"
    aria-label="Close group manager"
    tabindex="-1"
    class="fixed inset-0 z-40 cursor-default bg-scrim/40"
    onclick={onclose}
  ></button>

  <div
    class="fixed top-1/2 left-1/2 z-50 w-[28rem] max-w-[90vw] -translate-x-1/2 -translate-y-1/2
           rounded-xl border border-outline-variant bg-surface-container-high p-6 shadow-level-3"
    role="dialog"
    aria-modal="true"
    aria-label="Cluster groups"
  >
    <h2 class="text-headline-small text-on-surface">Cluster groups</h2>
    <p class="mt-1 text-body-small text-on-surface-variant">
      Groups organise the picker. Every context starts in {DEFAULT_GROUP_NAME}; move clusters
      around from each cluster's card.
    </p>

    <!-- New group -->
    <div class="mt-5 flex items-start gap-2">
      <label class="relative block flex-1">
        <span
          class="absolute -top-2 left-3 z-10 bg-surface-container-high px-1 text-body-small text-on-surface-variant"
        >
          Group name
        </span>
        <input
          type="text"
          bind:value={newName}
          placeholder="e.g. Production EU"
          onkeydown={(event) => event.key === 'Enter' && addGroup()}
          class="h-12 w-full rounded-xs border border-outline bg-transparent px-4 text-body-large
                 text-on-surface transition-colors duration-150 ease-standard
                 hover:border-on-surface focus:border-primary focus:outline-none"
        />
      </label>
      <Button variant="tonal" class="mt-2" onclick={addGroup}>Add</Button>
    </div>
    {#if newError}
      <p class="mt-1.5 text-body-small text-error">{newError}</p>
    {/if}

    <!-- Groups -->
    <ul class="mt-4 flex max-h-72 flex-col overflow-y-auto">
      <li class="flex min-h-12 items-center gap-3 rounded-sm px-3 py-2">
        <span class="flex-1 text-body-medium text-on-surface">{DEFAULT_GROUP_NAME}</span>
        <span class="text-body-small text-on-surface-variant tabular-nums">
          {membersOf.get(DEFAULT_GROUP_ID) ?? 0}
          {(membersOf.get(DEFAULT_GROUP_ID) ?? 0) === 1 ? 'cluster' : 'clusters'}
        </span>
      </li>

      {#each groups.groups as group (group.id)}
        <li
          class="flex min-h-12 items-center gap-3 rounded-sm px-3 py-2
                 transition-colors duration-150 ease-standard hover:bg-surface-container"
        >
          {#if renamingId === group.id}
            <input
              type="text"
              bind:value={renameValue}
              onkeydown={onRenameKeydown}
              aria-label="Rename {group.name}"
              use:claimFocus
              class="h-9 min-w-0 flex-1 rounded-xs border border-primary bg-transparent px-3
                     text-body-medium text-on-surface focus:outline-none"
            />
            <button
              type="button"
              onclick={commitRename}
              class="shrink-0 text-label-large text-primary hover:underline"
            >
              Save
            </button>
            <button
              type="button"
              onclick={cancelRename}
              class="shrink-0 text-label-large text-on-surface-variant hover:text-on-surface"
            >
              Cancel
            </button>
          {:else}
            <span class="min-w-0 flex-1 truncate text-body-medium text-on-surface">
              {group.name}
            </span>
            <span class="shrink-0 text-body-small text-on-surface-variant tabular-nums">
              {membersOf.get(group.id) ?? 0}
              {(membersOf.get(group.id) ?? 0) === 1 ? 'cluster' : 'clusters'}
            </span>

            {#if confirmDeleteId === group.id}
              <button
                type="button"
                onclick={() => {
                  groups.remove(group.id)
                  confirmDeleteId = null
                }}
                class="shrink-0 text-label-large text-error hover:underline"
              >
                Confirm delete
              </button>
              <button
                type="button"
                onclick={() => (confirmDeleteId = null)}
                class="shrink-0 text-label-large text-on-surface-variant hover:text-on-surface"
              >
                Keep
              </button>
            {:else}
              <button
                type="button"
                onclick={() => startRename(group.id, group.name)}
                aria-label="Rename {group.name}"
                title="Rename"
                class="state-layer grid size-8 shrink-0 place-items-center rounded-full
                       text-on-surface-variant hover:text-on-surface"
              >
                <svg viewBox="0 0 24 24" class="size-4" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">
                  <path d="M17 3a2.8 2.8 0 1 1 4 4L7.5 20.5 2 22l1.5-5.5Z" />
                </svg>
              </button>
              <button
                type="button"
                onclick={() => (confirmDeleteId = group.id)}
                aria-label="Delete {group.name}"
                title="Delete"
                class="state-layer grid size-8 shrink-0 place-items-center rounded-full
                       text-on-surface-variant hover:text-error"
              >
                <svg viewBox="0 0 24 24" class="size-4" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">
                  <path d="M3 6h18M8 6V4a1 1 0 0 1 1-1h6a1 1 0 0 1 1 1v2m3 0v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6M10 11v6M14 11v6" />
                </svg>
              </button>
            {/if}
          {/if}
        </li>
      {/each}
    </ul>
    {#if renameError}
      <p class="mt-1.5 text-body-small text-error">{renameError}</p>
    {/if}

    <div class="mt-6 flex justify-end">
      <Button onclick={onclose}>Done</Button>
    </div>
  </div>
{/if}
