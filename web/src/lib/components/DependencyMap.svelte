<!--
  The dependency chain around one pod, drawn as a map.

  WHAT IT IS FOR. Every other view here answers a question about one object.
  This answers the one nobody can hold in their head on an unfamiliar cluster:
  what reaches this pod, and what does it need to work. Following a request
  from an Ingress to a container is otherwise four `kubectl describe`s and a
  guess about which Service's selector matches.

  DRAWN AS SVG RATHER THAN AS A CHART, and that was a correction. ECharts drew
  this first, and its graph series connects node CENTRES clipped to the symbol
  — a label is not part of a node, so every line began inside an icon and ran
  out through the text under it. Routing around that needed invisible waypoint
  nodes, which made each edge three separate links: hovering lit one segment of
  a route, overlapping segments composited into a heavier stroke, and corners
  could not be rounded at all. Each of those is a consequence of using a chart
  to draw a diagram. The geometry now lives in graphLayout.ts, where it is
  tested, and this file draws it.

  Colour carries one thing only: whether something is worth looking at. A map
  where everything is one colour says where things are; the colour says where
  to start.
-->
<script lang="ts">
  import { podGraph, workloadGraph, objectGraph, type PodGraph } from '$lib/api/client'
  import { toApiError } from '$lib/api/errors'
  import { iconGeometry } from '$lib/graphIcons'
  import { layout, type Layout, type LaidOutNode } from '$lib/graphLayout'
  import { fold } from '$lib/graphFold'
  import { preferences } from '$stores/preferences.svelte'
  import PaneToolbar from './PaneToolbar.svelte'
  import ToolbarButton from './ToolbarButton.svelte'
  import { Maximize2, Columns3, Rows3, ZoomIn, ZoomOut, Crosshair, Layers } from '@lucide/svelte'

  interface Props {
    clusterId: string
    namespace: string
    /** The object the map is drawn around. */
    name: string
    /**
     * Its Kubernetes kind.
     *
     * "Pod" draws the chain with the pod in the middle; a workload kind draws
     * the fan — one controller over however many pods it currently has; and
     * anything else draws the neighbourhood, with the object in the middle,
     * what its spec names below it and what owns it above. Three shapes,
     * because the SUBJECT decides the structure — the backend builds them
     * separately and this decides which to ask for.
     */
    kind: string
    /**
     * The navigator catalogue id of the object, for the third shape.
     *
     * A KIND NAME IS NOT ENOUGH TO READ AN ARBITRARY OBJECT. The backend has
     * to know the API group, version and resource to build a path, and the
     * catalogue id is what carries all three — "Widget" alone would need a
     * lookup the drawer has already done. Unused for a Pod or a workload,
     * whose two shapes are keyed by kind.
     */
    kindId?: string
    /** Follows a node into its own panel. */
    onopen?: (kindName: string, name: string, namespace: string) => void
    /** Offered when the pane can still be made bigger. */
    onmaximize?: () => void
  }

  let { clusterId, namespace, name, kind, kindId, onopen, onmaximize }: Props = $props()

  /**
   * The kinds whose map is the FAN — one controller over its pods.
   *
   * Named as a set rather than left as "anything that is not a Pod", which is
   * what this used to mean and what silently sent a Service to the workload
   * endpoint the moment the map was offered on every kind. The backend refuses
   * an unknown workload kind, so the failure was visible; a map that asked the
   * wrong endpoint at all is the bug worth naming.
   */
  const WORKLOAD_MAP_KINDS = new Set([
    'Deployment',
    'StatefulSet',
    'DaemonSet',
    'ReplicaSet',
    'Job',
    'CronJob',
  ])

  /**
   * Which way the chain runs.
   *
   * HORIZONTAL BY DEFAULT, because a box is wider than it is tall and a wide
   * box wants a wide gap beside it rather than above it. Vertical is offered
   * because a pod with many attached resources is a long row, and turning the
   * map on its side is the cheapest way to see all of it. Remembered like
   * every other display preference.
   */
  const orientation = $derived(preferences.mapOrientation)

  let graph = $state.raw<PodGraph | null>(null)
  let failure = $state('')
  let loading = $state(false)
  let loadedFor = $state('')

  let viewport = $state<HTMLDivElement | null>(null)
  let paneWidth = $state(0)
  let paneHeight = $state(0)

  /** Pan and zoom, applied as one transform on the drawing. */
  let zoom = $state(1)
  let panX = $state(0)
  let panY = $state(0)
  let dragging = $state(false)
  let dragFrom = { x: 0, y: 0, panX: 0, panY: 0 }

  /** The box the pointer is over, which lights its own lines. */
  let hovered = $state<string | null>(null)

  /**
   * Sibling sets the operator has opened.
   *
   * THE GRAPH IS ALWAYS COMPLETE; this only decides what is drawn. A folded
   * set is one box standing for its members, and expanding puts them back —
   * so a busy map stays navigable without anything being left out of it.
   */
  let expanded = $state.raw<Set<string>>(new Set())

  const folded = $derived(graph ? fold(graph, expanded) : null)

  const plan = $derived<Layout | null>(
    folded ? layout(folded, orientation === 'horizontal') : null,
  )

  /** Which drawn nodes are folded sets, by id. */
  const foldedByID = $derived(
    new Map((folded?.nodes ?? []).filter((n) => n.fold).map((n) => [n.id, n.fold!])),
  )

  /** Whether every foldable set is currently open. */
  const allExpanded = $derived(
    Boolean(folded?.groups.length) && folded!.groups.every((g) => expanded.has(g.id)),
  )

  /** Opens every set at once, or closes them all. */
  function toggleAllFolds(): void {
    expanded = allExpanded ? new Set() : new Set((folded?.groups ?? []).map((g) => g.id))
  }

  /** Opens or closes one set. */
  function toggleFold(id: string): void {
    const next = new Set(expanded)
    if (next.has(id)) next.delete(id)
    else next.add(id)
    expanded = next
  }

  /**
   * Whether an edge should show its flow.
   *
   * EVERYTHING WHEN NOTHING IS HOVERED, and only the route in question when
   * something is. At rest the movement says the map is a set of directed
   * dependencies rather than a static diagram; under the pointer, stopping it
   * everywhere else is what makes the one path stand out — the eye follows
   * motion, so leaving it on for all of them would drown the answer.
   */
  function flowing(id: string): boolean {
    return hovered === null || lit.has(id)
  }

  /** Edges touching the hovered box, so a whole route lights at once. */
  const lit = $derived.by(() => {
    if (!hovered || !plan) return new Set<string>()
    return new Set(
      plan.edges.filter((e) => e.from === hovered || e.to === hovered).map((e) => e.id),
    )
  })

  /**
   * What identifies the map currently drawn.
   *
   * The catalogue id is part of it because two kinds can share a Kind name in
   * different API groups — "Application" exists in three — and a key that did
   * not separate them would serve one operator's map for the other's object.
   */
  const loadKey = $derived(`${clusterId}/${namespace}/${kind}/${kindId ?? ''}/${name}`)

  async function load(): Promise<void> {
    const key = loadKey
    if (loading || loadedFor === key) return

    loading = true
    failure = ''
    try {
      graph =
        kind === 'Pod'
          ? await podGraph(clusterId, namespace, name)
          : WORKLOAD_MAP_KINDS.has(kind)
            ? await workloadGraph(clusterId, namespace, kind, name)
            : await objectGraph(clusterId, kindId ?? '', namespace, name)
      loadedFor = key
      // A different object's sets are not this one's.
      expanded = new Set()
      fit()
    } catch (error) {
      failure = toApiError(error).message
      graph = null
    } finally {
      loading = false
    }
  }

  // Fetched when the pod changes, not on a timer: a dependency chain changes
  // when somebody changes it, and redrawing a map under a reader is worse than
  // it being a few seconds stale.
  $effect(() => {
    if (loadKey !== loadedFor) void load()
  })

  /** Sizes the drawing to the pane, which is also the way back from a pan. */
  function fit(): void {
    if (!plan || paneWidth === 0 || paneHeight === 0) return

    const scale = Math.min(
      paneWidth / plan.bounds.width,
      paneHeight / plan.bounds.height,
      // NEVER MAGNIFIED PAST LIFE SIZE. A map of three boxes stretched to fill
      // a wide pane draws its text at a size nothing else in the application
      // uses, which reads as a different application.
      1,
    )

    zoom = scale
    panX = (paneWidth - plan.bounds.width * scale) / 2 - plan.bounds.x * scale
    panY = (paneHeight - plan.bounds.height * scale) / 2 - plan.bounds.y * scale
  }

  // Re-fit when the layout or the pane changes. Both happen without anybody
  // asking — switching orientation, opening the maximise dialog.
  $effect(() => {
    void plan
    void paneWidth
    void paneHeight
    fit()
  })

  /**
   * Zooms about the centre of the pane.
   *
   * Anchored at the centre rather than the origin: zooming from a corner walks
   * the map off to one side, so three presses of a button that should have
   * magnified what somebody is looking at leaves them looking at nothing.
   */
  function zoomBy(factor: number): void {
    const next = clamp(zoom * factor)
    if (next === zoom) return

    const cx = paneWidth / 2
    const cy = paneHeight / 2
    panX = cx - ((cx - panX) / zoom) * next
    panY = cy - ((cy - panY) / zoom) * next
    zoom = next
  }

  /** A floor and a ceiling, so the map cannot be pinched into an illegible knot. */
  function clamp(value: number): number {
    return Math.min(Math.max(value, 0.3), 3)
  }

  function onWheel(event: WheelEvent): void {
    event.preventDefault()

    // About the POINTER, which is what a trackpad gesture means: the thing
    // under the fingers should stay under them.
    const next = clamp(zoom * (event.deltaY < 0 ? 1.1 : 1 / 1.1))
    if (next === zoom) return

    const box = viewport!.getBoundingClientRect()
    const px = event.clientX - box.left
    const py = event.clientY - box.top

    panX = px - ((px - panX) / zoom) * next
    panY = py - ((py - panY) / zoom) * next
    zoom = next
  }

  function onPointerDown(event: PointerEvent): void {
    // Only a drag on the background pans; a drag starting on a box would fight
    // the click that follows it.
    if ((event.target as Element).closest('[data-node]')) return

    dragging = true
    dragFrom = { x: event.clientX, y: event.clientY, panX, panY }
    ;(event.currentTarget as Element).setPointerCapture(event.pointerId)
  }

  function onPointerMove(event: PointerEvent): void {
    if (!dragging) return
    panX = dragFrom.panX + (event.clientX - dragFrom.x)
    panY = dragFrom.panY + (event.clientY - dragFrom.y)
  }

  function onPointerUp(event: PointerEvent): void {
    dragging = false
    ;(event.currentTarget as Element).releasePointerCapture?.(event.pointerId)
  }

  /**
   * Follows a node into its own panel.
   *
   * THE KUBERNETES KIND, VERBATIM. The drawer resolves this against the
   * navigator's catalogue, which is keyed by `Kind` — "Ingress", "ConfigMap" —
   * so a lowercased plural matches nothing and the click silently does
   * nothing at all. That is what it did: every node, every kind.
   */
  function open(node: LaidOutNode): void {
    // A folded set is not an object. Clicking it opens the set, which is the
    // only thing it could usefully mean.
    if (foldedByID.has(node.id)) {
      toggleFold(node.id)
      return
    }

    // Containers are not objects and have no panel of their own; the pod they
    // belong to is already the subject of this map.
    if (!node.apiKind || !onopen) return
    onopen(node.apiKind, node.name, node.namespace || namespace)
  }

  /**
   * Shortens a name to what the box can hold.
   *
   * Measured in characters rather than pixels, which is approximate and good
   * enough: the alternative is measuring text in the DOM on every redraw for a
   * label that is truncated either way.
   */
  function fitText(value: string, characters: number): string {
    return value.length <= characters ? value : value.slice(0, characters - 1) + '…'
  }
