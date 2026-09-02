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

  The series comes from `usageHistory`, which keeps whatever the list polls
  already fetched — so a chart opens with a shape rather than a blank frame.
  It is bounded by the configured window and by what was actually sampled: a
  node's series is continuous because the assessment runs on every poll, while
  a pod's pauses whenever the pod list is not the list being polled. Those
  pauses are DRAWN as breaks rather than joined; see `series` below.
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

  /**
   * The references that were asked for and do not exist.
   *
   * Said out loud rather than left as a chart with fewer lines on it. An
   * absent line is ambiguous — undeclared, or declared so far above the usage
   * that it is off the top? — and that ambiguity is exactly what hid a bug
   * for the whole life of these charts: the lines were being built and
   * dropped by ECharts, and a chart with no lines looked like a pod with no
   * limits. Naming the missing ones makes the two states different on screen.
   */
  const undeclared = $derived(markers.filter((marker) => marker.value <= 0))

  let container = $state<HTMLDivElement | null>(null)
  let chart: Chart | null = null
  let failed = $state(false)

  const values = $derived(
    samples.map((sample) => (metric === 'cpu' ? sample.cpuCores : sample.memoryBytes)),
  )

  /**
   * The series as [timestamp, value] pairs, broken wherever time was lost.
   *
   * TWO THINGS HERE, AND BOTH ARE ABOUT NOT LYING WITH THE X AXIS.
   *
   * The pairs exist because the axis used to be a `category`: one slot per
   * sample, evenly spaced, whatever the clock said. A minute with no samples
   * in it was drawn as a single step identical to a two-second one, so the
   * chart compressed its own gaps out of existence and the line looked
   * continuous when it was not.
   *
   * The nulls exist because a real gap must LOOK like one. Samples stop
   * whenever the object is not in the list being polled — walk from the pod
   * list to the node list and every pod's series pauses — and joining the two
   * ends draws a straight line through a minute nobody measured, which is
   * indistinguishable from a minute of steady usage.
   *
   * ECharts breaks a line on null by default (`connectNulls` is false), so
   * the inserted point is the whole mechanism.
   */
  const series = $derived.by(() => {
    const points: [number, number | null][] = []

    for (const [index, sample] of samples.entries()) {
      const value = metric === 'cpu' ? sample.cpuCores : sample.memoryBytes
      const previous = samples[index - 1]

      // Three times the typical spacing, rather than a fixed number of
      // seconds: the poll interval is configurable, so a threshold in seconds
      // would either break every line on a slow refresh or never break one on
      // a fast refresh.
      if (previous && sample.at - previous.at > gapThreshold * 3) {
        points.push([previous.at + 1, null])
      }
      points.push([sample.at, value])
    }

    return points
  })

  /**
   * The typical spacing between samples, in milliseconds.
   *
   * The MEDIAN rather than the mean, because the mean is dragged upward by
   * exactly the gaps this is used to detect — one long pause in a short
   * series would raise the threshold above itself and the gap would go
   * undrawn.
   */
  const gapThreshold = $derived.by(() => {
    if (samples.length < 3) return Number.POSITIVE_INFINITY

    const deltas = samples
      .slice(1)
      .map((sample, index) => sample.at - samples[index].at)
      .sort((a, b) => a - b)

    return deltas[Math.floor(deltas.length / 2)]
  })

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

    // The figure is in the label, not only on the axis. A dashed line marked
    // "Limit" still leaves the reading to be taken off the y axis by eye,
    // which is the work the line was drawn to save.
    const marks = lines.map((line) => ({
      yAxis: line.value,
      lineStyle: { color: line.tone === 'critical' ? critical : warn, type: 'dashed' },
      label: {
        formatter: `${line.label} ${format(line.value)}`,
        color: ink,
        position: 'insideEndTop',
      },
    }))

    return {
      animation: false,
      // Tight, because this sits inside a drawer section rather than on a
      // dashboard. The axis labels are the only chrome that earns its space.
      grid: { top: 8, right: 8, bottom: 20, left: 52 },
      xAxis: {
        // Time, not category. A category axis spaces samples evenly whatever
        // the clock said, which drew a pause in sampling as though no time
        // had passed in it.
        type: 'time',
        axisLabel: {
          color: ink,
          fontSize: 10,
          hideOverlap: true,
          formatter: (value: number) =>
            new Date(value).toLocaleTimeString(undefined, {
              hour: '2-digit',
              minute: '2-digit',
            }),
        },
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
        formatter: (params: { value: [number, number | null] }[]) => {
          const point = params[0]?.value
          if (!point || point[1] === null) return ''
          return `${new Date(point[0]).toLocaleTimeString()}<br/>${format(point[1])}`
        },
      },
      series: [
        {
          type: 'line',
          smooth: false,
          showSymbol: false,
          data: series,
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

  <!--
    What the chart is NOT measured against. A container with no limit is a
    deliberate and common shape rather than an oversight, so this is stated
    plainly rather than as a warning — but it has to be stated, because the
    alternative is a chart that looks the same as one whose reference lines
    failed to draw.
  -->
  {#if undeclared.length > 0}
    <p class="mt-1 text-body-small text-on-surface-variant/60">
      {undeclared.map((marker) => `No ${marker.label.toLowerCase()} declared`).join(' · ')}.
    </p>
  {/if}
{/if}
