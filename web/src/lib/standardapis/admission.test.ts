import { describe, expect, it } from 'vitest'

import {
  admissionPolicyBinding,
  mutatingAdmissionPolicy,
  validatingAdmissionPolicy,
} from './admission'

/** A policy the API server type-checked cleanly — the ordinary case. */
const healthyPolicy = {
  apiVersion: 'admissionregistration.k8s.io/v1',
  kind: 'ValidatingAdmissionPolicy',
  metadata: { name: 'replica-limit' },
  spec: {
    failurePolicy: 'Fail',
    paramKind: { apiVersion: 'example.net/v1', kind: 'ReplicaLimit' },
    matchConstraints: {
      matchPolicy: 'Equivalent',
      resourceRules: [
        {
          operations: ['CREATE', 'UPDATE'],
          apiGroups: ['apps'],
          apiVersions: ['v1'],
          resources: ['deployments'],
          scope: 'Namespaced',
        },
      ],
      excludeResourceRules: [
        {
          operations: ['UPDATE'],
          apiGroups: ['apps'],
          apiVersions: ['v1'],
          resources: ['deployments'],
          resourceNames: ['kube-system-exempt'],
        },
      ],
      namespaceSelector: { matchLabels: { 'policy/enforced': 'true' } },
    },
    matchConditions: [
      { name: 'exclude-system', expression: 'object.metadata.namespace != "kube-system"' },
    ],
    variables: [{ name: 'replicas', expression: 'object.spec.replicas' }],
    validations: [
      {
        expression: 'variables.replicas <= params.maxReplicas',
        message: 'too many replicas',
        reason: 'Invalid',
      },
      {
        expression: 'has(object.spec.template.spec.securityContext)',
        messageExpression: '"pod template needs a security context in " + object.metadata.name',
      },
    ],
    auditAnnotations: [
      { key: 'replica-count', valueExpression: 'string(variables.replicas)' },
    ],
  },
  status: {
    observedGeneration: 3,
    conditions: [],
  },
}

/** A policy the API server's own type check complained about. */
const rejectedPolicy = {
  apiVersion: 'admissionregistration.k8s.io/v1',
  kind: 'ValidatingAdmissionPolicy',
  metadata: { name: 'broken' },
  spec: {
    // No failurePolicy: the API reads that as Fail, which is the difference
    // between a broken policy blocking every write and being ignored.
    matchConstraints: {
      resourceRules: [
        {
          operations: ['*'],
          // The core group, which the API writes as the empty string.
          apiGroups: [''],
          apiVersions: ['v1'],
          resources: ['pods'],
        },
      ],
    },
    validations: [{ expression: 'object.spec.notAField == 1' }],
  },
  status: {
    observedGeneration: 1,
    typeChecking: {
      expressionWarnings: [
        {
          fieldRef: 'spec.validations[0].expression',
          warning: 'apps/v1, Kind=Deployment: undefined field "notAField"',
        },
      ],
    },
    conditions: [
      {
        type: 'TypeChecked',
        status: 'False',
        reason: 'TypeCheckingFailed',
        lastTransitionTime: '2026-09-04T08:00:00Z',
      },
    ],
  },
}

const enforcingBinding = {
  apiVersion: 'admissionregistration.k8s.io/v1',
  kind: 'ValidatingAdmissionPolicyBinding',
  metadata: { name: 'replica-limit-binding' },
  spec: {
    policyName: 'replica-limit',
    paramRef: {
      name: 'default-limit',
      namespace: 'policy',
      parameterNotFoundAction: 'Deny',
    },
    matchResources: {
      namespaceSelector: { matchExpressions: [{ key: 'env', operator: 'In', values: ['prod'] }] },
    },
    validationActions: ['Deny', 'Audit'],
  },
}

const mutatingPolicy = {
  apiVersion: 'admissionregistration.k8s.io/v1alpha1',
  kind: 'MutatingAdmissionPolicy',
  metadata: { name: 'add-sidecar-label' },
  spec: {
    failurePolicy: 'Ignore',
    reinvocationPolicy: 'IfNeeded',
    matchConstraints: {
      resourceRules: [
        { operations: ['CREATE'], apiGroups: [''], apiVersions: ['v1'], resources: ['pods'] },
      ],
    },
    variables: [{ name: 'team', expression: 'object.metadata.labels["team"]' }],
    mutations: [
      {
        patchType: 'ApplyConfiguration',
        applyConfiguration: {
          expression: 'Object{ metadata: Object.metadata{ labels: {"injected": "true"} } }',
        },
      },
      {
        patchType: 'JSONPatch',
        jsonPatch: { expression: '[JSONPatch{op: "add", path: "/metadata/labels/x", value: "y"}]' },
      },
    ],
  },
}

