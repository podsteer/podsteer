/**
 * What an Argo Rollouts Rollout says about itself, in Argo's own words.
 *
 * QUOTATION, NOT VERDICT. The phase, the pause conditions, the analysis runs
 * and the replica counts are lifted verbatim out of the Rollout's own
 * manifest — the one GET the drawer already made. `status.phase` is the
 * controller's conclusion and is shown in the controller's vocabulary
 * (Progressing, Paused, Healthy, Degraded); PodSteer does not recompute it
 * from the counts, does not go and read the ReplicaSets, and does not decide
 * whether a canary at 20% is "stuck". A word this file has never seen renders
 * as itself.
 *
 * TWO FACTS THAT LOOK LIKE ONE. `spec.paused` is a human's pause — set by
 * hand or by `kubectl argo rollouts pause` — and `status.pauseConditions` is
 * the CONTROLLER holding at a step, an inconclusive analysis or a blue-green
 * pre-promotion gate. `promote` clears the second; only unsetting the spec
 * field clears the first. Collapsing them into one "paused" row leaves an
 * operator pressing Promote at something Promote cannot move.
 *
 * NOTE ON THE API GROUP. `Rollout` shares `argoproj.io` with Argo CD's
 * `Application`, and they are different controllers that are commonly
 * installed apart — see `ROLLOUTS_GROUP` in panel.ts for why the constant is
 * declared there rather than imported from the GitOps parsers.
 *
 * Field names follow the Rollout specification (argoproj.io/v1alpha1):
 * https://argo-rollouts.readthedocs.io/en/stable/features/specification/
 */

/** An AnalysisRun the Rollout's status references. */
export interface RolloutAnalysisRun {
  /** Which analysis this is: 'background', 'canary step', 'pre-promotion' or 'post-promotion'. */
  role: string
  name: string
  /** Argo's own phase for the run: Pending, Running, Successful, Failed, Error, Inconclusive. */
  status: string
  message: string
}

/** A reason the Rollout is paused, as the controller recorded it. */
export interface RolloutPause {
  /** Argo's reason: CanaryPauseStep, BlueGreenPause, InconclusiveAnalysisRun, … */
  reason: string
  startTime: string
}

export interface ArgoRollout {
  /** status.phase: Progressing, Paused, Healthy, Degraded — Argo's own vocabulary. */
  phase: string
  message: string
  /** 'canary', 'blueGreen', or empty when the spec declares neither. */
  strategy: string
  /**
   * status.currentStepIndex, as a 1-based step NUMBER for display. Null for a
   * strategy with no steps at all, where "step N of M" would be a fiction.
   */
  step: number | null
  /** How many steps the canary strategy declares. Zero for blue-green. */
  steps: number
  /** spec.paused — set by hand or by `kubectl argo rollouts pause`. */
  paused: boolean
  /**
   * status.pauseConditions — why the CONTROLLER is holding, which is a different
   * fact from spec.paused and the one `promote` clears.
   */
  pauseConditions: RolloutPause[]
  /** status.abort and status.abortedAt. */
  aborted: boolean
  abortedAt: string
  /** spec.replicas, and the four counts status reports. Null when the field is absent. */
  desiredReplicas: number | null
  replicas: number | null
  updatedReplicas: number | null
  readyReplicas: number | null
  availableReplicas: number | null
  /** status.currentPodHash and status.stableRS — equal means the update is fully rolled out. */
  currentPodHash: string
  stableRS: string
  analysisRuns: RolloutAnalysisRun[]
  /**
   * status.conditions, in the order Argo wrote them. Ordinary metav1 conditions,
   * unlike Argo CD's Application conditions which carry no status.
   */
  conditions: { type: string; status: string; reason: string; message: string; since: string }[]
  /** Blue-green only: status.blueGreen.activeSelector / previewSelector. */
  activeSelector: string
  previewSelector: string
  /** Canary only: status.canary.weights.canary.weight, the traffic share now. Null when unset. */
  canaryWeight: number | null
}

/** The parts of the manifest this reads. */
interface RolloutManifest {
  spec?: {
    replicas?: number
    paused?: boolean
    strategy?: {
      canary?: { steps?: unknown[] }
      blueGreen?: unknown
    }
  }
  status?: {
    phase?: string
    message?: string
    currentStepIndex?: number
    pauseConditions?: { reason?: string; startTime?: string }[]
    abort?: boolean
    abortedAt?: string
    replicas?: number
    updatedReplicas?: number
    readyReplicas?: number
    availableReplicas?: number
    currentPodHash?: string
    stableRS?: string
    conditions?: {
      type?: string
      status?: string
      reason?: string
      message?: string
      lastTransitionTime?: string
    }[]
    canary?: {
      currentBackgroundAnalysisRunStatus?: RawAnalysisRun
      currentStepAnalysisRunStatus?: RawAnalysisRun
      weights?: { canary?: { weight?: number } }
    }
    blueGreen?: {
      activeSelector?: string
      previewSelector?: string
      prePromotionAnalysisRunStatus?: RawAnalysisRun
      postPromotionAnalysisRunStatus?: RawAnalysisRun
    }
  }
}

interface RawAnalysisRun {
  name?: string
  status?: string
  message?: string
}

