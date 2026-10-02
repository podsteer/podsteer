/**
 * The topology's layout, off the interface thread.
 *
 * dagre is synchronous, and a five-thousand-box topology takes about a second
 * of it — a second in which, on the main thread, nothing scrolls, no button
 * answers and the spinner saying "laying out" cannot spin. Run here, the page
 * stays alive and layoutClient.ts can abandon a layout nobody wants any more
 * by terminating this worker outright.
 *
 * Nothing but geometry crosses: box ids in, positions and routes out. The
 * page joins them back to what it already holds.
 */
import { layoutCompound } from './graphLayout'
import type { LayoutRequest, LayoutResponse } from './layoutClient'

const scope = self as unknown as {
  onmessage: ((event: MessageEvent<LayoutRequest>) => void) | null
  postMessage(message: LayoutResponse): void
}

scope.onmessage = (event) => {
  const { seq, nodes, edges, parents, horizontal } = event.data
  const started = performance.now()
  try {
    const layout = layoutCompound({ nodes, edges }, new Map(parents), horizontal)
    scope.postMessage({ seq, layout, ms: performance.now() - started })
  } catch (error) {
    scope.postMessage({ seq, error: error instanceof Error ? error.message : String(error), ms: 0 })
  }
}
