<!--
  Every node's load, in the dimensions that decide whether more will fit.

  A cluster total hides the shape of the problem. "46% requested" across
  eighteen nodes is equally consistent with every node at 46% and with half of
  them at 90% while the rest idle — and only the second explains why a pod will
  not schedule on a cluster that looks half empty. That difference is the whole
  reason this chart exists, and it is visible in one glance here and in no
  number anywhere else on the page.

  Horizontal bars rather than a heatmap grid. A heatmap encodes magnitude as
  colour, which is the harder channel to read precisely and the one that fails
  for the colour-blind; bar length is exact, comparable at a glance, and leaves
  colour free to carry the thing it is actually good at — which dimension is
  which, and which node is in trouble.
-->
<script lang="ts">
  import type { NodeLoad } from '$lib/api/client'
  import { preferences } from '$stores/preferences.svelte'
  import type { Chart } from '$lib/echarts'

  interface Props {
    loads: NodeLoad[]
    /** Called when a node is clicked, to open it. */
    onselect?: (name: string) => void
  }

  let { loads, onselect }: Props = $props()

  let container = $state<HTMLDivElement | null>(null)
  let chart: Chart | null = null
  let failed = $state(false)

  /**
   * How many nodes are drawn before the reader asks for more.
   *
   * Five is a card; eighteen is a page of its own. The list is sorted busiest
   * first, so the five that matter are the five already shown and expanding is
   * a choice rather than a chore.
   */
  const COLLAPSED = 5

  let expanded = $state(false)

  const shown = $derived(expanded ? loads : loads.slice(0, COLLAPSED))
  const hidden = $derived(Math.max(0, loads.length - COLLAPSED))

  /**
   * Row height, plus the axis and legend.
   *
   * Sized from what is actually drawn rather than from the whole fleet, so
   * collapsing the list collapses the card with it.
   */
  const height = $derived(shown.length * 46 + 62)

  /**
   * Node names share a prefix, and it is never the interesting part.
   *
   * Every node here begins "euc3-de1-dev-", so truncating from the right —
   * which is what an axis does by default — produced eighteen labels reading
   * "euc3-de1-dev-eck-01-worke…", identical to each other and useless. The
   * shared opening is dropped instead, leaving the part that differs.
   */
  const prefix = $derived.by(() => {
    if (loads.length < 2) return ''

    const names = loads.map((load) => load.name)
    let length = 0
    while (length < names[0].length && names.every((name) => name[length] === names[0][length])) {
      length += 1
    }
    // Cut back to a separator so the remainder starts at a word rather than
    // mid-token: "ck-01-worker" helps nobody.
    const cut = names[0].slice(0, length)
    const boundary = Math.max(cut.lastIndexOf('-'), cut.lastIndexOf('.'))
    return boundary > 0 ? cut.slice(0, boundary + 1) : ''
  })

  const shortName = (name: string): string =>
    prefix && name.startsWith(prefix) ? name.slice(prefix.length) : name

  /** See TrendChart: the palette is read from CSS so the chart follows the theme. */
  function palette(): Record<string, string> {
    const styles = getComputedStyle(document.documentElement)
    const read = (name: string, fallback: string) =>
      styles.getPropertyValue(name).trim() || fallback
    return {
      primary: read('--primary', '#8ab4f8'),
      secondary: read('--secondary', '#ccc2dc'),
      tertiary: read('--tertiary', '#efb8c8'),
      success: read('--success', '#8bd5a0'),
      onSurface: read('--on-surface', '#e6e1e9'),
      onSurfaceVariant: read('--on-surface-variant', '#cac4d0'),
      outline: read('--outline-variant', '#49454f'),
      surface: read('--surface-container-high', '#2b2930'),
      warning: read('--warning', '#f5c267'),
      error: read('--error', '#f2b8b5'),
      font: styles.getPropertyValue('--font-sans').trim() || 'system-ui, sans-serif',
    }
  }

  function buildOption(): unknown {
    const colours = palette()

    // Reversed because ECharts draws a category axis bottom-up, and the
    // busiest node — the one this chart is FOR — belongs at the top.
    const rows = [...shown].reverse()

    /**
     * One dimension, in one colour.
     *
     * Fixed rather than painted per value. The previous version coloured each
     * bar by its own reading, which meant the legend swatch — drawn from the
     * series colour — disagreed with every bar it was supposed to identify:
     * the Memory key showed green above pink bars. Severity is carried by the
     * threshold lines instead, where it does not have to fight the legend.
     */
    const series = (name: string, pick: (load: NodeLoad) => number, colour: string): unknown => ({
      name,
      type: 'bar',
      barMaxWidth: 6,
      barGap: '20%',
      itemStyle: { color: colour, borderRadius: 3 },
      data: rows.map((load) => Math.max(0, Math.round(pick(load) * 10) / 10)),
    })

    // Disk is only a series when at least one kubelet answered. A row of
    // zeroes would read as "these disks are empty" rather than as "nobody
    // asked", which is the opposite of the truth.
    const anyDisk = rows.some((load) => load.diskPercent >= 0)

    const marker = (at: number, colour: string, label: string): unknown => ({
      xAxis: at,
      lineStyle: { color: colour, type: 'dashed', width: 1 },
      // At the top of the rule, not the bottom: the bottom is where the axis
      // ticks are, and an "80%" threshold label landed exactly on the "80%"
      // gridline label underneath it.
      label: {
        formatter: label,
        color: colour,
        fontSize: 11,
        fontFamily: colours.font,
        position: 'end',
      },
    })

    return {
      backgroundColor: 'transparent',
      animation: false,
      textStyle: { fontFamily: colours.font },
      grid: { left: 8, right: 24, top: 34, bottom: 4, containLabel: true },
      legend: {
        top: 0,
        right: 0,
        itemWidth: 10,
        itemHeight: 10,
        itemGap: 14,
        textStyle: { color: colours.onSurfaceVariant, fontSize: 12, fontFamily: colours.font },
      },
      tooltip: {
        trigger: 'axis',
        axisPointer: { type: 'shadow' },
        backgroundColor: colours.surface,
        borderColor: colours.outline,
        textStyle: { color: colours.onSurface, fontSize: 12, fontFamily: colours.font },
        valueFormatter: (value: number) => `${value}%`,
      },
      xAxis: {
        type: 'value',
        max: 100,
        axisLabel: {
          color: colours.onSurfaceVariant,
          fontSize: 12,
          fontFamily: colours.font,
          formatter: '{value}%',
        },
        splitLine: { lineStyle: { color: colours.outline, opacity: 0.3 } },
      },
      yAxis: {
        type: 'category',
        data: rows.map((load) => shortName(load.name)),
        axisLabel: {
          color: colours.onSurface,
          fontSize: 12,
          fontFamily: colours.font,
          width: 190,
          overflow: 'truncate',
        },
        axisLine: { lineStyle: { color: colours.outline } },
        axisTick: { show: false },
      },
      series: [
        {
          ...(series('CPU requested', (load) => load.cpuPercent, colours.primary) as object),
          // The thresholds ride on the first series so they are drawn once
          // rather than four times on top of each other.
          markLine: {
            silent: true,
            symbol: 'none',
            data: [marker(80, colours.warning, '80%'), marker(90, colours.error, '90%')],
          },
        },
        series('Memory requested', (load) => load.memoryPercent, colours.tertiary),
        series('Pod slots', (load) => load.podPercent, colours.secondary),
        ...(anyDisk
          ? [series('Disk used', (load) => Math.max(0, load.diskPercent), colours.success)]
          : []),
      ],
    }
  }

  // Created once the module resolves, then kept in step with the data and the
  // theme. See TrendChart: disposal is not optional, because ECharts registers
  // window listeners and a canvas that would outlive this component.
  $effect(() => {
    const element = container
    if (!element) return

    let disposed = false
    let observer: ResizeObserver | undefined

    void import('$lib/echarts')
      .then((echarts) => {
        if (disposed) return

        chart = echarts.createChart(element)
        chart.setOption(buildOption(), true)
        observer = new ResizeObserver(() => chart?.resize())
        observer.observe(element)
      })
      .catch(() => {
        failed = true
      })

    return () => {
      disposed = true
      observer?.disconnect()
      chart?.dispose()
      chart = null
    }
  })

  // Redraw on data, on expansion and on theme. resolvedTheme rather than the
  // preference, so it repaints when the OS flips at sunset without anything
  // being chosen.
  $effect(() => {
    void shown
    void preferences.resolvedTheme
    chart?.setOption(buildOption(), true)
    // The container's height changes when the list expands, and a canvas
    // sized at build time keeps the height it was built at.
    chart?.resize()
  })

  /**
   * Clicking a bar opens its node.
   *
   * Bound imperatively rather than through the chart's own event API, which
   * the minimal Chart surface in $lib/echarts deliberately does not expose:
   * the row under the pointer is enough to identify the node, and keeping the
   * shared type small is worth more than a typed handler here.
   */
  function pickNode(event: MouseEvent): void {
    if (!onselect || !container) return

    const bounds = container.getBoundingClientRect()
    const rows = [...shown].reverse()
    // The plot area starts below the legend; rows divide what is left evenly.
    const top = bounds.top + 34
    const usable = bounds.height - 38
    if (usable <= 0 || rows.length === 0) return

    const index = Math.floor(((event.clientY - top) / usable) * rows.length)
    if (index < 0 || index >= rows.length) return
    onselect(rows[index].name)
  }
</script>

{#if failed}
  <p
    class="flex items-center justify-center text-body-small text-on-surface-variant/60"
    style="height: {height}px"
  >
    The chart could not be loaded.
  </p>
{:else}
  <!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions -->
  <div
    bind:this={container}
    onclick={pickNode}
    style="height: {height}px"
    class="w-full {onselect ? 'cursor-pointer' : ''}"
  ></div>

  <!-- Under the chart rather than beside the heading: it changes what is
       below it, and a control that reaches across a card to do that is one
       people press by accident. -->
  {#if hidden > 0}
    <button
      type="button"
      onclick={() => (expanded = !expanded)}
      aria-expanded={expanded}
      class="state-layer mt-1 self-start rounded-xs px-1.5 py-1 text-label-medium text-primary
             transition-colors duration-100 hover:bg-primary/10"
    >
      {expanded ? `Show the busiest ${COLLAPSED}` : `Show all ${loads.length} nodes`}
    </button>
  {/if}
{/if}
