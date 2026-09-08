/**
 * The three numbers a node has, and what their disagreement means.
 *
 * REQUESTS, USAGE AND ALLOCATABLE ARE NOT THREE VIEWS OF ONE FIGURE. They are
 * three different measurements, and every interesting thing about a node is in
 * the gaps between them:
 *
 *   - ALLOCATABLE is what the kubelet says the scheduler may hand out.
 *   - REQUESTS are what the pods already placed here reserved. This is the
 *     only one the scheduler looks at, so it decides whether anything new can
 *     land — however idle the machine is.
 *   - USAGE is what metrics-server measured. It decides whether the machine is
 *     actually working, and whether the kubelet is about to start evicting.
 *
 * PodSteer showed usage against allocatable on a node's panel and requests
 * against allocatable on the cluster overview, which meant the one comparison
 * that explains a stuck cluster — "reserved 95%, using 8%" — appeared on
 * neither. This module is the sentence that says it, kept out of the component
 * so the thresholds are arguable in a test rather than buried in a template.
 */

/** How a note should read. */
export type CapacityTone = 'info' | 'warn'

export interface CapacityNote {
  text: string
  tone: CapacityTone
}

/**
 * Above this share of allocatable reserved, the scheduler is nearly out of
 * room whatever the machine is doing. 90 rather than 100 because the last
 * tenth is rarely placeable: it has to fit one pod's whole request.
 */
export const SCHEDULER_TIGHT = 90

/**
 * How many times its own usage a node must have reserved before the gap is
 * worth a sentence. Twice is the point where the reservation, rather than the
 * work, is what fills the node — and it is well past the ordinary headroom
 * anybody sizing a request deliberately leaves.
 */
export const RESERVED_MULTIPLE = 2

/**
 * How far past its requests a node must be working before that is worth
 * saying. A quarter, because requests are a floor rather than a cap and every
 * healthy node drifts over its own by a little.
 */
export const OVER_REQUEST_MARGIN = 1.25

/**
 * What to say about one dimension of one node, if anything.
 *
 * Every argument is in the same unit — millicores, or bytes — and the caller
 * keeps them that way; mixing them would produce a confident sentence about a
 * comparison nobody made.
 *
 * Returns null when the three numbers say nothing an operator would act on.
 * SILENCE IS THE COMMON CASE and it is deliberate: a note beside every figure
 * on every node is one nobody reads by the third node.
 */
export function capacityNote(
  requests: number,
  usage: number,
  allocatable: number,
  dimension: string,
): CapacityNote | null {
  if (allocatable <= 0) return null

  const reservedShare = (requests / allocatable) * 100

  // The scheduler's own arithmetic first, because it is the one that stops
  // things happening. A node whose pods reserved more than it has is not a
  // rounding error: it is a node that was resized, or whose pods were placed
  // when it was larger.
  if (requests > allocatable) {
    return {
      tone: 'warn',
      text: `Pods here have reserved more ${dimension} than this node has to give. Nothing new will be scheduled onto it until something leaves.`,
    }
  }

  if (reservedShare >= SCHEDULER_TIGHT) {
    return {
      tone: 'warn',
      text: `${Math.round(reservedShare)}% of ${dimension} is already reserved, so the scheduler has almost nothing left here — whatever the node is actually using.`,
    }
  }

  // No requests at all is its own answer, and a useful one: the scheduler
  // treats this node as empty however hard it is working, which is exactly
  // how a node ends up overloaded with BestEffort pods.
  if (requests === 0) {
    return usage > 0
      ? {
          tone: 'info',
          text: `The pods on this node request no ${dimension} at all, so the scheduler counts it as free however much it is using.`,
        }
      : null
  }

  if (usage > 0 && requests >= usage * RESERVED_MULTIPLE && reservedShare >= 50) {
    return {
      tone: 'info',
      text: `Most of the reserved ${dimension} is idle: the scheduler counts the reservation, not the usage, so this node is fuller than it looks.`,
    }
  }

  if (usage > requests * OVER_REQUEST_MARGIN) {
    return {
      tone: 'info',
      text: `This node is using more ${dimension} than its pods reserved. Requests are a floor rather than a cap, so nothing is wrong — but the scheduler is placing work here on figures the pods are already past.`,
    }
  }

  return null
}

/**
 * The share of allocatable a figure represents, clamped for a bar.
 *
 * Over 100 is kept as a number rather than clamped here — the caller decides
 * whether to draw it clipped — but never negative and never NaN, because a
 * bar cannot be either and a node with no allocatable reported is the case
 * that produces both.
 */
export function shareOf(value: number, allocatable: number): number {
  if (!Number.isFinite(value) || !Number.isFinite(allocatable) || allocatable <= 0) return 0
  return Math.max(0, (value / allocatable) * 100)
}