/**
 * Reads a Rollout, or null when there is no manifest at all.
 *
 * A Rollout with no status yet — just applied, or one the controller has not
 * reached — comes back with empty strings and nulls rather than null, so the
 * panel shows the strategy somebody just wrote and says nothing about the
 * progress, which is the truth of it.
 */
export function argoRollout(manifest: unknown): ArgoRollout | null {
  if (!manifest || typeof manifest !== 'object') return null
  const { spec = {}, status = {} } = manifest as RolloutManifest

  const canary = spec.strategy?.canary
  const steps = canary?.steps?.length ?? 0

  return {
    phase: status.phase ?? '',
    message: status.message ?? '',
    strategy: canary ? 'canary' : spec.strategy?.blueGreen ? 'blueGreen' : '',
    step: displayStep(status.currentStepIndex, steps),
    steps,
    paused: spec.paused ?? false,
    pauseConditions: (status.pauseConditions ?? []).map((pause) => ({
      reason: pause.reason ?? '',
      startTime: pause.startTime ?? '',
    })),
    aborted: status.abort ?? false,
    abortedAt: status.abortedAt ?? '',
    // Null and never zero: a Rollout whose spec omits `replicas` takes the
    // CRD's default, and rendering that as zero would report a healthy
    // workload as scaled to nothing. The same holds for the status counts,
    // which Kubernetes omits rather than writing as 0.
    desiredReplicas: spec.replicas ?? null,
    replicas: status.replicas ?? null,
    updatedReplicas: status.updatedReplicas ?? null,
    readyReplicas: status.readyReplicas ?? null,
    availableReplicas: status.availableReplicas ?? null,
    currentPodHash: status.currentPodHash ?? '',
    stableRS: status.stableRS ?? '',
    analysisRuns: analysisRunsOf(status),
    conditions: (status.conditions ?? []).map((condition) => ({
      type: condition.type ?? '',
      status: condition.status ?? '',
      reason: condition.reason ?? '',
      message: condition.message ?? '',
      since: condition.lastTransitionTime ?? '',
    })),
    activeSelector: status.blueGreen?.activeSelector ?? '',
    previewSelector: status.blueGreen?.previewSelector ?? '',
    canaryWeight: status.canary?.weights?.canary?.weight ?? null,
  }
}

/**
 * The step number a panel shows, from the 0-based index Argo records.
 *
 * `status.currentStepIndex` IS A 0-BASED INDEX AND THE DISPLAY IS 1-BASED, so
 * index 0 is "step 1 of 4". This is exactly the off-by-one that reads as
 * correct in every case an operator is unlikely to look at — a Rollout parked
 * on its last step shows the right number of steps and the wrong one for
 * where it is — so the conversion is made once, here, rather than in the
 * component.
 *
 * The clamp covers the other end of the same trap: Argo sets the index to the
 * step COUNT when every step has completed, which is one past the last index,
 * and `index + 1` would then read "step 5 of 4". Null when the strategy
 * declares no steps at all, or when the controller has not recorded an index,
 * because "step N of 0" is a fiction either way.
 */
function displayStep(index: number | undefined, steps: number): number | null {
  if (steps <= 0) return null
  if (index === undefined || index === null) return null
  return Math.min(index + 1, steps)
}

/**
 * The analysis runs the status references, in a fixed order: the canary's
 * background and step runs, then blue-green's pre- and post-promotion runs.
 *
 * Only the ones actually present are emitted. A Rollout runs at most a couple
 * of these at once — the strategies are mutually exclusive — so the order is
 * a stable reading order rather than a claim about what runs when.
 */
function analysisRunsOf(status: NonNullable<RolloutManifest['status']>): RolloutAnalysisRun[] {
  const candidates: [string, RawAnalysisRun | undefined][] = [
    ['background', status.canary?.currentBackgroundAnalysisRunStatus],
    ['canary step', status.canary?.currentStepAnalysisRunStatus],
    ['pre-promotion', status.blueGreen?.prePromotionAnalysisRunStatus],
    ['post-promotion', status.blueGreen?.postPromotionAnalysisRunStatus],
  ]

  const runs: RolloutAnalysisRun[] = []
  for (const [role, run] of candidates) {
    if (!run) continue
    runs.push({
      role,
      name: run.name ?? '',
      status: run.status ?? '',
      message: run.message ?? '',
    })
  }
  return runs
}

/**
 * How Argo Rollouts' own UI grades one of its phase words, as a DetailList
 * tone.
 *
 * THIS IS ARGO'S GRADING, NOT OURS — a word this table does not know is left
 * uncoloured. Degraded, Error and Failed are red in Argo Rollouts' own
 * dashboard and in `kubectl argo rollouts get`; Paused and Inconclusive are
 * amber, because both are states somebody has to act on and neither is a
 * failure. Progressing and Healthy are deliberately plain: a rollout in
 * progress is the ordinary case, and colouring it would make every rollout
 * look like an incident. The table is a one-to-one reading of a documented
 * vocabulary and compares nothing, which is what keeps it a quotation.
 */
export function rolloutTone(word: string): 'warn' | 'critical' | undefined {
  switch (word) {
    case 'Degraded':
    case 'Error':
    case 'Failed':
      return 'critical'
    case 'Paused':
    case 'Inconclusive':
      return 'warn'
    default:
      return undefined
  }
}
