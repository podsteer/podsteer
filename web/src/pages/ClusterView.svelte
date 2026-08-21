<!--
  The cluster picker, shown when no tab is in front.

  It lists every context in the local kubeconfig under the operator's own
  organisation: project, then group, then clusters. Opening is an explicit
  action rather than something that happens at launch.

  Four things here are deliberate:

  • **Both levels collapse, and the state persists.** Someone with forty
    contexts across six systems wants most of them shut, and wants them still
    shut tomorrow. A collapsed container still reports how many of its clusters
    are open, because that is the fact you would otherwise have to expand it to
    learn.

  • **Groups are the drop targets, not projects.** A cluster lives in a group,
    and a project is only ever reached through one — so dropping onto a project
    would have to invent which of its groups you meant.

  • **Dragging is armed from a handle, not the card.** The application disables
    text selection globally and opts back in with `data-selectable`, which the
    cluster name uses. A permanently draggable card would take that back, so
    the card becomes draggable only while the pointer is on its grip.

  • **Drag is the shortcut, not the mechanism.** Every move is also available
    from the folder menu on each card, which is what keyboard and screen-reader
    users get. Nothing is reachable by drag alone.
-->
<script lang="ts">
  import Button from '$lib/components/Button.svelte'
  import Card from '$lib/components/Card.svelte'
  import EmptyState from '$lib/components/EmptyState.svelte'
  import ErrorBanner from '$lib/components/ErrorBanner.svelte'
  import OrganiseDialog from '$lib/components/OrganiseDialog.svelte'
  import MoveClusterMenu from '$lib/components/MoveClusterMenu.svelte'
  import StatusIndicator from '$lib/components/StatusIndicator.svelte'
  import { groupKey, organisation } from '$stores/organisation.svelte'
  import { workspace } from '$stores/workspace.svelte'
  import {
    Server,
    FolderOpen,
    FolderTree,
    CheckCircle,
    Globe,
    User,
    Layers,
    ChevronDown,
    GripVertical,
    Unplug,
  } from '@lucide/svelte'

  let organiseOpen = $state(false)

  /** The cluster currently being dragged, if any. */
  let draggingId = $state<string | null>(null)
  /** The group key the pointer is currently over during a drag. */
  let dropTargetKey = $state<string | null>(null)
  /** The cluster whose grip is held, and which may therefore be dragged. */
  let armedId = $state<string | null>(null)
  /** The cluster being disconnected, so its button can show it. */
  let disconnectingId = $state<string | null>(null)

  /**
   * Empty projects and groups are shown, not hidden.
   *
   * Hiding them was wrong twice over. Creating one in the organiser changed
   * nothing on screen, so there was no way to tell it had worked; and
   * revealing them only once a drag began moved the layout under the cursor at
   * exactly the moment the operator was aiming at something.
   */
  const sections = $derived(organisation.sections(workspace.clusters, true))

  function startDrag(event: DragEvent, clusterId: string): void {
    if (armedId !== clusterId || !event.dataTransfer) {
      event.preventDefault()
      return
    }
    draggingId = clusterId
    event.dataTransfer.effectAllowed = 'move'
    // Written as well as tracked in state: some environments only expose the
    // payload through the DataTransfer, and an empty one cancels the drop.
    event.dataTransfer.setData('text/plain', clusterId)
  }

  function endDrag(): void {
    draggingId = null
    dropTargetKey = null
    armedId = null
  }

  function dragOver(event: DragEvent, key: string): void {
    if (draggingId === null) return
    // Preventing the default is what marks this a valid drop target; without
    // it the browser refuses the drop and the card springs back.
    event.preventDefault()
    if (event.dataTransfer) event.dataTransfer.dropEffect = 'move'
    dropTargetKey = key
  }

  function drop(event: DragEvent, projectId: string, groupId: string): void {
    event.preventDefault()
    const clusterId = draggingId ?? event.dataTransfer?.getData('text/plain') ?? ''
    if (clusterId) {
      const at = organisation.placementOf(clusterId)
      if (at.project !== projectId || at.group !== groupId) {
        organisation.place(clusterId, projectId, groupId)
      }
    }
    endDrag()
  }

  async function disconnect(clusterId: string): Promise<void> {
    disconnectingId = clusterId
    try {
      await workspace.close(clusterId)
    } finally {
      disconnectingId = null
    }
  }

  /** How many of a set of clusters are currently open. */
  function openCount(clusters: Array<{ id: string }>): number {
    return clusters.filter((cluster) => workspace.openIds.has(cluster.id)).length
  }
