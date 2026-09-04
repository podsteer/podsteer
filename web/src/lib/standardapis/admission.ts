/**
 * What an admission policy checks, and what a binding puts it in force over.
 *
 * A policy and its binding are two halves of one decision and neither is
 * legible alone: the policy holds the CEL that decides, the binding holds the
 * scope it decides over and whether a failure denies, warns or is only
 * recorded. A binding with no `validationActions` enforces nothing at all, and
 * a policy with no binding is inert — both are ordinary states while a policy
 * is being rolled out, and the panel shows what each object says rather than
 * ruling on the pair.
 *
 * QUOTATION, NOT VERDICT, and here the temptation is specific and refused:
 * nothing below evaluates an expression, decides whether a policy would admit
 * an object, or resolves a binding's `policyName` to check the policy exists.
 * A CEL expression is shown as written. The one thing the API server itself
 * says about an expression — `status.typeChecking.expressionWarnings`, its own
 * type check of the CEL against the matched kinds — is carried through
 * verbatim, because that is the server's statement and not ours.
 *
 * BOTH FAMILIES SHARE THIS FILE because they share their shape. A mutating
 * policy has mutations where a validating one has validations, and everything
 * that scopes either — paramKind, matchConstraints, matchConditions, variables,
 * failurePolicy — is field for field identical, as are the two bindings.
 * Selection stays per Kind in `panel.ts`; only the reading is shared.
 *
 * Field names follow the admissionregistration.k8s.io types, whose v1alpha1,
 * v1beta1 and v1 all serve these fields identically — which is why, as
 * everywhere in this directory, the version is never examined.
 */

import {
  conditionsOf,
  numberOr,
  selectorTerms,
  type StandardCondition,
} from './panel'

/** The Kind a ValidatingAdmissionPolicyBinding's `policyName` refers to. */
export const VALIDATING_POLICY_KIND = 'ValidatingAdmissionPolicy'

/** The Kind a MutatingAdmissionPolicyBinding's `policyName` refers to. */
export const MUTATING_POLICY_KIND = 'MutatingAdmissionPolicy'

/** One rule of a match block — which requests the policy is asked about. */
export interface PolicyRule {
  /** CREATE, UPDATE, DELETE, CONNECT or "*", verbatim. */
  operations: string[]
  /** An empty group is the core group, and "*" is every group. */
  apiGroups: string[]
  apiVersions: string[]
  resources: string[]
  /** Cluster, Namespaced or "*"; empty when the rule names none. */
  scope: string
  /** `resourceNames`, when the rule narrows to named objects. */
  resourceNames: string[]
}

/** Everything that decides which requests reach a policy. */
export interface PolicyMatch {
  /** Exact or Equivalent; empty when unset, which the API reads as Equivalent. */
  matchPolicy: string
  rules: PolicyRule[]
  excludeRules: PolicyRule[]
  /**
   * The namespace selector's terms.
   *
   * An ABSENT selector matches every namespace and a PRESENT empty one does
   * too, so an empty list here says only that nothing narrows by namespace.
   */
  namespaceSelector: string[]
  objectSelector: string[]
}

/** One validation, as its author wrote it. */
export interface PolicyValidation {
  /** The CEL that decides. Shown as written; nothing here evaluates it. */
  expression: string
  /** The message on failure, when it is a fixed string. */
  message: string
  /** The message on failure, when it is itself CEL. */
  messageExpression: string
  /** The HTTP-ish reason the API server returns; empty means Invalid. */
  reason: string
}

/** One mutation, as its author wrote it. */
export interface PolicyMutation {
  /** ApplyConfiguration or JSONPatch — which decides where the expression lives. */
  patchType: string
  /** The CEL producing the patch, from whichever of the two fields carries it. */
  expression: string
}

/** A named CEL expression: a match condition, or a variable other expressions reuse. */
export interface PolicyExpression {
  name: string
  expression: string
}

/** What the API server's own type check said about an expression. */
export interface TypeCheckWarning {
  /** Which field of the policy the warning is about. */
  fieldRef: string
  warning: string
}

export interface ValidatingPolicyView {
  /** Fail or Ignore; empty when unset, which the API reads as Fail. */
  failurePolicy: string
  /** The kind of object `params` refers to in the CEL, when the policy takes one. */
  paramKind: { apiVersion: string; kind: string } | null
  match: PolicyMatch
  matchConditions: PolicyExpression[]
  variables: PolicyExpression[]
  validations: PolicyValidation[]
  auditAnnotations: { key: string; valueExpression: string }[]
  /** `status.observedGeneration`; null when the API server has not written one. */
  observedGeneration: number | null
  typeCheckWarnings: TypeCheckWarning[]
  conditions: StandardCondition[]
}

