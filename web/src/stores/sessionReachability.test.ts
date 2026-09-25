import { beforeEach, describe, expect, it, vi } from 'vitest'

// Both of the reads a refresh makes are controlled here, because what is
// under test is precisely which of them decides the cluster's health: the
// list (which a live watch can answer from memory) or the assessment (which
// always goes to the API server).
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
import type { Cluster, ResourceTable } from '$lib/api/client'

const cluster = { id: 'dev', name: 'dev', defaultNamespace: 'default' } as unknown as Cluster

const table = () =>
  ({ kindId: 'networking.k8s.io/v1/ingresses', title: '', namespaced: true, columns: [], rows: [] }) as unknown as ResourceTable

/** A session on a plain table kind, so #fetch goes through listTable. */
function session(): ClusterSession {
  const open = new ClusterSession(cluster)
  open.selectedKindId = 'networking.k8s.io/v1/ingresses'
  return open
}

/** Settles the assessment, which refresh() starts without awaiting. */
const settle = () => new Promise((resolve) => setTimeout(resolve, 0))

describe('whether a cluster is answering', () => {
  beforeEach(() => {
    listTable.mockReset()
    getOverview.mockReset()
    getOverview.mockRejectedValue(new Error('[unknown] not part of this test'))
  })

  it('starts out answering, having no reason to think otherwise', () => {
    const open = session()

    expect(open.answering).toBe(true)
    expect(open.unreachableSince).toBeNull()
  })

  it('stops answering when the list fails on transport', async () => {
    const open = session()
    listTable.mockRejectedValue(new Error('[unreachable] The cluster did not respond'))

    await open.refresh()

    expect(open.answering).toBe(false)
    expect(open.unreachableSince).not.toBeNull()
  })

  it('stops answering when only the assessment fails — the list may be a watch store', async () => {
    // THE BUG THIS GUARDS. A watched kind is served from an in-memory store
    // without touching the network, so the rows keep arriving after the VPN
    // goes away. The assessment is the read that always leaves the machine,
    // and it is what the tab's dot is entitled to believe.
    const open = session()
    listTable.mockResolvedValue(table())
    getOverview.mockRejectedValue(new Error('[unreachable] The cluster could not be contacted'))

    await open.refresh()
    await settle()

    expect(open.answering).toBe(false)
  })

  it('stays answering when a read is refused — that is a permission, not a network', async () => {
    // Painting the tab red for an RBAC refusal sends somebody to check a VPN
    // over an account that may not list one kind.
    const open = session()
    listTable.mockRejectedValue(new Error('[forbidden] your account may not list ingresses'))

    await open.refresh()
    await settle()

    expect(open.answering).toBe(true)
    expect(open.unreachableSince).toBeNull()
  })

  it('keeps the FIRST failure’s time, so the interface can say how long', async () => {
    const open = session()
    listTable.mockRejectedValue(new Error('[unreachable] The cluster did not respond'))

    await open.refresh()
    const first = open.unreachableSince

    await new Promise((resolve) => setTimeout(resolve, 5))
    await open.refresh()

    expect(open.unreachableSince).toBe(first)
  })

  it('answers again when the assessment gets through, not when the rows do', async () => {
    // Recovery is the same asymmetry as the failure: rows can arrive from a
    // watch store with the network still gone, so they are not what lifts
    // this. The assessment answering is.
    const open = session()
    listTable.mockResolvedValue(table())
    getOverview.mockRejectedValueOnce(new Error('[unreachable] The cluster could not be contacted'))

    await open.refresh()
    await settle()
    expect(open.answering).toBe(false)

    // The rows alone, once more — and the verdict must stand.
    getOverview.mockRejectedValueOnce(new Error('[unknown] still nothing from the assessment'))
    await open.refresh()
    await settle()
    expect(open.answering).toBe(false)

    getOverview.mockResolvedValue({ findings: [], unavailable: [] })
    await open.refresh()
    await settle()

    expect(open.answering).toBe(true)
    expect(open.unreachableSince).toBeNull()
  })
})
