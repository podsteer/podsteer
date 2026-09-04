import { describe, expect, it } from 'vitest'
import {
  countedSubjects,
  describeDecision,
  distinctSubjects,
  pathRows,
  reviewState,
  subjectLabel,
  verbRows,
} from './rbac'
import type { AccessDecision, PolicyRule, RBACSubject, RoleBindingRef } from './api/client'

/** Builds a rule with only the fields a case needs. */
function rule(fields: Partial<PolicyRule>): PolicyRule {
  return {
    verbs: [],
    apiGroups: [],
    resources: [],
    resourceNames: [],
    nonResourceUrls: [],
    ...fields,
  } as PolicyRule
}

/** Builds a decision with only the fields a case needs. */
function decision(fields: Partial<AccessDecision>): AccessDecision {
  return {
    status: 'answered',
    refusal: '',
    allowed: false,
    denied: false,
    reason: '',
    evaluationError: '',
    ...fields,
  } as AccessDecision
}

/** Builds a subject. */
function subject(kind: string, name: string, namespace = ''): RBACSubject {
  return { kind, name, namespace } as RBACSubject
}

/** Narrows a state that must be unavailable, so its message can be read. */
function unavailableState(status: string, refusal: string) {
  const state = reviewState(status, refusal)
  if (state.kind !== 'unavailable') {
    throw new Error(`expected an unavailable state for ${status}, got ${state.kind}`)
  }
  return state
}

describe('reviewState', () => {
  it('lets an answered pane render its own rows', () => {
    expect(reviewState('answered', '')).toEqual({ kind: 'answered' })
  })

  it('carries the backend sentence, which names the permission that would fix it', () => {
    const state = reviewState('forbidden', 'Your account may not ask this. It needs `create` on x.')

    expect(state).toEqual({
      kind: 'unavailable',
      status: 'forbidden',
      tone: 'info',
      message: 'Your account may not ask this. It needs `create` on x.',
    })
  })

  it('draws a refusal as information and a failure as an error', () => {
    // A refusal is not a fault — plenty of healthy accounts cannot ask — so
    // only the transient case is worth showing as something to retry.
    expect(reviewState('forbidden', 'x')).toMatchObject({ tone: 'info' })
    expect(reviewState('absent', 'x')).toMatchObject({ tone: 'info' })
    expect(reviewState('failed', 'x')).toMatchObject({ tone: 'error' })
  })

  it('falls back to a sentence rather than rendering a blank pane', () => {
    expect(unavailableState('forbidden', '').message).toContain('may not ask')
    expect(unavailableState('absent', '').message).not.toBe('')
    expect(unavailableState('failed', '').message).toContain('try again')
  })

  it('treats an unrecognised status as a failure rather than as an answer', () => {
    // The dangerous direction is the other one: reading something unknown as
    // "answered" would render an empty table as a complete set of permissions.
    expect(reviewState('something-new', 'x')).toMatchObject({ status: 'failed', tone: 'error' })
  })
})

describe('verbRows', () => {
  it('gathers the same resource onto one row', () => {
    const rows = verbRows([
      rule({ verbs: ['get'], apiGroups: ['apps'], resources: ['deployments'] }),
      rule({ verbs: ['list', 'watch'], apiGroups: ['apps'], resources: ['deployments'] }),
    ])

    expect(rows).toEqual([
      { group: 'apps', resource: 'deployments', verbs: ['get', 'list', 'watch'], resourceNames: [] },
    ])
  })

  it('expands the cross product a rule actually means', () => {
    const rows = verbRows([
      rule({ verbs: ['get'], apiGroups: ['apps', 'batch'], resources: ['deployments', 'jobs'] }),
    ])

    expect(rows.map((row) => `${row.group}/${row.resource}`)).toEqual([
      'apps/deployments',
      'apps/jobs',
      'batch/deployments',
      'batch/jobs',
    ])
  })

  it('reads a rule with no apiGroups as the core group', () => {
    const rows = verbRows([rule({ verbs: ['get'], resources: ['pods'] })])

    expect(rows).toEqual([{ group: '', resource: 'pods', verbs: ['get'], resourceNames: [] }])
  })

  it('keeps a rule narrowed to named objects on its own row', () => {
    // Folding these together would claim `delete` on every ConfigMap in the
    // namespace, which is the one direction this table must not be wrong in.
    const rows = verbRows([
      rule({ verbs: ['get'], resources: ['configmaps'] }),
      rule({ verbs: ['delete'], resources: ['configmaps'], resourceNames: ['app-config'] }),
    ])

    expect(rows).toHaveLength(2)
    expect(rows.find((row) => row.resourceNames.length === 0)?.verbs).toEqual(['get'])
    expect(rows.find((row) => row.resourceNames.length === 1)?.verbs).toEqual(['delete'])
  })

  it('leads a row with the wildcard verb', () => {
    const rows = verbRows([rule({ verbs: ['watch', '*', 'get'], resources: ['pods'] })])

    expect(rows[0].verbs).toEqual(['*', 'get', 'watch'])
  })

  it('removes duplicate verbs granted by two roles', () => {
    const rows = verbRows([
      rule({ verbs: ['get', 'list'], resources: ['pods'] }),
      rule({ verbs: ['get'], resources: ['pods'] }),
    ])

    expect(rows[0].verbs).toEqual(['get', 'list'])
  })

  it('keeps a subresource distinct from its parent', () => {
    // `pods/log` is a different thing to grant than `pods`, and a table that
    // merged them would report reading logs as reading pods.
    const rows = verbRows([
      rule({ verbs: ['get'], resources: ['pods'] }),
      rule({ verbs: ['get'], resources: ['pods/log'] }),
    ])

    expect(rows.map((row) => row.resource)).toEqual(['pods', 'pods/log'])
  })

  it('is empty for no rules', () => {
    expect(verbRows([])).toEqual([])
  })
})

