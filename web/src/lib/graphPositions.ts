/**
 * Keeps a redrawn topology still.
 *
 * A MAP THAT MOVES UNDER SOMEBODY READING IT IS WORSE THAN A STALE ONE. The
 * topology redraws without being asked — Live mode, a Refresh after "Changed"
 * — and a fresh layout of a graph that changed by one pod can shift every box
 * on the page. So a redraw never re-fits the view, and:
 *
 * - when the drawn SHAPE is the same (same boxes, same groups — a state or a
 *   count changed), every box keeps exactly the position it had, and the new
 *   layout's coordinates are thrown away;
 * - when it is not, the new layout is accepted, and the view is panned by
 *   however far the ANCHOR moved — the box nearest the centre of the pane
 *   that exists in both — so the thing somebody was looking at stays under
 *   their eyes and everything else rearranges around it.
 *
 * Only the first guarantee is absolute. A changed shape can still move what
 * is away from the centre; that is the price of not freezing a layout that no
 * longer fits its graph.
 */
import { elbow, rounded, type CompoundLayout, type PlacedEdge } from './graphLayout'

/** What was drawn: the layout, and the shape it was a layout of. */
export interface Drawn {
  layout: CompoundLayout
  /** From `shapeKey` — equal keys mean the same boxes in the same frames. */
  shape: string
}

export interface View {
  panX: number
  panY: number
  zoom: number
  width: number
  height: number
}

/**
 * Identifies the drawn shape: every box, and the frame it sits in.
 *
 * Lines are deliberately NOT part of it. A line appearing between two boxes
 * that were already drawn is a change worth showing and no reason to move
 * either of them.
 */
export function shapeKey(nodeIds: Iterable<string>, parents: Map<string, string>): string {
  return [...nodeIds]
    .map((id) => `${id}\u0001${parents.get(id) ?? ''}`)
    .sort()
    .join('\u0000')
}

export interface Preserved {
  layout: CompoundLayout
  panX: number
  panY: number
  /** Whether every box kept its position. */
  kept: boolean
}

/**
 * The layout to draw, and the pan to draw it at, given what was drawn before.
 *
 * `previous` null is a first drawing: nothing to preserve, and the caller
 * fits it.
 */
export function preserve(
  previous: Drawn | null,
  next: Drawn,
  view: View,
  horizontal: boolean,
): Preserved {
  if (!previous) return { layout: next.layout, panX: view.panX, panY: view.panY, kept: false }

  if (previous.shape === next.shape) {
    return {
      layout: keepPositions(previous.layout, next.layout, horizontal),
      panX: view.panX,
      panY: view.panY,
      kept: true,
    }
  }

  const shift = anchorShift(previous.layout, next.layout, view)
  if (!shift) return { layout: next.layout, panX: view.panX, panY: view.panY, kept: false }
  return {
    layout: next.layout,
    panX: view.panX + shift.dx * view.zoom,
    panY: view.panY + shift.dy * view.zoom,
    kept: false,
  }
}

/**
 * The new layout's lines, on the old layout's boxes.
 *
 * A line that was drawn before between the same two boxes keeps its route;
 * a new one gets the plain three-segment route the packer gives lines between
 * pieces, because the box positions it was routed for are not the ones drawn.
 */
export function keepPositions(
  previous: CompoundLayout,
  next: CompoundLayout,
  horizontal: boolean,
): CompoundLayout {
  const placed = new Map(previous.nodes.map((node) => [node.id, node]))
  const oldEdges = new Map(previous.edges.map((edge) => [`${edge.from}->${edge.to}`, edge]))
  const edges: PlacedEdge[] = next.edges.map((edge) => {
    const held = oldEdges.get(`${edge.from}->${edge.to}`)
    if (held) return { ...edge, points: held.points, path: held.path }
    const points = elbow(placed.get(edge.from)!, placed.get(edge.to)!, horizontal)
    return { ...edge, points, path: rounded(points) }
  })
  return {
    nodes: next.nodes.map((node) => placed.get(node.id) ?? node),
    edges,
    groups: previous.groups,
    bounds: previous.bounds,
  }
}

/**
 * How far the anchor moved, in layout coordinates — the box present in both
 * layouts whose OLD position was nearest the centre of the pane. Null when the
 * two layouts share no box at all.
 */
export function anchorShift(
  previous: CompoundLayout,
  next: CompoundLayout,
  view: View,
): { dx: number; dy: number; anchor: string } | null {
  const centreX = (view.width / 2 - view.panX) / view.zoom
  const centreY = (view.height / 2 - view.panY) / view.zoom
  const now = new Map(next.nodes.map((node) => [node.id, node]))

  let best: { dx: number; dy: number; anchor: string } | null = null
  let nearest = Infinity
  for (const node of previous.nodes) {
    const moved = now.get(node.id)
    if (!moved) continue
    const distance = Math.hypot(node.x - centreX, node.y - centreY)
    if (distance < nearest || (distance === nearest && best && node.id < best.anchor)) {
      nearest = distance
      best = { dx: node.x - moved.x, dy: node.y - moved.y, anchor: node.id }
    }
  }
  return best
}

/**
 * Whether a new layout is FITTED to the pane rather than kept in place.
 *
 * Always for the first drawing of a scope (`fitNext`). Otherwise only while
 * the view is still the fit it was last given (`fitHeld` — nobody has panned
 * or zoomed since) AND the drawing is a different shape: a grouping change,
 * a kind toggled, a node set that moved. A fitted view that keeps the old
 * anchor after the map changed shape is simply off-centre — the top empty
 * and the bottom cut — because there is no reading position to protect. Once
 * somebody has moved the view, the anchor rule holds and nothing jumps.
 */
export function refitsOnLayout(input: {
  fitNext: boolean
  fitHeld: boolean
  drawnShape: string | null
  shape: string
}): boolean {
  if (input.fitNext) return true
  return input.fitHeld && input.drawnShape !== null && input.drawnShape !== input.shape
}
