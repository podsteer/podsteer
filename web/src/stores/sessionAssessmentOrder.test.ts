import { beforeEach, describe, expect, it, vi } from 'vitest'

// The assessment is the read under test; the list is stubbed to succeed so
// nothing else moves the tab's health while two assessments are in flight.
const listTable = vi.fn()
const getOverview = vi.fn()
vi.mock('$lib/api/client', async () => {
  const actual = await vi.importActual<Record<string, unknown>>('$lib/api/client')
  return {
    ...actual,
    listTable: (...args: unknown[]) => listTable(...args),
    getOverview: (...args: unknown[]) => getOverview(...args),
    listKinds: vi.fn().mockRejectedValue(new Error('[unknown] no cluster in a test')),
  }
})

import { ClusterSession } from './session.svelte'
import type { Cluster, Overview, ResourceTable } from '$lib/api/client'

const cluster = { id: 'dev', name: 'dev', defaultNamespace: 'default' } as unknown as Cluster

const table = () =>
  ({ kindId: 'networking.k8s.io/v1/ingresses', title: '', namespaced: true, columns: [], rows: [] }) as unknown as ResourceTable

/** An assessment carrying one finding, so the two are told apart by id. */
const assessment = (id: string): Overview =>
  ({ findings: [{ id, severity: 'warning', title: id }], unavailable: [] }) as unknown as Overview

/** A promise this test resolves by hand, so responses can be ordered. */
function deferred<T>(): { promise: Promise<T>; resolve: (value: T) => void; reject: (reason: unknown) => void } {
  let resolve!: (value: T) => void
  let reject!: (reason: unknown) => void
  const promise = new Promise<T>((res, rej) => {
    resolve = res
    reject = rej
  })
  return { promise, resolve, reject }
}

/** A session on a plain table kind, so the assessment runs on the side. */
function session(): ClusterSession {
  const open = new ClusterSession(cluster)
  open.selectedKindId = 'networking.k8s.io/v1/ingresses'
  return open
}

const settle = () => new Promise((resolve) => setTimeout(resolve, 0))

describe('two assessments in flight', () => {
  beforeEach(() => {
    listTable.mockReset()
    listTable.mockResolvedValue(table())
    getOverview.mockReset()
  })

  it('ignores the older one when it answers last', async () => {
    // THE BUG THIS GUARDS. The assessment runs on every tick whatever is on
    // screen, so a slow one overlaps the next, and nothing in the response
    // says which tick asked for it. The older answer landing last rewound the
    // badge, recorded the timeline out of order, and — worst — rewound the
    // baseline #adopt diffs against, so the next tick re-announced a finding
    // the operator had already been alerted about.
    const slow = deferred<Overview>()
    const quick = deferred<Overview>()
    getOverview.mockReturnValueOnce(slow.promise).mockReturnValueOnce(quick.promise)

    const open = session()
    await open.refresh()
    await open.refresh()

    quick.resolve(assessment('newer'))
    await settle()
    slow.resolve(assessment('older'))
    await settle()

    expect(open.overview?.findings?.[0]?.id).toBe('newer')
  })

  it('ignores an older failure too, once a newer read has answered', async () => {
    // A superseded failure is not evidence: reporting it marks the tab
    // unreachable moments after a newer read proved the cluster is there.
    const slow = deferred<Overview>()
    const quick = deferred<Overview>()
    getOverview.mockReturnValueOnce(slow.promise).mockReturnValueOnce(quick.promise)

    const open = session()
    await open.refresh()
    await open.refresh()

    quick.resolve(assessment('newer'))
    await settle()
    slow.reject(new Error('[unreachable] The cluster could not be contacted'))
    await settle()

    expect(open.answering).toBe(true)
    expect(open.unreachableSince).toBeNull()
  })
})
