<!--
  The object detail drawer, rendered as an overlay.

  It floats above the table rather than sitting beside it, so opening a row
  never reflows the list underneath. A scrim behind it dismisses on click,
  and Escape closes.

  The drawer has tabs: Overview, Logs, Terminal, Events, and YAML.
  Action buttons in the header allow delete, scale, restart, and edit.
-->
<script lang="ts">
  import { flash } from '$lib/flash.svelte'
  import { escapeLayer, type EscapeClaim } from '$lib/escape'
  import type { ClusterSession } from '$stores/session.svelte'
  import { WORKLOAD_KIND_BY_ID } from '$stores/session.svelte'
  import LogViewer from './LogViewer.svelte'
  import ResourceOverview from './ResourceOverview.svelte'
  import EventsView from './EventsView.svelte'
  import EventDetail from './EventDetail.svelte'
  import ApplicationDetail from './ApplicationDetail.svelte'
  import { iconForKind } from '$lib/kindIcons'
  import { parse } from 'yaml'
  import { forwards } from '$stores/forwards.svelte'
  import YamlPane from './YamlPane.svelte'
  import Button from './Button.svelte'
  import ToolbarButton from './ToolbarButton.svelte'
  import ToolbarToggle from './ToolbarToggle.svelte'
  import PaneDialog from './PaneDialog.svelte'
  import { withoutManagedFields } from '$lib/manifest'
  import { gitOpsOwner, revertWarning } from '$lib/gitops'
  import GitOpsBadge from './GitOpsBadge.svelte'
  import { organisation } from '$stores/organisation.svelte'
  import {
    preferences,
    detailWidthBounds,
    detailLabelWidthCSS,
    DEFAULT_DETAIL_FRACTION,
    DETAIL_MIN_REM,
    DETAIL_MAX_REM,
    DETAIL_MAX_SHARE,
  } from '$stores/preferences.svelte'
  import DeleteDialog from './DeleteDialog.svelte'
  import ScaleDialog from './ScaleDialog.svelte'
  import RestartDialog from './RestartDialog.svelte'
  import KubectlHint from './KubectlHint.svelte'
  import { apply as kubectlApply, resourceArgForKind } from '$lib/kubectl'
  import TriggerDialog from './TriggerDialog.svelte'
  import SuspendDialog from './SuspendDialog.svelte'
  import CordonDialog from './CordonDialog.svelte'
  import DrainDialog from './DrainDialog.svelte'
  import EvictDialog from './EvictDialog.svelte'
  import SetImageDialog from './SetImageDialog.svelte'
  import Terminal from './Terminal.svelte'
  import DependencyMap from './DependencyMap.svelte'
  import { DeleteResource, RestartRollout } from '$lib/wailsjs/go/wails/ManagementAPI'
  import { ListPodsForWorkload } from '$lib/wailsjs/go/wails/WorkloadAPI'
  import { triggerCronJob, suspendWorkload, cordonNode, evictPod, type Pod } from '$lib/api/client'
  import { podTemplateOf, type PodTemplate } from '$lib/podTemplate'
  import {
    X,
    Info,
    ScrollText,
    TerminalSquare,
    Workflow,
    Activity,
    FileCode,
    RotateCcw,
    Scale,
    ImageUp,
    Pencil,
    Copy,
    Check,
    Trash2,
    Maximize2,
    TriangleAlert,
    Eye,
    EyeOff,
    Plug,
    Loader,
    Play,
    CirclePlay,
    CirclePause,
    Ban,
    LogOut,
  } from '@lucide/svelte'

  interface Props {
    session: ClusterSession
  }

  let { session }: Props = $props()

  type Tab = 'overview' | 'logs' | 'terminal' | 'map' | 'events' | 'yaml'
  let activeTab = $state<Tab>('overview')
  const copied = flash(1500)
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
  let maximized = $state<'yaml' | 'logs' | 'terminal' | 'map' | null>(null)
  let restartDialogOpen = $state(false)
  let triggerDialogOpen = $state(false)
  let suspendDialogOpen = $state(false)
  let cordonDialogOpen = $state(false)
  let drainDialogOpen = $state(false)
  let evictDialogOpen = $state(false)
  let setImageDialogOpen = $state(false)
  let actionError = $state<string | null>(null)
  let workloadPods = $state<Pod[]>([])

  /** "Image updated" after SetImageDialog applies every changed container. The same self-clearing flag as `copied` and `triggered` — see $lib/flash.svelte. */
  const imageUpdated = flash(3000)

  /**
   * Names the Job a "Run now" just created, for a few seconds.
   *
   * The same self-clearing flag LogViewer and RowMenu use for "Copied!" —
   * see $lib/flash.svelte — paired with the name here because the flag
   * itself carries no content of its own.
   */
  const triggered = flash(4000)
  let triggeredJobName = $state('')

  /** The selected kind's own icon, so the drawer is marked like its row. */
  const KindIcon = $derived(
    session.selectedKind ? iconForKind(session.selectedKind) : undefined,
  )

  const isPod = $derived(session.selectedKindId === 'core/v1/pods')

  const isEvent = $derived(session.selectedKindId === 'core/v1/events')
  const isApplication = $derived(!!session.selectedApplication)
  const isSecret = $derived(session.selectedKindId === 'core/v1/secrets')

  /**
   * The guardrails for the group this cluster sits in.
   *
   * Read fresh on every access rather than cached on connect, because an
   * operator can change a group's environment or read-only flag in Organise
   * while a tab for one of its clusters is sitting open right here — the
   * banner and the disabled controls below have to follow that immediately,
   * not on the next reconnect.
   */
  const groupPlacement = $derived(organisation.placementOf(session.cluster.id))
  const groupName = $derived(
    organisation
      .groupsIn(groupPlacement.project)
      .find((group) => group.id === groupPlacement.group)?.name ?? 'Default',
  )
  const groupSettings = $derived(
    organisation.settingsFor(groupPlacement.project, groupPlacement.group),
  )
  const isProduction = $derived(groupSettings.environment === 'production')
  /** Non-null exactly when the production banner below should show. */
  const productionGroup = $derived(isProduction ? groupName : null)
  const isReadOnly = $derived(groupSettings.readOnly)
  // Matches the backend's own message (see app/adapters/wails/errors.go's
  // CodeReadOnly and Terminal.svelte's READ_ONLY_REASON) so an operator sees
  // one sentence for this, however they reach it.
  const readOnlyReason = 'This cluster is marked read-only in PodSteer. Change that under Organise.'

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

  const canEdit = $derived(!!session.manifest && !isEvent && !secretsHidden && !isReadOnly)

  const editHint = $derived(
    isReadOnly
      ? readOnlyReason
      : isEvent
        ? 'An event is a record of something that happened — there is nothing to change'
        : secretsHidden
          ? 'Reveal the values first — saving now would overwrite them with their placeholders'
          : session.manifest
            ? 'Edit YAML'
            : 'Nothing loaded yet',
  )

  /**
   * The kubectl equivalent of Apply: what PodSteer actually sends is the
   * edited manifest itself, so the only thing worth showing is the
   * invocation that would read it from stdin — see $lib/kubectl.apply.
   */
  const applyCommand = $derived(
    kubectlApply(session.cluster.id, session.selectedKind?.namespaced ? session.selectedNamespace : undefined),
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
   * The navigator id for a kind named by its Kubernetes Kind, or null.
   *
   * Resolved against what THIS cluster serves rather than a table compiled in
   * here, which is what makes a link to a CRD work and a link to a kind the
   * account cannot list correctly absent.
   */
  function kindIdFor(kindName: string): string | null {
    return session.kinds.find((kind) => kind.kind === kindName)?.id ?? null
  }

  /**
   * Follows a reference from the open pane to the object it names.
   *
   * The pane is full of names that are really addresses — the node a pod is
   * on, the ReplicaSet that owns it — and until now every one of them was
   * text to be copied and pasted into a search box. Following one closes this
   * drawer and opens that object's, which is the same motion as clicking a
   * row in the list behind it.
   */
  async function openObject(kindName: string, name: string, namespace: string): Promise<void> {
    const kind = session.kinds.find((entry) => entry.kind === kindName)
    if (!kind) return
    // One call, because the two halves are one move: the list has to end up
    // somewhere that contains the object, or the panel opens without the row
    // its live sections are read from. See ClusterSession.openObject.
    await session.openObject(kind.id, name, namespace, kind.namespaced)
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

  // The same three kinds as isRestartable — Deployment, StatefulSet and
  // DaemonSet are exactly the controllers whose pod template sits at
  // spec.template, which is what ManagementPort.SetImage's patch targets.
  // Named separately anyway: the two happen to coincide today, and tying
  // them together would make a future kind that supports one but not the
  // other an awkward split rather than a one-line change.
  const isSetImageable = $derived(
    session.selectedKindId === 'apps/v1/deployments' ||
    session.selectedKindId === 'apps/v1/statefulsets' ||
    session.selectedKindId === 'apps/v1/daemonsets'
  )

  /**
   * The open workload's pod template, for SetImageDialog to list containers
   * from.
   *
   * FROM session.manifest, NEVER FROM THE WATCH STORE — the drawer's own copy
   * of the object's manifest, which is what podTemplateOf's own doc comment
   * requires. Mirrors ResourceOverview's identical derivation.
   */
  const workloadPodTemplate = $derived.by((): PodTemplate | null => {
    if (!isSetImageable || !session.manifest) return null
    try {
      return podTemplateOf(parse(session.manifest), session.selectedKind?.kind)
    } catch {
      return null
    }
  })

  const isCronJob = $derived(session.selectedKindId === 'batch/v1/cronjobs')
  const isJob = $derived(session.selectedKindId === 'batch/v1/jobs')
  const isNode = $derived(session.selectedKindId === 'core/v1/nodes')

  /** The Kubernetes kind of the open workload, or null when it is not one. */
  const mappedWorkloadKind = $derived(
    session.selectedKindId ? (WORKLOAD_KIND_BY_ID[session.selectedKindId] ?? null) : null,
  )

  /** Whether the open object is one of the six controllers. */
  const isWorkloadKind = $derived(Boolean(mappedWorkloadKind))

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

  // Broader than isScalable: Trigger and Suspend act on CronJobs and Jobs
  // too, and ResourceOverview already excludes those two kinds from what it
  // does with this prop, so widening it here is safe.
  const selectedWorkload = $derived(
    isWorkloadKind ? session.workloads.find(w => w.name === session.selectedName && w.namespace === session.selectedNamespace) : null
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

  /**
   * Identifies the object the drawer is showing, WHOLE.
   *
   * The reset below used to watch the name alone, and a name is not an
   * identity here: selecting `postgres-0` in staging and then `postgres-0`
   * in production — routine with StatefulSets, and one click apart in an
   * all-namespaces list — changed nothing this effect could see. The tab
   * stayed put and the pane inside it was never remounted, so an open
   * Terminal went on talking to staging under a header reading production.
   */
  const shownObject = $derived(
    `${session.cluster.id}|${session.selectedKindId}|${session.selectedNamespace}|${session.selectedName ?? ''}`,
  )

  $effect(() => {
    shownObject
    activeTab = 'overview'
    actionError = null
    // A "Created <job>" notice from a Run now on the PREVIOUS object must not
    // keep showing over a different one now open in its place.
    triggered.cancel()
    imageUpdated.cancel()
  })

  /**
   * Arrow keys move between tabs, which is what a tablist is for.
   *
   * Only the selected tab is in the tab order (see `tabindex` below), so this
   * is the only way to reach the others from the keyboard — and it is the way
   * every other tablist works, so nobody has to be told.
   */
  /**
   * Arrow keys resize the panel. Pointer-only before, so a keyboard operator
   * could not narrow a drawer covering the list they were reading. Enter
   * restores the default, matching the double-click.
   */
  function onResizeKeydown(event: KeyboardEvent): void {
    const STEP = 0.02
    let fraction: number
    switch (event.key) {
      // Left widens: the panel is anchored to the right edge, so dragging its
      // handle left makes it bigger, and the key has to agree with the drag.
      case 'ArrowLeft':
        fraction = preferences.detailWidthFraction + STEP
        break
      case 'ArrowRight':
        fraction = preferences.detailWidthFraction - STEP
        break
      case 'Home':
        fraction = 0
        break
      case 'End':
        fraction = 1
        break
      case 'Enter':
        fraction = DEFAULT_DETAIL_FRACTION
        break
      default:
        return
    }
    event.preventDefault()
    preferences.setDetailWidth(fraction)
  }

  function onTabKeydown(event: KeyboardEvent): void {
    const step = event.key === 'ArrowRight' ? 1 : event.key === 'ArrowLeft' ? -1 : 0
    if (step === 0 && event.key !== 'Home' && event.key !== 'End') return

    const shown = tabs.filter((tab) => tab.show())
    if (shown.length === 0) return

    const at = shown.findIndex((tab) => tab.id === activeTab)
    const next =
      event.key === 'Home'
        ? 0
        : event.key === 'End'
          ? shown.length - 1
          : (at + step + shown.length) % shown.length

    event.preventDefault()
    activeTab = shown[next].id
    // The strip is a roving tabindex, so focus has to follow the selection or
    // the next arrow press would be dispatched from a tab that is now -1.
    document.getElementById(`detail-tab-${shown[next].id}`)?.focus()
  }

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
    copied.show()
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

  async function handleTrigger(): Promise<void> {
    if (!selectedWorkload) return
    try {
      const jobName = await triggerCronJob(session.cluster.id, selectedWorkload.namespace, selectedWorkload.name)
      triggerDialogOpen = false
      triggeredJobName = jobName
      triggered.show()
      await session.refresh()
    } catch (error) {
      actionError = `Failed to trigger: ${error}`
    }
  }

  /** Suspends or resumes the selected CronJob or Job. Resume needs no dialog. */
  async function handleSuspend(suspend: boolean): Promise<void> {
    if (!session.selectedKind || !selectedWorkload) return
    try {
      await suspendWorkload(
        session.cluster.id,
        session.selectedKind.kind,
        selectedWorkload.namespace,
        selectedWorkload.name,
        suspend,
      )
      suspendDialogOpen = false
      await session.refresh()
    } catch (error) {
      actionError = `Failed to ${suspend ? 'suspend' : 'resume'}: ${error}`
    }
  }

  /** Cordons or uncordons the selected node. Uncordon needs no dialog — it
   * undoes a visible, deliberate state rather than doing anything the
   * cluster cannot immediately reverse. */
  async function handleCordon(cordon: boolean): Promise<void> {
    if (!session.selectedName) return
    try {
      await cordonNode(session.cluster.id, session.selectedName, cordon)
      cordonDialogOpen = false
      await session.refresh()
    } catch (error) {
      actionError = `Failed to ${cordon ? 'cordon' : 'uncordon'}: ${error}`
    }
  }

  /** Evicts the selected pod through the eviction subresource. */
  async function handleEvict(): Promise<void> {
    if (!session.selectedNamespace || !session.selectedName) return
    try {
      await evictPod(session.cluster.id, session.selectedNamespace, session.selectedName, -1)
      evictDialogOpen = false
      await session.refresh()
    } catch (error) {
      actionError = `Failed to evict: ${error}`
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
    if (event.key !== 'Escape' || !session.selectedName) return
    // Only when nothing nearer holds it. A row menu open inside the drawer
    // used to be closed by the same keystroke that closed the drawer — which
    // ALSO stops an edit, and stopping an edit discards the draft.
    if (!escape?.owns()) return
    session.closeDetail()
  }

  const tabs: { id: Tab; label: string; icon: typeof Info; show: () => boolean }[] = [
    { id: 'overview', label: 'Overview', icon: Info, show: () => true },
    { id: 'logs', label: 'Logs', icon: ScrollText, show: () => isPod || isWorkloadWithLogs },
    { id: 'terminal', label: 'Terminal', icon: TerminalSquare, show: () => isPod || isWorkloadWithLogs },
    // Pods and the six controllers. A pod's map is a chain with the pod in
    // the middle; a workload's is a fan — one controller over the pods it
    // currently has — and both are worth walking, which is what the map is
    // for. Nothing else has dependencies to draw.
    { id: 'map', label: 'Map', icon: Workflow, show: () => isPod || isWorkloadKind },
    // An event has no events of its own, and asking for them returns the
    // empty list that means "nothing recent" — which reads as a fault here
    // rather than as the tautology it is.
    { id: 'events', label: 'Events', icon: Activity, show: () => !isEvent && !isApplication },
    // AN APPLICATION HAS NO MANIFEST. It is a set of objects that agree about
    // a label, so there is nothing to GET by that name, nothing to edit and
    // nothing to delete — and a YAML tab offering to show one would be an
    // empty pane promising an object that does not exist.
    { id: 'yaml', label: 'YAML', icon: FileCode, show: () => !isApplication },
  ]

  // --- Resizing ------------------------------------------------------------
  //
  // The same gesture as the navigator's, mirrored: this panel is anchored to
  // the right, so dragging its left edge LEFTWARD makes it wider.

  let resizing = $state(false)
  /**
   * The width during a drag, in pixels, before it becomes a share.
   *
   * Pixels only while the pointer is down, because that is what a pointer
   * gives: the moment the drag ends it is divided by the window width and
   * stored as a share, which is what survives a different screen. Writing to
   * preferences on every pointermove would serialise the whole preferences
   * payload into a synchronous localStorage.setItem sixty times a second, and
   * the gesture has one outcome worth keeping — where it ended.
   */
  let draggedWidth = $state<number | null>(null)

  /** The root font size, since the panel's floor and ceiling are in rem. */
  function rootFontSize(): number {
    const size = parseFloat(getComputedStyle(document.documentElement).fontSize)
    return Number.isFinite(size) && size > 0 ? size : 16
  }

  function startResize(event: PointerEvent): void {
    event.preventDefault()
    resizing = true
    ;(event.currentTarget as HTMLElement).setPointerCapture(event.pointerId)
  }

  function onResizeMove(event: PointerEvent): void {
    if (!resizing) return

    // From the window's right edge to the pointer. Taken from the pointer's
    // absolute position rather than from a delta against where the drag
    // started: the two diverge as soon as a clamp bites, and the panel then
    // stops following the pointer until it has been dragged all the way back.
    const { min, max } = detailWidthBounds(window.innerWidth, rootFontSize())
    draggedWidth = Math.min(max, Math.max(min, window.innerWidth - event.clientX))
  }

  function endResize(): void {
    if (draggedWidth !== null && window.innerWidth > 0) {
      preferences.setDetailWidth(draggedWidth / window.innerWidth)
    }
    draggedWidth = null
    resizing = false
  }

  /**
   * The drawer's claim on Escape, so a menu opened inside it wins.
   *
   * It matters more here than anywhere: this Escape also stops an edit, and
   * stopping an edit discards the draft.
   */
  let escape = $state<EscapeClaim | null>(null)
  $effect(() => {
    if (!session.selectedName) return
    const held = escapeLayer()
    escape = held
    return () => {
      held.release()
      escape = null
    }
  })

  // Nothing left running behind a component that has gone away.
  $effect(() => () => copied.cancel())
  $effect(() => () => triggered.cancel())
  $effect(() => () => imageUpdated.cancel())
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

<!--
  The one-line warning every write dialog shows on a production cluster —
  see CLAUDE.md, "Where the environment shows". The YAML tab has no dialog
  of its own (editing is a mode of the pane, not a modal), so this is where
  the same fact reaches an operator about to press Apply.
-->
{#snippet productionBanner()}
  {#if productionGroup}
    <p
      class="flex w-full items-start gap-2 rounded-sm border border-error/30 bg-error-container/40
             px-3 py-2 text-body-small text-on-error-container"
    >
      <TriangleAlert class="mt-0.5 size-4 shrink-0" strokeWidth={1.8} />
      This cluster is in {productionGroup}, marked production.
    </p>
  {/if}
{/snippet}

{#snippet logsSurface()}
  <!--
    KEYED ON THE WHOLE IDENTITY, because both panes attach on mount and
    neither reacts to the pod changing under it. Without this, moving between
    two pods of the same name in different clusters left the stream and the
    shell attached to the first — and the terminal's teardown then filed the
    OLD session's id under the NEW pod's key, so the next visit attached to
    the previous cluster's shell and cheerfully reported "Connected".
  -->
  {#key shownObject}
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
  {/key}
{/snippet}

{#snippet mapSurface()}
  {#if isPod && selectedPod}
    <DependencyMap
      clusterId={session.cluster.id}
      namespace={selectedPod.namespace}
      name={selectedPod.name}
      kind="Pod"
      onopen={openObject}
      onmaximize={maximized === 'map' ? undefined : () => (maximized = 'map')}
    />
  {:else if mappedWorkloadKind && session.selectedName}
    <DependencyMap
      clusterId={session.cluster.id}
      namespace={session.selectedNamespace}
      name={session.selectedName}
      kind={mappedWorkloadKind}
      onopen={openObject}
      onmaximize={maximized === 'map' ? undefined : () => (maximized = 'map')}
    />
  {/if}
{/snippet}

{#snippet terminalSurface()}
  {#key shownObject}
    {#if isPod && selectedPod}
    <Terminal
      clusterId={session.cluster.id}
      namespace={selectedPod.namespace}
      podName={selectedPod.name}
      containerName={selectedPod.containers?.[0]?.name ?? ''}
      containers={selectedPod.containers?.map((c) => ({ name: c.name, tty: c.tty })) ?? []}
      readOnly={isReadOnly}
      onmaximize={maximized === 'terminal' ? undefined : () => (maximized = 'terminal')}
    />
    {:else if isWorkloadWithLogs && workloadPods.length > 0}
    <Terminal
      clusterId={session.cluster.id}
      namespace={workloadPods[0].namespace}
      podName={workloadPods[0].name}
      containerName={workloadPods[0].containers?.[0]?.name ?? ''}
      containers={workloadPods[0].containers?.map((c: any) => ({ name: c.name, tty: c.tty })) ?? []}
      readOnly={isReadOnly}
      onmaximize={maximized === 'terminal' ? undefined : () => (maximized = 'terminal')}
    />
    {/if}
  {/key}
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
      {:else if isSecret && session.secretsRevealed}
        <!--
          And the way back. Revealing used to replace this control with
          nothing, so re-masking a Secret meant closing the panel and opening
          it again — an oversight rather than a policy, and one the reveal on
          an environment variable does not share.
        -->
        <ToolbarButton
          icon={EyeOff}
          label="Hide values"
          title="Put the values back behind their placeholders"
          onclick={() => session.hideManifestSecrets()}
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
        icon={copied.on ? Check : Copy}
        label="Copy manifest"
        title={copied.on ? 'Copied' : 'Copy manifest'}
        active={copied.on}
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

<!--
  Masks a revealed Secret when the window stops being looked at — which in
  practice is the moment somebody alt-tabs to start a screen share or accepts
  a call. The same rule the environment-variable reveal follows.

  NOT WHILE EDITING. Re-masking re-reads the manifest, so doing it under
  somebody mid-edit would throw their work away to hide a value they are
  deliberately working with. A revealed Secret behind an unsaved edit is the
  one case where leaving it on screen is the lesser harm.

  No timer, deliberately, unlike the environment-variable reveal: that hides
  one value somebody glanced at, while this is a manifest being read, and
  expiring it every thirty seconds would mean repeatedly re-asking — turning
  one audited read into a dozen.
-->
<svelte:window
  onkeydown={onKeydown}
  onblur={() => {
    if (isSecret && session.secretsRevealed && !editing) void session.hideManifestSecrets()
  }}
/>

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

  <!--
    A SHARE OF THE WINDOW, CLAMPED AT BOTH ENDS. The panel used to be a fixed
    44rem, which is about half a laptop and about a fifth of an ultrawide — and
    the complaint it answers is relative, because what matters is how much of
    the list behind it is still readable. A share is also what transfers
    between people: the same setting means the same thing on a 13-inch laptop
    and on a 34-inch monitor, which a pixel width does not.

    A share alone breaks in the other direction, which is what the clamp is
    for: a quarter of a small laptop is narrower than one row of this panel's
    own two columns, and half an ultrawide is a page of whitespace. `min` with
    90vw on top of that, so a very narrow window still shows the list is there.

    In CSS rather than measured in JavaScript, so the panel keeps its share
    when the window is resized without anything listening for it. A drag in
    progress is the one time a pixel width is used — see draggedWidth.
  -->
  <!--
    A DIALOG, AND DELIBERATELY NOT aria-modal. This had a label and no role at
    all, so the name was announced on nothing. `dialog` is what it is: it sits
    over a full-window scrim and Escape closes it.

    aria-modal is left off on purpose. It tells assistive technology that
    nothing outside exists, and honouring that claim means trapping Tab —
    right for a dialog asking one question, wrong for a browsing surface
    people move in and out of while reading the list behind it. The dialogs
    that DO make the claim use `use:modal`, which keeps it.

    A <div> rather than the <aside> this was, because an aside means
    complementary content and a dialog is not that — and Svelte's own a11y
    check says so.
  -->
  <div
    style="--detail-label-width: {detailLabelWidthCSS(preferences.detailLabelShare)}; width: {draggedWidth !== null
      ? `${draggedWidth}px`
      : `min(${DETAIL_MAX_SHARE * 100}vw, clamp(${DETAIL_MIN_REM}rem, ${
          preferences.detailWidthFraction * 100
        }vw, ${DETAIL_MAX_REM}rem))`}"
    class="fixed top-0 right-0 bottom-0 z-50 flex flex-col
           border-l border-outline-variant/60 bg-surface shadow-level-3"
    role="dialog"
    aria-label="Object details"
  >
    <!--
      Drag the edge to resize, the same gesture and the same handle as the
      navigator on the other side of the window — one of these being
      draggable and the other not would be two ideas about the same thing.

      What is STORED is still a share, so a width chosen here means the same
      on somebody else's screen. Double-click restores the default.
    -->
    <!--
      svelte-ignore a11y_no_noninteractive_tabindex, a11y_no_noninteractive_element_interactions

      Both warnings are false here — see ColumnDivider.svelte. A focusable
      separator is the window-splitter pattern.
    -->
    <span
      role="separator"
      aria-orientation="vertical"
      aria-label="Resize the detail panel"
      aria-valuenow={Math.round(preferences.detailWidthFraction * 100)}
      aria-valuemin={0}
      aria-valuemax={100}
      aria-valuetext="{Math.round(preferences.detailWidthFraction * 100)}% of the window"
      tabindex="0"
      onkeydown={onResizeKeydown}
      class="absolute top-0 -left-1 z-20 h-full w-2 cursor-col-resize
             after:absolute after:top-0 after:left-1/2 after:h-full after:w-px
             after:-translate-x-1/2 after:bg-transparent after:transition-colors
             after:duration-100 hover:after:bg-primary/50 {resizing
        ? 'after:w-0.5 after:bg-primary'
        : ''}"
      onpointerdown={startResize}
      onpointermove={onResizeMove}
      onpointerup={endResize}
      onpointercancel={endResize}
      ondblclick={() => preferences.setDetailWidth(DEFAULT_DETAIL_FRACTION)}
    ></span>
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
        <h2 class="flex min-w-0 items-center gap-2">
          <span class="truncate text-title-medium font-semibold text-on-surface" data-selectable>
            {session.selectedName}
          </span>

          <!--
            Repeated from the list, because a forward moves between pods when
            one is replaced and this pane is where somebody arrives to check
            whether THIS is the pod holding it.
          -->
          {#each forwards.forPod(session.cluster.id, session.selectedNamespace, session.selectedName ?? '') as forward (forward.id)}
            <span
              class="inline-flex shrink-0 items-center gap-1 rounded bg-primary/12 px-1.5
                     text-body-small text-primary"
              title={forward.reconnecting
                ? `Waiting for a replacement pod — was ${forward.address}`
                : `${forward.address} → container port ${forward.remotePort}`}
            >
              {#if forward.reconnecting}
                <Loader class="size-3 animate-spin" strokeWidth={2} />
              {:else}
                <Plug class="size-3" strokeWidth={2} />
              {/if}
              {forward.localPort}
            </span>
          {/each}
        </h2>

        <!-- Kind, then namespace, which is where it lives. The namespace is a
             link because it is somewhere to go: it filters the whole
             application to that namespace, which is what somebody reading a
             detail usually wants next. -->
        <!--
          CENTRED, NOT BASELINED. A flex container's baseline is its first
          item's, and the badge beside this text is itself a flex box whose
          first item is an icon — so an SVG's bottom edge was being lined up
          with the text's baseline, which sat the whole pill a few pixels
          high. Every item on this line is the same size, so centring reads
          identically for the text and correctly for the pill.
        -->
        <p class="flex min-w-0 items-center gap-1.5 text-body-small text-on-surface-variant/70">
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
      <!-- NEVER HIDDEN FOR READ-ONLY. A disabled button with a reason is a
           feature somebody can find and understand; a missing one reads as a
           feature that does not exist. See CLAUDE.md's read-only section. -->
      {#if isReadOnly}
        <p id="drawer-readonly-hint" class="sr-only">{readOnlyReason}</p>
      {/if}
      <div class="flex items-center gap-0.5">
        {#if isRestartable}
          <button
            type="button"
            onclick={() => (restartDialogOpen = true)}
            disabled={isReadOnly}
            aria-label="Restart rollout"
            aria-describedby={isReadOnly ? 'drawer-readonly-hint' : undefined}
            title={isReadOnly ? readOnlyReason : 'Restart rollout'}
            class="state-layer grid size-8 shrink-0 place-items-center rounded-full
                   text-on-surface-variant transition-colors duration-100 hover:bg-surface-container hover:text-on-surface
                   disabled:pointer-events-none disabled:opacity-38"
          >
            <RotateCcw class="size-4" strokeWidth={1.8} />
          </button>
        {/if}

        {#if isScalable}
          <button
            type="button"
            onclick={() => (scaleDialogOpen = true)}
            disabled={isReadOnly}
            aria-label="Scale"
            aria-describedby={isReadOnly ? 'drawer-readonly-hint' : undefined}
            title={isReadOnly ? readOnlyReason : 'Scale replicas'}
            class="state-layer grid size-8 shrink-0 place-items-center rounded-full
                   text-on-surface-variant transition-colors duration-100 hover:bg-surface-container hover:text-on-surface
                   disabled:pointer-events-none disabled:opacity-38"
          >
            <Scale class="size-4" strokeWidth={1.8} />
          </button>
        {/if}

        {#if isSetImageable}
          <button
            type="button"
            onclick={() => (setImageDialogOpen = true)}
            disabled={isReadOnly}
            aria-label="Set image"
            aria-describedby={isReadOnly ? 'drawer-readonly-hint' : undefined}
            title={isReadOnly ? readOnlyReason : 'Set image'}
            class="state-layer grid size-8 shrink-0 place-items-center rounded-full
                   text-on-surface-variant transition-colors duration-100 hover:bg-surface-container hover:text-on-surface
                   disabled:pointer-events-none disabled:opacity-38"
          >
            <ImageUp class="size-4" strokeWidth={1.8} />
          </button>
        {/if}

        <!-- "Run now" — CronJobs only. Creates a Job outside the schedule;
             see TriggerDialog for what that means for history limits. -->
        {#if isCronJob}
          <button
            type="button"
            onclick={() => (triggerDialogOpen = true)}
            aria-label="Run now"
            title="Run now"
            class="state-layer grid size-8 shrink-0 place-items-center rounded-full
                   text-on-surface-variant transition-colors duration-100 hover:bg-surface-container hover:text-on-surface"
          >
            <Play class="size-4" strokeWidth={1.8} />
          </button>
        {/if}

        <!-- Suspend/Resume — CronJobs and Jobs. Resume acts immediately with
             no dialog: it undoes a visible, deliberate state rather than
             doing anything destructive, unlike suspending a running Job. -->
        {#if (isCronJob || isJob) && selectedWorkload}
          {@const workload = selectedWorkload}
          <button
            type="button"
            onclick={() => (workload.suspended ? handleSuspend(false) : (suspendDialogOpen = true))}
            aria-label={workload.suspended ? 'Resume' : 'Suspend'}
            title={workload.suspended ? 'Resume' : 'Suspend'}
            class="state-layer grid size-8 shrink-0 place-items-center rounded-full
                   text-on-surface-variant transition-colors duration-100 hover:bg-surface-container hover:text-on-surface"
          >
            {#if workload.suspended}
              <CirclePlay class="size-4" strokeWidth={1.8} />
            {:else}
              <CirclePause class="size-4" strokeWidth={1.8} />
            {/if}
          </button>
        {/if}

        <!-- Cordon/Uncordon and Drain — nodes only. Uncordon acts at once,
             the same reasoning as Resume above; cordoning and draining both
             open a dialog because each changes what the cluster is willing
             to schedule, cordoning implicitly and draining by force. -->
        {#if isNode}
          <button
            type="button"
            onclick={() => (session.selectedNode?.unschedulable ? handleCordon(false) : (cordonDialogOpen = true))}
            aria-label={session.selectedNode?.unschedulable ? 'Uncordon' : 'Cordon'}
            title={session.selectedNode?.unschedulable ? 'Uncordon' : 'Cordon'}
            class="state-layer grid size-8 shrink-0 place-items-center rounded-full
                   text-on-surface-variant transition-colors duration-100 hover:bg-surface-container hover:text-on-surface"
          >
            <Ban class="size-4 {session.selectedNode?.unschedulable ? 'text-warning' : ''}" strokeWidth={1.8} />
          </button>

          <button
            type="button"
            onclick={() => (drainDialogOpen = true)}
            aria-label="Drain…"
            title="Drain node"
            class="state-layer grid size-8 shrink-0 place-items-center rounded-full
                   text-on-surface-variant transition-colors duration-100 hover:bg-surface-container hover:text-on-surface"
          >
            <LogOut class="size-4" strokeWidth={1.8} />
          </button>
        {/if}

        <!-- Evict — pods only. Distinct from Delete: goes through the
             eviction subresource, which a PodDisruptionBudget can refuse. -->
        {#if isPod}
          <button
            type="button"
            onclick={() => (evictDialogOpen = true)}
            aria-label="Evict"
            title="Evict pod"
            class="state-layer grid size-8 shrink-0 place-items-center rounded-full
                   text-on-surface-variant transition-colors duration-100 hover:bg-surface-container hover:text-on-surface"
          >
            <LogOut class="size-4" strokeWidth={1.8} />
          </button>
        {/if}

        <!-- Edit and Copy used to sit here. They act on the manifest, so they
             now live in the YAML tab's toolbar beside it — a control belongs
             next to the thing it changes, and from the Overview tab "Copy"
             gave no clue that what landed on the clipboard was YAML. Delete
             stays: it acts on the object, not on any one view of it.

             Absent for an application, which is not an object: there is
             nothing to delete that is not one of its members, and a control
             offering to would either do nothing or do far more than it
             says. -->
        {#if !isApplication}
        <button
          type="button"
          onclick={() => (deleteDialogOpen = true)}
          disabled={isReadOnly}
          aria-label="Delete"
          aria-describedby={isReadOnly ? 'drawer-readonly-hint' : undefined}
          title={isReadOnly ? readOnlyReason : 'Delete resource'}
          class="state-layer grid size-8 shrink-0 place-items-center rounded-full
                 text-on-surface-variant transition-colors duration-100 hover:bg-error/10 hover:text-error
                 disabled:pointer-events-none disabled:opacity-38"
        >
          <Trash2 class="size-4" strokeWidth={1.8} />
        </button>
        {/if}

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

    <!--
      Tabs, WITH THE SEMANTICS OF TABS. These were six plain buttons in a row,
      with the active one marked by colour and a two-pixel underline and
      nothing else — so a screen reader heard six identical buttons, could not
      tell which pane was showing, and paid six Tab stops to reach the content
      on every object opened.

      A real tablist costs one Tab stop for the whole strip and moves between
      tabs with the arrow keys, which is both the standard and less work for
      everybody.
    -->
    <div
      class="flex border-b border-outline-variant/60 bg-surface-container-low/50 px-2"
      role="tablist"
      aria-label="Detail views"
      tabindex={-1}
      onkeydown={onTabKeydown}
    >
      {#each tabs as tab (tab.id)}
        {#if tab.show()}
          {@const TabIcon = tab.icon}
          {@const active = activeTab === tab.id}
          <button
            type="button"
            role="tab"
            id="detail-tab-{tab.id}"
            aria-selected={active}
            aria-controls="detail-panel"
            tabindex={active ? 0 : -1}
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

    <!-- "Run now" success notice: names the Job it created, since the
         operator has no other way to find it among the CronJob's history
         without knowing what to look for. -->
    {#if triggered.on}
      <div class="flex items-center gap-2 border-b border-success/20 bg-success-container/50 px-4 py-2 text-body-small text-on-success-container">
        <Check class="size-3.5 shrink-0 text-success" strokeWidth={2} />
        Created job <strong data-selectable>{triggeredJobName}</strong>
      </div>
    {/if}

    {#if imageUpdated.on}
      <div class="flex items-center gap-2 border-b border-success/20 bg-success-container/50 px-4 py-2 text-body-small text-on-success-container">
        <Check class="size-3.5 shrink-0 text-success" strokeWidth={2} />
        Image updated
      </div>
    {/if}

    <!-- Tab content -->
    <div
      class="min-h-0 flex-1 overflow-auto bg-surface-container-lowest"
      id="detail-panel"
      role="tabpanel"
      aria-labelledby="detail-tab-{activeTab}"
    >
      {#if activeTab === 'overview' && session.selectedApplication}
        <!-- An application is not an object: nothing to fetch, nothing to
             edit, nothing to delete. Its pane is built from the row. -->
        <ApplicationDetail
          application={session.selectedApplication}
          usage={session.usage}
          onbrowse={(kindId, namespace) => void session.browseKind(kindId, namespace)}
          onnamespace={(namespace) => void session.selectNamespace(namespace)}
        />
      {:else if activeTab === 'overview' && isEvent}
        <!-- The same reference-following the generic overview gets: an event
             names an object, a node and a namespace, and each is somewhere to
             go. `kindIdFor` is what keeps a link off a kind this cluster does
             not serve — a CRD removed since the event fired, or nodes an
             account cannot list. -->
        <EventDetail
          event={parsedEvent}
          canOpen={kindIdFor}
          onopen={openObject}
          onnamespace={(namespace) => void session.selectNamespace(namespace)}
        />
      {:else if activeTab === 'overview'}
        <ResourceOverview
          manifest={session.manifest}
          selectedPod={selectedPod}
          selectedNode={session.selectedNode}
          selectedNamespaceRow={session.selectedNamespaceRow}
          selectedWorkload={selectedWorkload}
          kind={session.selectedKind?.kind}
          usage={session.usage}
          backend={session.overview?.backend}
          clusterId={session.cluster.id}
          canOpen={kindIdFor}
          onopen={openObject}
          onnamespace={(namespace) => void session.selectNamespace(namespace)}
          onbrowse={(kindId, namespace) => void session.browseKind(kindId, namespace)}
          tick={session.lastRefreshedAt}
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
        {#if maximized === 'terminal'}
          <!-- The pane is in the dialog. Saying so beats an empty tab, which
               reads as a pane that failed to load. -->
          <div class="flex h-full items-center justify-center p-4">
            <p class="text-body-medium text-on-surface-variant/70">
              Showing the terminal in a larger window.
            </p>
          </div>
        {:else if isPod && selectedPod}
          {@render terminalSurface()}
        {:else if isWorkloadWithLogs && workloadPods.length > 0}
          {@render terminalSurface()}
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
      {:else if activeTab === 'map'}
        {#if maximized === 'map'}
          <!-- The pane is in the dialog. Saying so beats an empty tab, which
               reads as a pane that failed to load. -->
          <div class="flex h-full items-center justify-center p-4">
            <p class="text-body-medium text-on-surface-variant/70">
              Showing the map in a larger window.
            </p>
          </div>
        {:else if (isPod && selectedPod) || mappedWorkloadKind}
          {@render mapSurface()}
        {:else}
          <div class="flex h-full flex-col items-center justify-center gap-2 p-4 text-on-surface-variant/60">
            <Workflow class="size-8" strokeWidth={1.2} />
            <p class="text-body-medium">This kind has no dependencies to map</p>
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
        {@render productionBanner()}
        {@render revertNotice()}
        <KubectlHint command={applyCommand} />
        <div class="flex items-center justify-end gap-3">
          <Button variant="outlined" onclick={stopEditing}>Cancel</Button>
          <Button variant="filled" disabled={isReadOnly} onclick={applyEdit}>Apply</Button>
        </div>
      </div>
    {/if}
  </div>

  <!-- Dialogs -->
  <DeleteDialog
    open={deleteDialogOpen}
    resourceName={session.selectedName}
    resourceKind={session.selectedKind?.singular ?? 'resource'}
    ctx={session.cluster.id}
    resource={session.selectedKind ? resourceArgForKind(session.selectedKind) : ''}
    namespace={session.selectedNamespace}
    {productionGroup}
    onclose={() => (deleteDialogOpen = false)}
    onconfirm={handleDelete}
  />

  {#if selectedWorkload && mappedWorkloadKind}
    <ScaleDialog
      open={scaleDialogOpen}
      currentReplicas={selectedWorkload.desired}
      ctx={session.cluster.id}
      kind={mappedWorkloadKind}
      name={selectedWorkload.name}
      namespace={selectedWorkload.namespace}
      checkAutoscalers={session.autoscalersFor}
      canOpen={kindIdFor}
      onopen={openObject}
      {productionGroup}
      onclose={() => (scaleDialogOpen = false)}
      onconfirm={handleScale}
    />

    <SetImageDialog
      open={setImageDialogOpen}
      ctx={session.cluster.id}
      kind={mappedWorkloadKind}
      name={selectedWorkload.name}
      namespace={selectedWorkload.namespace}
      template={workloadPodTemplate}
      {productionGroup}
      onclose={() => (setImageDialogOpen = false)}
      onapplied={async () => {
        setImageDialogOpen = false
        imageUpdated.show()
        await session.refresh()
      }}
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
        {@render productionBanner()}
        {@render revertNotice()}
        <!-- flex-1, the same as revertNotice: this row is justify-end, so
             whatever is not a button has to claim the leading space itself
             or the row centres on nothing. -->
        <div class="min-w-0 flex-1">
          <KubectlHint command={applyCommand} />
        </div>
        <Button variant="outlined" onclick={stopEditing}>Cancel</Button>
        <Button variant="filled" disabled={isReadOnly} onclick={applyEdit}>Apply</Button>
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

  <PaneDialog
    open={maximized === 'terminal'}
    icon={KindIcon}
    kind={session.selectedKind?.singular}
    name={session.selectedName ?? ''}
    label="Terminal"
    onclose={() => (maximized = null)}
  >
    {@render terminalSurface()}
  </PaneDialog>

  <PaneDialog
    open={maximized === 'map'}
    icon={KindIcon}
    kind={session.selectedKind?.singular}
    name={session.selectedName ?? ''}
    label="Map"
    onclose={() => (maximized = null)}
  >
    {@render mapSurface()}
  </PaneDialog>

  {#if selectedWorkload}
    <RestartDialog
      open={restartDialogOpen}
      workloadName={selectedWorkload.name}
      workloadKind={session.selectedKind?.singular ?? 'workload'}
      ctx={session.cluster.id}
      namespace={selectedWorkload.namespace}
      {productionGroup}
      onclose={() => (restartDialogOpen = false)}
      onconfirm={async () => {
        restartDialogOpen = false
        await handleRestart()
      }}
    />

    <TriggerDialog
      open={triggerDialogOpen}
      workloadName={selectedWorkload.name}
      onclose={() => (triggerDialogOpen = false)}
      onconfirm={handleTrigger}
    />

    <SuspendDialog
      open={suspendDialogOpen}
      workloadName={selectedWorkload.name}
      workloadKind={session.selectedKind?.kind ?? 'CronJob'}
      onclose={() => (suspendDialogOpen = false)}
      onconfirm={() => handleSuspend(true)}
    />
  {/if}

  {#if isNode}
    <CordonDialog
      open={cordonDialogOpen}
      nodeName={session.selectedName}
      onclose={() => (cordonDialogOpen = false)}
      onconfirm={() => handleCordon(true)}
    />

    <DrainDialog
      open={drainDialogOpen}
      clusterId={session.cluster.id}
      nodeName={session.selectedName}
      onclose={() => (drainDialogOpen = false)}
      ondrained={() => void session.refresh()}
      onerror={(message) => (actionError = `Failed to drain: ${message}`)}
    />
  {/if}

  {#if isPod}
    <EvictDialog
      open={evictDialogOpen}
      podName={session.selectedName}
      onclose={() => (evictDialogOpen = false)}
      onconfirm={handleEvict}
    />
  {/if}
{/if}
