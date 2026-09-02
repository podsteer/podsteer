/**
 * The only ECharts PodSteer ships.
 *
 * This module exists so the imports below can be *static named* imports.
 * Registering the same pieces inline with `await import('echarts/charts')` and
 * then reaching into the namespace defeats tree-shaking entirely: the bundler
 * cannot prove which members are used, so it keeps every chart type ECharts
 * has, including the ones nothing here draws.
 *
 * The module is still loaded lazily — the component `await import()`s *this*
 * file, so nothing here is parsed until a chart is first drawn, and static
 * imports within a lazily-imported module tree-shake normally.
 *
 * ADDING A CHART TYPE IS A ONE-LINE CHANGE HERE and needs no arithmetic: keep
 * the list to what is actually drawn because an unregistered chart fails at
 * runtime and an unused one is dead code, not because of what it weighs.
 *
 * THE TRAP, AND IT HAS ALREADY COST SOMETHING. An unregistered CHART fails
 * loudly; an unregistered COMPONENT does not fail at all. The option is
 * accepted, the chart draws, and the part of it that needed the component is
 * silently absent. MarkLineComponent was missing here for the whole life of
 * the usage charts, so every one of them drew usage against no reference line
 * — the request and the limit were passed in, built into the option, and
 * dropped on the floor. Nothing logged, nothing threw, and the charts looked
 * finished.
 *
 * So: anything an option references — markLine, markPoint, markArea, dataZoom,
 * a visualMap, a title — needs its component listed below, and the way to
 * verify one is to look at the chart, not at the console.
 */

import { init, use } from 'echarts/core'
import { LineChart } from 'echarts/charts'
import {
  GridComponent,
  LegendComponent,
  MarkLineComponent,
  TooltipComponent,
} from 'echarts/components'
import { CanvasRenderer } from 'echarts/renderers'

use([
  LineChart,
  GridComponent,
  TooltipComponent,
  LegendComponent,
  // The request, the limit and a node's allocatable. See UsageChart.
  MarkLineComponent,
  CanvasRenderer,
])

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
