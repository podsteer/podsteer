import { describe, expect, it } from 'vitest'
import { createLayoutClient, liveDelayMs, Superseded, type LayoutRequest, type LayoutResponse } from './layoutClient'
import { layoutCompound } from './graphLayout'

/** A stand-in Worker that answers when told to, so ordering can be controlled. */
class FakeWorker {
  static made: FakeWorker[] = []
  onmessage: ((event: MessageEvent<LayoutResponse>) => void) | null = null
  onerror: ((event: ErrorEvent) => void) | null = null
  received: LayoutRequest[] = []
  terminated = false
  constructor() {
    FakeWorker.made.push(this)
  }
  postMessage(request: LayoutRequest) {
    this.received.push(request)
  }
  terminate() {
    this.terminated = true
  }
  answer(index = 0) {
    const request = this.received[index]
    const layout = layoutCompound({ nodes: request.nodes, edges: request.edges }, new Map(request.parents), request.horizontal)
    this.onmessage?.({ data: { seq: request.seq, layout, ms: 1 } } as MessageEvent<LayoutResponse>)
  }
}

const request = (ids: string[]) => ({
  nodes: ids.map((id) => ({ id })),
  edges: [],
  parents: [] as [string, string][],
  horizontal: true,
})

describe('layout client', () => {
  it('rejects a superseded request and stops the worker busy with it', async () => {
    FakeWorker.made = []
    const client = createLayoutClient(() => new FakeWorker() as unknown as Worker)

    const first = client.layout(request(['a']))
    const second = client.layout(request(['a', 'b']))

    await expect(first).rejects.toBeInstanceOf(Superseded)
    expect(FakeWorker.made[0].terminated).toBe(true)

    // The replacement worker got only the latest request.
    const fresh = FakeWorker.made[1]
    expect(fresh.received.map((r) => r.nodes.length)).toEqual([2])
    fresh.answer()
    const result = await second
    expect(result.layout.nodes.map((n) => n.id)).toEqual(['a', 'b'])
  })

  it('ignores a late answer for an old request', async () => {
    FakeWorker.made = []
    const client = createLayoutClient(() => new FakeWorker() as unknown as Worker)
    const only = client.layout(request(['x']))
    const worker = FakeWorker.made[0]
    // An answer carrying a sequence nobody is waiting for changes nothing.
    worker.onmessage?.({ data: { seq: 999, error: 'stale', ms: 0 } } as MessageEvent<LayoutResponse>)
    worker.answer()
    await expect(only).resolves.toMatchObject({ ms: 1 })
  })

  it('runs on the main thread when there is no worker', async () => {
    const client = createLayoutClient(null)
    const result = await client.layout(request(['a', 'b', 'c']))
    expect(result.layout.nodes).toHaveLength(3)
  })

  it('waits longer before a live redraw the bigger the map', () => {
    expect(liveDelayMs(10)).toBe(1_000)
    expect(liveDelayMs(499)).toBe(1_000)
    expect(liveDelayMs(500)).toBe(2_000)
    expect(liveDelayMs(1_999)).toBe(2_000)
    expect(liveDelayMs(2_000)).toBe(5_000)
    expect(liveDelayMs(4_999)).toBe(5_000)
    expect(liveDelayMs(5_000)).toBe(15_000)
  })
})
