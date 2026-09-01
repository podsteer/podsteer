/**
 * The only ECharts PodSteer ships.
 *
 * This module exists so the imports below can be *static named* imports.
 * Registering the same pieces inline with `await import('echarts/charts')` and
 * then reaching into the namespace defeats tree-shaking entirely — the bundler
 * cannot prove which members are used, so it keeps every chart type ECharts
 * has. Measured on this project, that was the difference between a 262 kB
 * chunk and a 60 kB one, for a page that draws a single line chart.
 *
 * The module is still loaded lazily: the component `await import()`s *this*
 * file, so nothing here reaches the browser until a chart is first drawn.
 * Static imports within a lazily-imported module tree-shake normally.
 */

import { init, use } from 'echarts/core'
import { GraphChart, LineChart } from 'echarts/charts'
import { GridComponent, LegendComponent, TooltipComponent } from 'echarts/components'
import { CanvasRenderer } from 'echarts/renderers'

use([LineChart, GraphChart, GridComponent, TooltipComponent, LegendComponent, CanvasRenderer])

/**
 * The minimal surface the charts use.
 *
 * Typed structurally rather than with ECharts' own types: the option object is
 * built as a plain literal, and importing EChartsOption would pull the full
 * type surface into every consumer for no benefit.
 */
export interface Chart {
  setOption(option: unknown, notMerge?: boolean): void
  resize(): void
  dispose(): void
}

/** Creates a chart bound to an element, with only the registered pieces. */
export function createChart(element: HTMLElement): Chart {
  return init(element, undefined, { renderer: 'canvas' }) as unknown as Chart
}
