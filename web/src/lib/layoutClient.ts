/**
 * Asks for a topology layout, and keeps only the latest answer.
 *
 * LATEST REQUEST WINS. Somebody collapsing three groups in a row, or Live mode
 * redrawing while a group is being opened, issues layouts faster than a large
 * one finishes — and an answer for a graph that is no longer the one on screen
 * must never be drawn, even briefly. Each request supersedes the one before:
 * the earlier promise rejects with `Superseded` (which callers ignore), and a
 * worker still busy with it is TERMINATED rather than left to finish work
 * nobody will read — a fresh worker costs far less than a second of dagre.
 *
 * Where there is no Worker (a unit test, a webview that refused to start one)
 * the same layout runs on the main thread, one tick later, under the same
 * latest-wins rule. Slower to feel, identical in result.
 */
import { layoutCompound, type CompoundLayout } from './graphLayout'

export interface LayoutRequest {
  seq: number
  nodes: { id: string }[]
  edges: { id: string; from: string; to: string }[]
  /** Box id → group id, as entries, because a Map is not what crosses best. */
  parents: [string, string][]
  horizontal: boolean
}

export type LayoutResponse =
  | { seq: number; layout: CompoundLayout; ms: number; error?: undefined }
  | { seq: number; error: string; ms: number; layout?: undefined }

/** A layout that a newer request replaced. Not a failure: ignore it. */
export class Superseded extends Error {
  constructor() {
    super('superseded by a newer layout')
    this.name = 'Superseded'
  }
}

export interface LayoutResult {
  layout: CompoundLayout
  /** How long the layout itself took, for the benchmark and the status line. */
  ms: number
}

export interface LayoutClient {
  layout(request: Omit<LayoutRequest, 'seq'>): Promise<LayoutResult>
  dispose(): void
}

/** How a worker is started — replaceable, so a test can say "there is none". */
export type WorkerFactory = () => Worker

export function defaultWorkerFactory(): WorkerFactory | null {
  if (typeof Worker === 'undefined') return null
  return () => new Worker(new URL('./graphLayout.worker.ts', import.meta.url), { type: 'module' })
}

export function createLayoutClient(spawn: WorkerFactory | null = defaultWorkerFactory()): LayoutClient {
  let worker: Worker | null = null
  let latest = 0
  let pending: { seq: number; resolve: (r: LayoutResult) => void; reject: (e: Error) => void } | null = null
  /** Once a worker has failed to start or crashed, the main thread does it. */
  let broken = spawn === null
  /** The request in flight, so a worker that dies mid-way can be replaced inline. */
  let lastRequest: LayoutRequest | null = null

  const settle = (response: LayoutResponse) => {
    if (!pending || response.seq !== pending.seq) return
    const { resolve, reject } = pending
    pending = null
    if (response.layout) resolve({ layout: response.layout, ms: response.ms })
    else reject(new Error(response.error ?? 'layout failed'))
  }

  const start = (): Worker | null => {
    if (broken || !spawn) return null
    try {
      const fresh = spawn()
      fresh.onmessage = (event: MessageEvent<LayoutResponse>) => settle(event.data)
      fresh.onerror = (event) => {
        // A worker that cannot load its module (a webview with a stricter
        // policy than expected) fails here, once; everything after runs on
        // the main thread rather than failing every layout.
        event.preventDefault?.()
        broken = true
        fresh.terminate()
        if (worker === fresh) worker = null
        if (pending && lastRequest) runInline(lastRequest)
      }
      return fresh
    } catch {
      broken = true
      return null
    }
  }

  const runInline = (request: LayoutRequest) => {
    setTimeout(() => {
      if (request.seq !== latest) return
      const started = performance.now()
      try {
        const layout = layoutCompound(
          { nodes: request.nodes, edges: request.edges },
          new Map(request.parents),
          request.horizontal,
        )
        settle({ seq: request.seq, layout, ms: performance.now() - started })
      } catch (error) {
        settle({ seq: request.seq, error: error instanceof Error ? error.message : String(error), ms: 0 })
      }
    }, 0)
  }

  return {
    layout(input) {
      const seq = ++latest
      if (pending) {
        pending.reject(new Superseded())
        pending = null
        // Busy with an answer nobody will read: stop it rather than wait.
        worker?.terminate()
        worker = null
      }
      const request: LayoutRequest = { ...input, seq }
      lastRequest = request
      const promise = new Promise<LayoutResult>((resolve, reject) => {
        pending = { seq, resolve, reject }
      })
      worker ??= start()
      if (worker) worker.postMessage(request)
      else runInline(request)
      return promise
    },
    dispose() {
      if (pending) pending.reject(new Superseded())
      pending = null
      worker?.terminate()
      worker = null
    },
  }
}

/**
 * How long Live mode waits after a change before redrawing, by size.
 *
 * A bigger map costs more to fetch, lay out and look at again, and changes
 * more often — a namespace with five thousand objects has something changing
 * every second — so it settles for longer before it redraws. 1, 2, 5 and 15
 * seconds at fewer than 500, 2,000, 5,000 boxes and above.
 */
export function liveDelayMs(nodeCount: number): number {
  if (nodeCount < 500) return 1_000
  if (nodeCount < 2_000) return 2_000
  if (nodeCount < 5_000) return 5_000
  return 15_000
}
