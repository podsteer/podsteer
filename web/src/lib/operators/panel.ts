/**
 * Which operator's detail panel an open object gets, if any.
 *
 * This extends what `$lib/gitops/panel.ts` established to the four operators
 * whose custom resources an operator opens most often: cert-manager,
 * KEDA, External Secrets and Argo Rollouts, plus the Trivy Operator's
 * vulnerability reports. The mechanism is the same and so is the reason for
 * it: SELECTION IS BY API GROUP AND KIND TOGETHER, never by Kind alone.
 * "Certificate" is a kind in cert-manager.io and, with an entirely different
 * spec, in cert.gardener.cloud; "Rollout" in argoproj.io is Argo Rollouts'
 * and has nothing to do with anything else wearing the word. A panel chosen
 * on half the coordinate opens on an object whose fields it does not have
 * and renders a column of empty rows.
 *
 * QUOTATION, NOT VERDICT — the doctrine every parser in this directory
 * follows. Each one lifts fields out of the manifest the drawer already
 * fetched: one GET, no LIST of anything, no Secret read. A controller's
 * status words are shown in that controller's vocabulary, and an enum value
 * none of these files has ever seen renders as itself rather than being
 * mapped onto something familiar. Anything that is a comparison, a threshold
 * or a conclusion belongs in the Go domain where a test can argue with it.
 */

/** The API group cert-manager's kinds live in. */
export const CERT_MANAGER_GROUP = 'cert-manager.io'

/** The API group KEDA's autoscaling kinds live in. */
export const KEDA_GROUP = 'keda.sh'

/**
 * The API group External Secrets uses. Both v1beta1 and v1 serve the same
 * kinds out of it, which is why the panel is selected on the GROUP and the
 * version is never examined — see externalsecrets.ts.
 */
export const EXTERNAL_SECRETS_GROUP = 'external-secrets.io'

/**
 * The API group Argo Rollouts' kinds live in.
 *
 * THIS IS THE SAME STRING AS `ARGO_GROUP` IN `$lib/gitops/argo.ts`, and it is
 * deliberately declared twice rather than imported. Argo CD and Argo Rollouts
 * are two different controllers, installed independently, that happen to share
 * a vendor's API group: `Application` is Argo CD's and `Rollout` is Argo
 * Rollouts', and a cluster commonly runs one without the other. Importing the
 * constant would tie this file's selection to the GitOps panel's and invite
 * the mistake the group-and-kind rule exists to prevent — `operatorPanelFor`
 * must never claim `Application`, and `gitOpsPanelFor` must never claim
 * `Rollout`. The two functions are exhaustive over their own kinds, so the
 * shared group costs nothing as long as neither reaches into the other's.
 */
export const ROLLOUTS_GROUP = 'argoproj.io'

/** The API group the Trivy Operator writes its reports into. */
export const TRIVY_GROUP = 'aquasecurity.github.io'

/** The panels this module can select. */
export type OperatorPanel =
  | 'cert-manager-certificate'
  | 'keda-scaledobject'
  | 'external-secret'
  | 'argo-rollout'
  | 'trivy-vulnerabilityreport'

/**
 * Selects a panel from the opened object's API group and Kind, or null.
 *
 * Null is the ordinary answer for every other kind, and on a cluster running
 * none of these operators nothing here is ever reached: their kinds only
 * exist in the navigator when discovery found their groups.
 */
export function operatorPanelFor(
  group: string | undefined,
  kind: string | undefined,
): OperatorPanel | null {
  if (group === CERT_MANAGER_GROUP && kind === 'Certificate') return 'cert-manager-certificate'
  if (group === KEDA_GROUP && kind === 'ScaledObject') return 'keda-scaledobject'
  if (group === EXTERNAL_SECRETS_GROUP && kind === 'ExternalSecret') return 'external-secret'
  if (group === ROLLOUTS_GROUP && kind === 'Rollout') return 'argo-rollout'
  if (group === TRIVY_GROUP && kind === 'VulnerabilityReport') return 'trivy-vulnerabilityreport'
  return null
}

/**
 * One metav1 condition as an operator wrote it — the shape all four
 * controllers share.
 *
 * Unlike Argo CD's Application conditions, which carry a type and a message
 * and no status at all, these are ordinary Kubernetes conditions: the status
 * is the fact and the reason is the controller's own machine-readable word
 * for why. Both are carried verbatim; deciding what a given reason MEANS is
 * not this file's business.
 */
export interface OperatorCondition {
  /** "True", "False" or "Unknown" — verbatim, whatever the controller wrote. */
  status: string
  reason: string
  message: string
  /** lastTransitionTime, RFC 3339. */
  since: string
}

/** A condition as it arrives in a manifest, before anything has defaulted it. */
interface RawCondition {
  type?: string
  status?: string
  reason?: string
  message?: string
  lastTransitionTime?: string
}

/**
 * Reads one named condition out of a `status.conditions` array, or null.
 *
 * Null means the controller has not written that condition, which is a
 * different fact from having written it with status "False" — a freshly
 * created object has said nothing yet, and a panel that rendered the absence
 * as a negative would report a failure that has not happened. The array
 * itself is taken as `unknown` so a status that is not an array at all (an
 * object part-way through a CRD migration, a hand-edited manifest) answers
 * null instead of throwing on the way to the panel.
 */
export function conditionOf(conditions: unknown, type: string): OperatorCondition | null {
  if (!Array.isArray(conditions)) return null

  const found = (conditions as RawCondition[]).find(
    (condition) => condition && typeof condition === 'object' && condition.type === type,
  )
  if (!found) return null

  return {
    status: found.status ?? '',
    reason: found.reason ?? '',
    message: found.message ?? '',
    since: found.lastTransitionTime ?? '',
  }
}

/**
 * Signed seconds until an RFC 3339 timestamp; NaN when it does not parse or
 * is empty.
 *
 * SIGNED, unlike `secondsSince` in `$lib/gitops/panel.ts`, which clamps at
 * zero because an age cannot run backwards. A certificate that has ALREADY
 * EXPIRED is the case this function exists for, and clamping would render it
 * as expiring today for as long as it stayed in the cluster. NaN for an
 * unreadable timestamp keeps the same discipline as `secondsSince`: a
 * timestamp this cannot read is not a deadline of now.
 */
export function secondsUntil(timestamp: string, now: number = Date.now()): number {
  if (!timestamp) return Number.NaN
  const then = new Date(timestamp).getTime()
  if (!Number.isFinite(then)) return Number.NaN
  return Math.floor((then - now) / 1000)
}
