<!--
  The topology: every object in a scope and every relationship between them,
  as one map. The dependency map's FIFTH shape, and the only one with no
  subject — it answers "what is here and how does it connect" for a namespace,
  several, or the cluster.

  ITS OWN PAGE, NOT A WIDER DependencyMap. That component is drawn around one
  object and fits itself to it; this one is drawn around nothing, can be five
  thousand boxes, and must hold still while somebody reads it. What is shared
  is shared as libraries — graphFold's folding rules, graphIcons' icons,
  graphLayout's dagre — and each step of the pipeline is its own tested module:

    kind toggles → fold → group        (graphGroup.ts — what is drawn)
    → layout, in a worker              (layoutClient.ts — latest request wins)
    → positions                        (graphPositions.ts — never move on a redraw)
    → cull                             (graphCull.ts — only what is on screen)
    → draw                             (here)

  IT NEVER REDRAWS ON A TICK. The graph is read when the page opens, when the
  scope changes and when somebody presses Refresh. Between those the backend's
  change feed says something in the scope changed, and the page says
  "Changed — Refresh" instead of moving the map; Live, opted into, redraws
  after a debounce that grows with the map. A redraw keeps every box where it
  was when the drawn shape is unchanged, and otherwise keeps the box nearest
  the centre of the pane where it was.

  EVERY LINE IS A RELATIONSHIP KUBERNETES HAS; the counts are complete; a set
  is as unwell as its worst member. Folding and grouping change what is drawn,
  never what the page says it found.
