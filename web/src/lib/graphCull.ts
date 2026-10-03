/**
 * Which boxes and lines are on screen.
 *
 * A topology of five thousand boxes is five thousand SVG groups, each with an
 * icon and two lines of text, and a browser that has to lay out and paint all
 * of them on every pan is a map that drags. Most of them are off screen at any
 * zoom where they can be read, so only what intersects the viewport (plus a
 * margin, so a pan does not reveal an empty edge before the next frame) is
 * put into the document.
 *
 * A BUCKET GRID rather than a tree: everything is the same size, the layout
 * packs it evenly, and a grid answers "what is in this rectangle" by visiting
 * only the cells the rectangle covers. Built once per layout, queried per pan.
 *
 * This decides what is DRAWN, never what EXISTS — the counts on the page are
 * the graph's, and the PNG export turns culling off for the frame it renders.
 */
import type { CompoundLayout } from './graphLayout'

export interface Rect {
  x: number
  y: number
  width: number
  height: number
}

export interface CullIndex {
  cell: number
  /** Cell key → ids of boxes, lines and frames touching it. */
  nodes: Map<string, string[]>
  edges: Map<string, string[]>
  groups: Map<string, string[]>
  /** Every rectangle, for the exact test after the cell lookup. */
  nodeRect: Map<string, Rect>
  edgeRect: Map<string, Rect>
  groupRect: Map<string, Rect>
}

/** The cell size: a few boxes across, so a viewport covers a handful of cells. */
export const CULL_CELL = 640

function cellsOf(rect: Rect, cell: number, visit: (key: string) => void): void {
  const x0 = Math.floor(rect.x / cell)
  const y0 = Math.floor(rect.y / cell)
  const x1 = Math.floor((rect.x + rect.width) / cell)
  const y1 = Math.floor((rect.y + rect.height) / cell)
  for (let cx = x0; cx <= x1; cx++) for (let cy = y0; cy <= y1; cy++) visit(`${cx},${cy}`)
}

function put(index: Map<string, string[]>, key: string, id: string): void {
  const list = index.get(key)
  if (list) list.push(id)
  else index.set(key, [id])
}

/** A line's extent, from the corners of its route. */
export function edgeBounds(points: { x: number; y: number }[]): Rect {
  let minX = Infinity
  let minY = Infinity
  let maxX = -Infinity
  let maxY = -Infinity
  for (const point of points) {
    minX = Math.min(minX, point.x)
    minY = Math.min(minY, point.y)
    maxX = Math.max(maxX, point.x)
    maxY = Math.max(maxY, point.y)
  }
  if (minX === Infinity) return { x: 0, y: 0, width: 0, height: 0 }
  return { x: minX, y: minY, width: maxX - minX, height: maxY - minY }
}

export function buildCullIndex(
  layout: Pick<CompoundLayout, 'nodes' | 'edges' | 'groups'>,
  cell = CULL_CELL,
): CullIndex {
  const index: CullIndex = {
    cell,
    nodes: new Map(),
    edges: new Map(),
    groups: new Map(),
    nodeRect: new Map(),
    edgeRect: new Map(),
    groupRect: new Map(),
  }
  for (const node of layout.nodes) {
    const rect = { x: node.x - node.width / 2, y: node.y - node.height / 2, width: node.width, height: node.height }
    index.nodeRect.set(node.id, rect)
    cellsOf(rect, cell, (key) => put(index.nodes, key, node.id))
  }
  for (const edge of layout.edges) {
    const rect = edgeBounds(edge.points)
    index.edgeRect.set(edge.id, rect)
    // A long line crossing the whole page touches many cells; it is filed in
    // every one, which is what lets a viewport in the middle of it find it.
    cellsOf(rect, cell, (key) => put(index.edges, key, edge.id))
  }
  for (const group of layout.groups) {
    const rect = { x: group.x, y: group.y, width: group.width, height: group.height }
    index.groupRect.set(group.id, rect)
    cellsOf(rect, cell, (key) => put(index.groups, key, group.id))
  }
  return index
}

function intersects(a: Rect, b: Rect): boolean {
  return a.x <= b.x + b.width && b.x <= a.x + a.width && a.y <= b.y + b.height && b.y <= a.y + a.height
}

export interface Visible {
  nodes: Set<string>
  edges: Set<string>
  groups: Set<string>
}

/** What intersects the viewport, grown by `margin` on every side. */
export function visible(index: CullIndex, viewport: Rect, margin = 0): Visible {
  const area = {
    x: viewport.x - margin,
    y: viewport.y - margin,
    width: viewport.width + margin * 2,
    height: viewport.height + margin * 2,
  }
  const out: Visible = { nodes: new Set(), edges: new Set(), groups: new Set() }
  cellsOf(area, index.cell, (key) => {
    for (const id of index.nodes.get(key) ?? []) {
      if (!out.nodes.has(id) && intersects(index.nodeRect.get(id)!, area)) out.nodes.add(id)
    }
    for (const id of index.edges.get(key) ?? []) {
      if (!out.edges.has(id) && intersects(index.edgeRect.get(id)!, area)) out.edges.add(id)
    }
    for (const id of index.groups.get(key) ?? []) {
      if (!out.groups.has(id) && intersects(index.groupRect.get(id)!, area)) out.groups.add(id)
    }
  })
  return out
}

/** The part of the drawing a pane shows, in layout coordinates. */
export function viewportOf(
  pan: { x: number; y: number },
  zoom: number,
  pane: { width: number; height: number },
): Rect {
  return { x: -pan.x / zoom, y: -pan.y / zoom, width: pane.width / zoom, height: pane.height / zoom }
}
