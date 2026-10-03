/**
 * Which of the topology's map controls fit in the page header, and which go
 * into its "⋯" menu.
 *
 * The topology's controls live in the SAME header row every list page uses —
 * title, count, search, then the page's own tools — and that row never wraps.
 * When it is narrow, the least-needed map controls move into an overflow menu,
 * lowest priority first: zoom (the wheel and +/- do it), orientation, collapse
 * all groups, fit, export, refresh, then Live. Search keeps at least
 * SEARCH_MIN; grouping, the applications menu, Changed, traffic and help
 * always stay.
 *
 * Widths are the drawn sizes of the controls (a 28px icon button, the gaps
 * between them), estimated rather than measured: measuring would need every
 * control rendered first, which is the wrap this exists to prevent.
 */

export type Overflowable = 'zoom' | 'orientation' | 'layers' | 'fit' | 'export' | 'refresh' | 'live'

/** Lowest priority first: the first to go into the menu. */
export const OVERFLOW_ORDER: readonly Overflowable[] = [
  'zoom',
  'orientation',
  'layers',
  'fit',
  'export',
  'refresh',
  'live',
]

const WIDTH: Record<Overflowable, number> = {
  zoom: 62, // two icon buttons
  orientation: 34,
  layers: 34,
  fit: 34,
  export: 34,
  refresh: 34,
  live: 78,
}

/** The search field never shrinks below this (8rem). */
export const SEARCH_MIN = 128
const GROUPING = 176
const APPS = 160
const LABEL_FIELD = 198
const CHANGED = 138
const TRAFFIC = 34
const TRAFFIC_OPTIONS = 34
const HELP = 38
const MENU = 34
/** The two rules between groups of controls. */
const DIVIDERS = 26

export interface HeaderState {
  apps: boolean
  labelField: boolean
  changed: boolean
  trafficOn: boolean
  /** Whether there are groups to collapse, so "layers" is drawn at all. */
  groups: boolean
}

/** The width the controls need with `menu` folded away. */
export function headerWidth(state: HeaderState, menu: ReadonlySet<Overflowable>): number {
  let width =
    SEARCH_MIN +
    GROUPING +
    (state.apps ? APPS : 0) +
    (state.labelField ? LABEL_FIELD : 0) +
    (state.changed ? CHANGED : 0) +
    TRAFFIC +
    (state.trafficOn ? TRAFFIC_OPTIONS : 0) +
    HELP +
    DIVIDERS
  for (const control of OVERFLOW_ORDER) {
    if (control === 'layers' && !state.groups) continue
    if (!menu.has(control)) width += WIDTH[control]
  }
  if (menu.size > 0) width += MENU
  return width
}

/**
 * The controls to put in the "⋯" menu for `available` pixels of header.
 * Zero or less means "not measured" — a first frame, a test — and folds
 * nothing.
 */
export function overflowControls(available: number, state: HeaderState): Set<Overflowable> {
  const menu = new Set<Overflowable>()
  if (!(available > 0)) return menu
  for (const control of OVERFLOW_ORDER) {
    if (headerWidth(state, menu) <= available) break
    if (control === 'layers' && !state.groups) continue
    menu.add(control)
  }
  return menu
}

/**
 * The header width left for the topology's controls in a window this wide:
 * the window less the navigator, the header's padding, the sidebar toggle,
 * the title and count, the terminal menu and the gaps between them. For the
 * layout tests; the page itself measures.
 */
export function availableForControls(windowWidth: number, navigatorWidth: number): number {
  const PADDING = 32
  const SIDEBAR_TOGGLE = 32
  const TITLE_AND_COUNT = 124
  const TERMINALS = 54
  const GAPS = 4 * 12
  return windowWidth - navigatorWidth - PADDING - SIDEBAR_TOGGLE - TITLE_AND_COUNT - TERMINALS - GAPS
}

import type { Snippet } from 'svelte'

/**
 * What the topology hands the workspace for its header row: the count beside
 * the title, the controls after it, and a way for ⌘K to reach its search.
 */
export interface TopologyHeaderContent {
  count: Snippet
  controls: Snippet
  focusSearch: () => void
}
