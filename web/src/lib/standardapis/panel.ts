/**
 * Which of Kubernetes' own newer APIs an open object belongs to, if any.
 *
 * A third family alongside `$lib/gitops/panel.ts` and `$lib/operators/panel.ts`,
 * built on the mechanism both established and for the same reason: SELECTION
 * IS BY API GROUP AND KIND TOGETHER, never by Kind alone. "Gateway" is a kind
 * in gateway.networking.k8s.io and, with nothing in common, in
 * networking.istio.io and in gloo.solo.io. A panel chosen on half the
 * coordinate opens on an object whose fields it does not have and renders a
 * column of empty rows.
 *
 * What differs here is the provenance rather than the mechanism. These are not
 * one vendor's custom resources: Gateway API is the declared successor to
 * Ingress and ships as CRDs under a k8s.io group, and resource.k8s.io and
 * admissionregistration.k8s.io are in-tree groups behind feature gates. All
 * three were already browsable through the generic table, which prints
 * whatever columns the API server prints; what none of them had was a panel
 * saying what the object actually declares.
 *
 * THE VERSION IS NEVER EXAMINED, and for resource.k8s.io that is the design
 * rather than a convenience. That group has been re-shaped in nearly every
 * release it has existed for — a claim named one class in the earliest
 * versions and names a list of device requests now, and the requests
 * themselves moved under `exactly` later still. Selecting on the group and
 * then reading by SHAPE means an object served by a version nobody here has
 * seen renders as much of itself as its fields allow, instead of nothing. The
 * same rule already applies to External Secrets, which serves one kind out of
 * two versions.
 *
 * QUOTATION, NOT VERDICT, and in this directory there is no exception at all —
 * not even the one `$lib/operators` names for a certificate's expiry. Every
 * field below is lifted from the manifest the drawer already fetched: one GET,
 * no LIST, no cross-object resolution. A route names its parent Gateway and a
 * binding names its policy; following one opens that object, and nothing here
 * fetches it to say whether it exists or whether it agreed. An unknown enum,
 * condition reason or filter type renders as itself.
 */

/**
 * The API group Gateway API's kinds live in.
 *
 * Every served version — v1, v1beta1, v1alpha2 — puts them here, which is why
 * only the group is matched.
 */
export const GATEWAY_GROUP = 'gateway.networking.k8s.io'

/** The API group Dynamic Resource Allocation's kinds live in. */
export const DEVICES_GROUP = 'resource.k8s.io'

/** The API group the admission policies and webhook configurations live in. */
export const ADMISSION_GROUP = 'admissionregistration.k8s.io'

/**
 * The Kind a Gateway API route's parent reference means when it names none.
 *
 * A documented CRD default rather than a guess: `parentRefs[].kind` defaults
 * to Gateway, and a reference relying on the default would otherwise render
 * with no kind and be unfollowable.
 */
export const PARENT_DEFAULT_KIND = 'Gateway'

/** The Kind a route's backend reference means when it names none — likewise a CRD default. */
export const BACKEND_DEFAULT_KIND = 'Service'

/** The panels this module can select. */
export type StandardPanel =
  | 'gateway-class'
  | 'gateway'
  | 'gateway-route'
  | 'resource-claim'
  | 'resource-claim-template'
  | 'device-class'
  | 'validating-admission-policy'
  | 'validating-admission-policy-binding'
  | 'mutating-admission-policy'
  | 'mutating-admission-policy-binding'

/**
 * Selects a panel from the opened object's API group and Kind, or null.
 *
 * Null is the ordinary answer for every other kind, and A GROUP ABSENT FROM
 * THE CLUSTER CHANGES NOTHING ANYWHERE: none of these kinds reach the
 * navigator unless discovery found the group, so on a cluster with no Gateway
 * API installed and no device or policy feature gate enabled nothing below is
 * ever reached.
 *
 * HTTPRoute and GRPCRoute share one panel because they share their shape:
 * parent references, hostnames, per-rule filters and backend references, and
 * the per-parent status are field for field the same, and only the inside of a
 * match differs. TLSRoute, TCPRoute and UDPRoute are deliberately NOT claimed —
 * their rules carry no matches and no filters at all, so this panel would
 * render two empty sections on them and the generic table already says as much.
 */