describe('reading a ValidatingAdmissionPolicy', () => {
  it('quotes the failure policy, the parameter kind and the validations', () => {
    const view = validatingAdmissionPolicy(healthyPolicy)

    expect(view?.failurePolicy).toBe('Fail')
    expect(view?.paramKind).toEqual({ apiVersion: 'example.net/v1', kind: 'ReplicaLimit' })
    expect(view?.validations).toEqual([
      {
        expression: 'variables.replicas <= params.maxReplicas',
        message: 'too many replicas',
        messageExpression: '',
        reason: 'Invalid',
      },
      {
        expression: 'has(object.spec.template.spec.securityContext)',
        message: '',
        messageExpression: '"pod template needs a security context in " + object.metadata.name',
        reason: '',
      },
    ])
  })

  it('reads the match constraints, including what they exclude', () => {
    const match = validatingAdmissionPolicy(healthyPolicy)?.match

    expect(match?.matchPolicy).toBe('Equivalent')
    expect(match?.rules[0]).toEqual({
      operations: ['CREATE', 'UPDATE'],
      apiGroups: ['apps'],
      apiVersions: ['v1'],
      resources: ['deployments'],
      scope: 'Namespaced',
      resourceNames: [],
    })
    expect(match?.excludeRules[0].resourceNames).toEqual(['kube-system-exempt'])
    expect(match?.namespaceSelector).toEqual(['policy/enforced=true'])
    expect(match?.objectSelector).toEqual([])
  })

  it('carries the match conditions and the variables other expressions reuse', () => {
    const view = validatingAdmissionPolicy(healthyPolicy)

    expect(view?.matchConditions).toEqual([
      { name: 'exclude-system', expression: 'object.metadata.namespace != "kube-system"' },
    ])
    expect(view?.variables).toEqual([{ name: 'replicas', expression: 'object.spec.replicas' }])
    expect(view?.auditAnnotations).toEqual([
      { key: 'replica-count', valueExpression: 'string(variables.replicas)' },
    ])
  })

  it('carries the API server’s own type-check warning verbatim', () => {
    // The server type-checks the CEL against the kinds the policy matches and
    // writes what it found. That is the server's statement; nothing here
    // evaluates an expression or decides whether the policy is right.
    const view = validatingAdmissionPolicy(rejectedPolicy)

    expect(view?.typeCheckWarnings).toEqual([
      {
        fieldRef: 'spec.validations[0].expression',
        warning: 'apps/v1, Kind=Deployment: undefined field "notAField"',
      },
    ])
    expect(view?.conditions[0]).toMatchObject({
      type: 'TypeChecked',
      status: 'False',
      reason: 'TypeCheckingFailed',
    })
  })

  it('leaves an unset failure policy empty rather than defaulting it here', () => {
    // The panel spells out that unset means Fail; the parser quotes what the
    // object carries, which is nothing.
    expect(validatingAdmissionPolicy(rejectedPolicy)?.failurePolicy).toBe('')
    expect(validatingAdmissionPolicy(rejectedPolicy)?.paramKind).toBeNull()
  })

  it('keeps the core group as the empty string the API writes', () => {
    expect(validatingAdmissionPolicy(rejectedPolicy)?.match.rules[0].apiGroups).toEqual([''])
  })

  it('keeps an unknown reason as itself', () => {
    const view = validatingAdmissionPolicy({
      spec: { validations: [{ expression: 'true', reason: 'SomethingNewIn134' }] },
    })

    expect(view?.validations[0].reason).toBe('SomethingNewIn134')
  })

  it('reads a policy with no status at all', () => {
    const view = validatingAdmissionPolicy({ spec: { validations: [{ expression: 'true' }] } })

    expect(view?.observedGeneration).toBeNull()
    expect(view?.typeCheckWarnings).toEqual([])
    expect(view?.conditions).toEqual([])
    expect(view?.match.rules).toEqual([])
  })

  it('answers null only when there is no manifest at all', () => {
    expect(validatingAdmissionPolicy(null)).toBeNull()
    expect(validatingAdmissionPolicy('not an object')).toBeNull()
    expect(validatingAdmissionPolicy({})).not.toBeNull()
  })
})