</script>

<svelte:window ondragend={endDrag} />

<div class="mx-auto w-full max-w-4xl px-8 py-10">
  <!-- Header -->
  <div class="mb-8 flex items-start justify-between gap-4">
    <div class="flex items-center gap-3">
      <div class="grid size-10 place-items-center rounded-lg bg-primary/10">
        <Server class="size-5 text-primary" strokeWidth={1.8} />
      </div>
      <div>
        <h2 class="text-headline-small font-semibold text-on-surface">Clusters</h2>
        <p class="text-body-medium text-on-surface-variant/70">
          {workspace.clusters.length} contexts from kubeconfig
          {#if workspace.sessions.length > 0}
            · {workspace.sessions.length} open
          {/if}
        </p>
      </div>
    </div>

    <Button variant="tonal" onclick={() => (organiseOpen = true)}>
      <FolderTree class="size-4" strokeWidth={1.8} />
      Organise
    </Button>
  </div>

  <ErrorBanner
    error={workspace.error}
    onretry={workspace.loadClusters}
    ondismiss={() => (workspace.error = null)}
    class="mb-6"
  />

  {#if workspace.clustersStatus === 'loading' && workspace.clusters.length === 0}
    <div class="flex flex-col items-center gap-3 py-16">
      <div class="size-10 animate-pulse rounded-full bg-surface-container-high"></div>
      <p class="text-body-medium text-on-surface-variant">Reading kubeconfig…</p>
    </div>
  {:else if workspace.clusters.length === 0}
    <EmptyState
      title="No clusters configured"
      description="PodSteer found no usable contexts in your kubeconfig. Add one with `kubectl config set-context`, then reload."
    >
      {#snippet action()}
        <Button variant="tonal" onclick={workspace.loadClusters}>Reload kubeconfig</Button>
      {/snippet}
    </EmptyState>
  {:else}
    <div class="flex flex-col gap-7">
      {#each sections as project (project.id)}
        {@const projectCollapsed = organisation.isCollapsed(project.id)}
        {@const projectOpen = project.groups.reduce((n, g) => n + openCount(g.clusters), 0)}
        <section aria-label="{project.name} project">
          <!-- Project header. Heavier than a group's, and ruled beneath, so
               three levels of nesting still read as three levels. -->
          <header class="flex items-center border-b border-outline-variant/50 pb-1.5">
            <button
              type="button"
              onclick={() => organisation.toggleCollapsed(project.id)}
              aria-expanded={!projectCollapsed}
              aria-controls="project-{project.id}"
              class="state-layer -mx-1 flex min-w-0 flex-1 items-center gap-2 rounded-md px-1 py-1
                     text-left transition-colors duration-150"
            >
              <ChevronDown
                class="size-4 shrink-0 text-on-surface-variant/70 transition-transform duration-150
                       {projectCollapsed ? '-rotate-90' : ''}"
                strokeWidth={2}
              />
              <FolderTree class="size-4 shrink-0 text-on-surface-variant/70" strokeWidth={1.8} />
              <h3 class="truncate text-title-medium font-semibold text-on-surface">
                {project.name}
              </h3>
              <span class="shrink-0 rounded-full bg-surface-container-high px-2 py-0.5 text-[11px]
                           tabular-nums text-on-surface-variant/60">
                {project.clusterCount}
              </span>
              {#if projectCollapsed && projectOpen > 0}
                <!-- The one fact you would have to expand to learn. -->
                <span class="shrink-0 rounded-full bg-primary/15 px-2 py-0.5 text-[11px]
                             font-medium text-primary">
                  {projectOpen} open
                </span>
              {/if}
            </button>
          </header>

          {#if !projectCollapsed}
            <div id="project-{project.id}" class="mt-3 flex flex-col gap-4">
              {#each project.groups as group (group.id)}
                {@const key = groupKey(project.id, group.id)}
                {@const groupCollapsed = organisation.isCollapsed(key)}
                {@const isTarget = dropTargetKey === key && draggingId !== null}
                <section
                  aria-label="{group.name} group in {project.name}"
                  class="rounded-xl transition-colors duration-150
                         {isTarget
                           ? 'bg-primary/[0.06] outline outline-2 outline-dashed outline-primary/40'
                           : ''}"
                  ondragover={(event) => dragOver(event, key)}
                  ondragleave={() => (dropTargetKey = null)}
                  ondrop={(event) => drop(event, project.id, group.id)}
                >
                  <!-- Header doubles as the collapse control and part of the
                       drop target, so a collapsed group can still be dropped
                       into. -->
                  <header class="flex items-center px-1">
                    <button
                      type="button"
                      onclick={() => organisation.toggleCollapsed(key)}
                      aria-expanded={!groupCollapsed}
                      aria-controls="group-{key}"
                      class="state-layer -mx-1 flex min-w-0 flex-1 items-center gap-2 rounded-md px-1 py-1
                             text-left transition-colors duration-150"
                    >
                      <ChevronDown
                        class="size-3.5 shrink-0 text-on-surface-variant/60 transition-transform duration-150
                               {groupCollapsed ? '-rotate-90' : ''}"
                        strokeWidth={2}
                      />
                      <Layers class="size-3.5 shrink-0 text-on-surface-variant/60" strokeWidth={1.8} />
                      <h4 class="truncate text-title-small font-medium text-on-surface">
                        {group.name}
                      </h4>
                      <span class="shrink-0 rounded-full bg-surface-container-high px-2 py-0.5 text-[11px]
                                   tabular-nums text-on-surface-variant/60">
                        {group.clusters.length}
                      </span>
                      {#if groupCollapsed && openCount(group.clusters) > 0}
                        <span class="shrink-0 rounded-full bg-primary/15 px-2 py-0.5 text-[11px]
                                     font-medium text-primary">
                          {openCount(group.clusters)} open
                        </span>
                      {/if}
                    </button>
                  </header>

                  {#if !groupCollapsed}
                    <!-- A real list: it is one, and it gives a screen reader
                         the count and per-item navigation a grid would not. -->
                    <div
                      id="group-{key}"
                      role="list"
                      class="mt-2 grid gap-3 sm:grid-cols-1 lg:grid-cols-2"
                    >
                      {#each group.clusters as cluster (cluster.id)}
                        {@const open = workspace.openIds.has(cluster.id)}
                        {@const dragging = draggingId === cluster.id}
                        <div
                          role="listitem"
                          draggable={armedId === cluster.id}
                          ondragstart={(event) => startDrag(event, cluster.id)}
                          ondragend={endDrag}
                          class="transition-opacity duration-150 {dragging ? 'opacity-40' : ''}"
                        >
                          <Card variant="outlined" class="group relative flex h-full flex-col gap-3 p-4
                                transition-all duration-150 hover:border-outline hover:shadow-sm
                                {open ? 'border-primary/30 bg-primary/[0.03]' : ''}">

                            <!-- Grip: arms the drag without making the card
                                 permanently draggable, which would take text
                                 selection away from the cluster name. -->
                            <div
                              role="presentation"
                              onpointerdown={() => (armedId = cluster.id)}
                              onpointerup={() => (armedId = null)}
                              title="Drag to another group"
                              class="absolute left-0.5 top-1/2 -translate-y-1/2 cursor-grab p-1
                                     text-on-surface-variant/25 opacity-0 transition-opacity duration-150
                                     group-hover:opacity-100 active:cursor-grabbing"
                            >
                              <GripVertical class="size-4" strokeWidth={1.8} />
                            </div>

                            <!-- Identity and detail, on their own row. The
                                 actions used to sit beside this, and a third
                                 button plus a long context name truncated the
                                 one thing the card exists to show. -->
                            <div class="flex items-start gap-3">
                              <div class="grid size-9 shrink-0 place-items-center rounded-lg
                                          {open ? 'bg-primary/10' : 'bg-surface-container-high'}">
                                <Server
                                  class="size-4.5 {open ? 'text-primary' : 'text-on-surface-variant/70'}"
                                  strokeWidth={1.8}
                                />
                              </div>

                              <div class="min-w-0 flex-1">
                                <div class="flex flex-wrap items-center gap-2">
                                  <h5 class="truncate text-title-small font-semibold text-on-surface" data-selectable>
                                    {cluster.id}
                                  </h5>
                                  {#if cluster.isCurrent}
                                    <span class="flex items-center gap-0.5 rounded-full bg-secondary-container
                                                 px-1.5 py-0.5 text-[10px] font-medium text-on-secondary-container">
                                      <CheckCircle class="size-2.5" strokeWidth={2.5} />
                                      current
                                    </span>
                                  {/if}
                                  {#if open}
                                    <span class="rounded-full bg-primary/15 px-1.5 py-0.5 text-[10px]
                                                 font-medium text-primary">
                                      open
                                    </span>
                                  {/if}
                                </div>

                                <div class="mt-1 flex flex-wrap items-center gap-x-3 gap-y-0.5 text-body-small text-on-surface-variant/70">
                                  <span class="flex items-center gap-1 truncate">
                                    <Globe class="size-3" strokeWidth={1.8} />
                                    {cluster.host}
                                  </span>
                                  <span class="flex items-center gap-1">
                                    <User class="size-3" strokeWidth={1.8} />
                                    {cluster.authInfo || 'no user'}
                                  </span>
                                  {#if cluster.defaultNamespace}
                                    <span class="flex items-center gap-1">
                                      <FolderOpen class="size-3" strokeWidth={1.8} />
                                      {cluster.defaultNamespace}
                                    </span>
                                  {/if}
                                </div>
                              </div>
                            </div>

                            <!-- Footer: what the cluster IS on the left, what
                                 you can do with it on the right. `mt-auto`
                                 holds it to the bottom so neighbouring cards
                                 line up whether or not one of them has a
                                 version to report. -->
                            <div class="mt-auto flex items-center justify-between gap-2 pt-1">
                              <div class="min-w-0">
                                {#if cluster.isReachable}
                                  <StatusIndicator
                                    tone="success"
                                    label="{cluster.version} · {cluster.platform}"
                                  />
                                {/if}
                              </div>

                              <div class="flex shrink-0 items-center gap-1">
                                {#if open}
                                  <!-- Disconnecting from here rather than only
                                       from the tab strip: someone tidying up is
                                       looking at the list of everything, not at
                                       one tab. -->
                                  <button
                                    type="button"
                                    onclick={() => disconnect(cluster.id)}
                                    disabled={disconnectingId === cluster.id}
                                    aria-label="Disconnect {cluster.id}"
                                    title="Disconnect"
                                    class="state-layer grid size-8 shrink-0 place-items-center rounded-full
                                           text-on-surface-variant transition-colors duration-150
                                           hover:text-error disabled:pointer-events-none disabled:opacity-40"
                                  >
                                    <Unplug class="size-4.5" strokeWidth={1.8} />
                                  </button>
                                {/if}

                                <MoveClusterMenu clusterId={cluster.id} />

                                <Button
                                  variant={open ? 'text' : cluster.isCurrent ? 'filled' : 'tonal'}
                                  loading={workspace.connectingTo === cluster.id}
                                  disabled={workspace.connectingTo !== null && workspace.connectingTo !== cluster.id}
                                  onclick={() => workspace.open(cluster.id)}
                                >
                                  {#if workspace.connectingTo === cluster.id}
                                    Connecting
                                  {:else if open}
                                    Go to tab
                                  {:else}
                                    Open
                                  {/if}
                                </Button>
                              </div>
                            </div>
                          </Card>
                        </div>
                      {/each}

                      {#if group.clusters.length === 0}
                        <!-- Doubles as the only hint that cards can be dragged
                             at all; the grip appears on hover, which nobody
                             discovers unless something tells them to try. -->
                        <p
                          class="col-span-full rounded-lg border border-dashed border-outline-variant
                                 px-4 py-5 text-center text-body-small text-on-surface-variant/60"
                        >
                          Empty — drag a cluster here, or use the folder button on its card
                        </p>
                      {/if}
                    </div>
                  {/if}
                </section>
              {/each}
            </div>
          {/if}
        </section>
      {/each}
    </div>
  {/if}
</div>

<OrganiseDialog open={organiseOpen} onclose={() => (organiseOpen = false)} />
