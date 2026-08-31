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
  import EventDetail from './EventDetail.svelte'
  import { iconForKind } from '$lib/kindIcons'
  import { parse } from 'yaml'
  import YamlPane from './YamlPane.svelte'
  import Button from './Button.svelte'
  import ToolbarButton from './ToolbarButton.svelte'
  import ToolbarToggle from './ToolbarToggle.svelte'
  import PaneDialog from './PaneDialog.svelte'
  import { withoutManagedFields } from '$lib/manifest'
  import { gitOpsOwner, revertWarning } from '$lib/gitops'
  import GitOpsBadge from './GitOpsBadge.svelte'
  import { preferences } from '$stores/preferences.svelte'
  import DeleteDialog from './DeleteDialog.svelte'
  import ScaleDialog from './ScaleDialog.svelte'
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
    Maximize2,
    TriangleAlert,
    Eye,
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
  /**
   * Whether the manifest is being edited, wherever it is being shown.
   *
   * Editing used to be a separate dialog, which meant the side panel could
   * only ever be read from — so changing one field meant opening a window
   * over the object you were looking at. It is a mode of the pane now, and
   * the pane appears in two places, so this lives here rather than in either
   * of them and carries across when one becomes the other.
   */
  let editing = $state(false)
  /** The edited text, or null when not editing. */
  let draft = $state<string | null>(null)
  /** What the draft started as, to tell a touched buffer from a fresh one. */
  let draftOrigin = $state('')

  /** Which pane, if any, has been given the whole window. */
  let maximized = $state<'yaml' | 'logs' | null>(null)
  let restartDialogOpen = $state(false)
  let actionError = $state<string | null>(null)
  let workloadPods = $state<Pod[]>([])

  /** The selected kind's own icon, so the drawer is marked like its row. */
  const KindIcon = $derived(
    session.selectedKind ? iconForKind(session.selectedKind) : undefined,
  )

  const isPod = $derived(session.selectedKindId === 'core/v1/pods')

  const isEvent = $derived(session.selectedKindId === 'core/v1/events')

  /**
   * The manifest as shown, which is not always the manifest as fetched.
   *
   * Filtered here rather than in the Go adapter so the toggle is instant and
   * costs no round trip — and so that what the API server sent is still the
   * thing held in memory, with the trimming a property of the view.
   */
  const shownManifest = $derived(
    session.manifest === null
      ? null
      : preferences.showManagedFields
        ? session.manifest
        : withoutManagedFields(session.manifest),
  )

  /**
   * Whether editing this object means anything.
   *
   * An event is a record of something that already happened: the API will
   * accept a patch and the cluster will take no notice, so offering the
   * action would be offering a change that cannot have an effect.
   */
  /**
   * The GitOps controller managing this object, if one is.
   *
   * Read from the manifest rather than from the list row, because the
   * evidence lives in labels and annotations that the table columns do not
   * carry — and because the manifest is already here for the YAML tab.
   */
  const managedBy = $derived.by(() => {
    if (!session.manifest) return null
    try {
      return gitOpsOwner(parse(session.manifest))
    } catch {
      return null
    }
  })

  /**
   * Whether this object is a Secret whose values are currently hidden.
   *
   * The manifest on screen has `<hidden, 24 bytes>` where the data was, so
   * saving it would write those placeholders over the real values — data
   * loss wearing the costume of an edit. Editing is therefore blocked until
   * the values are deliberately revealed.
   */
  const secretsHidden = $derived(
    session.selectedKindId === 'core/v1/secrets' && !session.secretsRevealed && !!session.manifest,
  )

  const canEdit = $derived(!!session.manifest && !isEvent && !secretsHidden)

  const editHint = $derived(
    isEvent
      ? 'An event is a record of something that happened — there is nothing to change'
      : secretsHidden
        ? 'Reveal the values first — saving now would overwrite them with their placeholders'
        : session.manifest
          ? 'Edit YAML'
          : 'Nothing loaded yet',
  )

  /**
   * The selected event, parsed from its own manifest.
   *
   * Everything an event says lives at the top level rather than under spec or
   * status, which is why the generic overview showed almost none of it.
   */
  const parsedEvent = $derived.by((): Record<string, unknown> | null => {
    if (!isEvent || !session.manifest) return null
    try {
      return parse(session.manifest) as Record<string, unknown>
    } catch {
      return null
    }
  })

  /**
   * The navigator id for the kind an event is about, or null when this
   * cluster does not serve it.
   *
   * An event can name a kind PodSteer has no list for — a CRD removed since
   * the event fired, most obviously — so the link is offered only when there
   * is somewhere for it to go.
   */
  const involvedKindId = $derived.by((): string | null => {
    const involved = parsedEvent?.involvedObject as Record<string, string> | undefined
    if (!involved?.kind) return null
    return session.kinds.find((kind) => kind.kind === involved.kind)?.id ?? null
  })

  /** Opens the object an event is about, in the list it belongs to. */
  async function openInvolved(target: { name: string; namespace: string }): Promise<void> {
    if (!involvedKindId) return
    await session.selectKind(involvedKindId)
    await session.openDetail(target.name, target.namespace)
  }

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

  /**
   * Which pod lookup is the current one.
   *
   * Clicking through several workloads leaves overlapping requests in flight,
   * and without this the LAST TO RETURN wins rather than the last asked for.
   * That result feeds the Logs and Terminal tabs, so a slow reply for the
   * deployment you have already navigated away from would stream logs from
   * the wrong workload's pods — and look entirely convincing while doing it.
   */
  let podRequest = 0

  $effect(() => {
    if (isWorkloadWithLogs && session.selectedName && session.selectedNamespace) {
      loadWorkloadPods()
    } else {
      podRequest++
      workloadPods = []
    }
  })

  async function loadWorkloadPods() {
    if (!session.selectedKind || !session.selectedName) return

    const request = ++podRequest
    try {
      const kind = session.selectedKind.kind
      const pods = await ListPodsForWorkload(
        session.cluster.id,
        session.selectedNamespace,
        kind,
        session.selectedName
      )
      if (request !== podRequest) return
      workloadPods = pods
    } catch (error) {
      if (request !== podRequest) return
      console.error('Failed to load workload pods:', error)
      workloadPods = []
    }
  }

  $effect(() => {
    session.selectedName
    activeTab = 'overview'
    actionError = null
  })

  /**
   * Copies the manifest AS SHOWN, managed fields included only if they are.
   *
   * The button sits in the YAML toolbar now, directly above the text, so what
   * it copies has to be that text. Copying the unfiltered object from a
   * control beside a filtered view put 465 lines on the clipboard while 232
   * were on screen — and the difference is invisible until it is pasted
   * somewhere. Anybody who wants the whole thing turns managed fields back on
   * first, which is exactly what the neighbouring control is for.
   */
  async function copyManifest(): Promise<void> {
    if (!shownManifest) return
    await navigator.clipboard.writeText(shownManifest)
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

  /** True once the draft differs from what it was seeded with. */
  const dirty = $derived(draft !== null && draft !== draftOrigin)

  function startEditing(): void {
    const seed = shownManifest ?? ''
    draft = seed
    draftOrigin = seed
    editing = true
  }

  function stopEditing(): void {
    editing = false
    draft = null
    draftOrigin = ''
  }

  async function applyEdit(): Promise<void> {
    if (draft === null) return
    try {
      await session.updateResource(draft)
      stopEditing()
      await session.refresh()
    } catch (error) {
      actionError = `Failed to update: ${error}`
    }
  }

  /**
   * Re-seeds the draft when the managed-fields view changes underneath it.
   *
   * Only while the buffer is untouched — the control is disabled once there
   * is something to lose, which is what makes this safe.
   */
  $effect(() => {
    const seed = preferences.showManagedFields
    void seed
    if (editing && !dirty) {
      const next = shownManifest ?? ''
      draft = next
      draftOrigin = next
    }
  })

  // Leaving the object, or the tab, ends an edit rather than carrying a draft
  // for one object onto another.
  $effect(() => {
    void session.selectedName
    void session.selectedNamespace
    stopEditing()
    maximized = null
  })

  function onKeydown(event: KeyboardEvent): void {
    if (event.key === 'Escape' && session.selectedName) session.closeDetail()
  }

  const tabs: { id: Tab; label: string; icon: typeof Info; show: () => boolean }[] = [
    { id: 'overview', label: 'Overview', icon: Info, show: () => true },
    { id: 'logs', label: 'Logs', icon: ScrollText, show: () => isPod || isWorkloadWithLogs },
    { id: 'terminal', label: 'Terminal', icon: TerminalSquare, show: () => isPod || isWorkloadWithLogs },
    // An event has no events of its own, and asking for them returns the
    // empty list that means "nothing recent" — which reads as a fault here
    // rather than as the tautology it is.
    { id: 'events', label: 'Events', icon: Activity, show: () => !isEvent },
    { id: 'yaml', label: 'YAML', icon: FileCode, show: () => true },
  ]
</script>

<!--
  The manifest pane, defined once and rendered in two places: in the drawer,
  and in the dialog that gives it the whole window. Sharing the snippet is
  what makes maximising the same surface rather than a second one — the same
  toolbar, the same controls, the same draft.
-->
<!--
  The log pane, for the drawer and for the dialog.

  Rendering it in the dialog re-mounts it, which restarts the stream and
  re-fetches the tail. That is the same thing switching tabs already does, and
  the alternative — keeping a hidden copy alive to preserve a buffer — would
  mean two streams open for one pod.
-->
<!--
  The one thing somebody editing a reconciled object needs to know, placed
  where the decision is made rather than where the object was opened.

  A chip in the header says WHO owns it; this says what happens if you press
  Apply anyway, which is a different question and only arises here.
-->
{#snippet revertNotice()}
  {#if managedBy}
    <p
      class="flex min-w-0 flex-1 items-start gap-2 text-body-small text-gauge-warn"
      role="status"
    >
      <TriangleAlert class="mt-0.5 size-4 shrink-0" strokeWidth={2} />
      <span class="min-w-0">{revertWarning(managedBy)}</span>
    </p>
  {/if}
{/snippet}

{#snippet logsSurface()}
  {#if isPod && selectedPod}
    <LogViewer
      clusterId={session.cluster.id}
      namespace={selectedPod.namespace}
      podName={selectedPod.name}
      containers={selectedPod.containers?.map((c) => c.name) ?? []}
      onmaximize={maximized === 'logs' ? undefined : () => (maximized = 'logs')}
    />
  {:else if isWorkloadWithLogs && workloadPods.length > 0}
    <LogViewer
      clusterId={session.cluster.id}
      namespace={session.selectedNamespace}
      pods={workloadPods.map((p) => ({
        name: p.name,
        containers: p.containers?.map((c: any) => c.name) ?? [],
      }))}
      onmaximize={maximized === 'logs' ? undefined : () => (maximized = 'logs')}
    />
  {/if}
{/snippet}

{#snippet yamlSurface()}
  <YamlPane
    content={editing ? (draft ?? '') : (shownManifest ?? '')}
    readonly={!editing}
    onchange={(value) => (draft = value)}
    managedFieldsDisabled={editing && dirty}
    managedFieldsDisabledReason="Can’t change while there are unsaved edits"
  >
    {#snippet actions()}
      <!--
        Reveal, for a Secret whose values are hidden. Its own control rather
        than something the Edit button does implicitly: this performs an
        audited read of the Secret, and an audit entry ought to correspond to
        somebody deciding to look, not to somebody clicking towards a
        different intention.
      -->
      {#if secretsHidden}
        <ToolbarButton
          icon={Eye}
          label="Reveal values"
          title="Read this Secret's values. This is an audited read."
          onclick={() => session.revealManifestSecrets()}
        />
      {/if}
      <ToolbarToggle
        icon={Pencil}
        label="Edit"
        pressed={editing}
        disabled={!canEdit}
        title={editing ? 'Editing — click to stop' : editHint}
        onclick={() => (editing ? stopEditing() : startEditing())}
      />
      <ToolbarButton
        icon={copied ? Check : Copy}
        label="Copy manifest"
        title={copied ? 'Copied' : 'Copy manifest'}
        active={copied}
        disabled={!shownManifest}
        onclick={copyManifest}
      />
      {#if maximized !== 'yaml'}
        <ToolbarButton
          icon={Maximize2}
          label="Maximize"
          title="Open in a larger window"
          onclick={() => (maximized = 'yaml')}
        />
      {/if}
    {/snippet}
  </YamlPane>
{/snippet}

<svelte:window onkeydown={onKeydown} />

{#if session.selectedName}
  <!-- Scrim: dimmed, not blurred.
       The row behind the drawer is what was clicked, and the rows around it
       are the context somebody reads the detail against — a blur takes both
       away to decorate a panel that already has its own surface and shadow to
       separate it. -->
  <button
    type="button"
    aria-label="Close details"
    tabindex="-1"
    class="fixed inset-0 z-40 cursor-default bg-scrim/30"
    onclick={session.closeDetail}
  ></button>

  <aside
    class="fixed top-0 right-0 bottom-0 z-50 flex w-[44rem] max-w-[90vw] flex-col
           border-l border-outline-variant/60 bg-surface shadow-level-3"
    aria-label="Object details"
  >
    <!-- Header.
         The kind's own icon and a path, the same way the event pane addresses
         the object it is about — so a drawer says what it is holding before
         its name is read, and says it with the mark the row was carrying. -->
    <header class="flex items-center gap-3 border-b border-outline-variant/60 px-4 py-3">
      {#if KindIcon}
        <span class="inline-flex shrink-0" title={session.selectedKind?.singular}>
          <KindIcon class="size-5 text-on-surface-variant/60" strokeWidth={1.75} />
        </span>
      {/if}

      <div class="min-w-0 flex-1">
        <h2 class="truncate text-title-medium font-semibold text-on-surface" data-selectable>
          {session.selectedName}
        </h2>

        <!-- Kind, then namespace, which is where it lives. The namespace is a
             link because it is somewhere to go: it filters the whole
             application to that namespace, which is what somebody reading a
             detail usually wants next. -->
        <p class="flex min-w-0 items-baseline gap-1.5 text-body-small text-on-surface-variant/70">
          <span class="shrink-0">{session.selectedKind?.singular ?? 'Object'}</span>
          {#if session.selectedNamespace}
            <span class="shrink-0 text-on-surface-variant/40" aria-hidden="true">/</span>
            <button
              type="button"
              onclick={() => session.selectNamespace(session.selectedNamespace)}
              class="resource-link min-w-0 truncate text-left"
              title="Filter to {session.selectedNamespace}"
            >
              {session.selectedNamespace}
            </button>
          {/if}

          <!-- Who owns it, beside where it lives. It belongs in the header
               rather than on the YAML tab because it is true of the object on
               every tab, and somebody restarting a rollout or scaling a
               deployment is about to be reconciled over just as surely as
               somebody editing the manifest. -->
          {#if managedBy}
            <span class="shrink-0 text-on-surface-variant/40" aria-hidden="true">·</span>
            <GitOpsBadge owner={managedBy} compact />
          {/if}
        </p>
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

        <!-- Edit and Copy used to sit here. They act on the manifest, so they
             now live in the YAML tab's toolbar beside it — a control belongs
             next to the thing it changes, and from the Overview tab "Copy"
             gave no clue that what landed on the clipboard was YAML. Delete
             stays: it acts on the object, not on any one view of it. -->
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
      {#if activeTab === 'overview' && isEvent}
        <EventDetail event={parsedEvent} canOpen={involvedKindId !== null} onopen={openInvolved} />
      {:else if activeTab === 'overview'}
        <ResourceOverview
          manifest={session.manifest}
          selectedPod={selectedPod}
          selectedWorkload={selectedWorkload}
          kind={session.selectedKind?.kind}
          usage={session.usage}
        />
      {:else if activeTab === 'logs'}
        {#if maximized === 'logs'}
          <!-- The pane is in the dialog. Saying so beats an empty tab, which
               reads as a pane that failed to load. -->
          <div class="flex h-full items-center justify-center p-4">
            <p class="text-body-medium text-on-surface-variant/70">
              Showing the logs in a larger window.
            </p>
          </div>
        {:else if isPod && selectedPod}
          {@render logsSurface()}
        {:else if isWorkloadWithLogs && workloadPods.length > 0}
          {@render logsSurface()}
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
          {:else if maximized === 'yaml'}
            <!-- The pane is in the dialog. Saying so beats an empty tab. -->
            <div class="flex h-full items-center justify-center p-4">
              <p class="text-body-medium text-on-surface-variant/70">
                Showing the manifest in a larger window.
              </p>
            </div>
          {:else if session.manifest}
            <!-- The toolbar only appears once there is text for it to govern:
                 a wrap button above a spinner controls nothing. -->
            {@render yamlSurface()}
          {/if}
        </div>
      {/if}
    </div>

    <!-- Committing an edit made in the panel.
         Only while editing, and only while the pane is here rather than in
         the dialog, which carries its own. A drawer that reserved a footer
         for a mode it is not in would lose a row of the manifest to a bar
         with nothing in it. -->
    {#if editing && activeTab === 'yaml' && maximized !== 'yaml'}
      <div
        class="flex shrink-0 flex-col gap-3 border-t border-outline-variant/60
               bg-surface-container-low px-4 py-3"
      >
        {@render revertNotice()}
        <div class="flex items-center justify-end gap-3">
          <Button variant="outlined" onclick={stopEditing}>Cancel</Button>
          <Button variant="filled" onclick={applyEdit}>Apply</Button>
        </div>
      </div>
    {/if}
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

  <!-- The same pane, given the window. Closing restores it to the drawer
       rather than discarding anything: the draft lives above both. -->
  <PaneDialog
    open={maximized === 'yaml'}
    icon={KindIcon}
    kind={session.selectedKind?.singular}
    name={session.selectedName ?? ''}
    label="Manifest"
    onclose={() => (maximized = null)}
  >
    {@render yamlSurface()}

    {#snippet footer()}
      {#if editing}
        {@render revertNotice()}
        <Button variant="outlined" onclick={stopEditing}>Cancel</Button>
        <Button variant="filled" onclick={applyEdit}>Apply</Button>
      {/if}
    {/snippet}
  </PaneDialog>

  <PaneDialog
    open={maximized === 'logs'}
    icon={KindIcon}
    kind={session.selectedKind?.singular}
    name={session.selectedName ?? ''}
    label="Logs"
    onclose={() => (maximized = null)}
  >
    {@render logsSurface()}
  </PaneDialog>

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
