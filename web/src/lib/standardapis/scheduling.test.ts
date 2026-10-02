import { describe, expect, it } from 'vitest'

import { standardPanelFor, SCHEDULING_GROUP } from './panel'
import { podGroup, podGroupNameOf, workload } from './scheduling'

const gangWorkload = {
  apiVersion: 'scheduling.k8s.io/v1beta1',
  kind: 'Workload',
  metadata: { name: 'training-job-workload', namespace: 'ml' },
  spec: {
    controllerRef: { apiGroup: 'batch', kind: 'Job', name: 'training-job' },
    podGroupTemplates: [
      {
        name: 'workers',
        schedulingPolicy: { gang: { minCount: 4 } },
        priorityClassName: 'high-priority',
        disruptionMode: { all: {} },
      },
      { name: 'helpers', schedulingPolicy: { basic: {} } },
    ],
  },
}

const group = {
  apiVersion: 'scheduling.k8s.io/v1beta1',
  kind: 'PodGroup',
  metadata: { name: 'workers-1', namespace: 'ml' },
  spec: {
    schedulingPolicy: { gang: { minCount: 4 } },
    workloadRef: { name: 'training-job-workload' },
    priority: 0,
    preemptionPolicy: 'Never',
    resourceClaims: [{ name: 'gpus' }],
  },
  status: {
    conditions: [
      {
        type: 'PodGroupInitiallyScheduled',
        status: 'False',
        reason: 'Unschedulable',
        message: 'insufficient capacity for the gang',
      },
    ],
  },
}

describe('selecting the gang scheduling panels', () => {
  it('needs the group and the kind to agree', () => {
    expect(standardPanelFor(SCHEDULING_GROUP, 'Workload')).toBe('workload')
    expect(standardPanelFor(SCHEDULING_GROUP, 'PodGroup')).toBe('pod-group')
    // PriorityClass shares the group and is not claimed.
    expect(standardPanelFor(SCHEDULING_GROUP, 'PriorityClass')).toBeNull()
    // "Workload" in another group is not this one.
    expect(standardPanelFor('example.com', 'Workload')).toBeNull()
    expect(standardPanelFor(undefined, 'PodGroup')).toBeNull()
  })
})

describe('reading a Workload', () => {
  it('reads each template, its policy and the gang minimum', () => {
    const view = workload(gangWorkload)!
    expect(view.controller).toEqual({ apiGroup: 'batch', kind: 'Job', name: 'training-job' })
    expect(view.templates).toEqual([
      {
        name: 'workers',
        policy: 'gang',
        minCount: 4,
        priorityClassName: 'high-priority',
        disruptionMode: 'all',
      },
      { name: 'helpers', policy: 'basic', minCount: null, priorityClassName: '', disruptionMode: '' },
    ])
  })

  it('keeps a policy it has never heard of, verbatim', () => {
    const view = workload({ spec: { podGroupTemplates: [{ name: 'x', schedulingPolicy: { future: {} } }] } })!
    expect(view.templates[0].policy).toBe('future')
  })

  it('counts composite templates without rendering them', () => {
    expect(workload({ spec: { compositePodGroupTemplates: [{}, {}] } })!.compositeTemplates).toBe(2)
  })

  it('survives a manifest with no spec, and answers null for no manifest', () => {
    expect(workload({})!.templates).toEqual([])
    expect(workload(undefined)).toBeNull()
  })
})

describe('reading a PodGroup', () => {
  it('reads the policy, the workload it came from and the scheduler condition', () => {
    const view = podGroup(group)!
    expect(view.policy).toBe('gang')
    expect(view.minCount).toBe(4)
    // The namespace falls back to the PodGroup's own.
    expect(view.workload).toEqual({ name: 'training-job-workload', namespace: 'ml' })
    expect(view.priority).toBe(0)
    expect(view.preemptionPolicy).toBe('Never')
    expect(view.resourceClaims).toEqual(['gpus'])
    expect(view.scheduled?.status).toBe('False')
    expect(view.scheduled?.reason).toBe('Unschedulable')
  })

  it('separates a condition not written from one written False', () => {
    const view = podGroup({ spec: { schedulingPolicy: { gang: { minCount: 2 } } } })!
    expect(view.scheduled).toBeNull()
    expect(view.disruptionTarget).toBeNull()
  })

  it('keeps a zero priority apart from an absent one', () => {
    expect(podGroup(group)!.priority).toBe(0)
    expect(podGroup({ spec: {} })!.priority).toBeNull()
  })
})

describe('the pod to PodGroup link', () => {
  it('reads spec.schedulingGroup.podGroupName and nothing else', () => {
    expect(podGroupNameOf({ spec: { schedulingGroup: { podGroupName: 'workers-1' } } })).toBe(
      'workers-1',
    )
  })

  it('does not infer a group from labels', () => {
    expect(podGroupNameOf({ metadata: { labels: { 'pod-group': 'workers-1' } }, spec: {} })).toBe('')
    expect(podGroupNameOf(undefined)).toBe('')
  })
})
