<!--
  "Move to group" menu on a cluster card in the picker.

  A small icon button opening a list of every group, with the current one
  checked. Closing happens on any outside click, on Escape, or after a choice
  — the same contract the rest of the app's transient surfaces keep.
-->
<script lang="ts">
  import { DEFAULT_GROUP_ID, DEFAULT_GROUP_NAME, groups } from '$stores/groups.svelte'

  interface Props {
    /** The cluster context name to move. */
    clusterId: string
  }

  let { clusterId }: Props = $props()

  let open = $state(false)

  const currentId = $derived(groups.groupIdOf(clusterId))

  function choose(groupId: string): void {
    groups.assign(clusterId, groupId)
    open = false
  }

  function onKeydown(event: KeyboardEvent): void {
    if (event.key === 'Escape' && open) open = false
  }
</script>

<svelte:window onkeydown={onKeydown} />

<div class="relative">
  <button
    type="button"
    onclick={() => (open = !open)}
    aria-label="Move {clusterId} to a group"
    aria-expanded={open}
    title="Move to group"
    class="state-layer grid size-8 shrink-0 place-items-center rounded-full
           transition-colors duration-150 ease-standard
           {open || currentId !== DEFAULT_GROUP_ID
             ? 'text-primary'
             : 'text-on-surface-variant hover:text-on-surface'}"
  >
    <svg viewBox="0 0 24 24" class="size-4.5" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">
      <path d="M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V7Z" />
    </svg>
  </button>

  {#if open}
    <!-- Transparent backdrop: one click anywhere else closes the menu. -->
    <button
      type="button"
      tabindex="-1"
      aria-label="Close menu"
      class="fixed inset-0 z-40 cursor-default"
      onclick={() => (open = false)}
    ></button>

    <div
      role="menu"
      aria-label="Groups"
      class="absolute right-0 top-full z-50 mt-1 w-56 overflow-hidden rounded-md border
             border-outline-variant bg-surface-container-high py-1 shadow-level-2"
    >
      {#each [{ id: DEFAULT_GROUP_ID, name: DEFAULT_GROUP_NAME }, ...groups.groups] as option (option.id)}
        <button
          type="button"
          role="menuitemradio"
          aria-checked={option.id === currentId}
          onclick={() => choose(option.id)}
          class="state-layer flex w-full items-center gap-2 px-3 py-2 text-left text-body-medium
                 {option.id === currentId ? 'text-on-surface' : 'text-on-surface-variant'}"
        >
          <span class="grid size-4 shrink-0 place-items-center text-primary" aria-hidden="true">
            {#if option.id === currentId}
              <svg viewBox="0 0 24 24" class="size-4" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
                <path d="m4 12 5 5L20 7" />
              </svg>
            {/if}
          </span>
          <span class="truncate">{option.name}</span>
        </button>
      {/each}
    </div>
  {/if}
</div>
