<!--
  The cluster picker, shown when no tab is in front.

  It lists every context in the local kubeconfig, grouped under the operator's
  groups. Opening is an explicit action rather than something that happens at
  launch.
-->
<script lang="ts">
  import Button from '$lib/components/Button.svelte'
  import Card from '$lib/components/Card.svelte'
  import EmptyState from '$lib/components/EmptyState.svelte'
  import ErrorBanner from '$lib/components/ErrorBanner.svelte'
  import ManageGroupsDialog from '$lib/components/ManageGroupsDialog.svelte'
  import MoveToGroupMenu from '$lib/components/MoveToGroupMenu.svelte'
  import StatusIndicator from '$lib/components/StatusIndicator.svelte'
  import { groups } from '$stores/groups.svelte'
  import { workspace } from '$stores/workspace.svelte'
  import {
    Server,
    FolderOpen,
    Plus,
    CheckCircle,
    Globe,
    User,
    Layers,
  } from '@lucide/svelte'

  let manageGroupsOpen = $state(false)

  const sections = $derived(groups.sections(workspace.clusters))
</script>

<div class="mx-auto w-full max-w-4xl px-8 py-10">
  <!-- Header -->
  <div class="mb-8 flex items-start justify-between gap-4">
    <div>
      <div class="flex items-center gap-3">
        <div class="grid size-10 place-items-center rounded-lg bg-primary/10">
          <Server class="size-5 text-primary" strokeWidth={1.8} />
        </div>
        <div>
          <h2 class="text-headline-small font-semibold text-on-surface">Clusters</h2>
          <p class="text-body-medium text-on-surface-variant/70">
            {workspace.clusters.length} contexts from kubeconfig
          </p>
        </div>
      </div>
    </div>

    <Button variant="tonal" onclick={() => (manageGroupsOpen = true)}>
      <FolderOpen class="size-4" strokeWidth={1.8} />
      Manage groups
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
      description="K8Sense found no usable contexts in your kubeconfig. Add one with `kubectl config set-context`, then reload."
    >
      {#snippet action()}
        <Button variant="tonal" onclick={workspace.loadClusters}>Reload kubeconfig</Button>
      {/snippet}
    </EmptyState>
  {:else}
    <div class="flex flex-col gap-8">
      {#each sections as section (section.id)}
        <section aria-label="{section.name} group">
          <header class="mb-3 flex items-center gap-2 px-1">
            <Layers class="size-4 text-on-surface-variant/60" strokeWidth={1.8} />
            <h3 class="text-title-small font-semibold text-on-surface">{section.name}</h3>
            <span class="rounded-full bg-surface-container-high px-2 py-0.5 text-[11px]
                         tabular-nums text-on-surface-variant/60">
              {section.clusters.length}
            </span>
          </header>

          <div class="grid gap-3 sm:grid-cols-1 lg:grid-cols-2">
            {#each section.clusters as cluster (cluster.id)}
              {@const open = workspace.openIds.has(cluster.id)}
              <Card variant="outlined" class="group relative flex items-start gap-3 p-4
                    transition-all duration-150 hover:border-outline hover:shadow-sm
                    {open ? 'border-primary/30 bg-primary/[0.03]' : ''}">
                
                <!-- Cluster icon -->
                <div class="grid size-9 shrink-0 place-items-center rounded-lg
                            {open ? 'bg-primary/10' : 'bg-surface-container-high'}">
                  <Server
                    class="size-4.5 {open ? 'text-primary' : 'text-on-surface-variant/70'}"
                    strokeWidth={1.8}
                  />
                </div>

                <!-- Cluster info -->
                <div class="min-w-0 flex-1">
                  <div class="flex items-center gap-2">
                    <h3 class="truncate text-title-small font-semibold text-on-surface" data-selectable>
                      {cluster.id}
                    </h3>
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

                  {#if cluster.isReachable}
                    <div class="mt-2">
                      <StatusIndicator
                        tone="success"
                        label="{cluster.version} · {cluster.platform}"
                      />
                    </div>
                  {/if}
                </div>

                <!-- Actions -->
                <div class="flex shrink-0 items-center gap-1">
                  <MoveToGroupMenu clusterId={cluster.id} />

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
              </Card>
            {/each}
          </div>
        </section>
      {/each}
    </div>
  {/if}
</div>

<ManageGroupsDialog open={manageGroupsOpen} onclose={() => (manageGroupsOpen = false)} />
