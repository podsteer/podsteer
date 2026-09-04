import { describe, expect, it } from 'vitest'
import type { K8sEvent, Pod, Workload } from './api/client'
import {
  fleetRowTarget,
  flattenFleet,
  hasClusterTerm,
  matchesChips,
  mergeFleet,
  stripModel,
  toggleClusterTerm,
  WORKLOAD_CHIPS,
  type ClusterAnswer,
  type ClusterRead,
} from './fleet'

/** A read of one cluster with the given verdict and rows. */
function read<T>(
  cluster: string,
  status: ClusterRead<T>['status'],
  items: T[] = [],
  extra: Partial<Pick<ClusterRead<T>, 'reason' | 'missing'>> = {},
): ClusterRead<T> {
  return { cluster, status, reason: extra.reason ?? '', missing: extra.missing ?? [], items }
}

/** A pod with only the fields these rules read. */
function pod(name: string, namespace = 'default'): Pod {
  return { name, namespace } as Pod
}

describe('mergeFleet', () => {
  it('keeps clusters in the order they answered and stamps nothing itself', () => {
    const merged = mergeFleet<Pod>(
      [],
      [read('prod', 'ok', [pod('api-0')]), read('dev', 'ok', [pod('api-0')])],
      1_000,
    )

    expect(merged.map((answer) => answer.cluster)).toEqual(['prod', 'dev'])
    expect(merged[0]).toMatchObject({ status: 'ok', rowsAt: 1_000, stale: false })
    expect(merged[0].rows).toEqual([pod('api-0')])
  })

  it('keeps the rows a slow cluster last showed, marked stale, until it answers', () => {
    const first = mergeFleet<Pod>([], [read('prod', 'ok', [pod('api-0')])], 1_000)
    const second = mergeFleet(first, [read('prod', 'slow')], 11_000)

    expect(second[0]).toMatchObject({ status: 'slow', stale: true, rowsAt: 1_000 })
    expect(second[0].rows).toEqual([pod('api-0')])

    // A late answer with rows replaces them.
    const third = mergeFleet(second, [read('prod', 'slow', [pod('api-1')])], 21_000)
    expect(third[0]).toMatchObject({ status: 'slow', stale: false, rowsAt: 21_000 })
    expect(third[0].rows).toEqual([pod('api-1')])
  })

  it('keeps the rows an unreachable cluster last showed, marked stale', () => {
    const first = mergeFleet<Pod>([], [read('prod', 'ok', [pod('api-0')])], 1_000)
    const second = mergeFleet(
      first,
      [read('prod', 'unreachable', [], { reason: 'The cluster did not respond' })],
      11_000,
    )

    expect(second[0]).toMatchObject({ status: 'unreachable', stale: true })
    expect(second[0].rows).toEqual([pod('api-0')])
  })

  it('shows nothing for a refused or failed cluster, whatever it showed before', () => {
    const first = mergeFleet<Pod>([], [read('prod', 'ok', [pod('api-0')])], 1_000)

    for (const status of ['forbidden', 'failed'] as const) {
      const next = mergeFleet(first, [read('prod', status, [], { reason: 'no' })], 11_000)
      expect(next[0]).toMatchObject({ status, stale: false, rowsAt: null })
      expect(next[0].rows).toEqual([])
    }
  })

  it('is not stale on the first slow read, when there was nothing to keep', () => {
    const merged = mergeFleet<Pod>([], [read('prod', 'slow')], 1_000)
    expect(merged[0]).toMatchObject({ status: 'slow', stale: false, rowsAt: null })
  })

  it('drops a cluster the read no longer includes — its tab closed', () => {
    const first = mergeFleet<Pod>(
      [],
      [read('prod', 'ok', [pod('api-0')]), read('dev', 'ok', [pod('api-0')])],
      1_000,
    )
    const second = mergeFleet(first, [read('prod', 'ok', [pod('api-0')])], 2_000)

    expect(second.map((answer) => answer.cluster)).toEqual(['prod'])
  })

  it('carries a partial answer with what it is missing', () => {
    const merged = mergeFleet<Workload>(
      [],
      [read('prod', 'partial', [{ name: 'web' } as Workload], { missing: ['CronJob'], reason: 'RBAC' })],
      1_000,
    )
    expect(merged[0]).toMatchObject({ status: 'partial', missing: ['CronJob'], reason: 'RBAC' })
    expect(merged[0].rows).toHaveLength(1)
  })
})

describe('flattenFleet', () => {
  it('stamps every row with its cluster and keeps cluster order', () => {
    const answers = mergeFleet<Pod>(
      [],
      [read('prod', 'ok', [pod('api-0'), pod('api-1')]), read('dev', 'ok', [pod('api-0')])],
      0,
    )

    const rows = flattenFleet(answers)
    expect(rows.map((row) => `${row.cluster}/${row.namespace}/${row.name}`)).toEqual([
      'prod/default/api-0',
      'prod/default/api-1',
      'dev/default/api-0',
    ])
  })

  it('does not lose a cluster whose rows are stale', () => {
    const first = mergeFleet<Pod>([], [read('prod', 'ok', [pod('api-0')])], 0)
    const second = mergeFleet(first, [read('prod', 'slow')], 10_000)
    expect(flattenFleet(second)).toHaveLength(1)
  })
})

