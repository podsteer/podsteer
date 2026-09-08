import { describe, expect, it } from 'vitest'

import { karpenterNodeClaim, karpenterNodePool } from './karpenter'
import { operatorPanelFor } from './panel'

describe('selecting a Karpenter panel', () => {
  it("claims Karpenter's own kinds and not a provider's node class", () => {
    expect(operatorPanelFor('karpenter.sh', 'NodePool')).toBe('karpenter-nodepool')
    expect(operatorPanelFor('karpenter.sh', 'NodeClaim')).toBe('karpenter-nodeclaim')

    // Provider configuration: a different shape per cloud, no status worth a
    // panel, and claiming it would render a column of empty rows where the
    // server's own table said something true.
    expect(operatorPanelFor('karpenter.k8s.aws', 'EC2NodeClass')).toBeNull()
    expect(operatorPanelFor('karpenter.azure.com', 'AKSNodeClass')).toBeNull()
  })

  it("does not claim a NodePool from somebody else's group", () => {
    // The group-and-kind rule this directory is built on. "NodePool" is also a
    // GKE kind and a Cluster API concept.
    expect(operatorPanelFor('container.googleapis.com', 'NodePool')).toBeNull()
  })
})

describe('reading a NodePool', () => {
  const pool = {
    spec: {
      template: {
        spec: {
          nodeClassRef: { group: 'karpenter.k8s.aws', kind: 'EC2NodeClass', name: 'default' },
          requirements: [
            { key: 'karpenter.sh/capacity-type', operator: 'In', values: ['spot', 'on-demand'] },
            { key: 'kubernetes.io/arch', operator: 'In', values: ['arm64'], minValues: 1 },
          ],
          taints: [{ key: 'workload', value: 'batch', effect: 'NoSchedule' }],
          expireAfter: '720h',
          terminationGracePeriod: '1h',
        },
      },
      disruption: {
        consolidationPolicy: 'WhenEmptyOrUnderutilized',
        consolidateAfter: '30s',
        budgets: [
          { nodes: '10%' },
          { nodes: '0', schedule: '0 9 * * mon-fri', duration: '8h', reasons: ['Underutilized'] },
        ],
      },
      limits: { cpu: '1000', memory: '1000Gi' },
      weight: 10,
    },
    status: {
      conditions: [
        { type: 'Ready', status: 'True', reason: '', message: '', lastTransitionTime: '2026-09-08T06:00:00Z' },
        {
          type: 'NodeClassReady',
          status: 'False',
          reason: 'NodeClassNotReady',
          message: 'subnet selector matched nothing',
          lastTransitionTime: '2026-09-08T06:00:00Z',
        },
      ],
      resources: { cpu: '48', memory: '192Gi', pods: '330' },
    },
  }

  it('quotes the disruption policy, which is the field that explains a vanished node', () => {
    // Consolidation is the single setting that decides whether Karpenter will
    // move a running workload to save money, and "where did that node go" is
    // the question this object is opened with.
    const read = karpenterNodePool(pool)!
    expect(read.consolidationPolicy).toBe('WhenEmptyOrUnderutilized')
    expect(read.consolidateAfter).toBe('30s')
  })

  it("carries each condition with the controller's own reason", () => {
    const read = karpenterNodePool(pool)!
    expect(read.ready?.status).toBe('True')
    expect(read.nodeClassReady?.status).toBe('False')
    expect(read.nodeClassReady?.reason).toBe('NodeClassNotReady')
    expect(read.nodeClassReady?.message).toBe('subnet selector matched nothing')
  })

  it('keeps requirements verbatim, including minValues', () => {
    const read = karpenterNodePool(pool)!
    expect(read.requirements[0]).toEqual({
      key: 'karpenter.sh/capacity-type',
      operator: 'In',
      values: ['spot', 'on-demand'],
      minValues: null,
    })
    expect(read.requirements[1].minValues).toBe(1)
  })

  it('reads a budget that is a percentage and one that is a schedule', () => {
    // Both shapes are valid and they mean different things: a percentage
    // bounds churn, a zero on a schedule is a blackout window.
    const read = karpenterNodePool(pool)!
    expect(read.budgets[0]).toEqual({ nodes: '10%', schedule: '', duration: '', reasons: [] })
    expect(read.budgets[1].nodes).toBe('0')
    expect(read.budgets[1].reasons).toEqual(['Underutilized'])
  })

  it('sorts resource maps, so a poll does not reshuffle the panel', () => {
    const read = karpenterNodePool(pool)!
    expect(read.allocated.map((entry) => entry.resource)).toEqual(['cpu', 'memory', 'pods'])
    expect(read.limits.map((entry) => entry.resource)).toEqual(['cpu', 'memory'])
  })

  it('reads a pool Karpenter has not reached yet without inventing a status', () => {
    // What somebody wrote is worth showing before the controller has agreed
    // to it; the absence of a condition is not a failure.
    const fresh = karpenterNodePool({ spec: { template: { spec: {} } } })!
    expect(fresh.ready).toBeNull()
    expect(fresh.consolidationPolicy).toBe('')
    expect(fresh.requirements).toEqual([])
    expect(fresh.nodeClassRef).toBeNull()
  })

  it('answers null only when there is no manifest at all', () => {
    expect(karpenterNodePool(null)).toBeNull()
    expect(karpenterNodePool('not an object')).toBeNull()
  })
})

