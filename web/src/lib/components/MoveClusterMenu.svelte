<!--
  "Move to" menu on a cluster card in the picker.

  A small icon button opening every destination the organisation offers:
  each project, and under it each of that project's groups. The whole tree is
  listed flat rather than as a submenu, because a submenu costs a hover-intent
  delay and a second aim for a list that is normally under a dozen rows.

  This is also the accessible half of moving a cluster. Cards can be dragged
  between groups, but a drag is unusable from a keyboard and invisible to a
  screen reader, so nothing may be reachable by drag alone.

  Closing happens on any outside click, on Escape, or after a choice — the same
  contract the rest of the application's transient surfaces keep.
-->
<script lang="ts">
  import {
    DEFAULT_GROUP_ID,
    DEFAULT_PROJECT_ID,
    organisation,
  } from '$stores/organisation.svelte'
  import { Check, FolderInput } from '@lucide/svelte'

  interface Props {
    /** The cluster context name to move. */
    clusterId: string
  }

  let { clusterId }: Props = $props()

  let open = $state(false)

  const placement = $derived(organisation.placementOf(clusterId))

  /**
   * Every destination, flattened, with each project's rows following its
   * heading. Derived rather than nested in the markup so the "is this the
   * current one" test stays a single comparison per row.
   */
  const destinations = $derived(
    organisation.allProjects().map((project) => ({
      ...project,
      groups: organisation.groupsIn(project.id),
    })),
  )

  function choose(projectId: string, groupId: string): void {
    organisation.place(clusterId, projectId, groupId)
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
    onclick={(event) => {
      // The card around this button activates the cluster on click. Opening a
      // menu must not also connect to it.
      event.stopPropagation()
      open = !open
    }}
    aria-label="Move {clusterId} to a project or group"
    aria-expanded={open}
    title="Move to…"
    class="state-layer grid size-8 shrink-0 place-items-center rounded-full
           transition-colors duration-150 ease-standard
           {open || placement.group !== DEFAULT_GROUP_ID || placement.project !== DEFAULT_PROJECT_ID
             ? 'text-primary'
             : 'text-on-surface-variant hover:text-on-surface'}"
  >
    <FolderInput class="size-4.5" strokeWidth={1.8} />
  </button>

  {#if open}
    <!-- Transparent backdrop: one click anywhere else closes the menu. -->
    <button
      type="button"
      tabindex="-1"
      aria-label="Close menu"
      class="fixed inset-0 z-40 cursor-default"
      onclick={(event) => {
        event.stopPropagation()
        open = false
      }}
    ></button>

    <div
      role="menu"
      aria-label="Move to"
      class="absolute right-0 top-full z-50 mt-1 max-h-80 w-64 overflow-y-auto rounded-md border
             border-outline-variant bg-surface-container-high py-1 shadow-level-2"
    >
      {#each destinations as project (project.id)}
        <!-- A heading, not an option: a project is only ever chosen by way of
             one of its groups, so making it clickable would offer a
             destination that does not exist. -->
        <p
          class="truncate px-3 pb-0.5 pt-2 text-[11px] font-semibold uppercase tracking-wider
                 text-on-surface-variant/60"
        >
          {project.name}
        </p>

        {#each project.groups as group (group.id)}
          {@const current = placement.project === project.id && placement.group === group.id}
          <button
            type="button"
            role="menuitemradio"
            aria-checked={current}
            onclick={(event) => {
              event.stopPropagation()
              choose(project.id, group.id)
            }}
            class="state-layer flex w-full items-center gap-2 py-2 pl-5 pr-3 text-left text-body-medium
                   {current ? 'text-on-surface' : 'text-on-surface-variant'}"
          >
            <span class="grid size-4 shrink-0 place-items-center text-primary" aria-hidden="true">
              {#if current}
                <Check class="size-4" strokeWidth={2.5} />
              {/if}
            </span>
            <span class="truncate">{group.name}</span>
          </button>
        {/each}
      {/each}
    </div>
  {/if}
</div>
