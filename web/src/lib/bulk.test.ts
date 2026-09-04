import { describe, expect, it } from 'vitest'

import {
  bulkActionsFor,
  bulkCommand,
  controllerOf,
  podItem,
  rowKey,
  tableRowItem,
  workloadItem,
} from './bulk'
import type { Pod, ResourceKind, TableRow, Workload } from './api/client'

describe('bulkActionsFor', () => {
  it('offers a Deployment and a StatefulSet restart, scale and delete', () => {
    expect(bulkActionsFor('Deployment')).toEqual(['restart', 'scale', 'delete'])
    expect(bulkActionsFor('StatefulSet')).toEqual(['restart', 'scale', 'delete'])
  })

  it('offers a DaemonSet no scale — it runs one pod per node', () => {
    expect(bulkActionsFor('DaemonSet')).toEqual(['restart', 'delete'])
  })

  it('offers a ReplicaSet no restart — it has no rollout', () => {
    expect(bulkActionsFor('ReplicaSet')).toEqual(['scale', 'delete'])
  })

  it('offers nodes cordon and uncordon', () => {
    expect(bulkActionsFor('Node')).toEqual(['cordon', 'uncordon', 'delete'])
  })

  it('offers every other kind delete alone, including a CRD', () => {
    // Jobs and CronJobs have no rollout and no replica count; pods cannot be
    // restarted; the generic table's rows support one verb. The review
    // dialog is where the per-object reasons are shown — this only decides
    // which buttons are worth drawing.
    for (const kind of ['Pod', 'Job', 'CronJob', 'ConfigMap', 'Secret', 'Application', '']) {
      expect(bulkActionsFor(kind)).toEqual(['delete'])
    }
  })
})

describe('controllerOf', () => {
  it('splits "Kind/name" on the first slash', () => {
    expect(controllerOf('ReplicaSet/web-abc')).toEqual({ kind: 'ReplicaSet', name: 'web-abc' })
  })

  it('gives an ownerless row an empty controller', () => {
    expect(controllerOf('')).toEqual({ kind: '', name: '' })
  })
})

describe('rowKey', () => {
  it('keys a namespaced row by namespace and name, and a cluster-scoped one by name', () => {
    expect(rowKey('web', 'api')).toBe('web/api')
    expect(rowKey('', 'node-1')).toBe('node-1')
  })
})

describe('items', () => {
  const podKind = { id: 'core/v1/pods', group: '', version: 'v1', kind: 'Pod', namespaced: true } as ResourceKind
  const deploymentKind = {
    id: 'apps/v1/deployments',
    group: 'apps',
    version: 'v1',
    kind: 'Deployment',
    namespaced: true,
  } as ResourceKind

  it('carries a pod with its controller split out of Controlled By', () => {
    const pod = { name: 'web-abc12', namespace: 'web', controlledBy: 'ReplicaSet/web-abc' } as Pod
    expect(podItem(podKind, pod)).toEqual({
      group: '',
      version: 'v1',
      kind: 'Pod',
      namespace: 'web',
      name: 'web-abc12',
      controllerKind: 'ReplicaSet',
      controllerName: 'web-abc',
      replicas: 0,
      unschedulable: false,
    })
  })

  it('carries a workload with its desired count, for scale', () => {
    const workload = { name: 'api', namespace: 'web', desired: 3, controlledBy: '' } as Workload
    const item = workloadItem(deploymentKind, workload)
    expect(item.replicas).toBe(3)
    expect(item.controllerKind).toBe('')
    expect(item.kind).toBe('Deployment')
  })

  it('carries a generic table row with no facts beyond its name', () => {
    const configMapKind = {
      id: 'core/v1/configmaps',
      group: '',
      version: 'v1',
      kind: 'ConfigMap',
      namespaced: true,
    } as ResourceKind
    const row = { name: 'settings', namespace: 'web', cells: [], labels: {}, annotations: {} } as TableRow
    const item = tableRowItem(configMapKind, row)
    expect(item).toMatchObject({ name: 'settings', namespace: 'web', controllerKind: '', controllerName: '' })
  })

  it('composes one kubectl line for the objects a bulk action will touch', () => {
    const targets = [
      { name: 'a', namespace: 'web' },
      { name: 'b', namespace: 'web' },
    ]
    expect(bulkCommand('prod', 'delete', podKind, targets, 0)).toBe(
      'kubectl --context prod -n web delete pods a b',
    )
    expect(bulkCommand('prod', 'restart', deploymentKind, targets, 0)).toBe(
      'kubectl --context prod -n web rollout restart deployment/a deployment/b',
    )
    expect(bulkCommand('prod', 'scale', deploymentKind, targets, 2)).toBe(
      'kubectl --context prod -n web scale deployment/a deployment/b --replicas=2',
    )

    const nodeKind = { id: 'core/v1/nodes', group: '', version: 'v1', kind: 'Node', namespaced: false } as ResourceKind
    const nodes = [
      { name: 'node-1', namespace: '' },
      { name: 'node-2', namespace: '' },
    ]
    expect(bulkCommand('prod', 'cordon', nodeKind, nodes, 0)).toBe('kubectl --context prod cordon node-1 node-2')
    expect(bulkCommand('prod', 'uncordon', nodeKind, nodes, 0)).toBe('kubectl --context prod uncordon node-1 node-2')
    expect(bulkCommand('prod', 'delete', nodeKind, nodes, 0)).toBe('kubectl --context prod delete nodes node-1 node-2')
  })

  it('drops the namespace of a cluster-scoped table row', () => {
    const classKind = {
      id: 'storage.k8s.io/v1/storageclasses',
      group: 'storage.k8s.io',
      version: 'v1',
      kind: 'StorageClass',
      namespaced: false,
    } as ResourceKind
    const row = { name: 'fast', namespace: '', cells: [], labels: {}, annotations: {} } as TableRow
    expect(tableRowItem(classKind, row).namespace).toBe('')
  })
})
