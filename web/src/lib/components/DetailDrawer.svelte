<!--
  The object detail drawer, rendered as an overlay.

  It floats above the table rather than sitting beside it, so opening a row
  never reflows the list underneath. A scrim behind it dismisses on click,
  and Escape closes.

  The drawer has tabs: Overview, Logs, Terminal, Events, and YAML.
  Action buttons in the header allow delete, scale, restart, and edit.
-->
<script lang="ts">
  import type { ClusterSession } from '$stores/session.svelte'
  import LogViewer from './LogViewer.svelte'
  import ResourceOverview from './ResourceOverview.svelte'
  import EventsView from './EventsView.svelte'
  import YamlEditor from './YamlEditor.svelte'
  import DeleteDialog from './DeleteDialog.svelte'
  import ScaleDialog from './ScaleDialog.svelte'
  import EditDialog from './EditDialog.svelte'
  import RestartDialog from './RestartDialog.svelte'
  import Terminal from './Terminal.svelte'
  import { DeleteResource, RestartRollout } from '$lib/wailsjs/go/wails/ManagementAPI'
  import { ListPodsForWorkload } from '$lib/wailsjs/go/wails/WorkloadAPI'
  import type { Pod } from '$lib/api/client'
  import {
    X,
    Info,
    ScrollText,
    TerminalSquare,
    Activity,
    FileCode,
    RotateCcw,
    Scale,
    Pencil,
    Copy,
    Check,
    Trash2,
  } from '@lucide/svelte'

  interface Props {
    session: ClusterSession
  }

  let { session }: Props = $props()

  type Tab = 'overview' | 'logs' | 'terminal' | 'events' | 'yaml'
  let activeTab = $state<Tab>('overview')
  let copied = $state(false)
  let deleteDialogOpen = $state(false)
  let scaleDialogOpen = $state(false)
  let editDialogOpen = $state(false)
  let restartDialogOpen = $state(false)
  let actionError = $state<string | null>(null)
  let workloadPods = $state<Pod[]>([])

  const subtitle = $derived(
    session.selectedNamespace
      ? `${session.selectedNamespace} · ${session.selectedKind?.singular ?? ''}`
      : (session.selectedKind?.singular ?? ''),
  )

  const isPod = $derived(session.selectedKindId === 'core/v1/pods')

  const isScalable = $derived(
    session.selectedKindId === 'apps/v1/deployments' ||
    session.selectedKindId === 'apps/v1/statefulsets'
  )

  const isRestartable = $derived(
    session.selectedKindId === 'apps/v1/deployments' ||
    session.selectedKindId === 'apps/v1/statefulsets' ||
    session.selectedKindId === 'apps/v1/daemonsets'
  )

  const isWorkloadWithLogs = $derived(
    session.selectedKindId === 'apps/v1/deployments' ||
    session.selectedKindId === 'apps/v1/statefulsets' ||
    session.selectedKindId === 'apps/v1/daemonsets' ||
    session.selectedKindId === 'apps/v1/replicasets'
  )

  const selectedPod = $derived(
    isPod ? session.pods.find(p => p.name === session.selectedName && p.namespace === session.selectedNamespace) : null
  )

  const containerNames = $derived(
    selectedPod?.containers.map(c => c.name) ?? []
  )

  const selectedWorkload = $derived(
    isScalable ? session.workloads.find(w => w.name === session.selectedName && w.namespace === session.selectedNamespace) : null
  )

  $effect(() => {
    if (isWorkloadWithLogs && session.selectedName && session.selectedNamespace) {
      loadWorkloadPods()
    } else {
      workloadPods = []
    }
  })

  async function loadWorkloadPods() {
    if (!session.selectedKind || !session.selectedName) return
    try {
      const kind = session.selectedKind.kind
      workloadPods = await ListPodsForWorkload(
        session.cluster.id,
        session.selectedNamespace,
        kind,
        session.selectedName
      )
    } catch (error) {
      console.error('Failed to load workload pods:', error)
      workloadPods = []
    }
  }

  $effect(() => {
    session.selectedName
    activeTab = 'overview'
    actionError = null
  })

  async function copyManifest(): Promise<void> {
    if (!session.manifest) return
    await navigator.clipboard.writeText(session.manifest)
    copied = true
    setTimeout(() => (copied = false), 1500)
  }

  async function handleDelete(): Promise<void> {
    if (!session.selectedKind || !session.selectedName) return
    try {
      await DeleteResource(
        session.cluster.id,
        session.selectedKind.group,
        session.selectedKind.version,
        session.selectedKind.kind,
        session.selectedNamespace,
        session.selectedName
      )
      deleteDialogOpen = false
      session.closeDetail()
      await session.refresh()
    } catch (error) {
      actionError = `Failed to delete: ${error}`
    }
  }

  async function handleScale(replicas: number): Promise<void> {
    if (!selectedWorkload) return
    try {
      const kind = session.selectedKindId === 'apps/v1/deployments' ? 'Deployment' : 'StatefulSet'
      await session.scaleWorkload(kind, selectedWorkload.name, selectedWorkload.namespace, replicas)
      scaleDialogOpen = false
      await session.refresh()
    } catch (error) {
      actionError = `Failed to scale: ${error}`
    }
  }

  async function handleRestart(): Promise<void> {
    if (!session.selectedKind || !selectedWorkload) return
    try {
      await RestartRollout(
        session.cluster.id,
        session.selectedKind.kind,
        selectedWorkload.namespace,
        selectedWorkload.name
      )
      await session.refresh()
    } catch (error) {
      actionError = `Failed to restart: ${error}`
    }
  }

  async function handleEdit(manifest: string): Promise<void> {
    try {
      await session.updateResource(manifest)
      editDialogOpen = false
      await session.refresh()
    } catch (error) {
      actionError = `Failed to update: ${error}`
    }
  }

  function onKeydown(event: KeyboardEvent): void {
    if (event.key === 'Escape' && session.selectedName) session.closeDetail()
  }

  const tabs: { id: Tab; label: string; icon: typeof Info; show: () => boolean }[] = [
    { id: 'overview', label: 'Overview', icon: Info, show: () => true },
    { id: 'logs', label: 'Logs', icon: ScrollText, show: () => isPod || isWorkloadWithLogs },
    { id: 'terminal', label: 'Terminal', icon: TerminalSquare, show: () => isPod || isWorkloadWithLogs },
    { id: 'events', label: 'Events', icon: Activity, show: () => true },
    { id: 'yaml', label: 'YAML', icon: FileCode, show: () => true },
  ]