-->
<script lang="ts">
  import { onDestroy, tick, untrack, type Snippet } from 'svelte'
  import { toApiError } from '$lib/api/errors'
  import { ALL_NAMESPACES } from '$lib/api/client'
  import type { ClusterSession, TopologyScope } from '$stores/session.svelte'
  import { preferences } from '$stores/preferences.svelte'
  import { iconGeometry } from '$lib/graphIcons'
  import { GROUP_HEADER, type CompoundLayout, type PlacedNode } from '$lib/graphLayout'
  import {
    describeCounts,
    foldView,
    group,
    hideGroups,
    kindCounts,
    toView,
    totalOf,
    type GroupBy,
    type GroupedGraph,
    type FilteredGroups,
    type FoldedView,
    type ViewNode,
  } from '$lib/graphGroup'
  import { buildCullIndex, visible, viewportOf } from '$lib/graphCull'
  import { preserve, shapeKey, type Drawn } from '$lib/graphPositions'
  import { createLayoutClient, liveDelayMs, Superseded } from '$lib/layoutClient'
  import { centreOn, locate, searchNodes } from '$lib/topologySearch'
  import { badgeFor, indexFindings, type FindingBadge } from '$lib/findingsOverlay'
  import { renderPng, topologyFilename } from '$lib/pngExport'
  import { changeConcerns, exportTopologyPNG, onTopologyChanged, releaseTopology, topology } from '$lib/topology/api'
  import { buildOverlay, type TrafficFilters, type TrafficOverlay } from '$lib/trafficLayer'
  import TrafficPanel from '$lib/components/TrafficPanel.svelte'
  import type { TopologyGraph, TopologyNode, TrafficLayer } from '$lib/topology/contract'
  import type { DecorationTone, EdgeDecoration, TopologyDecorator } from '$lib/topology/decorations'
  import PaneToolbar from '$lib/components/PaneToolbar.svelte'
  import ToolbarButton from '$lib/components/ToolbarButton.svelte'
  import Select from '$lib/components/Select.svelte'
  import ToolbarSearch from '$lib/components/ToolbarSearch.svelte'
  import HelpButton from '$lib/components/HelpButton.svelte'
  import { help } from '$stores/help.svelte'
  import type { HelpSection } from '$lib/help'
  import {
    Activity,
    ChevronDown,
    SlidersHorizontal,
    Columns3,
    Crosshair,
    ImageDown,
    Layers,
    RefreshCw,
    Rows3,
    ZoomIn,
    ZoomOut,
  } from '@lucide/svelte'

  interface Props {
    session: ClusterSession
    /**
     * Observed traffic to draw, handed in rather than asked for — what a test
     * or another host does. Without it the page's own TrafficPanel supplies
     * the layer when somebody switches it on. Either way it is drawn as an
     * OVERLAY (buildOverlay), never as edges of the graph.
     */
    traffic?: TrafficLayer
    /** Decorates the dependency lines and draws a layer's own lines. */
    decorator?: TopologyDecorator
    /** Replaces the built-in TrafficPanel, rendered in the layer strip. */
    trafficControls?: Snippet
  }

  let { session, traffic, decorator, trafficControls }: Props = $props()

  // --- Scope ---------------------------------------------------------------

  const scope = $derived(session.topologyScopeNow)
  const scopeKey = $derived(
    `${session.cluster.id}|${scope.all ? '*' : [...scope.namespaces].sort().join(',')}`,
  )
  const scopeLabel = $derived(
    scope.all
      ? 'All namespaces'
      : scope.namespaces.length === 0
        ? 'No namespace'
        : scope.namespaces.length <= 2
          ? scope.namespaces.join(', ')
          : `${scope.namespaces.length} namespaces`,
  )

  // The namespace filter SEEDS the scope; moving it while this page is open
  // means "draw that one", so a scope chosen here gives way to it.
  let seenNamespace = untrack(() => session.namespace)
  $effect(() => {
    const current = session.namespace
    if (current === seenNamespace) return
    seenNamespace = current
    session.topologyScope =
      current === ALL_NAMESPACES ? { namespaces: [], all: true } : { namespaces: [current], all: false }
  })

  let scopeOpen = $state(false)
  let draftAll = $state(false)
  let draftNamespaces = $state<string[]>([])
  let scopeFilter = $state('')

  function openScope(): void {
    draftAll = scope.all
    draftNamespaces = [...scope.namespaces]
    scopeFilter = ''
    scopeOpen = true
    void session.refreshNamespaces()
  }

  function applyScope(): void {
    const next: TopologyScope = draftAll
      ? { namespaces: [], all: true }
      : { namespaces: [...draftNamespaces].sort(), all: false }
    session.topologyScope = next
    scopeOpen = false
  }

  function toggleDraft(name: string): void {
    draftNamespaces = draftNamespaces.includes(name)
      ? draftNamespaces.filter((n) => n !== name)
      : [...draftNamespaces, name]
  }

  const namespaceChoices = $derived(
    session.namespaces
      .map((namespace) => namespace.name)
      .filter((name) => !scopeFilter || name.toLowerCase().includes(scopeFilter.toLowerCase())),
  )

  // --- The graph -----------------------------------------------------------

  let graph = $state.raw<TopologyGraph | null>(null)
  let loading = $state(false)
  let failure = $state('')
  let loadedFor = $state('')
  /** Fit the view to the next layout: true for a new scope, false for a redraw. */
  let fitNext = true
  let request = 0

  async function load(background: boolean): Promise<void> {
    const key = scopeKey
    const asked = { ...scope, namespaces: [...scope.namespaces] }
    const mine = ++request
    loading = true
    if (!background) failure = ''
    try {
      const next = await topology(session.cluster.id, asked.namespaces, asked.all)
      if (mine !== request) return
      if (loadedFor !== key) {
        // A different scope's sets and groups are not this one's.
        expandedFolds = new Set()
        collapsedGroups = new Set()
        fitNext = true
        drawn = null
      }
      graph = next
      loadedFor = key
      failure = ''
      changed = false
    } catch (error) {
      if (mine !== request) return
      failure = toApiError(error).message
      if (!background) graph = null
    } finally {
      if (mine === request) loading = false
    }
  }

  $effect(() => {
    if (scopeKey !== loadedFor && !(scope.namespaces.length === 0 && !scope.all)) void load(false)
  })

  // The backend feeds `topology:changed` for every scope drawn; when this page
  // goes away — or moves to another cluster — that cluster's feed is released
  // rather than left to expire.
  $effect(() => {
    const cluster = session.cluster.id
    return () => void releaseTopology(cluster)
  })

  // --- What is drawn -------------------------------------------------------

  /** Kinds switched off. The counts beside each toggle stay complete. */
  let hiddenKinds = $state.raw<Set<string>>(new Set())
  let expandedFolds = $state.raw<Set<string>>(new Set())
  let collapsedGroups = $state.raw<Set<string>>(new Set())

  type GroupChoice = 'none' | 'namespace' | 'app' | 'label'
  let groupChoice = $state<GroupChoice>('namespace')
  let groupLabel = $state('app.kubernetes.io/part-of')
  const groupBy = $derived<GroupBy>(
    groupChoice === 'label' ? (groupLabel.trim() ? { label: groupLabel.trim() } : 'none') : groupChoice,
  )

  // A kind with nothing of it in this scope is not a choice: no "Pod 0".
  const toggles = $derived(graph ? kindCounts(graph).filter((toggle) => toggle.count > 0) : [])
  const view = $derived(graph ? toView(graph, hiddenKinds) : null)
  const folded = $derived<FoldedView | null>(view ? foldView(view, expandedFolds) : null)
  /** Every group, before any application is unticked — what the app menu lists. */
  const groupedAll = $derived<GroupedGraph | null>(folded ? group(folded, groupBy, collapsedGroups) : null)
  /**
   * Applications left off the drawing. Held here and written through to the
   * session, which keeps it per cluster for when the page is opened again.
   */
  let unticked = $state.raw<ReadonlySet<string>>(untrack(() => session.topologyHiddenApps ?? new Set()))
  /** Only while grouping by application: under any other grouping all are drawn. */
  const hiddenApps = $derived<ReadonlySet<string>>(groupChoice === 'app' ? unticked : new Set())

  function setUnticked(next: ReadonlySet<string>): void {
    unticked = next
    session.topologyHiddenApps = next
  }
  const grouped = $derived<FilteredGroups | null>(groupedAll ? hideGroups(groupedAll, hiddenApps) : null)

  // --- The application menu -------------------------------------------------

  let appsOpen = $state(false)
  let appFilter = $state('')
  const appChoices = $derived(
    groupChoice === 'app'
      ? (groupedAll?.groups ?? []).filter(
          (g) => !appFilter || g.label.toLowerCase().includes(appFilter.toLowerCase()),
        )
      : [],
  )
  const appCount = $derived(groupChoice === 'app' ? (groupedAll?.groups.length ?? 0) : 0)
  const appsLabel = $derived(
    hiddenApps.size === 0 ? 'All applications' : `${appCount - (grouped?.hidden.length ?? 0)} of ${appCount} applications`,
  )

  function toggleApp(id: string): void {
    const next = new Set(unticked)
    if (next.has(id)) next.delete(id)
    else next.add(id)
    setUnticked(next)
  }

  function showAllApps(all: boolean): void {
    setUnticked(all ? new Set() : new Set((groupedAll?.groups ?? []).map((g) => g.id)))
  }

  const orientation = $derived(preferences.mapOrientation)
  const horizontal = $derived(orientation === 'horizontal')

  function toggleKind(apiKind: string): void {
    const next = new Set(hiddenKinds)
    if (next.has(apiKind)) next.delete(apiKind)
    else next.add(apiKind)
    hiddenKinds = next
  }

  function toggleFold(id: string): void {
    const next = new Set(expandedFolds)
    if (next.has(id)) next.delete(id)
    else next.add(id)
    expandedFolds = next
  }

  function toggleGroup(id: string): void {
    const next = new Set(collapsedGroups)
    if (next.has(id)) next.delete(id)
    else next.add(id)
    collapsedGroups = next
  }

  const allCollapsed = $derived(
    Boolean(grouped?.groups.length) && grouped!.groups.every((g) => collapsedGroups.has(g.id)),
  )

  function toggleAllGroups(): void {
    collapsedGroups = allCollapsed ? new Set() : new Set((grouped?.groups ?? []).map((g) => g.id))
  }

  // --- Layout --------------------------------------------------------------

  const layouts = createLayoutClient()
  onDestroy(() => layouts.dispose())

  /** What is on screen: the layout, and the graph it is a layout of. */
  let drawn = $state.raw<Drawn | null>(null)
  let drawnGraph = $state.raw<{ grouped: GroupedGraph; folded: FoldedView } | null>(null)
  let layingOut = $state(false)
  let layoutMs = $state(0)
  let layoutFailure = $state('')

  $effect(() => {
    const target = grouped
    const foldedNow = folded
    const across = horizontal
    if (!target || !foldedNow) return

    const parents = [...target.parents]
    const shape = `${across ? 'h' : 'v'}|${shapeKey(
      target.nodes.map((n) => n.id),
      target.parents,
    )}`

    layingOut = true
    layouts
      .layout({
        nodes: target.nodes.map((node) => ({ id: node.id })),
        edges: target.edges.map((edge) => ({ id: edge.id, from: edge.from, to: edge.to })),
        parents,
        horizontal: across,
      })
      .then(({ layout, ms }) => {
        layoutMs = ms
        layoutFailure = ''
        const viewNow = { panX, panY, zoom, width: paneWidth, height: paneHeight }
        const kept = preserve(fitNext ? null : drawn, { layout, shape }, viewNow, across)
        drawn = { layout: kept.layout, shape }
        drawnGraph = { grouped: target, folded: foldedNow }
        panX = kept.panX
        panY = kept.panY
        layingOut = false
        if (fitNext) {
          fitNext = false
          fit()
        }
      })
      .catch((error: unknown) => {
        if (error instanceof Superseded) return
        layingOut = false
        layoutFailure = error instanceof Error ? error.message : String(error)
      })
  })

  const layout = $derived<CompoundLayout | null>(drawn?.layout ?? null)
  const nodeMeta = $derived(new Map((drawnGraph?.grouped.nodes ?? []).map((node) => [node.id, node])))
  const groupMeta = $derived(new Map((drawnGraph?.grouped.groups ?? []).map((g) => [g.id, g])))
  const placedById = $derived(new Map((layout?.nodes ?? []).map((node) => [node.id, node])))

  // --- Pan, zoom and culling ----------------------------------------------

  let viewport = $state<HTMLDivElement | null>(null)
  let svg = $state<SVGSVGElement | null>(null)
  let content = $state<SVGGElement | null>(null)
  let paneWidth = $state(0)
  let paneHeight = $state(0)
  let zoom = $state(1)
  let panX = $state(0)
  let panY = $state(0)
  let dragging = $state(false)
  let dragFrom = { x: 0, y: 0, panX: 0, panY: 0 }

  /** A floor far lower than the dependency map's: five thousand boxes have to fit. */
  const MIN_ZOOM = 0.04
  const MAX_ZOOM = 3
  /** Below this the text is unreadable, so it is not drawn at all. */
  const TEXT_ZOOM = 0.4

  function clamp(value: number): number {
    return Math.min(Math.max(value, MIN_ZOOM), MAX_ZOOM)
  }

  /**
   * Whether the view is still the fit it was given — nobody has panned or
   * zoomed since. While it is, a change in the PANE's size (the kind-toggle
   * row re-wrapping after the first draw, a resized window) fits again,
   * because the fit was measured against a pane that no longer exists. A
   * redraw of the MAP never refits: that would move what somebody is reading.
   */
  let fitHeld = false

  function fit(): void {
    if (!layout || paneWidth === 0 || paneHeight === 0) return
    fitHeld = true
    const scale = clamp(Math.min(paneWidth / layout.bounds.width, paneHeight / layout.bounds.height, 1))
    zoom = scale
    panX = (paneWidth - layout.bounds.width * scale) / 2 - layout.bounds.x * scale
    panY = (paneHeight - layout.bounds.height * scale) / 2 - layout.bounds.y * scale
  }

  // The first time the pane has a size, fit whatever is already drawn; after
  // that, refit on a pane resize only while the fit is untouched. The map
  // itself is read untracked, so a redraw is never a reason to fit.
  let sized = false
  $effect(() => {
    const width = paneWidth
    const height = paneHeight
    if (width === 0 || height === 0 || !untrack(() => layout)) return
    if (sized && !fitHeld) return
    sized = true
    untrack(fit)
  })

  function zoomAbout(factor: number, px: number, py: number): void {
    const next = clamp(zoom * factor)
    if (next === zoom) return
    fitHeld = false
    panX = px - ((px - panX) / zoom) * next
    panY = py - ((py - panY) / zoom) * next
    zoom = next
  }

  function onWheel(event: WheelEvent): void {
    event.preventDefault()
    const box = viewport!.getBoundingClientRect()
    zoomAbout(event.deltaY < 0 ? 1.1 : 1 / 1.1, event.clientX - box.left, event.clientY - box.top)
  }

  function onPointerDown(event: PointerEvent): void {
    if ((event.target as Element).closest('[data-node],[data-group-header]')) return
    dragging = true
    dragFrom = { x: event.clientX, y: event.clientY, panX, panY }
    ;(event.currentTarget as Element).setPointerCapture?.(event.pointerId)
  }

  function onPointerMove(event: PointerEvent): void {
    if (!dragging) return
    fitHeld = false
    panX = dragFrom.panX + (event.clientX - dragFrom.x)
    panY = dragFrom.panY + (event.clientY - dragFrom.y)
  }

  function onPointerUp(event: PointerEvent): void {
    dragging = false
    ;(event.currentTarget as Element).releasePointerCapture?.(event.pointerId)
  }

  /** Arrows pan, + and - zoom, 0 fits — for the map with the keyboard alone. */
  function onViewportKey(event: KeyboardEvent): void {
    if (event.target !== viewport) return
    const step = 60
    if (event.key.startsWith('Arrow')) fitHeld = false
    switch (event.key) {
      case 'ArrowLeft':
        panX += step
        break
      case 'ArrowRight':
        panX -= step
        break
      case 'ArrowUp':
        panY += step
        break
      case 'ArrowDown':
        panY -= step
        break
      case '+':
      case '=':
        zoomAbout(1.25, paneWidth / 2, paneHeight / 2)
        break
      case '-':
        zoomAbout(1 / 1.25, paneWidth / 2, paneHeight / 2)
        break
      case '0':
        fit()
        break
      default:
        return
    }
    event.preventDefault()
  }

  const cullIndex = $derived(layout ? buildCullIndex(layout) : null)
  /** Set while a PNG is rendered: everything is drawn, not only what is on screen. */
  let exporting = $state(false)
  // An unmeasured pane (the first frame, or a test's DOM) culls nothing: there
  // is no viewport yet to cull against.
  const onScreen = $derived(
    cullIndex && !exporting && paneWidth > 0 && paneHeight > 0
      ? visible(cullIndex, viewportOf({ x: panX, y: panY }, zoom, { width: paneWidth, height: paneHeight }), 120 / zoom)
      : null,
  )
  const nodesToDraw = $derived(
    (layout?.nodes ?? []).filter((node) => !onScreen || onScreen.nodes.has(node.id)),
  )
  const edgesToDraw = $derived(
    (layout?.edges ?? []).filter((edge) => !onScreen || onScreen.edges.has(edge.id)),
  )
  const groupsToDraw = $derived(
    (layout?.groups ?? []).filter((g) => !onScreen || onScreen.groups.has(g.id)),
  )
  const showText = $derived(exporting || zoom >= TEXT_ZOOM)
  /** Line labels need more room than box text before they can be read. */
  const EDGE_TEXT_ZOOM = 0.6
  const showEdgeText = $derived(exporting || zoom >= EDGE_TEXT_ZOOM)

  /** The drawn graph's lines by id: their kinds and their own label. */
  const edgeMeta = $derived(new Map((drawnGraph?.grouped.edges ?? []).map((edge) => [edge.id, edge])))

  /** Where a label sits: the middle of the route's middle segment. */
  function midpoint(points: { x: number; y: number }[]): { x: number; y: number } {
    if (points.length === 0) return { x: 0, y: 0 }
    if (points.length === 1) return points[0]
    const i = Math.floor((points.length - 1) / 2)
    return { x: (points[i].x + points[i + 1].x) / 2, y: (points[i].y + points[i + 1].y) / 2 }
  }

  /** A line in words, for its title and its accessible name — label included. */
  function edgeSentence(
    from: string,
    to: string,
    meta: { kinds: string[]; label: string } | undefined,
    decoration: EdgeDecoration | undefined,
  ): string {
    const a = nodeMeta.get(from)
    const b = nodeMeta.get(to)
    const name = (node: ViewNode | undefined, id: string) =>
      node ? `${node.apiKind || (node.set === 'group' ? 'Group' : '')} ${node.name}`.trim() : id
    let said = `${name(a, from)} ${(meta?.kinds ?? []).join(', ')} ${name(b, to)}`
    if (meta?.label) said += ` (${meta.label})`
    if (decoration?.title) said += `\n${decoration.title}`
    return said
  }

  // --- Findings ------------------------------------------------------------

  const realNodes = $derived(new Map((graph?.nodes ?? []).map((node) => [node.id, node])))
  /** Backend-summarised pod sets and the pods each stands for, by name. */
  const summaryMembers = $derived(
    (graph?.nodes ?? [])
      .filter((node) => node.podSummary)
      .map((node) => ({ id: node.id, namespace: node.namespace, members: node.podSummary!.members })),
  )
  const findings = $derived(indexFindings(session.activeIssues, summaryMembers))

  function membersOf(ids: string[]): TopologyNode[] {
    const out: TopologyNode[] = []
    for (const id of ids) {
      const node = realNodes.get(id)
      if (node) out.push(node)
    }
    return out
  }

  const badges = $derived.by(() => {
    const map = new Map<string, FindingBadge>()
    if (findings.bySubject.size === 0) return map
    for (const node of drawnGraph?.grouped.nodes ?? []) {
      const badge = badgeFor(findings, membersOf(node.members))
      if (badge) map.set(node.id, badge)
    }
    for (const g of drawnGraph?.grouped.groups ?? []) {
      const badge = badgeFor(findings, membersOf(g.members))
      if (badge) map.set(g.id, badge)
    }
    return map
  })

  function badgeLabel(badge: FindingBadge): string {
    const first = badge.findings[0]
    return badge.count === 1
      ? `Finding: ${first.title}`
      : `${badge.count} findings, the worst: ${first.title}`
  }

  // --- Search --------------------------------------------------------------

  let query = $state('')
  let matchIndex = $state(0)
  let focused = $state<string | null>(null)
  let searchNote = $state('')
  const matches = $derived(graph && query.trim() ? searchNodes(graph.nodes, query) : [])

  $effect(() => {
    void query
    matchIndex = 0
    focused = null
    searchNote = ''
  })

  function goToMatch(step: number): void {
    if (matches.length === 0 || !drawnGraph) return
    matchIndex = (matchIndex + step + matches.length) % matches.length
    const id = matches[matchIndex]
    const target = realNodes.get(id)
    const drawnIds = new Set(nodeMeta.keys())
    const at = locate(id, drawnIds, [drawnGraph.folded.standIn, drawnGraph.grouped.standIn])
    if (!at) {
      focused = null
      searchNote = target
        ? `${target.apiKind} ${target.name} is not drawn: its kind or its application is hidden.`
        : ''
      return
    }
    const box = placedById.get(at)
    if (!box) return
    const next = centreOn(box, { width: paneWidth, height: paneHeight }, zoom)
    fitHeld = false
    zoom = next.zoom
    panX = next.panX
    panY = next.panY
    focused = at
    searchNote =
      at !== id && target ? `${target.apiKind} ${target.name} is inside ${nodeMeta.get(at)?.name ?? 'a set'}.` : ''
  }

  /** Enter: the first match, then the next; Shift+Enter: the previous. */
  function nextMatch(): void {
    goToMatch(matchIndex === 0 && focused === null ? 0 : 1)
  }

  // --- Changed, and Live ---------------------------------------------------

  let changed = $state(false)
  let live = $state(false)
  let liveTimer: ReturnType<typeof setTimeout> | null = null

  $effect(() => {
    const cluster = session.cluster.id
    const drawnScope = scope
    const off = onTopologyChanged((event) => {
      if (!graph || !changeConcerns(event, cluster, drawnScope)) return
      if (!live) {
        changed = true
        return
      }
      // COALESCED, not reset: a busy namespace changes every second, and a
      // timer restarted by each change would never fire.
      if (liveTimer) return
      liveTimer = setTimeout(() => {
        liveTimer = null
        void load(true)
      }, liveDelayMs(graph.nodes.length))
    })
    return () => {
      off()
      if (liveTimer) clearTimeout(liveTimer)
      liveTimer = null
    }
  })

  function toggleLive(): void {
    live = !live
    // Turning Live on with a change already waiting means "catch up now".
    if (live && changed) void load(true)
  }

  // --- Following a box -----------------------------------------------------

  /** Opens the object's own drawer — the Kubernetes Kind, verbatim. */
  async function open(node: ViewNode): Promise<void> {
    if (node.set === 'fold') return toggleFold(node.id)
    if (node.set === 'group') return toggleGroup(node.id)
    if (node.set === 'summary' || !node.apiKind) return
    const kind = session.kinds.find((entry) => entry.kind === node.apiKind)
    if (!kind) return
    // OVER the map: the drawer opens where every list opens it, and the
    // topology — scope, groups, viewport — stays exactly as it is behind it.
    await session.openDetailOver(kind.id, node.name, node.namespace)
  }

  function nodeLabel(node: ViewNode, badge: FindingBadge | undefined): string {
    const what =
      node.set === 'group'
        ? `Group ${node.name}, ${describeCounts(node.counts)}. Press to expand`
        : node.set === 'fold'
          ? `${node.name}, folded. Press to expand`
          : node.set === 'summary'
            ? `${node.name}, ${totalOf(node.counts)} pods summarised by the backend`
            : `Open ${node.apiKind} ${node.name}${node.namespace ? ` in ${node.namespace}` : ''}`
    const state = node.state === 'bad' ? ', failing' : node.state === 'warn' ? ', degraded' : ''
    const found = badge ? `, ${badge.count} finding${badge.count === 1 ? '' : 's'}` : ''
    return `${what}${state}${found}`
  }

  // --- Decoration ----------------------------------------------------------

  const TONE: Record<DecorationTone, string> = {
    normal: 'stroke-gauge-normal',
    warn: 'stroke-gauge-warn',
    critical: 'stroke-gauge-critical',
    muted: 'stroke-outline',
  }

  const decorations = $derived.by(() => {
    const map = new Map<string, EdgeDecoration>()
    if (!decorator?.decorateEdge || !drawnGraph) return map
    for (const edge of drawnGraph.grouped.edges) {
      const decoration = decorator.decorateEdge({
        from: nodeMeta.get(edge.from)?.members ?? [edge.from],
        to: nodeMeta.get(edge.to)?.members ?? [edge.to],
        kinds: edge.kinds,
      })
      if (decoration) map.set(edge.id, decoration)
    }
    return map
  })

  /** A layer's own lines, mapped onto the boxes that draw their ends. */
  const overlay = $derived.by(() => {
    if (!decorator?.overlayEdges?.length || !drawnGraph || !layout) return []
    const drawnIds = new Set(nodeMeta.keys())
    const standIns = [drawnGraph.folded.standIn, drawnGraph.grouped.standIn]
    const seen = new Set<string>()
    const out: { id: string; path: string; decoration: EdgeDecoration; mid: { x: number; y: number } }[] = []
    for (const edge of decorator.overlayEdges) {
      const from = locate(edge.from, drawnIds, standIns)
      const to = locate(edge.to, drawnIds, standIns)
      if (!from || !to || from === to) continue
      const key = `${from}->${to}`
      if (seen.has(key)) continue
      seen.add(key)
      const a = placedById.get(from)
      const b = placedById.get(to)
      if (!a || !b) continue
      const mx = (a.x + b.x) / 2
      const my = (a.y + b.y) / 2
      // Bowed to one side, so it never lies on top of a dependency line.
      const bow = Math.min(80, Math.hypot(b.x - a.x, b.y - a.y) / 4)
      const length = Math.hypot(b.x - a.x, b.y - a.y) || 1
      const cx = mx - ((b.y - a.y) / length) * bow
      const cy = my + ((b.x - a.x) / length) * bow
      out.push({
        id: edge.id,
        path: `M ${a.x} ${a.y} Q ${cx} ${cy} ${b.x} ${b.y}`,
        decoration: edge.decoration,
        mid: { x: (mx + cx) / 2, y: (my + cy) / 2 },
      })
    }
    return out
  })

  // --- Traffic --------------------------------------------------------------

  /** The layer the panel loaded; null while off, loading or failed. */
  let panelLayer = $state.raw<TrafficLayer | null>(null)
  let trafficFilters = $state<TrafficFilters>({ hideSystem: true, hideExternal: false })
  /** The toolbar's traffic toggle, and its options popover. */
  let trafficOn = $state(false)
  let trafficOptionsOpen = $state(false)
  /** What the traffic layer has to explain now, for Help. */
  let trafficSaid = $state.raw<{ problem: boolean; sections: HelpSection[] }>({ problem: false, sections: [] })

  function toggleTraffic(): void {
    trafficOn = !trafficOn
    // Its controls open with it: the source and window are the next question.
    trafficOptionsOpen = trafficOn
  }
  const trafficShown = $derived(traffic ?? panelLayer)

  /**
   * The traffic overlay on the boxes as drawn.
   *
   * An endpoint's node may not be a box of its own right now — folded into a
   * set, inside a collapsed group — so it is RESOLVED to the box standing for
   * it first, exactly as search locates a match. Only an endpoint whose kind
   * is switched off has no box, and buildOverlay counts it as off the map.
   */
  const trafficOverlay = $derived.by<TrafficOverlay | null>(() => {
    if (!trafficShown || !drawnGraph || !layout) return null
    const drawnIds = new Set(nodeMeta.keys())
    const standIns = [drawnGraph.folded.standIn, drawnGraph.grouped.standIn]
    const box = (id: string) => (id ? (locate(id, drawnIds, standIns) ?? id) : id)
    const layer = {
      ...trafficShown,
      edges: trafficShown.edges.map((edge) => ({
        ...edge,
        source: { ...edge.source, nodeId: box(edge.source.nodeId) },
        dest: { ...edge.dest, nodeId: box(edge.dest.nodeId) },
      })),
    }
    const centres = new Map<string, { x: number; y: number }>()
    for (const node of layout.nodes) centres.set(node.id, { x: node.x, y: node.y })
    return buildOverlay(layer, centres, trafficFilters)
  })

  /** What a PNG covers: the layout, and the traffic column beside it. */
  const exportBounds = $derived.by(() => {
    if (!layout) return null
    const nodes = trafficOverlay?.nodes ?? []
    if (nodes.length === 0) return layout.bounds
    const right = Math.max(layout.bounds.x + layout.bounds.width, ...nodes.map((n) => n.x + 100))
    const bottom = Math.max(layout.bounds.y + layout.bounds.height, ...nodes.map((n) => n.y + 40))
    return { x: layout.bounds.x, y: layout.bounds.y, width: right - layout.bounds.x, height: bottom - layout.bounds.y }
  })

  // --- Help: what this drawing says about itself ---------------------------

  /**
   * What is true of THIS drawing, for the Help panel — the notices that used
   * to stand under the map. Bounded is a standing fact of every drawing and
   * marks nothing; an unreadable kind, a summarised scope or a traffic layer
   * that cannot draw puts a dot on the (?), so none of them goes unseen.
   */
  const drawingSections = $derived.by<HelpSection[]>(() => {
    const out: HelpSection[] = []
    const body: string[] = []
    if (graph && graph.unreadable.length > 0) {
      body.push(
        `Unreadable: could not read ${graph.unreadable.join(', ')} in this scope, so anything reached through them is missing from this map.`,
      )
    }
    if (graph?.summarised) {
      body.push(
        "Summarised: this scope has more pods than can be drawn one by one, so each owner's pods are one box with complete counts. Narrow the scope to see them individually.",
      )
    }
    if (graph?.bounded) body.push(`Bounded: ${graph.bounded}.`)
    if (grouped && grouped.hidden.length > 0) {
      body.push(
        `${grouped.hidden.length} ${grouped.hidden.length === 1 ? 'application is' : 'applications are'} hidden (${grouped.hiddenObjects} objects): ${grouped.hidden.map((g) => g.label).join(', ')}.`,
      )
    }
    if (body.length > 0) out.push({ heading: 'This drawing', body })
    const traffic = [...trafficSaid.sections]
    if (trafficOverlay && (trafficOverlay.skipped.filtered > 0 || trafficOverlay.skipped.offMap > 0)) {
      const notes: string[] = []
      if (trafficOverlay.skipped.filtered > 0) {
        notes.push(`${trafficOverlay.skipped.filtered} traffic line${trafficOverlay.skipped.filtered === 1 ? ' is' : 's are'} hidden by the filters.`)
      }
      if (trafficOverlay.skipped.offMap > 0) {
        notes.push(`${trafficOverlay.skipped.offMap} not drawn: their ends are of a kind switched off.`)
      }
      if (traffic.length > 0) traffic[0] = { ...traffic[0], body: [...traffic[0].body, ...notes] }
      else traffic.push({ heading: 'Observed traffic now', body: notes })
    }
    return [...out, ...traffic]
  })

  const helpNotice = $derived.by(() => {
    if (graph && graph.unreadable.length > 0) return `could not read ${graph.unreadable.join(', ')}`
    if (graph?.summarised) return 'pods are summarised'
    if (trafficOn && trafficSaid.problem) return 'traffic cannot be drawn'
    return undefined
  })

  $effect(() => {
    help.provide('topology', drawingSections)
  })
  onDestroy(() => help.withdraw('topology'))

  // --- Motion ---------------------------------------------------------------

  /**
   * Lines move the way the dependency map's do — dashes travelling from source
   * to target — and the traffic layer's move faster the busier the line. NOT
   * for anybody who asked for less motion (no animation at all, not a slower
   * one), and not above ANIMATION_LIMIT drawn lines, where animating every
   * dash costs more frames than it is worth.
   */
  const ANIMATION_LIMIT = 200
  let reducedMotion = $state(false)
  $effect(() => {
    if (typeof matchMedia !== 'function') return
    const query = matchMedia('(prefers-reduced-motion: reduce)')
    reducedMotion = query.matches
    const changed = (event: MediaQueryListEvent) => (reducedMotion = event.matches)
    query.addEventListener?.('change', changed)
    return () => query.removeEventListener?.('change', changed)
  })
  const animate = $derived(
    !reducedMotion &&
      !exporting &&
      (layout?.edges.length ?? 0) + (trafficOverlay?.edges.length ?? 0) <= ANIMATION_LIMIT,
  )

  /** A traffic line's dash speed: the widest (busiest) about four times the thinnest. */
  function trafficSeconds(width: number): number {
    return Math.max(0.4, 2.2 - (width - 1) * 0.25)
  }

  // --- Export --------------------------------------------------------------

  let exportNote = $state('')

  async function exportPng(): Promise<void> {
    if (!svg || !content || !layout) return
    exportNote = ''
    exporting = true
    try {
      await tick()
      const ground = getComputedStyle(document.documentElement).getPropertyValue('--surface').trim() || '#ffffff'
      const image = await renderPng(svg, content, exportBounds ?? layout.bounds, ground)
      exporting = false
      const path = await exportTopologyPNG(topologyFilename(session.cluster.id, scope), image.base64)
      if (!path) return
      exportNote = image.capped
        ? `Saved to ${path} — at ${Math.round(image.scale * 100)}% of life size, the largest image this window can draw.`
        : `Saved to ${path}.`
    } catch (error) {
      exportNote = `Could not export: ${toApiError(error).message}`
    } finally {
      exporting = false
    }
  }

  // --- Drawing helpers -----------------------------------------------------

  function stateStroke(node: ViewNode): string {
    switch (node.state) {
      case 'bad':
        return 'stroke-gauge-critical'
      case 'warn':
        return 'stroke-gauge-warn'
      case 'ok':
        return 'stroke-gauge-normal'
      default:
        return 'stroke-on-surface-variant'
    }
  }

  function fitText(value: string, characters: number): string {
    return value.length <= characters ? value : value.slice(0, characters - 1) + '…'
  }

  const kindLine = (node: ViewNode): string =>
    node.set === 'group' ? 'Group' : node.set ? describeCounts(node.counts) : node.apiKind

  const groupChoices = [
    { value: 'namespace', label: 'Group by namespace' },
    { value: 'app', label: 'Group by application' },
    { value: 'label', label: 'Group by label' },
    { value: 'none', label: 'No groups' },
  ]
