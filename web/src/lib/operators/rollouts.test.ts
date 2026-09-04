import { describe, expect, it } from 'vitest'

import { argoRollout, rolloutTone } from './rollouts'

/** A canary part-way through its steps, paused at a manual gate. */
const canary = {
  apiVersion: 'argoproj.io/v1alpha1',
  kind: 'Rollout',
  metadata: { name: 'web', namespace: 'shop' },
  spec: {
    replicas: 10,
    paused: false,
    strategy: {
      canary: {
        steps: [
          { setWeight: 20 },
          { pause: { duration: '5m' } },
          { setWeight: 50 },
          { pause: {} },
          { setWeight: 100 },
        ],
      },
    },
  },
  status: {
    phase: 'Paused',
    message: 'CanaryPauseStep',
    currentStepIndex: 3,
    pauseConditions: [{ reason: 'CanaryPauseStep', startTime: '2026-09-04T11:40:00Z' }],
    replicas: 10,
    updatedReplicas: 5,
    readyReplicas: 10,
    availableReplicas: 10,
    currentPodHash: '6b7c9d4f5',
    stableRS: '5a4b3c2d1',
    conditions: [
      { type: 'Available', status: 'True', reason: 'AvailableReason', message: 'Rollout is serving traffic from the active service.', lastTransitionTime: '2026-09-01T08:00:00Z' },
      { type: 'Progressing', status: 'True', reason: 'RolloutPaused', message: 'Rollout is paused', lastTransitionTime: '2026-09-04T11:40:00Z' },
    ],
    canary: {
      currentBackgroundAnalysisRunStatus: { name: 'web-6b7c9d4f5-2-bg', status: 'Running', message: '' },
      currentStepAnalysisRunStatus: { name: 'web-6b7c9d4f5-3', status: 'Inconclusive', message: 'metric "error-rate" assessed Inconclusive' },
      weights: { canary: { weight: 50 } },
    },
  },
}

/** A blue-green rollout Argo has aborted after a failed pre-promotion analysis. */
const aborted = {
  apiVersion: 'argoproj.io/v1alpha1',
  kind: 'Rollout',
  metadata: { name: 'api', namespace: 'shop' },
  spec: {
    replicas: 4,
    paused: true,
    strategy: { blueGreen: { activeService: 'api', previewService: 'api-preview' } },
  },
  status: {
    phase: 'Degraded',
    message: 'RolloutAborted: Rollout aborted update to revision 12',
    abort: true,
    abortedAt: '2026-09-04T10:05:00Z',
    pauseConditions: [{ reason: 'BlueGreenPause', startTime: '2026-09-04T10:00:00Z' }],
    replicas: 4,
    updatedReplicas: 0,
    readyReplicas: 4,
    availableReplicas: 4,
    currentPodHash: '9f8e7d6c5',
    stableRS: '1a2b3c4d5',
    conditions: [
      { type: 'Progressing', status: 'False', reason: 'RolloutAborted', message: 'Rollout aborted update to revision 12', lastTransitionTime: '2026-09-04T10:05:00Z' },
    ],
    blueGreen: {
      activeSelector: '1a2b3c4d5',
      previewSelector: '9f8e7d6c5',
      prePromotionAnalysisRunStatus: { name: 'api-9f8e7d6c5-pre', status: 'Failed', message: 'metric "latency" assessed Failed' },
      postPromotionAnalysisRunStatus: { name: 'api-1a2b3c4d5-post', status: 'Successful', message: '' },
    },
  },
}