describe('stripModel', () => {
  const answer = (
    status: ClusterAnswer<Pod>['status'],
    rows: Pod[],
    overrides: Partial<ClusterAnswer<Pod>> = {},
  ): ClusterAnswer<Pod> => ({
    cluster: 'prod',
    status,
    reason: '',
    missing: [],
    rows,
    rowsAt: 0,
    stale: false,
    ...overrides,
  })

  it('gives each status its tone and word, in tab order', () => {
    const strip = stripModel(
      [
        answer('ok', [pod('a')]),
        { ...answer('forbidden', []), cluster: 'dev', rowsAt: null },
        { ...answer('slow', []), cluster: 'edge', rowsAt: null },
      ],
      5_000,
    )

    expect(strip.map((entry) => entry.cluster)).toEqual(['prod', 'dev', 'edge'])
    expect(strip[0]).toMatchObject({ tone: 'success', label: 'Read', rows: 1, ageSeconds: 5 })
    expect(strip[1]).toMatchObject({ tone: 'warning', label: 'Forbidden', rows: 0, ageSeconds: null })
    expect(strip[2]).toMatchObject({ tone: 'info', label: 'Slow', rows: 0 })
  })

  it('says why, and how old the rows shown are, in the title', () => {
    const [refused] = stripModel(
      [answer('forbidden', [], { reason: 'Your account is not allowed to perform this operation', rowsAt: null })],
      0,
    )
    expect(refused.title).toBe('prod — Your account is not allowed to perform this operation')

    const [slow] = stripModel([answer('slow', [pod('a')], { stale: true, rowsAt: 0 })], 12_000)
    expect(slow.title).toBe('prod — still reading; showing 1 row from 12s ago')

    const [partial] = stripModel(
      [answer('partial', [pod('a'), pod('b')], { missing: ['CronJob', 'Job'], reason: 'RBAC' })],
      0,
    )
    expect(partial.title).toBe('prod — 2 rows; CronJob, Job not read: RBAC')
  })
})

describe('fleetRowTarget', () => {
  it('opens a pod as a Pod in its own cluster', () => {
    const row = { name: 'api-0', namespace: 'shop', cluster: 'prod' } as Pod & { cluster: string }
    expect(fleetRowTarget('pods', row)).toEqual({
      cluster: 'prod',
      kind: 'Pod',
      name: 'api-0',
      namespace: 'shop',
    })
  })

  it("opens a workload as the row's own kind", () => {
    const row = { kind: 'CronJob', name: 'nightly', namespace: 'batch', cluster: 'dev' } as Workload & {
      cluster: string
    }
    expect(fleetRowTarget('workloads', row)).toEqual({
      cluster: 'dev',
      kind: 'CronJob',
      name: 'nightly',
      namespace: 'batch',
    })
  })

  it('opens an event as itself, not as the object it is about', () => {
    const row = {
      name: 'api-0.17c2',
      namespace: 'shop',
      involvedName: 'api-0',
      cluster: 'prod',
    } as K8sEvent & { cluster: string }
    expect(fleetRowTarget('events', row)).toEqual({
      cluster: 'prod',
      kind: 'Event',
      name: 'api-0.17c2',
      namespace: 'shop',
    })
  })
})

describe('chips', () => {
  it('OR across the selected chips and pass everything with none selected', () => {
    const rolling = { isHealthy: true, isRolling: true, suspended: false } as Workload
    const broken = { isHealthy: false, isRolling: false, suspended: false } as Workload
    const fine = { isHealthy: true, isRolling: false, suspended: false } as Workload

    expect(matchesChips(fine, WORKLOAD_CHIPS, [])).toBe(true)
    expect(matchesChips(fine, WORKLOAD_CHIPS, ['unhealthy'])).toBe(false)
    expect(matchesChips(broken, WORKLOAD_CHIPS, ['unhealthy'])).toBe(true)
    expect(matchesChips(rolling, WORKLOAD_CHIPS, ['unhealthy', 'rolling'])).toBe(true)
  })
})

describe('the cluster: term a strip chip toggles', () => {
  it('adds the term, then removes exactly it', () => {
    expect(toggleClusterTerm('', 'prod')).toBe('cluster:prod')
    expect(toggleClusterTerm('web cluster:prod', 'prod')).toBe('web')
    expect(toggleClusterTerm('web', 'prod')).toBe('web cluster:prod')
    expect(hasClusterTerm('web cluster:prod', 'prod')).toBe(true)
    expect(hasClusterTerm('web cluster:production', 'prod')).toBe(false)
  })

  it('quotes a name with a space, and parses back as one term', () => {
    expect(toggleClusterTerm('', 'my cluster')).toBe('cluster:"my cluster"')
    expect(hasClusterTerm('cluster:"my cluster"', 'my cluster')).toBe(true)
  })
})
