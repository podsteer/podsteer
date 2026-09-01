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
  import { iconURI } from '$lib/graphIcons'
  import { preferences } from '$stores/preferences.svelte'
  import PaneToolbar from './PaneToolbar.svelte'
  import ToolbarButton from './ToolbarButton.svelte'
  import { Maximize2, Columns3, Rows3 } from '@lucide/svelte'

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
   * Lays the graph out by tier.
   *
   * POSITIONS ARE COMPUTED, not left to a force simulation. A force layout of
   * twenty nodes settles somewhere different every time it is run, so the same
   * pod would draw a different picture on each visit and nothing could be
   * found twice. A node's position along the path is its tier; its position
   * across is its place within that tier.
   */
  function positioned(data: PodGraph): { nodes: unknown[]; links: unknown[] } {
    const ink = token('--on-surface-variant', '#9aa0a6')
    const text = token('--on-surface', '#e6e0e9')
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

    // Only the tiers that have something in them take space. A pod with no
    // ingress should not be drawn with an empty column above it — the gap
    // reads as a missing thing rather than as an absent one.
    const present = [...byTier.keys()].sort((a, b) => a - b)
    const place = new Map(present.map((tier, index) => [tier, index]))

    const nodes = data.nodes.map((node) => {
      const row = byTier.get(node.tier)!
      const index = row.indexOf(node)
      // Centred within the tier: a tier of one sits in the middle rather than
      // hard against the edge, which is what a bare index would give.
      const across = ((index + 1) / (row.length + 1)) * 100
      const along = (place.get(node.tier) ?? 0) * 26

      const colour = node.healthy ? (node.subject ? accent : good) : bad

      return {
        id: node.id,
        name: node.name,
        x: horizontal ? along : across,
        y: horizontal ? across : along,
        // THE ICON IS THE SYMBOL. Drawn from the same Lucide geometry the
        // navigator gives this kind, so the map and the tree cannot disagree
        // about what a Service looks like.
        symbol: iconURI(node.kind, colour),
        symbolSize: node.subject ? 30 : 22,
        label: {
          show: true,
          // Beside the node when the chain runs across, beneath it when it
          // runs down — in both cases along the axis with room to spare.
          position: horizontal ? 'right' : 'bottom',
          align: horizontal ? 'left' : 'center',
          color: text,
          fontSize: 11,
          lineHeight: 15,
          // KIND ABOVE NAME, and the kind in bold. A map of twenty boxes is
          // read by shape first: the kind says what a thing is, the name says
          // which one, and that is the order somebody needs them in.
          formatter: `{k|${node.apiKind || 'Container'}}\n{n|${node.name}}`,
          rich: {
            k: { color: text, fontWeight: 'bold', fontSize: 11, lineHeight: 15 },
            n: { color: ink, fontSize: 10, lineHeight: 14 },
          },
        },
        podsteer: node,
      }
    })

    const links = data.edges.map((edge) => ({
      source: edge.from,
      target: edge.to,
      label: {
        show: Boolean(edge.label),
        formatter: edge.label,
        fontSize: 9,
        color: ink,
        opacity: 0.7,
      },
      // STRAIGHT, not curved. A dependency map is read for its structure, and
      // a curve implies a route where there is only a relationship.
      lineStyle: { color: ink, opacity: 0.4, curveness: 0, width: 1.2 },
    }))

    return { nodes, links }
  }

  function option(data: PodGraph): unknown {
    const { nodes, links } = positioned(data)

    return {
      animation: false,
      // The labels ARE the information now, so nothing is left for a tooltip
      // to add — and a tooltip on a map somebody is panning is a box that
      // follows the cursor over the thing being looked at.
      tooltip: { show: false },
      series: [
        {
          type: 'graph',
          layout: 'none',
          roam: true,
          data: nodes,
          links,
          edgeSymbol: ['none', 'arrow'],
          edgeSymbolSize: 7,
          // Room for the labels, which extend well past the symbol.
          left: orientation === 'horizontal' ? '4%' : '8%',
          right: orientation === 'horizontal' ? '18%' : '8%',
          top: '8%',
          bottom: orientation === 'horizontal' ? '8%' : '14%',
          emphasis: { focus: 'adjacency', scale: false },
        },
      ],
    }
  }

  async function draw(): Promise<void> {
    if (!container || !graph) return

    if (!chart) {
      const { createChart } = await import('$lib/echarts')
      if (!container) return
      chart = createChart(container)

      // FOLLOWING IS THE POINT. A map that shows a failing ConfigMap and
      // makes somebody go and find it in the navigator has stopped halfway.
      chart.on('click', (params: unknown) => {
        const node = (params as { data?: { podsteer?: GraphNodeData } }).data?.podsteer
        // Containers are not objects and have no panel of their own; the pod
        // they belong to is already the subject of this map.
        if (!node?.apiKind || !onopen) return
        onopen(node.apiKind.toLowerCase() + 's', node.name, node.namespace || namespace)
      })
    }
    chart.setOption(option(graph), true)
  }

  // Redrawn when the data changes AND when the layout does — the orientation
  // is read inside `option`, so without naming it here a toggle would change
  // the preference and leave the picture as it was.
  $effect(() => {
    void graph
    void orientation
    void draw()
  })

  $effect(() => {
    if (!container) return
    const observer = new ResizeObserver(() => chart?.resize())
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