export interface MutatingPolicyView {
  failurePolicy: string
  /** IfNeeded or Never; empty when unset, which the API reads as Never. */
  reinvocationPolicy: string
  paramKind: { apiVersion: string; kind: string } | null
  match: PolicyMatch
  matchConditions: PolicyExpression[]
  variables: PolicyExpression[]
  mutations: PolicyMutation[]
}

export interface PolicyBindingView {
  /** The policy this binding puts in force. Followable; never resolved here. */
  policyName: string
  /** The object supplying `params`, when the binding names one. */
  paramRef: {
    name: string
    namespace: string
    selector: string[]
    /** Deny or Allow; empty when unset, which the API reads as Deny. */
    notFoundAction: string
  } | null
  match: PolicyMatch
  /**
   * Deny, Warn and Audit, verbatim and in the order written.
   *
   * EMPTY IS MEANINGFUL AND IS NOT A DEFAULT: a validating binding with no
   * actions enforces nothing, and the panel says so rather than leaving a
   * blank row that reads as "not configured". A mutating binding has no such
   * field at all, and its empty list means only that.
   */
  validationActions: string[]
}

interface RawRule {
  operations?: string[]
  apiGroups?: string[]
  apiVersions?: string[]
  resources?: string[]
  scope?: string
  resourceNames?: string[]
}

interface RawMatch {
  matchPolicy?: string
  resourceRules?: RawRule[]
  excludeResourceRules?: RawRule[]
  namespaceSelector?: unknown
  objectSelector?: unknown
}

interface RawParamKind {
  apiVersion?: string
  kind?: string
}

interface RawExpression {
  name?: string
  expression?: string
}

interface ValidatingPolicyManifest {
  spec?: {
    failurePolicy?: string
    paramKind?: RawParamKind
    matchConstraints?: RawMatch
    matchConditions?: RawExpression[]
    variables?: RawExpression[]
    validations?: {
      expression?: string
      message?: string
      messageExpression?: string
      reason?: string
    }[]
    auditAnnotations?: { key?: string; valueExpression?: string }[]
  }
  status?: {
    observedGeneration?: number
    typeChecking?: { expressionWarnings?: { fieldRef?: string; warning?: string }[] }
    conditions?: unknown
  }
}

interface MutatingPolicyManifest {
  spec?: {
    failurePolicy?: string
    reinvocationPolicy?: string
    paramKind?: RawParamKind
    matchConstraints?: RawMatch
    matchConditions?: RawExpression[]
    variables?: RawExpression[]
    mutations?: {
      patchType?: string
      applyConfiguration?: { expression?: string }
      jsonPatch?: { expression?: string }
    }[]
  }
}

interface PolicyBindingManifest {
  spec?: {
    policyName?: string
    paramRef?: {
      name?: string
      namespace?: string
      selector?: unknown
      parameterNotFoundAction?: string
    }
    matchResources?: RawMatch
    validationActions?: string[]
  }
}

/**
 * Reads a ValidatingAdmissionPolicy, or null when there is no manifest at all.
 *
 * A policy nothing has bound comes back exactly like one that is enforced
 * everywhere: the object itself does not know. Which bindings point at it is a
 * LIST of another kind, and the panel does not make one — see the header.
 */
export function validatingAdmissionPolicy(manifest: unknown): ValidatingPolicyView | null {
  if (!manifest || typeof manifest !== 'object') return null
  const { spec = {}, status = {} } = manifest as ValidatingPolicyManifest

  return {
    failurePolicy: spec.failurePolicy ?? '',
    paramKind: paramKindOf(spec.paramKind),
    match: matchOf(spec.matchConstraints),
    matchConditions: expressionsOf(spec.matchConditions),
    variables: expressionsOf(spec.variables),
    validations: (spec.validations ?? [])
      .filter((validation) => validation && typeof validation === 'object')
      .map((validation) => ({
        expression: validation.expression ?? '',
        message: validation.message ?? '',
        messageExpression: validation.messageExpression ?? '',
        reason: validation.reason ?? '',
      })),
    auditAnnotations: (spec.auditAnnotations ?? [])
      .filter((annotation) => annotation && typeof annotation === 'object')
      .map((annotation) => ({
        key: annotation.key ?? '',
        valueExpression: annotation.valueExpression ?? '',
      })),
    observedGeneration: numberOr(status.observedGeneration),
    typeCheckWarnings: (status.typeChecking?.expressionWarnings ?? [])
      .filter((warning) => warning && typeof warning === 'object')
      .map((warning) => ({ fieldRef: warning.fieldRef ?? '', warning: warning.warning ?? '' })),
    conditions: conditionsOf(status.conditions),
  }
}

