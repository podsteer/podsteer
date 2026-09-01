<!--
  The dependency chain around one pod, drawn as a map.

  WHAT IT IS FOR. Every other view here answers a question about one object.
  This answers the one nobody can hold in their head on an unfamiliar cluster:
  what reaches this pod, and what does it need to work. Following a request
  from an Ingress to a container is otherwise four `kubectl describe`s and a
  guess about which Service's selector matches.

  DRAWN IN TIERS, not as a free-floating web. A force layout would let the
  reader find the shape themselves; laid out from what is outside the cluster
  down to what actually runs, the picture says which way traffic goes before
  anybody reads a label. Attached resources — config, secrets, the node — sit
  at the bottom because they are dependencies but not steps on the path.

  Colour carries one thing only: whether something is worth looking at. A map
  where everything is one colour says where things are; the colour says where
  to start.
-->
<script lang="ts">
  import { onDestroy } from 'svelte'
  import type { Chart } from '$lib/echarts'
  import { podGraph, type PodGraph } from '$lib/api/client'
  import { toApiError } from '$lib/api/errors'
  import { onThemeChange } from '$lib/terminalTheme'
  import { iconURI, preloadIcons } from '$lib/graphIcons'
  import { preferences } from '$stores/preferences.svelte'
  import PaneToolbar from './PaneToolbar.svelte'
  import ToolbarButton from './ToolbarButton.svelte'
  import { Maximize2, Columns3, Rows3, ZoomIn, ZoomOut, Crosshair } from '@lucide/svelte'

  /** The little of a graph node a click handler needs. */
  interface GraphNodeData {
    apiKind: string
    name: string
    namespace: string
  }

  interface Props {
    clusterId: string
    namespace: string
    podName: string
    /** Follows a node into its own panel. */
    onopen?: (kindName: string, name: string, namespace: string) => void
    /** Offered when the pane can still be made bigger. */
    onmaximize?: () => void
  }

  let { clusterId, namespace, podName, onopen, onmaximize }: Props = $props()

  /**
   * Which way the chain runs.
   *
   * HORIZONTAL BY DEFAULT, because the labels are wide and the tiers are few:
   * laid out left to right there is room beside each node for its kind and its
   * name, where stacked vertically the same labels collide. Vertical is
   * offered because a pod with many containers is a long row, and turning the
   * map on its side is the cheapest way to see all of it.
   *
   * Remembered like every other display preference, so it does not have to be
   * set again on the next pod.
   */
  const orientation = $derived(preferences.mapOrientation)

  let container = $state<HTMLDivElement | null>(null)
  let chart: Chart | null = null
  let graph = $state.raw<PodGraph | null>(null)
  let failure = $state('')
  let loading = $state(false)
  let loadedFor = $state('')
  let stopWatchingTheme: (() => void) | null = null

  async function load(): Promise<void> {
    const key = `${clusterId}/${namespace}/${podName}`
    if (loading || loadedFor === key) return

    loading = true
    failure = ''
    try {
      graph = await podGraph(clusterId, namespace, podName)
      loadedFor = key
    } catch (error) {
      failure = toApiError(error).message
      graph = null
    } finally {
      loading = false
    }
  }

  // A different pod invalidates the map. Fetched here rather than on a timer:
  // a dependency chain changes when somebody changes it, not every ten
  // seconds, and redrawing a graph under a reader is worse than staleness.
  $effect(() => {
    const key = `${clusterId}/${namespace}/${podName}`
    if (key !== loadedFor) void load()
  })

  /** Reads a CSS custom property, so the chart follows the application's theme. */
  function token(name: string, fallback: string): string {
    const value = getComputedStyle(document.documentElement).getPropertyValue(name).trim()
    return value === '' ? fallback : value
  }

  /**
   * The gap between tiers and between nodes within one, in chart units.
   *
   * Arbitrary numbers in an arbitrary space — ECharts fits the whole node
   * bounding box into the pane — so what matters is only their RATIO. The
   * across-gap is generous because that axis carries the labels.
   */
  const TIER_GAP = 260
  const NODE_GAP = 120

  /**
   * Lays the graph out by tier, and routes every edge as a right angle.
   *
   * POSITIONS ARE COMPUTED, not left to a force simulation. A force layout of
   * twenty nodes settles somewhere different every time it is run, so the same
   * pod would draw a different picture on each visit and nothing could be
   * found twice.
   *
   * EDGES TURN CORNERS RATHER THAN CUTTING ACROSS. ECharts' graph series has
   * no orthogonal router, so each edge is drawn as three segments through two
   * INVISIBLE WAYPOINTS at the midpoint between the tiers. Diagonals were the
   * real source of the clutter: they crossed each other and passed under the
   * labels, where right angles share corridors and leave the space between
   * tiers empty for text.
   */
  function positioned(data: PodGraph): { nodes: unknown[]; links: unknown[]; icons: string[] } {
    const ink = token('--on-surface-variant', '#9aa0a6')
    const text = token('--on-surface', '#e6e0e9')
    const line = token('--outline', '#938f99')
    const good = token('--gauge-normal', '#4f86e8')
    const bad = token('--gauge-critical', '#d8453c')
    const accent = token('--primary', '#8ab4f8')
    const horizontal = orientation === 'horizontal'

    const byTier = new Map<number, typeof data.nodes>()
    for (const node of data.nodes) {
      const row = byTier.get(node.tier)
      if (row) row.push(node)
      else byTier.set(node.tier, [node])
    }

    // Only tiers with something in them take space. A pod with no ingress
    // should not sit under an empty column — the gap reads as a missing thing
    // rather than an absent one.
    const present = [...byTier.keys()].sort((a, b) => a - b)
    const place = new Map(present.map((tier, index) => [tier, index]))

    const at = new Map<string, { x: number; y: number }>()
    const icons: string[] = []

    const nodes = data.nodes.map((node) => {
      const row = byTier.get(node.tier)!
      const index = row.indexOf(node)
      // Centred within the tier, so a tier of one sits opposite the middle of
      // a tier of six rather than against its edge.
      const across = (index - (row.length - 1) / 2) * NODE_GAP
      const along = (place.get(node.tier) ?? 0) * TIER_GAP

      const x = horizontal ? along : across
      const y = horizontal ? across : along
      at.set(node.id, { x, y })

      const colour = node.healthy ? (node.subject ? accent : good) : bad
      const symbol = iconURI(node.kind, colour)
      icons.push(symbol)

      return {
        id: node.id,
        name: node.name,
        x,
        y,
        symbol,
        symbolSize: node.subject ? 30 : 24,
        label: {
          show: true,
          // Below the node in both orientations. Beside it, a long pod name
          // ran straight into whatever was in the next tier — which is what
          // put "…d64xh" on top of "Container" in the horizontal layout.
          position: 'bottom',
          distance: 8,
          align: 'center',
          color: text,
          fontSize: 11,
          lineHeight: 15,
          // Kind in bold above the name. A map of twenty boxes is read by
          // shape first: the kind says what a thing is, the name says which
          // one, and that is the order they are needed in.
          formatter: `{k|${node.apiKind || 'Container'}}\n{n|${node.name}}`,
          rich: {
            k: { color: text, fontWeight: 'bold', fontSize: 11, lineHeight: 15 },
            n: { color: ink, fontSize: 10, lineHeight: 14 },
          },
        },
        emphasis: { label: { show: true } },
        podsteer: node,
      }
    })

    // The waypoints. Invisible, unlabelled, and not clickable — they exist
    // only so a line can turn a corner.
    const waypoints: unknown[] = []
    const links: unknown[] = []

    for (const [index, edge] of data.edges.entries()) {
      const from = at.get(edge.from)
      const to = at.get(edge.to)
      if (!from || !to) continue

      const style = { color: line, opacity: 0.45, width: 1.2, curveness: 0 }

      // Already square: nothing to route around.
      if (from.x === to.x || from.y === to.y) {
        links.push({ source: edge.from, target: edge.to, lineStyle: style })
        continue
      }

      // The turn happens halfway between the tiers, so every edge crossing
      // the same gap shares one corridor instead of fanning.
      const midAlong = horizontal ? (from.x + to.x) / 2 : (from.y + to.y) / 2
      const a = horizontal ? { x: midAlong, y: from.y } : { x: from.x, y: midAlong }
      const b = horizontal ? { x: midAlong, y: to.y } : { x: to.x, y: midAlong }

      const first = `bend/${index}/a`
      const second = `bend/${index}/b`
      for (const [id, point] of [
        [first, a],
        [second, b],
      ] as const) {
        waypoints.push({
          id,
          name: '',
          x: point.x,
          y: point.y,
          symbol: 'none',
          symbolSize: 0,
          label: { show: false },
          emphasis: { disabled: true },
          silent: true,
        })
      }

      links.push(
        { source: edge.from, target: first, lineStyle: style },
        { source: first, target: second, lineStyle: style },
        { source: second, target: edge.to, lineStyle: style },
      )
    }

    return { nodes: [...nodes, ...waypoints], links, icons }
  }

  function option(data: PodGraph): { option: unknown; icons: string[] } {
    const line = token('--outline', '#938f99')
    const text = token('--on-surface', '#e6e0e9')
    const { nodes, links, icons } = positioned(data)

    return {
      icons,
      option: {
        animation: false,
        // Everything a tooltip would say is on the node already, and a tooltip
        // on a map somebody is panning is a box that follows the cursor over
        // what they are looking at.
        tooltip: { show: false },
        series: [
          {
            type: 'graph',
            layout: 'none',
            roam: true,
            // A FLOOR AND A CEILING ON THE ZOOM. Without a floor the map can
            // be pinched down to an illegible knot of overlapping labels,
            // which is easy to do by accident on a trackpad and hard to undo
            // without a reset.
            scaleLimit: { min: 0.5, max: 4 },
            data: nodes,
            links,
            edgeSymbol: ['none', 'arrow'],
            edgeSymbolSize: 8,
            // Labels off the edges entirely. Six edges reading "environment"
            // fanning into one tier is the clutter, not the lines — the
            // relationship is legible from what the two ends are.
            emphasis: {
              focus: 'adjacency',
              scale: false,
              // The adjacent lines go bright instead of everything else going
              // dark: the point of hovering is to trace one path, and that
              // reads better as the path lighting up than as the map
              // dimming out.
              lineStyle: { color: text, opacity: 1, width: 2 },
              itemStyle: { opacity: 1 },
            },
            blur: {
              // GENTLY. At the default the rest of the map disappears, which
              // loses the context that made hovering worth doing.
              itemStyle: { opacity: 0.45 },
              lineStyle: { color: line, opacity: 0.15 },
              label: { opacity: 0.35 },
            },
            left: '6%',
            right: '6%',
            top: '10%',
            bottom: '12%',
          },
        ],
      },
    }
  }

  async function draw(): Promise<void> {
    if (!container || !graph) return

    // NOT INTO A BOX WITH NO SIZE. Switching to this tab, or moving the pane
    // into the maximise dialog, runs this before layout has given the element
    // any height — and a chart laid out against a zero box draws every node on
    // top of every other, which is the knot of overlapping labels it looked
    // like. The ResizeObserver calls back the moment there is room.
    const box = container.getBoundingClientRect()
    if (box.width < 40 || box.height < 40) return

    if (!chart) {
      const { createChart } = await import('$lib/echarts')
      if (!container) return
      chart = createChart(container)

      // FOLLOWING IS THE POINT. A map that shows a failing ConfigMap and
      // makes somebody go and find it in the navigator has stopped halfway.
      chart.on('click', (params: unknown) => {
        const node = (params as { data?: { podsteer?: GraphNodeData } }).data?.podsteer
        // Containers are not objects and have no panel of their own; the pod
        // they belong to is already the subject of this map. Waypoints carry
        // no payload at all.
        if (!node?.apiKind || !onopen) return
        onopen(node.apiKind.toLowerCase() + 's', node.name, node.namespace || namespace)
      })
    }

    const built = option(graph)
    // BEFORE setOption, NOT AFTER. ECharts paints an image symbol with
    // whatever the browser has decoded, and with animation off there is no
    // second frame to catch up on — so a cold cache renders the nodes bare
    // and leaves them that way.
    await preloadIcons(built.icons)
    if (!chart) return

    chart.setOption(built.option, true)
    // The pane may have changed size while the pane was hidden — moving into
    // the maximise dialog is exactly that — and a chart laid out against a
    // stale box draws everything squeezed into a corner.
    chart.resize()
  }

  /**
   * Steps the zoom, for the toolbar's buttons.
   *
   * Anchored at the CENTRE of the pane rather than its origin: zooming from
   * the corner walks the map off to one side, so three presses of a button
   * that should have magnified what somebody is looking at leaves them
   * looking at nothing.
   */
  function zoomBy(factor: number): void {
    if (!container) return
    const box = container.getBoundingClientRect()
    chart?.dispatchAction({
      type: 'graphRoam',
      zoom: factor,
      originX: box.width / 2,
      originY: box.height / 2,
    })
  }

  /** Puts the map back where it started, which pan and zoom make necessary. */
  function resetView(): void {
    void draw()
  }

  $effect(() => {
    void graph
    void orientation
    void draw()
  })

  $effect(() => {
    if (!container) return
    const observer = new ResizeObserver(() => {
      // A chart that was never drawn — because the pane had no size — has
      // nothing to resize, so this draws instead of resizing when there is
      // still no series in it.
      if (chart) chart.resize()
      else void draw()
    })
    observer.observe(container)

    stopWatchingTheme = onThemeChange(() => void draw())
    return () => {
      observer.disconnect()
      stopWatchingTheme?.()
    }
  })

  onDestroy(() => {
    chart?.dispose()
    chart = null
  })
