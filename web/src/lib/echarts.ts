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
  /** Subscribes to a chart event — used by the dependency map for clicks. */
  on(event: string, handler: (params: unknown) => void): void
  /** Drives the chart from code, for the map's zoom buttons. */
  dispatchAction(action: Record<string, unknown>): void
  resize(): void
  dispose(): void
}

/** Creates a chart bound to an element, with only the registered pieces. */
export function createChart(element: HTMLElement): Chart {
  return init(element, undefined, { renderer: 'canvas' }) as unknown as Chart
}
