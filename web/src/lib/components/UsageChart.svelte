<!--
  The open pod's usage, against the two lines that decide what happens to it.

  The point of the chart, and the reason it is not a sparkline: usage on its
  own is a number without a verdict. Drawn against the REQUEST and the LIMIT
  it becomes a reading — below the request is headroom nobody is using, above
  it is borrowed capacity nothing guarantees, and near the limit is where the
  kernel starts throttling or killing. Lens draws the same three series; what
  it does not do is say which band the pod is currently in.

  ECharts rather than the inline SVG this replaces, because the reference
  lines are the whole point and a sparkline cannot carry them: no axis, no
  tooltip, and no way to tell 60% of a request from 60% of a limit. The
  library is already a dependency and already loaded lazily.

  The series is only as long as the drawer has been open — nothing retains
  per-pod history, and pretending otherwise is what the empty state avoids.
-->
<script lang="ts">
  import type { Chart } from '$lib/echarts'
  import type { UsageSample } from '$stores/session.svelte'
  import { preferences } from '$stores/preferences.svelte'

  interface Props {
    samples: UsageSample[]
    /** Which resource this chart is for. */
    metric: 'cpu' | 'memory'
    /**
     * The lines the usage is judged against, in the SAME UNIT as the samples
     * — cores for CPU, bytes for memory.
     *
     * A list rather than a request and a limit, because what a reading is
     * measured against depends on what is being measured: a pod has a request
     * it should be near and a limit it must not reach, while a node has one
     * ceiling and no notion of either. Encoding the pod's two into the
     * component would have meant a node passing its allocatable as a "limit",
     * which is not what allocatable is.
     *
     * A value of zero is dropped rather than drawn: a line at zero for an
     * undeclared limit reads as "the limit is nothing", the opposite of what
     * an absent one means.
     */
    markers?: { value: number; label: string; tone: 'warn' | 'critical' }[]
    /** Formats a value for the axis and the tooltip. */
    format: (value: number) => string
  }

  let { samples, metric, markers = [], format }: Props = $props()

  const lines = $derived(markers.filter((marker) => marker.value > 0))

  let container = $state<HTMLDivElement | null>(null)
  let chart: Chart | null = null
  let failed = $state(false)

  const values = $derived(
    samples.map((sample) => (metric === 'cpu' ? sample.cpuCores : sample.memoryBytes)),
  )

  /**
   * Where the top of the chart sits.
   *
   * The limit when there is one and usage is under it, so the bar the pod is
   * measured against is always visible — a chart scaled to usage alone hides
   * the very line that matters. Usage wins when it exceeds the limit, because
   * an overshoot must never be drawn off the top of the frame.
   */
  const ceiling = $derived.by(() => {
    const peak = Math.max(...values, ...lines.map((line) => line.value), 0)
    return peak > 0 ? peak * 1.1 : 1
  })

  function buildOption(): unknown {
    const theme = getComputedStyle(document.documentElement)
    const ink = theme.getPropertyValue('--on-surface-variant').trim() || '#9aa0a6'
    const line = theme.getPropertyValue('--primary').trim() || '#7aa2f7'
    const warn = theme.getPropertyValue('--gauge-warn').trim() || '#e0a458'
    const critical = theme.getPropertyValue('--gauge-critical').trim() || '#e06c75'

    const marks = lines.map((line) => ({
      yAxis: line.value,
      lineStyle: { color: line.tone === 'critical' ? critical : warn, type: 'dashed' },
      label: { formatter: line.label, color: ink, position: 'insideEndTop' },
    }))

    return {
      animation: false,
      // Tight, because this sits inside a drawer section rather than on a
      // dashboard. The axis labels are the only chrome that earns its space.
      grid: { top: 8, right: 8, bottom: 20, left: 52 },
      xAxis: {
        type: 'category',
        data: samples.map((sample) => new Date(sample.at).toLocaleTimeString()),
        axisLabel: { color: ink, fontSize: 10, showMaxLabel: true },
        axisLine: { lineStyle: { color: ink, opacity: 0.2 } },
        splitLine: { show: false },
      },
      yAxis: {
        type: 'value',
        min: 0,
        max: ceiling,
        axisLabel: { color: ink, fontSize: 10, formatter: (value: number) => format(value) },
        splitLine: { lineStyle: { color: ink, opacity: 0.12 } },
      },
      tooltip: {
        trigger: 'axis',
        formatter: (params: { value: number; name: string }[]) =>
          `${params[0]?.name}<br/>${format(params[0]?.value ?? 0)}`,
      },
      series: [
        {
          type: 'line',
          smooth: false,
          showSymbol: false,
          data: values,
          lineStyle: { color: line, width: 1.6 },
          areaStyle: { color: line, opacity: 0.12 },
          markLine: marks.length
            ? { silent: true, symbol: 'none', data: marks, label: { fontSize: 10 } }
            : undefined,
        },
      ],
    }
  }

  // Created once and kept, with the option merged on change. Rebuilding the
  // chart per update would discard the canvas and flicker on every refresh.
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
        // A chart that will not load must not take the pane with it.
        failed = true
      })

    return () => {
      disposed = true
      observer?.disconnect()
      chart?.dispose()
      chart = null
    }
  })

  // Redrawn when the samples change, or when the theme does — the colours are
  // read from CSS custom properties, so a theme switch needs a fresh option
  // rather than a resize.
  $effect(() => {
    void values
    void lines
    void preferences.themePreference
    chart?.setOption(buildOption(), true)
  })
</script>

{#if failed}
  <p class="text-body-small text-on-surface-variant/60">The chart could not be loaded.</p>
{:else}
  <!--
    DRAWN IMMEDIATELY, EMPTY. This used to render a line of text until two
    samples had arrived, so the section appeared to be missing for the first
    refresh or two and then a chart materialised in its place — which reads as
    the pane still loading rather than as a series that has not started.
    Everything jumped when it swapped.

    An empty frame is also not empty: the axis and the request and limit lines
    are known before any measurement, so what is on screen from the first
    moment is the shape the usage will be judged against. The caption says why
    there is no line yet.
  -->
  <div class="relative">
    <div bind:this={container} class="h-32 w-full"></div>
    {#if samples.length < 2}
      <p
        class="pointer-events-none absolute inset-0 flex items-center justify-center
               text-body-small text-on-surface-variant/60"
      >
        Watching — the line appears after a few refreshes.
      </p>
    {/if}
  </div>
{/if}