/**
 * Reads a MutatingAdmissionPolicy, or null when there is no manifest at all.
 *
 * A mutating policy carries NO STATUS — the API server writes none, so unlike
 * its validating sibling there is no type check to quote and no conditions to
 * render. That absence is the object's shape and not a missing read.
 */
export function mutatingAdmissionPolicy(manifest: unknown): MutatingPolicyView | null {
  if (!manifest || typeof manifest !== 'object') return null
  const { spec = {} } = manifest as MutatingPolicyManifest

  return {
    failurePolicy: spec.failurePolicy ?? '',
    reinvocationPolicy: spec.reinvocationPolicy ?? '',
    paramKind: paramKindOf(spec.paramKind),
    match: matchOf(spec.matchConstraints),
    matchConditions: expressionsOf(spec.matchConditions),
    variables: expressionsOf(spec.variables),
    mutations: (spec.mutations ?? [])
      .filter((mutation) => mutation && typeof mutation === 'object')
      .map((mutation) => ({
        patchType: mutation.patchType ?? '',
        // The patch type says which field holds the expression, but the
        // expression is taken from whichever one is actually present: an
        // object whose patchType and body disagree still has one expression,
        // and showing it is more use than showing an empty row.
        expression:
          mutation.applyConfiguration?.expression ?? mutation.jsonPatch?.expression ?? '',
      })),
  }
}

/**
 * Reads either binding, or null when there is no manifest at all.
 *
 * ONE PARSER FOR BOTH: a validating and a mutating binding differ only in
 * `validationActions`, which the mutating one does not have and which comes
 * back empty for it. The KIND the `policyName` refers to is not decided here —
 * the panel knows which it is from its own selection, and guessing it from the
 * fields present would be a coin toss on a binding that names no actions.
 */
export function admissionPolicyBinding(manifest: unknown): PolicyBindingView | null {
  if (!manifest || typeof manifest !== 'object') return null
  const { spec = {} } = manifest as PolicyBindingManifest
  const param = spec.paramRef

  return {
    policyName: spec.policyName ?? '',
    paramRef: param
      ? {
          name: param.name ?? '',
          namespace: param.namespace ?? '',
          selector: selectorTerms(param.selector),
          notFoundAction: param.parameterNotFoundAction ?? '',
        }
      : null,
    match: matchOf(spec.matchResources),
    validationActions: spec.validationActions ?? [],
  }
}

function paramKindOf(paramKind: RawParamKind | undefined): { apiVersion: string; kind: string } | null {
  if (!paramKind) return null
  return { apiVersion: paramKind.apiVersion ?? '', kind: paramKind.kind ?? '' }
}

/**
 * A match block, from `matchConstraints` on a policy or `matchResources` on a
 * binding — the same shape under two names.
 */
function matchOf(match: RawMatch | undefined): PolicyMatch {
  return {
    matchPolicy: match?.matchPolicy ?? '',
    rules: rulesOf(match?.resourceRules),
    excludeRules: rulesOf(match?.excludeResourceRules),
    namespaceSelector: selectorTerms(match?.namespaceSelector),
    objectSelector: selectorTerms(match?.objectSelector),
  }
}

function rulesOf(rules: RawRule[] | undefined): PolicyRule[] {
  return (rules ?? [])
    .filter((rule) => rule && typeof rule === 'object')
    .map((rule) => ({
      operations: rule.operations ?? [],
      apiGroups: rule.apiGroups ?? [],
      apiVersions: rule.apiVersions ?? [],
      resources: rule.resources ?? [],
      scope: rule.scope ?? '',
      resourceNames: rule.resourceNames ?? [],
    }))
}

function expressionsOf(expressions: RawExpression[] | undefined): PolicyExpression[] {
  return (expressions ?? [])
    .filter((expression) => expression && typeof expression === 'object')
    .map((expression) => ({ name: expression.name ?? '', expression: expression.expression ?? '' }))
}