describe('reading a MutatingAdmissionPolicy', () => {
  it('quotes both policies and the mutations, whichever field holds the expression', () => {
    const view = mutatingAdmissionPolicy(mutatingPolicy)

    expect(view?.failurePolicy).toBe('Ignore')
    expect(view?.reinvocationPolicy).toBe('IfNeeded')
    expect(view?.mutations).toEqual([
      {
        patchType: 'ApplyConfiguration',
        expression: 'Object{ metadata: Object.metadata{ labels: {"injected": "true"} } }',
      },
      {
        patchType: 'JSONPatch',
        expression: '[JSONPatch{op: "add", path: "/metadata/labels/x", value: "y"}]',
      },
    ])
    expect(view?.variables).toEqual([{ name: 'team', expression: 'object.metadata.labels["team"]' }])
  })

  it('reads its match constraints through the same reader as the validating one', () => {
    expect(mutatingAdmissionPolicy(mutatingPolicy)?.match.rules[0]).toMatchObject({
      operations: ['CREATE'],
      resources: ['pods'],
    })
  })

  it('keeps an unknown patch type as itself', () => {
    const view = mutatingAdmissionPolicy({
      spec: { mutations: [{ patchType: 'SomethingNew', jsonPatch: { expression: '[]' } }] },
    })

    expect(view?.mutations[0]).toEqual({ patchType: 'SomethingNew', expression: '[]' })
  })

  it('reads a policy with no mutations, which has no status either', () => {
    // A mutating policy carries no status at all — the API server writes none.
    const view = mutatingAdmissionPolicy({ spec: { failurePolicy: 'Fail' } })

    expect(view?.mutations).toEqual([])
    expect(view?.reinvocationPolicy).toBe('')
  })

  it('answers null only when there is no manifest at all', () => {
    expect(mutatingAdmissionPolicy(undefined)).toBeNull()
    expect(mutatingAdmissionPolicy({})).not.toBeNull()
  })
})

describe('reading a policy binding', () => {
  it('quotes the policy it names, its parameters and its validation actions', () => {
    const view = admissionPolicyBinding(enforcingBinding)

    expect(view?.policyName).toBe('replica-limit')
    expect(view?.paramRef).toEqual({
      name: 'default-limit',
      namespace: 'policy',
      selector: [],
      notFoundAction: 'Deny',
    })
    expect(view?.validationActions).toEqual(['Deny', 'Audit'])
    expect(view?.match.namespaceSelector).toEqual(['env In (prod)'])
  })

  it('reads a binding that enforces nothing as the empty list it is', () => {
    // A binding with no actions is inert, and that is a fact about the object
    // rather than a default to fill in.
    const view = admissionPolicyBinding({ spec: { policyName: 'replica-limit' } })

    expect(view?.validationActions).toEqual([])
    expect(view?.paramRef).toBeNull()
    expect(view?.match.rules).toEqual([])
  })

  it('reads a mutating binding, which has no validation actions at all', () => {
    const view = admissionPolicyBinding({
      apiVersion: 'admissionregistration.k8s.io/v1alpha1',
      kind: 'MutatingAdmissionPolicyBinding',
      spec: {
        policyName: 'add-sidecar-label',
        matchResources: { objectSelector: { matchLabels: { inject: 'yes' } } },
      },
    })

    expect(view?.policyName).toBe('add-sidecar-label')
    expect(view?.validationActions).toEqual([])
    expect(view?.match.objectSelector).toEqual(['inject=yes'])
  })

  it('keeps a parameter selector and an unknown not-found action', () => {
    const view = admissionPolicyBinding({
      spec: {
        policyName: 'p',
        paramRef: {
          selector: { matchLabels: { tier: 'prod' } },
          parameterNotFoundAction: 'SomethingNew',
        },
      },
    })

    expect(view?.paramRef?.selector).toEqual(['tier=prod'])
    expect(view?.paramRef?.notFoundAction).toBe('SomethingNew')
    expect(view?.paramRef?.name).toBe('')
  })

  it('answers null only when there is no manifest at all', () => {
    expect(admissionPolicyBinding(null)).toBeNull()
    expect(admissionPolicyBinding(7)).toBeNull()
    expect(admissionPolicyBinding({})).not.toBeNull()
  })
})
