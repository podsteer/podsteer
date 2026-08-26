<!--
  A cluster trend over time.

  ECharts is loaded with a dynamic import and a modular registration: the
  barrel import pulls in every chart type ECharts has, roughly doubling what
  the browser must parse for the three we draw. Deferring it means the
  dashboard's first paint pays nothing for a chart the operator may not scroll
  to — which matters here, since the whole premise of PodSteer is starting fast.

  Two honesty rules the series obey:

    • Requests and capacity are always drawn; they are declarations, and are
      known whether or not metrics-server exists.
    • Usage is drawn only where it was measured. An unmeasured moment becomes a
      gap in the line, never a zero — a flat line at the bottom of the chart
      reads as an idle cluster, which is the opposite of "we could not see".
-->
<script lang="ts">
  import type { Sample } from '$lib/api/client'
  import type { Chart } from '$lib/echarts'
  import { preferences } from '$stores/preferences.svelte'

  interface Props {
    samples: Sample[]
    /** Which quantity to plot. */
    metric: 'cpu' | 'memory' | 'pods'
    /** Chart height in pixels. */
    height?: number
  }

  let { samples, metric, height = 200 }: Props = $props()

  let container: HTMLDivElement | undefined = $state()
  /** The ECharts instance, once the module has loaded. */
  let chart: Chart | null = null
  let failed = $state(false)

  /** Formats a CPU millicore value for an axis or tooltip. */
  function formatCPU(milli: number): string {
    return milli >= 1000 ? `${(milli / 1000).toFixed(1)}` : `${Math.round(milli)}m`
  }

  /** Formats bytes in the binary units Kubernetes itself uses. */
  function formatBytes(bytes: number): string {
    if (bytes <= 0) return '0'
    const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB']
    let value = bytes
    let unit = 0
    while (value >= 1024 && unit < units.length - 1) {
      value /= 1024
      unit++
    }
    return `${value.toFixed(1)}${units[unit]}`
  }

  const METRICS = {
    cpu: {
      label: 'CPU',
      format: formatCPU,
      usage: (sample: Sample) => sample.cpuUsage,
      requests: (sample: Sample) => sample.cpuRequests,
      capacity: (sample: Sample) => sample.cpuAllocatable,
    },
    memory: {
      label: 'Memory',
      format: formatBytes,
      usage: (sample: Sample) => sample.memoryUsage,
      requests: (sample: Sample) => sample.memoryRequests,
      capacity: (sample: Sample) => sample.memoryAllocatable,
    },
    pods: {
      label: 'Pods',
      format: (value: number) => String(Math.round(value)),
      usage: (sample: Sample) => sample.podsScheduled,
      requests: (sample: Sample) => sample.podsNotReady,
      capacity: () => 0,
    },
  } as const

  /**
   * Resolves the theme's own colours from CSS custom properties rather than
   * hard-coding them, so the chart follows the light/dark toggle instead of
   * being a rectangle of the wrong palette.
   */
  /**
   * The last palette read, keyed by the theme it was read under.
   *
   * getComputedStyle forces the browser to flush pending style work, and this
   * was called on every redraw — which is every ten-second refresh, per chart.
   * The answer can only change when the resolved theme does, so it is read
   * once per theme instead.
   */
  let paletteCache: { theme: string; colours: Record<string, string> } | null = null

  function palette(): Record<string, string> {
    const theme = preferences.resolvedTheme
    if (paletteCache?.theme === theme) return paletteCache.colours

    const styles = getComputedStyle(document.documentElement)
    const read = (name: string, fallback: string) =>
      styles.getPropertyValue(name).trim() || fallback
    const colours = {
      primary: read('--primary', '#b69df8'),
      onSurface: read('--on-surface', '#e6e1e9'),
      onSurfaceVariant: read('--on-surface-variant', '#cac4d0'),
      outline: read('--outline-variant', '#49454f'),
      surface: read('--surface-container-low', '#1d1b20'),
      warning: read('--warning', '#f5c267'),
    }
    paletteCache = { theme, colours }
    return colours
  }

  function buildOption(): unknown {
    const colours = palette()
    const config = METRICS[metric]
    const isPods = metric === 'pods'

    // The gap rule: `null` breaks an ECharts line, which is exactly what an
    // unmeasured moment should look like.
    const usage = samples.map((sample) =>
      isPods || sample.measured ? config.usage(sample) : null,
    )
    const requests = samples.map((sample) => config.requests(sample))
    const capacity = samples.map((sample) => config.capacity(sample))

    // Points are [timestamp, value] pairs so the time axis places them by
    // when they happened rather than by their index — which matters the
    // moment a sample is missed and the spacing stops being uniform.
    const pair = (values: (number | null)[]) =>
      values.map((value, index) => [samples[index]?.at ?? 0, value])

    const series: unknown[] = [
      {
        name: isPods ? 'Scheduled' : 'Used',
        type: 'line',
        smooth: 0.2,
        showSymbol: false,
        connectNulls: false,
        lineStyle: { width: 2, color: colours.primary },
        areaStyle: { color: colours.primary, opacity: 0.16 },
        data: pair(usage),
      },
      {
        name: isPods ? 'Not ready' : 'Requested',
        type: 'line',
        smooth: 0.2,
        showSymbol: false,
        lineStyle: {
          width: 1.5,
          type: 'dashed',
          color: isPods ? colours.warning : colours.onSurfaceVariant,
        },
        data: pair(requests),
      },
    ]

    if (!isPods) {
      series.push({
        name: 'Allocatable',
        type: 'line',
        smooth: false,
        showSymbol: false,
        lineStyle: { width: 1, type: 'dotted', color: colours.outline },
        data: pair(capacity),
      })
    }

    return {
      animation: false,
      grid: { left: 52, right: 12, top: 28, bottom: 24 },
      tooltip: {
        trigger: 'axis',
        backgroundColor: colours.surface,
        borderColor: colours.outline,
        textStyle: { color: colours.onSurface, fontSize: 12 },
        valueFormatter: (value: number | null) =>
          value === null || value === undefined ? 'not measured' : config.format(value),
      },
      legend: {
        top: 0,
        right: 0,
        icon: 'roundRect',
        itemWidth: 10,
        itemHeight: 3,
        textStyle: { color: colours.onSurfaceVariant, fontSize: 11 },
      },
      xAxis: {
        type: 'time',
        axisLine: { lineStyle: { color: colours.outline } },
        axisLabel: { color: colours.onSurfaceVariant, fontSize: 11, hideOverlap: true },
        splitLine: { show: false },
      },
      yAxis: {
        type: 'value',
        min: 0,
        axisLabel: {
          color: colours.onSurfaceVariant,
          fontSize: 11,
          formatter: (value: number) => config.format(value),
        },
        splitLine: { lineStyle: { color: colours.outline, opacity: 0.35 } },
      },
      series,
    }
  }

  // Create the chart once the module resolves, then keep it in step with the
  // data and the theme. Disposal is not optional: ECharts registers window
  // listeners and a canvas that would outlive the component otherwise.
  $effect(() => {
    const element = container
    if (!element) return

    let disposed = false
    let observer: ResizeObserver | undefined

    // The wrapper module registers only the pieces this chart uses; see
    // $lib/echarts for why that indirection is load-bearing rather than
    // stylistic.
    void import('$lib/echarts')
      .then((echarts) => {
        if (disposed) return

        chart = echarts.createChart(element)
        chart.setOption(buildOption(), true)

        observer = new ResizeObserver(() => chart?.resize())
        observer.observe(element)
      })
      .catch(() => {
        // A chart that will not load must not take the dashboard with it.
        failed = true
      })

    return () => {
      disposed = true
      observer?.disconnect()
      chart?.dispose()
      chart = null
    }
  })

  /** What the last draw was configured for, to decide merge vs rebuild. */
  let lastTheme = ''
  let lastMetric = ''

  // Redraw when the data or the theme changes. Reading both here is what
  // registers the dependency; `preferences.resolvedTheme` is not otherwise
  // used — but it is the resolved scheme rather than the choice, so the chart
  // repaints when the OS flips at sunset and the preference has not changed.
  $effect(() => {
    void samples
    const theme = preferences.resolvedTheme
    void metric

    // notMerge only when the shape of the chart can actually have changed.
    //
    // A theme flip or a different metric rewrites axes, colours and formatters
    // and wants a clean rebuild. A new sample arriving does not: it is the
    // same chart with more points on it, and merging lets ECharts update the
    // series in place rather than tearing down and recreating every component
    // ten seconds after it did so last.
    const rebuild = theme !== lastTheme || metric !== lastMetric
    lastTheme = theme
    lastMetric = metric

    chart?.setOption(buildOption(), rebuild)
  })
</script>

{#if failed}
  <p class="flex items-center justify-center text-body-small text-on-surface-variant/60" style="height: {height}px">
    The chart could not be loaded.
  </p>
{:else}
  <div bind:this={container} style="height: {height}px" class="w-full"></div>
{/if}
