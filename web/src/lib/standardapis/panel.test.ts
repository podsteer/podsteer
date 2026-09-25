import { describe, expect, it } from 'vitest'

import { gitOpsPanelFor } from '$lib/gitops/panel'
import { operatorPanelFor } from '$lib/operators/panel'

import {
  ADMISSION_GROUP,
  DEVICES_GROUP,
  GATEWAY_GROUP,
  conditionsOf,
  namedCondition,
  numberOr,
  selectorTerms,
  standardPanelFor,
} from './panel'

describe('selecting a standard API panel', () => {
  it('selects a panel only when the group and the kind agree', () => {
    expect(standardPanelFor(GATEWAY_GROUP, 'GatewayClass')).toBe('gateway-class')
    expect(standardPanelFor(GATEWAY_GROUP, 'Gateway')).toBe('gateway')
    expect(standardPanelFor(GATEWAY_GROUP, 'HTTPRoute')).toBe('gateway-route')
    expect(standardPanelFor(GATEWAY_GROUP, 'GRPCRoute')).toBe('gateway-route')
    expect(standardPanelFor(DEVICES_GROUP, 'ResourceClaim')).toBe('resource-claim')
    expect(standardPanelFor(DEVICES_GROUP, 'ResourceClaimTemplate')).toBe('resource-claim-template')
    expect(standardPanelFor(DEVICES_GROUP, 'DeviceClass')).toBe('device-class')
    expect(standardPanelFor(ADMISSION_GROUP, 'ValidatingAdmissionPolicy')).toBe(
      'validating-admission-policy',
    )
    expect(standardPanelFor(ADMISSION_GROUP, 'ValidatingAdmissionPolicyBinding')).toBe(
      'validating-admission-policy-binding',
    )
    expect(standardPanelFor(ADMISSION_GROUP, 'MutatingAdmissionPolicy')).toBe(
      'mutating-admission-policy',
    )
    expect(standardPanelFor(ADMISSION_GROUP, 'MutatingAdmissionPolicyBinding')).toBe(
      'mutating-admission-policy-binding',
    )
  })

  it('refuses another mesh’s Gateway, which is a different object entirely', () => {
    // "Gateway" is a kind in networking.istio.io and in gloo.solo.io with a
    // spec that shares neither listeners nor gatewayClassName. A Kind alone
    // would open this panel on one and render a column of empty rows.
    expect(standardPanelFor('networking.istio.io', 'Gateway')).toBeNull()
    expect(standardPanelFor('gloo.solo.io', 'Gateway')).toBeNull()
    expect(standardPanelFor('example.com', 'HTTPRoute')).toBeNull()
  })

  it('claims no kind another family already owns', () => {
    // Each of the three selectors is exhaustive over only its own kinds, which
    // is what lets them sit side by side in one drawer.
    expect(standardPanelFor('argoproj.io', 'Application')).toBeNull()
    expect(standardPanelFor('cert-manager.io', 'Certificate')).toBeNull()
    expect(gitOpsPanelFor(GATEWAY_GROUP, 'Gateway')).toBeNull()
    expect(operatorPanelFor(GATEWAY_GROUP, 'Gateway')).toBeNull()
  })

  it('answers null for a sibling kind of a group it does claim', () => {
    // The alpha routes carry no matches and no filters, so the route panel
    // would render two empty sections on them; ResourceSlice and the webhook
    // configurations are simply not modelled. All stay on the generic table.
    expect(standardPanelFor(GATEWAY_GROUP, 'TLSRoute')).toBeNull()
    expect(standardPanelFor(GATEWAY_GROUP, 'ReferenceGrant')).toBeNull()
    expect(standardPanelFor(DEVICES_GROUP, 'ResourceSlice')).toBeNull()
    expect(standardPanelFor(ADMISSION_GROUP, 'ValidatingWebhookConfiguration')).toBeNull()
  })

  it('answers null for an ordinary kind and for an absent coordinate', () => {
    expect(standardPanelFor('apps', 'Deployment')).toBeNull()
    expect(standardPanelFor('', 'Secret')).toBeNull()
    expect(standardPanelFor(undefined, 'Gateway')).toBeNull()
    expect(standardPanelFor(GATEWAY_GROUP, undefined)).toBeNull()
  })
})

describe('reading a condition list', () => {
  const raw = [
    {
      type: 'Accepted',
      status: 'True',
      reason: 'Accepted',
      message: 'the route was accepted',
      lastTransitionTime: '2026-09-04T09:00:00Z',
    },
    { type: 'ResolvedRefs', status: 'False', reason: 'BackendNotFound' },
  ]

  it('quotes every condition in the order the controller wrote it', () => {
    expect(conditionsOf(raw)).toEqual([
      {
        type: 'Accepted',
        status: 'True',
        reason: 'Accepted',
        message: 'the route was accepted',
        since: '2026-09-04T09:00:00Z',
      },
      {
        type: 'ResolvedRefs',
        status: 'False',
        reason: 'BackendNotFound',
        message: '',
        since: '',
      },
    ])
  })

  it('keeps a condition type it has never heard of', () => {
    // An implementation is free to add its own types, and a panel that read
    // only the ones it knew would drop the one explaining the problem.
    const own = conditionsOf([{ type: 'example.com/QuotaExceeded', status: 'True' }])
    expect(own[0].type).toBe('example.com/QuotaExceeded')
  })

  it('answers an empty list for a status that is not an array', () => {
    expect(conditionsOf(undefined)).toEqual([])
    expect(conditionsOf('not an array')).toEqual([])
    expect(conditionsOf({ Accepted: true })).toEqual([])
  })

  it('separates a condition not written from one written False', () => {
    const conditions = conditionsOf(raw)
    expect(namedCondition(conditions, 'ResolvedRefs')?.status).toBe('False')
    expect(namedCondition(conditions, 'Programmed')).toBeNull()
    expect(namedCondition([], 'Accepted')).toBeNull()
  })
})

describe('reading a label selector', () => {
  it('renders both halves, which are ANDed', () => {
    expect(
      selectorTerms({
        matchLabels: { tier: 'prod' },
        matchExpressions: [{ key: 'team', operator: 'In', values: ['a', 'b'] }],
      }),
    ).toEqual(['tier=prod', 'team In (a, b)'])
  })

  it('renders a valueless operator without empty brackets', () => {
    expect(selectorTerms({ matchExpressions: [{ key: 'gpu', operator: 'Exists' }] })).toEqual([
      'gpu Exists',
    ])
  })

  it('quotes an operator it has never seen rather than dropping the term', () => {
    expect(selectorTerms({ matchExpressions: [{ key: 'k', operator: 'Whatever' }] })).toEqual([
      'k Whatever',
    ])
  })

  it('answers an empty list for an absent or unreadable selector', () => {
    expect(selectorTerms(undefined)).toEqual([])
    expect(selectorTerms('everything')).toEqual([])
    expect(selectorTerms({})).toEqual([])
  })
})

describe('keeping an omitted number apart from zero', () => {
  it('is null for absent and the value for zero', () => {
    // A weight of 0 takes a backend out of the pool and an omitted weight is
    // 1, so collapsing the two says the opposite of what the object does.
    expect(numberOr(undefined)).toBeNull()
    expect(numberOr(0)).toBe(0)
    expect(numberOr(80)).toBe(80)
  })

  it('is null for anything that is not a finite number', () => {
    expect(numberOr('80')).toBeNull()
    expect(numberOr(Number.NaN)).toBeNull()
    expect(numberOr(null)).toBeNull()
  })
})