</script>

<div class="flex min-h-0 flex-1 flex-col">
  <PaneToolbar>
    <!-- Scope: some namespaces or all of them. -->
    <div class="relative">
      <button
        type="button"
        onclick={() => (scopeOpen ? (scopeOpen = false) : openScope())}
        aria-expanded={scopeOpen}
        aria-haspopup="dialog"
        class="state-layer flex h-8 items-center gap-1.5 rounded-sm px-2 text-body-medium text-on-surface
               hover:bg-surface-container"
        title="What the topology draws"
      >
        <span class="max-w-56 truncate">{scopeLabel}</span>
        <ChevronDown class="size-3.5 text-on-surface-variant" strokeWidth={2} />
      </button>
      {#if scopeOpen}
        <div
          role="dialog"
          aria-label="Topology scope"
          tabindex="-1"
          onkeydown={(event) => {
            if (event.key === 'Escape') {
              event.stopPropagation()
              scopeOpen = false
            }
          }}
          class="absolute left-0 top-9 z-30 flex max-h-96 w-72 flex-col gap-2 rounded-md border
                 border-outline-variant bg-surface-container p-3 shadow-lg"
        >
          <label class="flex items-center gap-2 text-body-medium text-on-surface">
            <input type="checkbox" bind:checked={draftAll} />
            All namespaces
          </label>
          <input
            type="search"
            bind:value={scopeFilter}
            placeholder="Filter namespaces…"
            disabled={draftAll}
            class="h-8 rounded-sm border border-outline-variant bg-surface px-2 text-body-small text-on-surface
                   disabled:opacity-50"
          />
          <ul class="min-h-0 flex-1 overflow-y-auto" aria-label="Namespaces">
            {#each namespaceChoices as name (name)}
              <li>
                <label class="flex items-center gap-2 py-0.5 text-body-small text-on-surface {draftAll ? 'opacity-50' : ''}">
                  <input
                    type="checkbox"
                    disabled={draftAll}
                    checked={draftAll || draftNamespaces.includes(name)}
                    onchange={() => toggleDraft(name)}
                  />
                  <span class="truncate">{name}</span>
                </label>
              </li>
            {:else}
              <li class="text-body-small text-on-surface-variant/70">No namespaces to choose from.</li>
            {/each}
          </ul>
          <div class="flex justify-end gap-2">
            <button
              type="button"
              class="state-layer rounded-sm px-3 py-1 text-label-large text-on-surface-variant hover:bg-surface-container-high"
              onclick={() => (scopeOpen = false)}
            >
              Cancel
            </button>
            <button
              type="button"
              disabled={!draftAll && draftNamespaces.length === 0}
              class="rounded-sm bg-primary px-3 py-1 text-label-large text-on-primary disabled:opacity-50"
              onclick={applyScope}
            >
              Draw
            </button>
          </div>
        </div>
      {/if}
    </div>

    <Select
      compact
      label="Grouping"
      value={groupChoice}
      options={groupChoices}
      onchange={(value) => {
        groupChoice = value as GroupChoice
        collapsedGroups = new Set()
      }}
    />
    {#if groupChoice === 'label'}
      <input
        type="text"
        bind:value={groupLabel}
        aria-label="Label key to group by"
        placeholder="label key"
        class="h-8 w-48 rounded-sm border border-outline-variant bg-surface px-2 text-body-small text-on-surface"
      />
    {/if}

    {#if groupChoice === 'app' && appCount > 0}
      <!-- Which applications are drawn. Unticked ones leave the map; the kind
           counts stay complete and Help says what is hidden. -->
      <div class="relative">
        <button
          type="button"
          onclick={() => {
            appsOpen = !appsOpen
            appFilter = ''
          }}
          aria-expanded={appsOpen}
          aria-haspopup="dialog"
          class="state-layer flex h-8 items-center gap-1.5 rounded-sm px-2 text-body-medium text-on-surface
                 hover:bg-surface-container"
          title="Which applications are drawn"
        >
          <span class="max-w-48 truncate">{appsLabel}</span>
          <ChevronDown class="size-3.5 text-on-surface-variant" strokeWidth={2} />
        </button>
        {#if appsOpen}
          <div
            role="dialog"
            aria-label="Applications drawn"
            tabindex="-1"
            onkeydown={(event) => {
              if (event.key === 'Escape') {
                event.stopPropagation()
                appsOpen = false
              }
            }}
            class="absolute left-0 top-9 z-30 flex max-h-96 w-72 flex-col gap-2 rounded-md border
                   border-outline-variant bg-surface-container p-3 shadow-lg"
          >
            <label class="flex items-center gap-2 text-body-medium text-on-surface">
              <input
                type="checkbox"
                checked={hiddenApps.size === 0}
                onchange={(event) => showAllApps(event.currentTarget.checked)}
              />
              All applications
            </label>
            <input
              type="text"
              autocomplete="off"
              spellcheck="false"
              bind:value={appFilter}
              placeholder="Filter applications…"
              aria-label="Filter applications"
              class="field h-8 px-2 text-body-small"
            />
            <ul class="min-h-0 flex-1 overflow-y-auto" aria-label="Applications">
              {#each appChoices as app (app.id)}
                <li>
                  <label class="flex items-center gap-2 py-0.5 text-body-small text-on-surface">
                    <input type="checkbox" checked={!hiddenApps.has(app.id)} onchange={() => toggleApp(app.id)} />
                    <span class="min-w-0 flex-1 truncate">{app.label}</span>
                    <span class="tabular-nums text-on-surface-variant">{totalOf(app.counts)}</span>
                  </label>
                </li>
              {:else}
                <li class="text-body-small text-on-surface-variant/70">No application matches.</li>
              {/each}
            </ul>
          </div>
        {/if}
      </div>
    {/if}

    <!-- The same field every pane's toolbar searches with. kind: and ns:
         narrow it as typed text; Enter and Shift+Enter step through matches. -->
    <div class="flex w-64 min-w-40 shrink">
      <ToolbarSearch
        value={query}
        label="Find on the topology"
        placeholder="Find… (kind:Service ns:web)"
        count={query.trim() ? (matches.length === 0 ? '0' : `${Math.min(matchIndex + 1, matches.length)}/${matches.length}`) : undefined}
        empty={query.trim() !== '' && matches.length === 0}
        onchange={(value) => (query = value)}
        onnext={nextMatch}
        onprevious={() => goToMatch(-1)}
      />
    </div>

    {#snippet trailing()}
      {#if changed}
        <!-- Said, not done: the map holds still until somebody asks. -->
        <button
          type="button"
          onclick={() => void load(true)}
          class="flex h-7 items-center gap-1 rounded-full bg-notice-warn px-2.5 text-label-medium text-gauge-warn-ink"
          title="Something in this scope changed since it was drawn"
        >
          <RefreshCw class="size-3.5" strokeWidth={2} />
          Changed — Refresh
        </button>
      {/if}
      <button
        type="button"
        role="switch"
        aria-checked={live}
        onclick={toggleLive}
        title={live
          ? 'Live: redraws after changes, waiting longer the bigger the map'
          : 'Redraw on its own after a change'}
        class="flex h-7 items-center gap-1.5 rounded-full border px-2.5 text-label-medium
               {live ? 'border-primary bg-primary/12 text-primary' : 'border-outline-variant text-on-surface-variant'}"
      >
        <span class="size-2 rounded-full {live ? 'bg-primary' : 'bg-outline'}" aria-hidden="true"></span>
        Live
      </button>
      <ToolbarButton icon={RefreshCw} label="Refresh" title="Read the topology again" onclick={() => void load(true)} disabled={loading} />

      <div class="mx-0.5 h-5 w-px shrink-0 bg-outline-variant/60" aria-hidden="true"></div>

      <!-- Observed traffic: a toggle, and its controls in a popover beside it.
           Off until pressed; nothing is asked of the cluster's Prometheus
           before that. What it found, or why not, is in Help. -->
      <div class="relative flex items-center" data-layer-controls>
        {#if trafficControls}
          {@render trafficControls()}
        {:else}
          <ToolbarButton
            icon={Activity}
            label="Observed traffic"
            title={trafficOn ? 'Hide observed traffic' : 'Show observed traffic, from the cluster’s own monitoring backend'}
            pressed={trafficOn}
            onclick={toggleTraffic}
          />
          {#if trafficOn}
            <ToolbarButton
              icon={SlidersHorizontal}
              label="Traffic options"
              title="Source, window and filters"
              pressed={trafficOptionsOpen}
              onclick={() => (trafficOptionsOpen = !trafficOptionsOpen)}
            />
          {/if}
          <!-- Mounted while the layer is on, shown only while the popover is
               open: closing the popover must not forget the layer. -->
          {#if trafficOn}
            <div
              role="dialog"
              aria-label="Observed traffic options"
              tabindex="-1"
              hidden={!trafficOptionsOpen}
              onkeydown={(event) => {
                if (event.key === 'Escape') {
                  event.stopPropagation()
                  trafficOptionsOpen = false
                }
              }}
              class="absolute right-0 top-9 z-30 w-[26rem] max-w-[90vw] rounded-md border border-outline-variant
                     bg-surface-container p-3 shadow-lg"
              data-traffic-popover
            >
              <TrafficPanel
                clusterId={session.cluster.id}
                namespaces={scope.namespaces}
                all={scope.all}
                bind:on={trafficOn}
                bind:filters={trafficFilters}
                onlayer={(next) => (panelLayer = next)}
                onhelp={(said) => (trafficSaid = said)}
              />
            </div>
          {/if}
        {/if}
      </div>
      <HelpButton topic="topology" about="the topology" notice={helpNotice} />

      <div class="mx-0.5 h-5 w-px shrink-0 bg-outline-variant/60" aria-hidden="true"></div>

      {#if grouped && grouped.groups.length > 0}
        <ToolbarButton
          icon={Layers}
          label={allCollapsed ? 'Expand all groups' : 'Collapse all groups'}
          title={allCollapsed ? 'Expand all groups' : `Collapse all ${grouped.groups.length} groups`}
          active={allCollapsed}
          onclick={toggleAllGroups}
        />
      {/if}
      <ToolbarButton icon={ZoomOut} label="Zoom out" title="Zoom out" onclick={() => zoomAbout(1 / 1.25, paneWidth / 2, paneHeight / 2)} />
      <ToolbarButton icon={ZoomIn} label="Zoom in" title="Zoom in" onclick={() => zoomAbout(1.25, paneWidth / 2, paneHeight / 2)} />
      <ToolbarButton icon={Crosshair} label="Fit to the pane" title="Fit to the pane" onclick={fit} />
      <ToolbarButton
        icon={orientation === 'horizontal' ? Rows3 : Columns3}
        label={orientation === 'horizontal' ? 'Lay out vertically' : 'Lay out horizontally'}
        title={orientation === 'horizontal' ? 'Lay out vertically' : 'Lay out horizontally'}
        onclick={() => preferences.setMapOrientation(orientation === 'horizontal' ? 'vertical' : 'horizontal')}
      />
      <ToolbarButton
        icon={ImageDown}
        label="Export as PNG"
        title="Save everything drawn as a PNG"
        onclick={() => void exportPng()}
        disabled={!layout || exporting}
      />
    {/snippet}
  </PaneToolbar>

  {#if toggles.length > 0}
    <!-- One toggle per Kind, with the COMPLETE count: switching a kind off
         changes what is drawn, never what the page says it found. -->
    <div
      class="flex shrink-0 flex-wrap items-center gap-1 border-b border-outline-variant/40 px-3 py-1.5"
      role="group"
      aria-label="Kinds drawn"
    >
      {#each toggles as toggle (toggle.apiKind)}
        {@const on = !hiddenKinds.has(toggle.apiKind)}
        <button
          type="button"
          aria-pressed={on}
          onclick={() => toggleKind(toggle.apiKind)}
          class="flex h-6 items-center gap-1 rounded-full border px-2 text-label-small
                 {on
            ? 'border-outline-variant bg-surface-container-high text-on-surface'
            : 'border-outline-variant/50 text-on-surface-variant/70 line-through'}"
        >
          {toggle.apiKind}
          <span class="tabular-nums text-on-surface-variant">{toggle.count}</span>
        </button>
      {/each}
    </div>
  {/if}

  {#if searchNote || exportNote}
    <p class="shrink-0 border-b border-outline-variant/40 px-4 py-1.5 text-body-small text-on-surface-variant" aria-live="polite">
      {searchNote || exportNote}
    </p>
  {/if}

  {#if !scope.all && scope.namespaces.length === 0}
    <p class="p-4 text-body-medium text-on-surface-variant/70">Choose a namespace to draw.</p>
  {:else if loading && !graph}
    <p class="p-4 text-body-medium text-on-surface-variant/70">Reading the topology of {scopeLabel}…</p>
  {:else if failure && !graph}
    <p class="p-4 text-body-medium text-error">{failure}</p>
  {:else}
    {#if failure}
      <p class="shrink-0 border-b border-outline-variant/40 bg-notice-warn px-4 py-1.5 text-body-small text-gauge-warn-ink">
        The last refresh failed, so this is the topology as it was: {failure}
      </p>
    {/if}
    <!-- svelte-ignore a11y_no_noninteractive_tabindex, a11y_no_noninteractive_element_interactions -->
    <!-- Focusable on purpose: the map is panned and zoomed from the keyboard
         here (arrows, + and -, 0), which is what `application` announces. -->
    <div
      bind:this={viewport}
      bind:clientWidth={paneWidth}
      bind:clientHeight={paneHeight}
      class="relative min-h-0 flex-1 overflow-hidden outline-none focus-visible:ring-2 focus-visible:ring-primary
             {dragging ? 'cursor-grabbing' : 'cursor-grab'}"
      onwheel={onWheel}
      onpointerdown={onPointerDown}
      onpointermove={onPointerMove}
      onpointerup={onPointerUp}
      onpointercancel={onPointerUp}
      onkeydown={onViewportKey}
      tabindex="0"
      role="application"
      aria-label="Topology of {scopeLabel}. Arrow keys pan, plus and minus zoom, zero fits."
    >
      {#if graph && graph.nodes.length === 0}
        <p class="p-4 text-body-medium text-on-surface-variant/70">Nothing in {scopeLabel} to draw.</p>
      {:else if layout}
        <svg bind:this={svg} class="size-full select-none" aria-label="Topology map">
          {#snippet box(node: ViewNode, placed: PlacedNode)}
            {@const half = { w: placed.width / 2, h: placed.height / 2 }}
            {@const badge = badges.get(node.id)}
            <rect
              x={-half.w} y={-half.h} width={placed.width} height={placed.height}
              rx="8"
              class="{node.set ? 'fill-surface-container-high' : 'fill-surface-container-low'}
                     {focused === node.id ? 'stroke-primary' : 'stroke-outline-variant'}"
              stroke-width={focused === node.id ? 2.5 : 1}
            />
            {#if node.detail && showText}
              <title>{node.name} — {node.detail}</title>
            {/if}
            {#if node.set === 'fold' || node.set === 'summary'}
              <!-- A set is drawn as a stack, as on the dependency map. -->
              <g transform="translate(-12 {-half.h + 6})" fill="none" stroke-width="2" stroke-linecap="round"
                 stroke-linejoin="round" class="{stateStroke(node)} opacity-40">
                <g transform="translate(4 4)">{@html iconGeometry(node.kind)}</g>
              </g>
            {/if}
            <g transform="translate(-12 {-half.h + 6})" fill="none" stroke-width="2" stroke-linecap="round"
               stroke-linejoin="round" class={stateStroke(node)}>
              {@html iconGeometry(node.kind)}
            </g>
            {#if showText}
              <text y={-half.h + 44} text-anchor="middle" class="fill-on-surface text-[11px] font-semibold">
                {fitText(kindLine(node), 28)}
              </text>
              <text y={-half.h + 58} text-anchor="middle" class="fill-on-surface-variant text-[10px]">
                {fitText(node.name, 26)}
              </text>
            {/if}
            {#if node.set === 'fold' || node.set === 'group'}
              <g transform="translate({half.w - 16} {-half.h + 12})">
                <circle r="8" class="fill-surface-container-highest stroke-outline-variant" />
                <path d="M -3.5 0 H 3.5 M 0 -3.5 V 3.5" stroke-width="1.6" stroke-linecap="round" class="stroke-on-surface" />
              </g>
            {/if}
            {#if badge}
              <!-- Findings that name this box, or anything it stands for. -->
              <g
                data-finding-badge
                transform="translate({-half.w + 14} {-half.h + 12})"
                role="button"
                tabindex="0"
                aria-label="{badgeLabel(badge)}. Open it on the overview"
                class="cursor-pointer"
                onclick={(event) => {
                  event.stopPropagation()
                  void session.openFinding(badge.findings[0].id)
                }}
                onkeydown={(event) => {
                  if (event.key === 'Enter' || event.key === ' ') {
                    event.preventDefault()
                    event.stopPropagation()
                    void session.openFinding(badge.findings[0].id)
                  }
                }}
              >
                <title>{badge.findings.map((f) => f.title).join('\n')}</title>
                <circle r="9" class={badge.severity === 'critical' ? 'fill-gauge-critical' : 'fill-gauge-warn'} />
                <text y="3.5" text-anchor="middle" class="fill-surface text-[10px] font-bold">
                  {badge.count > 9 ? '9+' : badge.count}
                </text>
              </g>
            {/if}
          {/snippet}

          <defs>
            <marker id="topo-arrow" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="6" markerHeight="6"
                    orient="auto-start-reverse">
              <path d="M 0 1 L 9 5 L 0 9 z" class="fill-outline" />
            </marker>
          </defs>

          <g bind:this={content} transform="translate({panX} {panY}) scale({zoom})">
            <!-- Frames first, then lines, then boxes: a box always sits on top. -->
            {#each groupsToDraw as frame (frame.id)}
              {@const meta = groupMeta.get(frame.id)}
              {@const badge = badges.get(frame.id)}
              <g transform="translate({frame.x} {frame.y})">
                <rect width={frame.width} height={frame.height} rx="10"
                      class="fill-surface-container-lowest/60 stroke-outline-variant" stroke-width="1" />
                {#if meta}
                  <g
                    data-group-header
                    role="button"
                    tabindex="0"
                    aria-expanded="true"
                    aria-label="Collapse {meta.label}: {describeCounts(meta.counts)}"
                    class="cursor-pointer"
                    onclick={() => toggleGroup(frame.id)}
                    onkeydown={(event) => {
                      if (event.key === 'Enter' || event.key === ' ') {
                        event.preventDefault()
                        toggleGroup(frame.id)
                      }
                    }}
                  >
                    <rect width={frame.width} height={GROUP_HEADER} rx="10" class="fill-transparent" />
                    <path d="M 12 12 L 16 17 L 20 12" fill="none" stroke-width="1.6" stroke-linecap="round"
                          class="stroke-on-surface-variant" />
                    <circle cx="30" cy="15" r="4"
                            class={meta.state === 'bad' ? 'fill-gauge-critical' : meta.state === 'warn' ? 'fill-gauge-warn' : meta.state === 'ok' ? 'fill-gauge-normal' : 'fill-outline'} />
                    <text x="40" y="19" class="fill-on-surface text-[12px] font-semibold">
                      {fitText(meta.label, Math.max(8, Math.floor((frame.width - 60) / 7)))}
                    </text>
                    {#if showText}
                      <text x={frame.width - 12} y="19" text-anchor="end" class="fill-on-surface-variant text-[10px]">
                        {totalOf(meta.counts)} objects{badge ? ` · ${badge.count} finding${badge.count === 1 ? '' : 's'}` : ''}
                      </text>
                    {/if}
                  </g>
                {/if}
              </g>
            {/each}

            {#each edgesToDraw as edge (edge.id)}
              {@const decoration = decorations.get(edge.id)}
              {@const meta = edgeMeta.get(edge.id)}
              {@const said = edgeSentence(edge.from, edge.to, meta, decoration)}
              <path
                data-edge
                d={edge.path}
                fill="none"
                stroke-width={decoration?.width ? Math.min(Math.max(decoration.width, 1), 12) : 1.25}
                marker-end="url(#topo-arrow)"
                class="{decoration?.tone ? TONE[decoration.tone] : 'stroke-outline'} {animate ? 'flow' : ''}"
                role="img"
                aria-label={said}
              >
                <title>{said}</title>
              </path>
              {#if meta?.label && showEdgeText}
                <!-- The line's own qualifier — "via Pod", "via ReplicaSet" —
                     at its middle, so a bridged line is never read as direct. -->
                {@const mid = midpoint(edge.points)}
                <text
                  data-edge-label
                  x={mid.x}
                  y={mid.y - 4}
                  text-anchor="middle"
                  class="fill-on-surface-variant stroke-surface text-[10px]"
                  stroke-width="3"
                  paint-order="stroke"
                >
                  {meta.label}
                </text>
              {/if}
            {/each}

            {#each overlay as line (line.id)}
              <path
                data-overlay-edge
                d={line.path}
                fill="none"
                stroke-dasharray="2 6"
                stroke-linecap="round"
                stroke-width={line.decoration.width ? Math.min(Math.max(line.decoration.width, 1), 12) : 2}
                class={TONE[line.decoration.tone ?? 'normal']}
              >
                {#if line.decoration.title}<title>{line.decoration.title}</title>{/if}
              </path>
              {#if line.decoration.label && showText}
                <text x={line.mid.x} y={line.mid.y} text-anchor="middle" class="fill-on-surface text-[10px]">
                  {line.decoration.label}
                </text>
              {/if}
            {/each}

            {#if trafficOverlay}
              <!-- Observed traffic: measured lines over the map, dotted and
                   coloured by error rate so they are never read as the
                   relationships underneath them. -->
              {#each trafficOverlay.edges as line (line.id)}
                <path
                  data-traffic-edge
                  d={line.path}
                  fill="none"
                  stroke-linecap="round"
                  stroke-dasharray={animate ? undefined : line.hot ? undefined : '1 5'}
                  stroke-width={line.width}
                  class={animate ? 'traffic-flow' : ''}
                  style="stroke: {line.colour}; {animate ? `animation-duration: ${trafficSeconds(line.width)}s` : ''}"
                  opacity={line.hot ? 1 : 0.8}
                >
                  <title>{line.tooltip}</title>
                </path>
              {/each}
              {#each trafficOverlay.nodes as other (other.id)}
                <!-- Ends of traffic that are not boxes of the topology: a host
                     outside the cluster, an unknown peer, a workload this
                     scope does not draw. They exist only on the overlay. -->
                <g data-traffic-node transform="translate({other.x} {other.y})" role="img"
                   aria-label="{other.kind === 'external' ? 'Outside the cluster' : other.kind === 'unknown' ? 'Unknown peer' : 'Not drawn here'}: {other.label}">
                  <rect x="-80" y="-18" width="160" height="36" rx="18" stroke-dasharray="4 3"
                        class="fill-surface-container-low stroke-outline" stroke-width="1" />
                  <text y="4" text-anchor="middle" class="fill-on-surface-variant text-[10px]">
                    {fitText(other.label, 24)}
                  </text>
                </g>
              {/each}
            {/if}

            {#each nodesToDraw as placed (placed.id)}
              {@const node = nodeMeta.get(placed.id)}
              {#if node}
                {#if node.set === 'summary'}
                  <g data-node transform="translate({placed.x} {placed.y})" role="img"
                     aria-label={nodeLabel(node, badges.get(node.id))}>
                    {@render box(node, placed)}
                  </g>
                {:else}
                  <g
                    data-node
                    transform="translate({placed.x} {placed.y})"
                    class="cursor-pointer"
                    role="button"
                    tabindex="0"
                    aria-label={nodeLabel(node, badges.get(node.id))}
                    onclick={() => void open(node)}
                    onkeydown={(event) => {
                      if (event.target !== event.currentTarget) return
                      if (event.key === 'Enter' || event.key === ' ') {
                        event.preventDefault()
                        void open(node)
                      }
                    }}
                  >
                    {@render box(node, placed)}
                  </g>
                {/if}
              {/if}
            {/each}
          </g>
        </svg>
      {:else if layingOut || loading}
        <p class="p-4 text-body-medium text-on-surface-variant/70">Laying out {graph?.nodes.length ?? 0} objects…</p>
      {/if}

      {#if layout && (layingOut || loading)}
        <span class="pointer-events-none absolute right-3 top-2 rounded-full bg-surface-container-high px-2 py-0.5
                     text-label-small text-on-surface-variant">
          {loading ? 'Reading…' : 'Laying out…'}
        </span>
      {/if}
    </div>

    {#if layoutFailure}
      <p class="shrink-0 border-t border-outline-variant/40 px-4 py-2 text-body-small text-error">
        Could not lay the topology out: {layoutFailure}
      </p>
    {/if}

    {#if graph && layout}
      <p class="shrink-0 border-t border-outline-variant/40 px-4 py-1 text-label-small tabular-nums text-on-surface-variant/70">
        {graph.nodes.length} objects, {graph.edges.length} relationships · {nodeMeta.size} boxes drawn
        {#if onScreen}· {nodesToDraw.length} on screen{/if}
        {#if layoutMs > 0}· laid out in {Math.round(layoutMs)} ms{/if}
        {#if grouped && grouped.hidden.length > 0}· {grouped.hidden.length} {grouped.hidden.length === 1 ? 'application' : 'applications'} hidden ({grouped.hiddenObjects} objects){/if}
      </p>
    {/if}
  {/if}
</div>

<style>
  /*
    The dependency map's flow, reused: dashes travel from source to target, so
    the movement reads as direction rather than decoration. Applied only while
    `animate` holds — never under prefers-reduced-motion, never above the
    drawn-line limit — and the media query below is a second guard.
  */
  .flow {
    stroke-dasharray: 5 9;
    animation: topology-flow 900ms linear infinite;
  }

  @keyframes topology-flow {
    to {
      stroke-dashoffset: -14;
    }
  }

  /* Observed traffic: a shorter dash, its speed set per line by its rate. */
  .traffic-flow {
    stroke-dasharray: 4 6;
    animation-name: traffic-flow;
    animation-timing-function: linear;
    animation-iteration-count: infinite;
  }

  @keyframes traffic-flow {
    to {
      stroke-dashoffset: -20;
    }
  }

  @media (prefers-reduced-motion: reduce) {
    .flow,
    .traffic-flow {
      animation: none;
    }
  }
</style>