export function standardPanelFor(
  group: string | undefined,
  kind: string | undefined,
): StandardPanel | null {
  if (group === GATEWAY_GROUP) {
    if (kind === 'GatewayClass') return 'gateway-class'
    if (kind === 'Gateway') return 'gateway'
    if (kind === 'HTTPRoute' || kind === 'GRPCRoute') return 'gateway-route'
    return null
  }

  if (group === DEVICES_GROUP) {
    if (kind === 'ResourceClaim') return 'resource-claim'
    if (kind === 'ResourceClaimTemplate') return 'resource-claim-template'
    if (kind === 'DeviceClass') return 'device-class'
    return null
  }

  if (group === ADMISSION_GROUP) {
    if (kind === 'ValidatingAdmissionPolicy') return 'validating-admission-policy'
    if (kind === 'ValidatingAdmissionPolicyBinding') return 'validating-admission-policy-binding'
    if (kind === 'MutatingAdmissionPolicy') return 'mutating-admission-policy'
    if (kind === 'MutatingAdmissionPolicyBinding') return 'mutating-admission-policy-binding'
    return null
  }

  return null
}

/**
 * A reference to another object, as the referring object wrote it.
 *
 * The one shape all three families point at each other with — a route's
 * parent, a backend, a claim's consumer, a binding's policy. THE KIND IS
 * VERBATIM, because it is what the drawer resolves a click by, and nothing
 * here checks that the object exists: that would be a second GET, and a
 * reference to something deleted is a fact worth rendering rather than a row
 * to hide.
 */
export interface StandardRef {
  /** The API group, empty for the core group. */
  group: string
  /** The Kubernetes Kind. */
  kind: string
  /** Empty when the reference names none, which usually means the referrer's own. */
  namespace: string
  name: string
}

/**
 * One metav1 condition, with its type.
 *
 * `conditionOf` in `$lib/operators/panel.ts` answers a different question —
 * one NAMED condition out of a status, with the type dropped because the
 * caller already knew it. These APIs need the whole array with the types kept:
 * a Gateway listener and a route's parent status each carry their own
 * condition list, an implementation is free to add types of its own to it, and
 * a panel reading only the types it had heard of would silently drop the one
 * that explained the problem.
 */
export interface StandardCondition {
  type: string
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
 * Reads a whole `conditions` array, in the order the controller wrote it.
 *
 * An empty array for anything that is not an array at all, so an object
 * part-way through a version migration answers "nothing reported" rather than
 * throwing on the way to the panel.
 */
export function conditionsOf(conditions: unknown): StandardCondition[] {
  if (!Array.isArray(conditions)) return []

  return (conditions as RawCondition[])
    .filter((condition) => condition && typeof condition === 'object')
    .map((condition) => ({
      type: condition.type ?? '',
      status: condition.status ?? '',
      reason: condition.reason ?? '',
      message: condition.message ?? '',
      since: condition.lastTransitionTime ?? '',
    }))
}

/**
 * Picks one condition out of an already-parsed list, or null.
 *
 * Null means the controller has not written it, which is a different fact from
 * having written it False — a Gateway the controller has not looked at yet has
 * said nothing, and rendering that as a rejection reports a failure that has
 * not happened.
 */
export function namedCondition(
  conditions: StandardCondition[],
  type: string,
): StandardCondition | null {
  return conditions.find((condition) => condition.type === type) ?? null
}

/** A label selector as it arrives, before anything has defaulted it. */
interface RawSelector {
  matchLabels?: Record<string, string>
  matchExpressions?: { key?: string; operator?: string; values?: string[] }[]
}

/**
 * A label selector as one line per term, in the syntax `kubectl` prints.
 *
 * Both halves, because they are ANDed and showing only `matchLabels` would
 * describe a wider selector than the one in force. An empty array means the
 * selector is ABSENT; a selector that is present and empty matches
 * EVERYTHING, which is the opposite — so callers keep the two apart rather
 * than collapsing both into "no rows".
 */
export function selectorTerms(selector: unknown): string[] {
  if (!selector || typeof selector !== 'object') return []
  const { matchLabels = {}, matchExpressions = [] } = selector as RawSelector

  const terms = Object.entries(matchLabels).map(([key, value]) => `${key}=${value}`)
  for (const expression of matchExpressions) {
    if (!expression || typeof expression !== 'object') continue
    const key = expression.key ?? ''
    // The operator is quoted rather than translated: Exists and DoesNotExist
    // take no values, In and NotIn take a list, and an operator this has never
    // seen still reads as what it is.
    const operator = expression.operator ?? ''
    const values = (expression.values ?? []).join(', ')
    terms.push(values ? `${key} ${operator} (${values})` : `${key} ${operator}`.trim())
  }
  return terms
}

/**
 * A number a manifest may legitimately omit, kept apart from zero.
 *
 * Zero is a real weight and a real device count in these APIs, so `?? 0` would
 * turn "unset" into a value the object does not carry — and for a backend
 * weight the two mean opposite things, since an omitted weight is 1 and an
 * explicit 0 takes the backend out of the pool.
 */
export function numberOr(value: unknown): number | null {
  return typeof value === 'number' && Number.isFinite(value) ? value : null
}
