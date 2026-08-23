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
   * Room per node, plus the axis and legend.
   *
   * Sized from the data rather than fixed: a three-node cluster in a 600px box
   * is three stripes floating in space, and a fifty-node cluster in one is
   * unreadable. Capped so a very large cluster scrolls its card instead of
   * pushing everything below it off the page.
   */
  const height = $derived(Math.min(680, Math.max(160, loads.length * 34 + 64)))

  /** See TrendChart: the palette is read from CSS so the chart follows the theme. */
  function palette(): Record<string, string> {
    const styles = getComputedStyle(document.documentElement)
    const read = (name: string, fallback: string) =>
      styles.getPropertyValue(name).trim() || fallback
    return {
      primary: read('--primary', '#b69df8'),
      secondary: read('--secondary', '#cbc2db'),
      tertiary: read('--tertiary', '#efb8c8'),
      onSurface: read('--on-surface', '#e6e1e9'),
      onSurfaceVariant: read('--on-surface-variant', '#cac4d0'),
      outline: read('--outline-variant', '#49454f'),
      surface: read('--surface-container-low', '#1d1b20'),
      warning: read('--warning', '#f5c267'),
      error: read('--error', '#f2b8b5'),
    }
  }

  function buildOption(): unknown {
    const colours = palette()

    // Reversed because ECharts draws a category axis bottom-up, and the
    // busiest node — the one this chart is FOR — belongs at the top.
    const rows = [...loads].reverse()
    const names = rows.map((load) => load.name)

    /**
     * One dimension's bars, coloured per node rather than per series.
     *
     * A node past 90% of anything is about to refuse work, so it is coloured
     * as the exception it is instead of being left for the reader to measure
     * against the axis.
     */
    const series = (
      name: string,
      pick: (load: NodeLoad) => number,
      base: string,
    ): unknown => ({
      name,
      type: 'bar',
      barMaxWidth: 7,
      itemStyle: {
        borderRadius: 2,
        color: (params: { dataIndex: number }) => {
          const value = pick(rows[params.dataIndex])
          if (value >= 90) return colours.error
          if (value >= 75) return colours.warning
          return base
        },
      },
      data: rows.map((load) => Math.max(0, Math.round(pick(load) * 10) / 10)),
    })

    // Disk is only a series when at least one kubelet answered. A row of
    // zeroes would read as "these disks are empty" rather than as "nobody
    // asked", which is the opposite of the truth.
    const anyDisk = rows.some((load) => load.diskPercent >= 0)

    return {
      backgroundColor: 'transparent',
      animation: false,
      grid: { left: 8, right: 40, top: 30, bottom: 8, containLabel: true },
      legend: {
        top: 0,
        right: 0,
        itemWidth: 10,
        itemHeight: 10,
        textStyle: { color: colours.onSurfaceVariant, fontSize: 11 },
      },
      tooltip: {
        trigger: 'axis',
        axisPointer: { type: 'shadow' },
        backgroundColor: colours.surface,
        borderColor: colours.outline,
        textStyle: { color: colours.onSurface, fontSize: 12 },
        valueFormatter: (value: number) => `${value}%`,
      },
      xAxis: {
        type: 'value',
        max: 100,
        axisLabel: { color: colours.onSurfaceVariant, fontSize: 11, formatter: '{value}%' },
        splitLine: { lineStyle: { color: colours.outline, opacity: 0.35 } },
      },
      yAxis: {
        type: 'category',
        data: names,
        axisLabel: {
          color: colours.onSurfaceVariant,
          fontSize: 11,
          width: 150,
          overflow: 'truncate',
        },
        axisLine: { lineStyle: { color: colours.outline } },
        axisTick: { show: false },
      },
      series: [
        series('CPU requested', (load) => load.cpuPercent, colours.primary),
        series('Memory requested', (load) => load.memoryPercent, colours.secondary),
        series('Pod slots', (load) => load.podPercent, colours.tertiary),
        ...(anyDisk
          ? [series('Disk used', (load) => Math.max(0, load.diskPercent), colours.onSurfaceVariant)]
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

  // Redraw on data and on theme. resolvedTheme rather than the preference, so
  // it repaints when the OS flips at sunset without anything being chosen.
  $effect(() => {
    void loads
    void preferences.resolvedTheme
    chart?.setOption(buildOption(), true)
  })

  /**
   * Clicking a bar opens its node.
   *
   * Bound imperatively rather than through the chart's own event API, which
   * the minimal Chart surface in $lib/echarts deliberately does not expose:
   * the y-axis label under the pointer is enough to identify the row, and
   * keeping the shared type small is worth more than a typed handler here.
   */
  function pickNode(event: MouseEvent): void {
    if (!onselect || !container) return

    const bounds = container.getBoundingClientRect()
    const rows = [...loads].reverse()
    // The plot area starts below the legend; rows divide what is left evenly.
    const top = bounds.top + 30
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
{/if}