describe('reading an Argo Rollouts Rollout', () => {
  it('quotes the phase and message in Argo’s own vocabulary', () => {
    // The controller's conclusion, not one recomputed here from the counts.
    expect(argoRollout(canary)?.phase).toBe('Paused')
    expect(argoRollout(aborted)?.phase).toBe('Degraded')
    expect(argoRollout(aborted)?.message).toBe('RolloutAborted: Rollout aborted update to revision 12')
  })

  it('names the strategy the spec declares, and nothing when it declares neither', () => {
    expect(argoRollout(canary)?.strategy).toBe('canary')
    expect(argoRollout(aborted)?.strategy).toBe('blueGreen')
    expect(argoRollout({ spec: {} })?.strategy).toBe('')
  })

  it('converts Argo’s 0-based step index into the 1-based number a panel shows', () => {
    // currentStepIndex 3 of five steps is "step 4 of 5". The off-by-one reads
    // as correct in every case nobody checks, which is why it is converted
    // once here rather than in the component.
    const rollout = argoRollout(canary)

    expect(rollout?.step).toBe(4)
    expect(rollout?.steps).toBe(5)
  })

  it('clamps the step number to the number of steps when every step has completed', () => {
    // Argo sets the index to the step COUNT once the last step is done,
    // which is one past the last index — `index + 1` would read "step 6 of 5".
    const finished = argoRollout({
      spec: { strategy: { canary: { steps: [{ setWeight: 20 }, { pause: {} }, { setWeight: 100 }] } } },
      status: { phase: 'Healthy', currentStepIndex: 3 },
    })

    expect(finished?.step).toBe(3)
    expect(finished?.steps).toBe(3)
  })

  it('has no step number for a strategy with no steps at all', () => {
    // "Step 1 of 0" is a fiction; so is a step for a blue-green rollout.
    expect(argoRollout(aborted)?.step).toBeNull()
    expect(argoRollout(aborted)?.steps).toBe(0)
    expect(argoRollout({ spec: { strategy: { canary: {} } }, status: { currentStepIndex: 0 } })?.step).toBeNull()
    // A canary with steps but no index recorded yet has no number either.
    expect(argoRollout({ spec: { strategy: { canary: { steps: [{ pause: {} }] } } } })?.step).toBeNull()
  })

  it('keeps a hand-set pause and a controller pause as two separate facts', () => {
    // `promote` clears the pause conditions and cannot touch spec.paused;
    // collapsing them leaves an operator pressing Promote at something
    // Promote cannot move.
    const held = argoRollout(canary)
    expect(held?.paused).toBe(false)
    expect(held?.pauseConditions).toEqual([
      { reason: 'CanaryPauseStep', startTime: '2026-09-04T11:40:00Z' },
    ])

    const byHand = argoRollout(aborted)
    expect(byHand?.paused).toBe(true)
    expect(byHand?.pauseConditions).toEqual([
      { reason: 'BlueGreenPause', startTime: '2026-09-04T10:00:00Z' },
    ])
  })

  it('reports an abort with the time Argo recorded for it', () => {
    const rollout = argoRollout(aborted)

    expect(rollout?.aborted).toBe(true)
    expect(rollout?.abortedAt).toBe('2026-09-04T10:05:00Z')
    expect(argoRollout(canary)?.aborted).toBe(false)
  })

  it('reads the desired count and the four counts status reports', () => {
    expect(argoRollout(canary)).toMatchObject({
      desiredReplicas: 10,
      replicas: 10,
      updatedReplicas: 5,
      readyReplicas: 10,
      availableReplicas: 10,
    })
  })

  it('carries the pod hashes that say whether the update is fully rolled out', () => {
    // Equal means done. The comparison itself is the panel's to draw; this
    // file only quotes the two hashes.
    expect(argoRollout(canary)).toMatchObject({ currentPodHash: '6b7c9d4f5', stableRS: '5a4b3c2d1' })
  })

  it('lists the analysis runs the status references, in a fixed reading order', () => {
    expect(argoRollout(canary)?.analysisRuns).toEqual([
      { role: 'background', name: 'web-6b7c9d4f5-2-bg', status: 'Running', message: '' },
      { role: 'canary step', name: 'web-6b7c9d4f5-3', status: 'Inconclusive', message: 'metric "error-rate" assessed Inconclusive' },
    ])

    expect(argoRollout(aborted)?.analysisRuns).toEqual([
      { role: 'pre-promotion', name: 'api-9f8e7d6c5-pre', status: 'Failed', message: 'metric "latency" assessed Failed' },
      { role: 'post-promotion', name: 'api-1a2b3c4d5-post', status: 'Successful', message: '' },
    ])
  })

  it('keeps each condition’s status beside its reason, unlike Argo CD’s', () => {
    // These are ordinary metav1 conditions; Argo CD's Application conditions
    // carry no status at all, which is why they get a different shape.
    expect(argoRollout(aborted)?.conditions).toEqual([
      {
        type: 'Progressing',
        status: 'False',
        reason: 'RolloutAborted',
        message: 'Rollout aborted update to revision 12',
        since: '2026-09-04T10:05:00Z',
      },
    ])
  })

  it('reads the selectors and the traffic weight each strategy records', () => {
    expect(argoRollout(canary)?.canaryWeight).toBe(50)
    expect(argoRollout(canary)?.activeSelector).toBe('')
    expect(argoRollout(aborted)).toMatchObject({
      activeSelector: '1a2b3c4d5',
      previewSelector: '9f8e7d6c5',
      canaryWeight: null,
    })
  })

  it('says nothing where the controller has said nothing', () => {
    // A just-applied Rollout has a spec and no status: empty strings and
    // nulls rather than a throw.
    const rollout = argoRollout({
      spec: { replicas: 3, strategy: { canary: { steps: [{ setWeight: 50 }, { pause: {} }] } } },
    })

    expect(rollout?.phase).toBe('')
    expect(rollout?.message).toBe('')
    expect(rollout?.step).toBeNull()
    expect(rollout?.steps).toBe(2)
    expect(rollout?.pauseConditions).toEqual([])
    expect(rollout?.analysisRuns).toEqual([])
    expect(rollout?.conditions).toEqual([])
    expect(rollout?.aborted).toBe(false)
    expect(rollout?.desiredReplicas).toBe(3)
    // Null and never zero: an absent count is a count Kubernetes has not
    // written, not a workload with nothing running.
    expect(rollout?.replicas).toBeNull()
    expect(rollout?.readyReplicas).toBeNull()
    expect(rollout?.currentPodHash).toBe('')
  })

  it('answers null for no manifest', () => {
    expect(argoRollout(null)).toBeNull()
    expect(argoRollout('not an object')).toBeNull()
  })
})

describe('grading a Rollout’s words the way Argo Rollouts does', () => {
  it('colours the words Argo’s own dashboard colours', () => {
    expect(rolloutTone('Degraded')).toBe('critical')
    expect(rolloutTone('Error')).toBe('critical')
    expect(rolloutTone('Failed')).toBe('critical')
    expect(rolloutTone('Paused')).toBe('warn')
    expect(rolloutTone('Inconclusive')).toBe('warn')
  })

  it('leaves the ordinary states plain, and any word it has never seen', () => {
    // A rollout in progress is the ordinary case, and colouring it would make
    // every rollout look like an incident. A word this table does not know is
    // left uncoloured rather than guessed at.
    expect(rolloutTone('Progressing')).toBeUndefined()
    expect(rolloutTone('Healthy')).toBeUndefined()
    expect(rolloutTone('Pending')).toBeUndefined()
    expect(rolloutTone('Successful')).toBeUndefined()
    expect(rolloutTone('SomethingArgoAddedLater')).toBeUndefined()
    expect(rolloutTone('degraded')).toBeUndefined()
    expect(rolloutTone('')).toBeUndefined()
  })
})