</script>


{#snippet box(node: LaidOutNode, half: { w: number; h: number })}
                <!-- The hit area is the whole object, icon and text together,
                     which is also what the lines are routed between. -->
                <rect
                  x={-half.w} y={-half.h} width={node.width} height={node.height}
                  rx="8"
                  class="fill-transparent {hovered === node.id
                    ? 'fill-surface-container-high/60'
                    : ''}"
                />

                <!--
                  A MISSING OBJECT IS OUTLINED, and dashed deliberately: a
                  dashed border is the one convention that already reads as
                  "not really there", which is exactly what a reference to
                  something nobody created is. Colour alone would say only
                  "unwell", and an object that exists and is failing needs
                  opposite advice from one that was named and is absent.
                -->
                {#if node.missing}
                  <rect
                    x={-half.w} y={-half.h} width={node.width} height={node.height}
                    rx="8"
                    fill="none"
                    stroke-width="1.5"
                    stroke-dasharray="4 4"
                    class="stroke-gauge-critical"
                  />
                {/if}

                <!--
                  The qualifier the box has no room for a line of: a pod's
                  phase, "not found", how many of a folded set are. Rendered as
                  a title so it costs no height and is read out by assistive
                  technology alongside the label.
                -->
                {#if node.detail}
                  <title>{node.name} — {node.detail}</title>
                {/if}

                <!--
                  A FOLDED SET IS DRAWN AS A STACK. Two offset outlines behind
                  the icon say "more than one" before the count is read, and
                  the +/- says it opens. Without that it is a box like any
                  other, and somebody would take "30 Pods" for the name of a
                  thing rather than a summary of thirty.
                -->
                {#if foldedByID.has(node.id)}
                  <g
                    transform="translate(-12 {-half.h + 6})"
                    fill="none"
                    stroke-width="2"
                    stroke-linecap="round"
                    stroke-linejoin="round"
                    class="{node.healthy
                      ? 'stroke-gauge-normal'
                      : 'stroke-gauge-critical'} opacity-30"
                  >
                    <g transform="translate(5 5)">{@html iconGeometry(node.kind)}</g>
                  </g>
                  <g
                    transform="translate(-12 {-half.h + 6})"
                    fill="none"
                    stroke-width="2"
                    stroke-linecap="round"
                    stroke-linejoin="round"
                    class="{node.healthy
                      ? 'stroke-gauge-normal'
                      : 'stroke-gauge-critical'} opacity-60"
                  >
                    <g transform="translate(2.5 2.5)">{@html iconGeometry(node.kind)}</g>
                  </g>
                {/if}

                <g
                  transform="translate(-12 {-half.h + 6})"
                  fill="none"
                  stroke-width="2"
                  stroke-linecap="round"
                  stroke-linejoin="round"
                  class={node.healthy
                    ? node.subject
                      ? 'stroke-primary'
                      : 'stroke-gauge-normal'
                    : 'stroke-gauge-critical'}
                >
                  {@html iconGeometry(node.kind)}
                </g>

                <!-- Kind above name. A map of twenty boxes is read by shape
                     first: the kind says what a thing is, the name says which
                     one, and that is the order they are needed in. -->
                <text
                  y={-half.h + 44}
                  text-anchor="middle"
                  class="fill-on-surface text-[11px] font-semibold"
                >
                  {node.apiKind || 'Container'}
                </text>
                <text
                  y={-half.h + 58}
                  text-anchor="middle"
                  class="fill-on-surface-variant text-[10px]"
                >
                  {fitText(node.name, 26)}
                </text>

                {#if foldedByID.has(node.id)}
                  <!--
                    Only a folded set carries the control. On an expanded set
                    it would be a badge per member, and the set's own box is
                    the way back — clicking it again closes it.
                  -->
                  <g transform="translate({half.w - 16} {-half.h + 10})">
                    <circle r="8" class="fill-surface-container-high stroke-outline-variant" />
                    <path
                      d="M -3.5 0 H 3.5 M 0 -3.5 V 3.5"
                      stroke-width="1.6"
                      stroke-linecap="round"
                      class="stroke-on-surface"
                    />
                  </g>
                {/if}
{/snippet}

<div class="flex h-full flex-col">
  <PaneToolbar>
    <span class="shrink-0 pl-1 text-body-small text-on-surface-variant">
      {#if graph}
        <!--
          The COMPLETE count, always. Folding changes what is drawn and must
          never change what the map says it found, or a collapsed set becomes
          a way to under-report a cluster.
        -->
        {graph.nodes.length} resources
      {:else}
        Dependencies
      {/if}
    </span>

    {#snippet trailing()}
      {#if folded && folded.groups.length > 0}
        <!--
          Only when there is something to fold. A control that is always
          present and usually does nothing is one people stop seeing.
        -->
        <ToolbarButton
          icon={Layers}
          label={allExpanded ? 'Collapse all groups' : 'Expand all groups'}
          title={allExpanded
            ? 'Collapse all groups'
            : `Expand all ${folded.groups.length} groups`}
          active={allExpanded}
          onclick={toggleAllFolds}
        />
        <div class="mx-0.5 h-5 w-px shrink-0 bg-outline-variant/60" aria-hidden="true"></div>
      {/if}

      <ToolbarButton icon={ZoomOut} label="Zoom out" title="Zoom out" onclick={() => zoomBy(1 / 1.25)} />
      <ToolbarButton icon={ZoomIn} label="Zoom in" title="Zoom in" onclick={() => zoomBy(1.25)} />
      <!-- Pan and zoom have no undo of their own, and a map dragged off screen
           looks like a map that failed to load. This is the way back. -->
      <ToolbarButton icon={Crosshair} label="Fit to the pane" title="Fit to the pane" onclick={fit} />

      <div class="mx-0.5 h-5 w-px shrink-0 bg-outline-variant/60" aria-hidden="true"></div>

      <!-- One control, two states. The icon shows what pressing it WILL DO,
           not the state it is in — a toggle that shows its own state is the
           commonest way to make somebody press it twice to find out. -->
      <ToolbarButton
        icon={orientation === 'horizontal' ? Rows3 : Columns3}
        label={orientation === 'horizontal' ? 'Lay out vertically' : 'Lay out horizontally'}
        title={orientation === 'horizontal' ? 'Lay out vertically' : 'Lay out horizontally'}
        onclick={() =>
          preferences.setMapOrientation(orientation === 'horizontal' ? 'vertical' : 'horizontal')}
      />

      {#if onmaximize}
        <div class="mx-0.5 h-5 w-px shrink-0 bg-outline-variant/60" aria-hidden="true"></div>
        <ToolbarButton icon={Maximize2} label="Maximise" title="Maximise" onclick={onmaximize} />
      {/if}
    {/snippet}
  </PaneToolbar>

  {#if loading && !graph}
    <p class="p-4 text-body-medium text-on-surface-variant/70">Reading the dependency chain…</p>
  {:else if failure}
    <p class="p-4 text-body-medium text-error">{failure}</p>
  {:else}
    <div
      bind:this={viewport}
      bind:clientWidth={paneWidth}
      bind:clientHeight={paneHeight}
      class="relative min-h-0 flex-1 overflow-hidden {dragging ? 'cursor-grabbing' : 'cursor-grab'}"
      onwheel={onWheel}
      onpointerdown={onPointerDown}
      onpointermove={onPointerMove}
      onpointerup={onPointerUp}
      onpointercancel={onPointerUp}
      role="presentation"
    >
      {#if plan}
        <svg class="size-full select-none" aria-label="Dependency map">
          <defs>
            <!-- One marker per state rather than one recoloured: an SVG marker
                 cannot inherit the stroke of the path that uses it. -->
            <marker id="dep-arrow" viewBox="0 0 10 10" refX="9" refY="5"
                    markerWidth="6" markerHeight="6" orient="auto-start-reverse">
              <path d="M 0 1 L 9 5 L 0 9 z" class="fill-outline" />
            </marker>
            <marker id="dep-arrow-lit" viewBox="0 0 10 10" refX="9" refY="5"
                    markerWidth="6" markerHeight="6" orient="auto-start-reverse">
              <path d="M 0 1 L 9 5 L 0 9 z" class="fill-on-surface" />
            </marker>
          </defs>

          <g transform="translate({panX} {panY}) scale({zoom})">
            <!--
              Lines first, so a box always sits over them. Each route is ONE
              path, so lighting it lights the whole way from one object to the
              other — and where two of them overlap nothing composites,
              because the stroke is opaque and drawn once.
            -->
            {#each plan.edges as edge (edge.id)}
              <path
                d={edge.path}
                fill="none"
                stroke-width="1.25"
                marker-end="url(#{lit.has(edge.id) ? 'dep-arrow-lit' : 'dep-arrow'})"
                class="{lit.has(edge.id) ? 'stroke-on-surface' : 'stroke-outline'} {flowing(
                  edge.id,
                )
                  ? 'flow'
                  : ''}"
              />
            {/each}

            {#each plan.nodes as node (node.id)}
              {@const half = { w: node.width / 2, h: node.height / 2 }}
              {#if node.apiKind}
                <!--
                  A followable object is a button. Containers are not objects
                  and have no panel of their own — the pod they belong to is
                  already the subject of this map — so they are drawn as a
                  picture rather than given interactive markup that leads
                  nowhere.
                -->
                <g
                  data-node
                  transform="translate({node.x} {node.y})"
                  class="cursor-pointer"
                  role="button"
                  tabindex="0"
                  aria-label="Open {node.apiKind} {node.name}{node.missing
                    ? ', which was not found'
                    : ''}"
                  onmouseenter={() => (hovered = node.id)}
                  onmouseleave={() => (hovered = null)}
                  onfocus={() => (hovered = node.id)}
                  onblur={() => (hovered = null)}
                  onclick={() => open(node)}
                  onkeydown={(event) => {
                    if (event.key === 'Enter' || event.key === ' ') {
                      event.preventDefault()
                      open(node)
                    }
                  }}
                >
                  {@render box(node, half)}
                </g>
              {:else}
                <g
                  data-node
                  transform="translate({node.x} {node.y})"
                  role="img"
                  aria-label="Container {node.name}"
                  onmouseenter={() => (hovered = node.id)}
                  onmouseleave={() => (hovered = null)}
                >
                  {@render box(node, half)}
                </g>
              {/if}
            {/each}
          </g>
        </svg>
      {/if}
    </div>

    {#if graph && graph.bounded}
      <!--
        SAYING WHY IS THE WHOLE POINT. Empty space under an object reads as
        "nothing depends on this", which is a claim nothing here checked:
        Kubernetes has no index of what refers to what, and answering it would
        mean listing every kind in the namespace every time this pane opened.
        Drawn separately from the unreadable line below because they are
        different facts — that one a permission would fix, this one it would
        not.
      -->
      <p
        class="shrink-0 border-t border-outline-variant/40 bg-surface-container-low px-4 py-2
               text-body-small text-on-surface-variant"
      >
        {graph.bounded}
      </p>
    {/if}

    {#if graph && graph.unreadable.length > 0}
      <!--
        An incomplete map has to say so. An account that cannot list ingresses
        gets a map with no ingress tier, and without this it looks like a pod
        nothing routes to — which is a different and much more alarming fact.
      -->
      <p
        class="shrink-0 border-t border-outline-variant/40 bg-surface-container-low px-4 py-2
               text-body-small text-on-surface-variant"
      >
        Could not read {graph.unreadable.join(' or ')} in this namespace, so anything reached
        through them is missing from this map.
      </p>
    {/if}
  {/if}
</div>

<style>
  /*
    The dashes travel from source to target, which is the direction the
    dependency runs — a decreasing offset advances along the path, so the
    movement reads as flow rather than as decoration.

    A dash pattern is only applied while animating. A static dashed line reads
    as "provisional" or "not real" in every other diagram somebody has seen,
    which is the wrong thing to say about a dependency that exists.
  */
  .flow {
    stroke-dasharray: 5 9;
    animation: dependency-flow 900ms linear infinite;
  }

  @keyframes dependency-flow {
    to {
      stroke-dashoffset: -14;
    }
  }

  /*
    Movement is the one thing a person cannot look away from, so anybody who
    has asked for less of it gets a solid line and no animation at all — not a
    slower one.
  */
  @media (prefers-reduced-motion: reduce) {
    .flow {
      stroke-dasharray: none;
      animation: none;
    }
  }
</style>