describe('pathRows', () => {
  it('gathers verbs per path and sorts them', () => {
    const rows = pathRows([
      rule({ verbs: ['get'], nonResourceUrls: ['/healthz', '/version'] }),
      rule({ verbs: ['head'], nonResourceUrls: ['/healthz'] }),
    ])

    expect(rows).toEqual([
      { path: '/healthz', verbs: ['get', 'head'] },
      { path: '/version', verbs: ['get'] },
    ])
  })

  it('ignores a resource rule, which has no path', () => {
    expect(pathRows([rule({ verbs: ['get'], resources: ['pods'] })])).toEqual([])
  })
})

describe('describeDecision', () => {
  it('reads an allowed answer with its reason verbatim', () => {
    const summary = describeDecision(
      decision({ allowed: true, reason: 'RBAC: allowed by ClusterRoleBinding "view"' }),
    )

    expect(summary.tone).toBe('allowed')
    expect(summary.reason).toBe('RBAC: allowed by ClusterRoleBinding "view"')
  })

  it('reads an explicit denial as denied', () => {
    expect(describeDecision(decision({ denied: true })).tone).toBe('denied')
  })

  it('does not report "no opinion" as a denial', () => {
    // Both flags false is the API's third answer: nothing allowed it and
    // nothing denied it. Calling that "Denied" puts words in the cluster's
    // mouth about a verdict it never gave.
    const summary = describeDecision(decision({ reason: 'no RBAC policy matched' }))

    expect(summary.tone).toBe('unknown')
    expect(summary.label).toContain('not explicitly denied')
    expect(summary.reason).toBe('no RBAC policy matched')
  })

  it('carries an evaluation error, which says the answer may be incomplete', () => {
    expect(describeDecision(decision({ allowed: true, evaluationError: 'webhook timed out' }))
      .evaluationError).toBe('webhook timed out')
  })
})

describe('subjectLabel', () => {
  it('qualifies a ServiceAccount with its namespace', () => {
    expect(subjectLabel(subject('ServiceAccount', 'reader', 'shop'))).toBe('shop/reader')
  })

  it('leaves a User and a Group unqualified', () => {
    expect(subjectLabel(subject('User', 'ada'))).toBe('ada')
    expect(subjectLabel(subject('Group', 'system:masters'))).toBe('system:masters')
  })
})

describe('countedSubjects', () => {
  it('counts one and many', () => {
    expect(countedSubjects(1)).toBe('1 subject')
    expect(countedSubjects(3)).toBe('3 subjects')
  })

  it('states an empty binding rather than hiding it', () => {
    expect(countedSubjects(0)).toBe('0 subjects')
  })
})

describe('distinctSubjects', () => {
  it('counts an account bound twice once', () => {
    const bindings: RoleBindingRef[] = [
      { subjects: [subject('Group', 'sre')] } as RoleBindingRef,
      { subjects: [subject('Group', 'sre'), subject('User', 'ada')] } as RoleBindingRef,
    ]

    expect(distinctSubjects(bindings).map((entry) => entry.name)).toEqual(['sre', 'ada'])
  })

  it('keeps two service accounts of the same name in different namespaces apart', () => {
    const bindings: RoleBindingRef[] = [
      {
        subjects: [
          subject('ServiceAccount', 'reader', 'shop'),
          subject('ServiceAccount', 'reader', 'billing'),
        ],
      } as RoleBindingRef,
    ]

    expect(distinctSubjects(bindings)).toHaveLength(2)
  })

  it('is empty for a binding that names nobody', () => {
    expect(distinctSubjects([{ subjects: [] } as unknown as RoleBindingRef])).toEqual([])
  })
})
