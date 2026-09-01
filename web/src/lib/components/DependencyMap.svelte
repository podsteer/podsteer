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

  interface Props {
    clusterId: string
    namespace: string
    podName: string
    /** Follows a node into its own panel. */
    onopen?: (kindName: string, name: string, namespace: string) => void
  }

  let { clusterId, namespace, podName, onopen }: Props = $props()

  let container = $state<HTMLDivElement | null>(null)
  let chart: Chart | null = null
  let graph = $state.raw<PodGraph | null>(null)
  let failure = $state('')
  let loading = $state(false)
  let loadedFor = $state('')
  let stopWatchingTheme: (() => void) | null = null

  /** What each tier is called, top to bottom, for the axis labels. */
  const TIERS = ['Ingress', 'Service', 'Controller', 'Pod', 'Containers', 'Attached']

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
   * found twice. Here a node's row is its tier and its column is its position
   * within it.
   */
  function positioned(data: PodGraph): { nodes: unknown[]; links: unknown[] } {
    const ink = token('--on-surface-variant', '#9aa0a6')
    const good = token('--gauge-normal', '#4f86e8')
    const bad = token('--gauge-critical', '#d8453c')
    const accent = token('--primary', '#8ab4f8')

    const byTier = new Map<number, typeof data.nodes>()
    for (const node of data.nodes) {
      const row = byTier.get(node.tier)
      if (row) row.push(node)
      else byTier.set(node.tier, [node])
    }

    const nodes = data.nodes.map((node) => {
      const row = byTier.get(node.tier)!
      const index = row.indexOf(node)
      // Spread across the width, centred: a tier of one sits in the middle
      // rather than hard left, which is what an index-based x would give.
      const x = ((index + 1) / (row.length + 1)) * 100

      return {
        id: node.id,
        name: node.name,
        x,
        y: node.tier * 20,
        // The subject is drawn larger. On a map of twenty boxes the object
        // somebody opened has to be findable without reading them all.
        symbolSize: node.subject ? 46 : 32,
        itemStyle: {
          color: node.healthy ? (node.subject ? accent : good) : bad,
          borderColor: node.subject ? accent : 'transparent',
          borderWidth: node.subject ? 3 : 0,
        },
        label: {
          show: true,
          position: 'right',
          color: ink,
          fontSize: 11,
          formatter: node.detail ? `${node.name}\n{d|${node.detail}}` : node.name,
          rich: { d: { color: ink, opacity: 0.6, fontSize: 10, lineHeight: 14 } },
        },
        // Carried through so a click can follow the node.
        podsteer: node,
      }
    })

    const links = data.edges.map((edge) => ({
      source: edge.from,
      target: edge.to,
      label: { show: Boolean(edge.label), formatter: edge.label, fontSize: 9, color: ink, opacity: 0.7 },
      lineStyle: { color: ink, opacity: 0.35, curveness: 0.08 },
    }))

    return { nodes, links }
  }

  function option(data: PodGraph): unknown {
    const { nodes, links } = positioned(data)

    return {
      animation: false,
      tooltip: {
        formatter: (params: { data?: { podsteer?: { apiKind: string; name: string; namespace: string; detail: string } } }) => {
          const node = params.data?.podsteer
          if (!node) return ''
          const kind = node.apiKind || 'Container'
          return `${kind} · ${node.name}${node.detail ? `<br/>${node.detail}` : ''}`
        },
      },
      series: [
        {
          type: 'graph',
          layout: 'none',
          coordinateSystem: undefined,
          roam: true,
          data: nodes,
          links,
          edgeSymbol: ['none', 'arrow'],
          edgeSymbolSize: 7,
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
    }
    chart.setOption(option(graph), true)
  }

  $effect(() => {
    void graph
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
  {#if loading && !graph}
    <p class="p-4 text-body-medium text-on-surface-variant/70">Reading the dependency chain…</p>
  {:else if failure}
    <p class="p-4 text-body-medium text-error">{failure}</p>
  {:else}
    <!--
      The tier names, as a fixed column beside the chart. Drawn in the markup
      rather than as chart axes: a graph series has no axes, and a reader
      needs to know that the top row is what is outside the cluster.
    -->
    <div class="flex min-h-0 flex-1">
      <div
        class="flex w-24 shrink-0 flex-col justify-around border-r border-outline-variant/40
               py-6 pl-3 text-label-small text-on-surface-variant/60"
        aria-hidden="true"
      >
        {#each TIERS as tier (tier)}
          <span>{tier}</span>
        {/each}
      </div>
      <div bind:this={container} class="min-h-0 min-w-0 flex-1"></div>
    </div>

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