</script>

<svelte:window onkeydown={onKeydown} />

{#if session.selectedName}
  <!-- Scrim -->
  <button
    type="button"
    aria-label="Close details"
    tabindex="-1"
    class="fixed inset-0 z-40 cursor-default bg-scrim/30 backdrop-blur-[1px]"
    onclick={session.closeDetail}
  ></button>

  <aside
    class="fixed top-0 right-0 bottom-0 z-50 flex w-[44rem] max-w-[90vw] flex-col
           border-l border-outline-variant/60 bg-surface shadow-level-3"
    aria-label="Object details"
  >
    <!-- Header -->
    <header class="flex items-center gap-3 border-b border-outline-variant/60 px-4 py-3">
      <div class="min-w-0 flex-1">
        <h2 class="truncate text-title-medium font-semibold text-on-surface" data-selectable>
          {session.selectedName}
        </h2>
        <p class="truncate text-body-small text-on-surface-variant/70">{subtitle}</p>
      </div>

      <!-- Action buttons -->
      <div class="flex items-center gap-0.5">
        {#if isRestartable}
          <button
            type="button"
            onclick={() => (restartDialogOpen = true)}
            aria-label="Restart rollout"
            title="Restart rollout"
            class="state-layer grid size-8 shrink-0 place-items-center rounded-full
                   text-on-surface-variant transition-colors duration-100 hover:bg-surface-container hover:text-on-surface"
          >
            <RotateCcw class="size-4" strokeWidth={1.8} />
          </button>
        {/if}

        {#if isScalable}
          <button
            type="button"
            onclick={() => (scaleDialogOpen = true)}
            aria-label="Scale"
            title="Scale replicas"
            class="state-layer grid size-8 shrink-0 place-items-center rounded-full
                   text-on-surface-variant transition-colors duration-100 hover:bg-surface-container hover:text-on-surface"
          >
            <Scale class="size-4" strokeWidth={1.8} />
          </button>
        {/if}

        <button
          type="button"
          onclick={() => (editDialogOpen = true)}
          disabled={!session.manifest}
          aria-label="Edit"
          title="Edit YAML"
          class="state-layer grid size-8 shrink-0 place-items-center rounded-full
                 transition-colors duration-100
                 {session.manifest ? 'text-on-surface-variant hover:bg-surface-container hover:text-on-surface' : 'text-on-surface-variant/30'}
                 disabled:pointer-events-none"
        >
          <Pencil class="size-4" strokeWidth={1.8} />
        </button>

        <button
          type="button"
          onclick={copyManifest}
          disabled={!session.manifest}
          aria-label="Copy manifest"
          title="Copy manifest"
          class="state-layer grid size-8 shrink-0 place-items-center rounded-full
                 transition-colors duration-100
                 {copied ? 'text-success' : 'text-on-surface-variant hover:bg-surface-container hover:text-on-surface'}
                 disabled:pointer-events-none disabled:opacity-30"
        >
          {#if copied}
            <Check class="size-4" strokeWidth={2.5} />
          {:else}
            <Copy class="size-4" strokeWidth={1.8} />
          {/if}
        </button>

        <button
          type="button"
          onclick={() => (deleteDialogOpen = true)}
          aria-label="Delete"
          title="Delete resource"
          class="state-layer grid size-8 shrink-0 place-items-center rounded-full
                 text-on-surface-variant transition-colors duration-100 hover:bg-error/10 hover:text-error"
        >
          <Trash2 class="size-4" strokeWidth={1.8} />
        </button>

        <div class="mx-1 h-5 w-px bg-outline-variant/40"></div>

        <button
          type="button"
          onclick={session.closeDetail}
          aria-label="Close details"
          class="state-layer grid size-8 shrink-0 place-items-center rounded-full
                 text-on-surface-variant transition-colors duration-100 hover:bg-surface-container hover:text-on-surface"
        >
          <X class="size-4" strokeWidth={2} />
        </button>
      </div>
    </header>

    <!-- Tabs -->
    <div class="flex border-b border-outline-variant/60 bg-surface-container-low/50 px-2">
      {#each tabs as tab (tab.id)}
        {#if tab.show()}
          {@const TabIcon = tab.icon}
          {@const active = activeTab === tab.id}
          <button
            type="button"
            onclick={() => (activeTab = tab.id)}
            class="flex items-center gap-1.5 border-b-2 px-3 py-2 text-body-small font-medium
                   transition-colors duration-100
                   {active
                     ? 'border-primary text-primary'
                     : 'border-transparent text-on-surface-variant hover:text-on-surface hover:border-outline-variant/50'}"
          >
            <TabIcon class="size-3.5" strokeWidth={active ? 2 : 1.8} />
            {tab.label}
          </button>
        {/if}
      {/each}
    </div>

    <!-- Error message -->
    {#if actionError}
      <div class="flex items-center gap-2 border-b border-error/20 bg-error-container/50 px-4 py-2 text-body-small text-on-error-container">
        <Activity class="size-3.5 shrink-0 text-error" strokeWidth={2} />
        {actionError}
      </div>
    {/if}

    <!-- Tab content -->
    <div class="min-h-0 flex-1 overflow-auto bg-surface-container-lowest">
      {#if activeTab === 'overview'}
        <ResourceOverview
          manifest={session.manifest}
          selectedPod={selectedPod}
          selectedWorkload={selectedWorkload}
          kind={session.selectedKind?.kind}
        />
      {:else if activeTab === 'logs'}
        {#if isPod && selectedPod}
          <LogViewer
            clusterId={session.cluster.id}
            namespace={selectedPod.namespace}
            podName={selectedPod.name}
            containers={selectedPod.containers?.map(c => c.name) ?? []}
          />
        {:else if isWorkloadWithLogs && workloadPods.length > 0}
          <LogViewer
            clusterId={session.cluster.id}
            namespace={session.selectedNamespace}
            pods={workloadPods.map(p => ({ name: p.name, containers: p.containers?.map((c: any) => c.name) ?? [] }))}
          />
        {:else if isWorkloadWithLogs}
          <div class="flex h-full flex-col items-center justify-center gap-2 p-4 text-on-surface-variant/60">
            <ScrollText class="size-8" strokeWidth={1.2} />
            <p class="text-body-medium">No pods found for this workload</p>
          </div>
        {:else}
          <div class="flex h-full flex-col items-center justify-center gap-2 p-4 text-on-surface-variant/60">
            <ScrollText class="size-8" strokeWidth={1.2} />
            <p class="text-body-medium">Logs are only available for pods and workloads</p>
          </div>
        {/if}
      {:else if activeTab === 'terminal'}
        {#if isPod && selectedPod}
          <Terminal
            clusterId={session.cluster.id}
            namespace={selectedPod.namespace}
            podName={selectedPod.name}
            containerName={selectedPod.containers?.[0]?.name ?? ''}
            containers={selectedPod.containers?.map(c => c.name) ?? []}
          />
        {:else if isWorkloadWithLogs && workloadPods.length > 0}
          <Terminal
            clusterId={session.cluster.id}
            namespace={workloadPods[0].namespace}
            podName={workloadPods[0].name}
            containerName={workloadPods[0].containers?.[0]?.name ?? ''}
            containers={workloadPods[0].containers?.map((c: any) => c.name) ?? []}
          />
        {:else if isWorkloadWithLogs}
          <div class="flex h-full flex-col items-center justify-center gap-2 p-4 text-on-surface-variant/60">
            <TerminalSquare class="size-8" strokeWidth={1.2} />
            <p class="text-body-medium">No pods found for this workload</p>
          </div>
        {:else}
          <div class="flex h-full flex-col items-center justify-center gap-2 p-4 text-on-surface-variant/60">
            <TerminalSquare class="size-8" strokeWidth={1.2} />
            <p class="text-body-medium">Terminal is only available for pods and workloads</p>
          </div>
        {/if}
      {:else if activeTab === 'events'}
        <EventsView
          clusterId={session.cluster.id}
          namespace={session.selectedNamespace}
          kind={session.selectedKind?.kind ?? ''}
          name={session.selectedName ?? ''}
        />
      {:else if activeTab === 'yaml'}
        <div class="h-full">
          {#if session.manifestStatus === 'loading'}
            <div class="flex h-full flex-col items-center justify-center gap-2 p-4 text-on-surface-variant/60">
              <FileCode class="size-8 animate-pulse" strokeWidth={1.2} />
              <p class="text-body-medium">Loading manifest…</p>
            </div>
          {:else if session.manifestStatus === 'error'}
            <div class="flex h-full flex-col items-center justify-center gap-2 p-4">
              <FileCode class="size-8 text-error/60" strokeWidth={1.2} />
              <p class="text-body-medium text-error">
                The manifest could not be read. The object may have been deleted.
              </p>
            </div>
          {:else if session.manifest}
            <YamlEditor content={session.manifest} readonly={true} />
          {/if}
        </div>
      {/if}
    </div>
  </aside>

  <!-- Dialogs -->
  <DeleteDialog
    open={deleteDialogOpen}
    resourceName={session.selectedName}
    resourceKind={session.selectedKind?.singular ?? 'resource'}
    onclose={() => (deleteDialogOpen = false)}
    onconfirm={handleDelete}
  />

  {#if selectedWorkload}
    <ScaleDialog
      open={scaleDialogOpen}
      currentReplicas={selectedWorkload.desired}
      onclose={() => (scaleDialogOpen = false)}
      onconfirm={handleScale}
    />
  {/if}

  <EditDialog
    open={editDialogOpen}
    manifest={session.manifest ?? ''}
    onclose={() => (editDialogOpen = false)}
    onconfirm={handleEdit}
  />

  {#if selectedWorkload}
    <RestartDialog
      open={restartDialogOpen}
      workloadName={selectedWorkload.name}
      workloadKind={session.selectedKind?.singular ?? 'workload'}
      onclose={() => (restartDialogOpen = false)}
      onconfirm={async () => {
        restartDialogOpen = false
        await handleRestart()
      }}
    />
  {/if}
{/if}
