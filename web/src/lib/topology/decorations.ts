/**
 * How another layer draws ON the topology without the topology knowing what
 * it is.
 *
 * Observed traffic is the first such layer (trafficLayer.ts, TrafficPanel),
 * and it is deliberately NOT a set of edges in the graph: an edge on the map
 * is a relationship Kubernetes has, and "these two workloads exchanged 40
 * requests a second" is a measurement of a window, from a source that may not
 * see everything. So a layer can do two things only:
 *
 * - DECORATE a dependency line the map already draws — widen it, tint it,
 *   label it — through `decorateEdge`;
 * - draw its OWN lines, `overlayEdges`, between boxes, styled apart from the
 *   dependency lines and never laid out, routed or counted as them.
 *
 * Ids are the topology's own node ids. The page maps each onto whatever box
 * draws it — the object, its folded set, its collapsed group — and merges
 * overlay lines that land on the same pair of boxes.
 */
import type { TopologyEdgeKind } from './contract'

/** Tones are theme tokens' meanings, never colours: the page picks the token. */
export type DecorationTone = 'normal' | 'warn' | 'critical' | 'muted'

export interface EdgeDecoration {
  /** Stroke width in layout pixels; the page clamps it. */
  width?: number
  tone?: DecorationTone
  /** A short label drawn at the line's midpoint, such as "42 rps". */
  label?: string
  /** The full statement, read on hover and by assistive technology. */
  title?: string
}

export interface OverlayEdge {
  id: string
  from: string
  to: string
  decoration: EdgeDecoration
}

export interface TopologyDecorator {
  /** Called for each drawn dependency line, with the topology ids at both ends. */
  decorateEdge?(edge: { from: string[]; to: string[]; kinds: TopologyEdgeKind[] }): EdgeDecoration | null
  overlayEdges?: OverlayEdge[]
}