</script>

<div class="flex h-full flex-col">
  <PaneToolbar>
    <span class="shrink-0 pl-1 text-body-small text-on-surface-variant">
      {#if graph}
        {graph.nodes.length} resources
      {:else}
        Dependencies
      {/if}
    </span>

    {#snippet trailing()}
      <!--
        One control, two states, rather than two buttons where only one is ever
        useful. The icon shows what pressing it WILL DO, not the state it is
        in — a toggle that shows its own state is the commonest way to make
        somebody press it twice to find out.
      -->
      <ToolbarButton icon={ZoomOut} label="Zoom out" title="Zoom out" onclick={() => zoomBy(0.8)} />
      <ToolbarButton icon={ZoomIn} label="Zoom in" title="Zoom in" onclick={() => zoomBy(1.25)} />
      <!--
        Pan and zoom have no undo of their own, and a map dragged off screen
        looks like a map that failed to load. This is the way back.
      -->
      <ToolbarButton icon={Crosshair} label="Fit to the pane" title="Fit to the pane" onclick={resetView} />

      <div class="mx-0.5 h-5 w-px shrink-0 bg-outline-variant/60" aria-hidden="true"></div>

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
    <!--
      The chart fills the pane. The tier names that used to run down the side
      are gone: they only made sense on a vertical layout, and with the kind
      now written on every node in bold they were labelling what each box
      already says about itself.
    -->
    <div bind:this={container} class="min-h-0 min-w-0 flex-1"></div>

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