describe('reading a NodeClaim', () => {
  const claim = {
    spec: {
      nodeClassRef: { group: 'karpenter.k8s.aws', kind: 'EC2NodeClass', name: 'default' },
      expireAfter: '720h',
    },
    status: {
      conditions: [
        { type: 'Ready', status: 'True', lastTransitionTime: '2026-09-08T05:00:00Z' },
        { type: 'Launched', status: 'True', lastTransitionTime: '2026-09-08T04:58:00Z' },
        {
          type: 'Drifted',
          status: 'True',
          reason: 'NodeClassDrift',
          message: 'AMI is no longer the selected one',
          lastTransitionTime: '2026-09-08T06:30:00Z',
        },
      ],
      nodeName: 'ip-10-0-1-23.eu-west-1.compute.internal',
      providerID: 'aws:///eu-west-1a/i-0abc123',
      imageID: 'ami-0abc123',
      capacity: { cpu: '8', memory: '32Gi' },
      allocatable: { cpu: '7910m', memory: '30Gi' },
      lastPodEventTime: '2026-09-08T06:29:00Z',
    },
  }

  it('carries Drifted, which is why a node is about to be replaced', () => {
    // The two conditions that explain a replacement — Drifted and Expired —
    // are the reason this panel is worth having. A generic table shows a
    // NodeClaim that is Ready and says nothing about it being on its way out.
    const read = karpenterNodeClaim(claim)!
    expect(read.drifted?.status).toBe('True')
    expect(read.drifted?.reason).toBe('NodeClassDrift')
    expect(read.expired).toBeNull()
  })

  it('names the Node and the instance without resolving either', () => {
    const read = karpenterNodeClaim(claim)!
    expect(read.nodeName).toBe('ip-10-0-1-23.eu-west-1.compute.internal')
    expect(read.providerID).toBe('aws:///eu-west-1a/i-0abc123')
    expect(read.imageID).toBe('ami-0abc123')
  })

  it('reads a claim that has not registered yet', () => {
    // Between Launched and Registered there is no Node object at all, and an
    // empty node name is the honest answer rather than a placeholder.
    const launching = karpenterNodeClaim({
      status: { conditions: [{ type: 'Launched', status: 'True' }] },
    })!
    expect(launching.nodeName).toBe('')
    expect(launching.launched?.status).toBe('True')
    expect(launching.registered).toBeNull()
    expect(launching.capacity).toEqual([])
  })
})
