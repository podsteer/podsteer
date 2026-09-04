import { describe, expect, it } from 'vitest'

import { ARGO_GROUP } from '$lib/gitops/argo'
import { gitOpsPanelFor } from '$lib/gitops/panel'

import {
  CERT_MANAGER_GROUP,
  EXTERNAL_SECRETS_GROUP,
  KEDA_GROUP,
  ROLLOUTS_GROUP,
  TRIVY_GROUP,
  conditionOf,
  operatorPanelFor,
  secondsUntil,
} from './panel'

const now = Date.parse('2026-09-04T12:00:00Z')

describe('selecting an operator panel', () => {
  it('selects a panel only when the group and the kind agree', () => {
    expect(operatorPanelFor(CERT_MANAGER_GROUP, 'Certificate')).toBe('cert-manager-certificate')
    expect(operatorPanelFor(KEDA_GROUP, 'ScaledObject')).toBe('keda-scaledobject')
    expect(operatorPanelFor(EXTERNAL_SECRETS_GROUP, 'ExternalSecret')).toBe('external-secret')
    expect(operatorPanelFor(ROLLOUTS_GROUP, 'Rollout')).toBe('argo-rollout')
    expect(operatorPanelFor(TRIVY_GROUP, 'VulnerabilityReport')).toBe('trivy-vulnerabilityreport')
  })

  it('refuses another vendor’s Certificate, which is a different object entirely', () => {
    // "Certificate" is also a kind in cert.gardener.cloud, with a spec that
    // shares neither issuerRef nor secretName. A Kind alone would open the
    // cert-manager panel on it and render a column of empty rows.
    expect(operatorPanelFor('cert.gardener.cloud', 'Certificate')).toBeNull()
    expect(operatorPanelFor('networking.internal.knative.dev', 'Certificate')).toBeNull()
  })

  it('never claims Argo CD’s Application, which shares Argo Rollouts’ group', () => {
    // argoproj.io holds both controllers' kinds, and a cluster commonly runs
    // one without the other — the group alone decides nothing here. Each
    // selector is exhaustive over its own kinds and must not reach into the
    // other's, which is what makes the shared group harmless.
    expect(ROLLOUTS_GROUP).toBe(ARGO_GROUP)
    expect(operatorPanelFor(ROLLOUTS_GROUP, 'Application')).toBeNull()
    expect(gitOpsPanelFor(ARGO_GROUP, 'Rollout')).toBeNull()
    expect(gitOpsPanelFor(ARGO_GROUP, 'Application')).toBe('argo-application')
    expect(operatorPanelFor(ROLLOUTS_GROUP, 'Rollout')).toBe('argo-rollout')
  })

  it('answers null for an ordinary kind and for an absent coordinate', () => {
    expect(operatorPanelFor('apps', 'Deployment')).toBeNull()
    expect(operatorPanelFor('', 'Secret')).toBeNull()
    expect(operatorPanelFor(undefined, 'Certificate')).toBeNull()
    expect(operatorPanelFor(CERT_MANAGER_GROUP, undefined)).toBeNull()
  })
})

describe('reading one condition out of a status', () => {
  const conditions = [
    {
      type: 'Ready',
      status: 'False',
      reason: 'Failed',
      message: 'the certificate request has failed',
      lastTransitionTime: '2026-09-04T09:00:00Z',
    },
    { type: 'Issuing', status: 'True', reason: 'Renewing' },
  ]

  it('quotes the status, reason and message the controller wrote', () => {
    expect(conditionOf(conditions, 'Ready')).toEqual({
      status: 'False',
      reason: 'Failed',
      message: 'the certificate request has failed',
      since: '2026-09-04T09:00:00Z',
    })
  })

  it('fills the fields a condition omitted with empty strings', () => {
    expect(conditionOf(conditions, 'Issuing')).toEqual({
      status: 'True',
      reason: 'Renewing',
      message: '',
      since: '',
    })
  })

  it('answers null for a condition the controller has not written', () => {
    // Not written is a different fact from written as False: a freshly
    // created object has said nothing, and rendering that as a negative
    // reports a failure that has not happened.
    expect(conditionOf(conditions, 'Fallback')).toBeNull()
    expect(conditionOf([], 'Ready')).toBeNull()
    expect(conditionOf(undefined, 'Ready')).toBeNull()
    expect(conditionOf('not an array', 'Ready')).toBeNull()
    expect(conditionOf({ Ready: true }, 'Ready')).toBeNull()
  })
})

describe('counting seconds until a timestamp', () => {
  it('is positive ahead of the deadline and negative behind it', () => {
    // Signed, unlike gitops’ secondsSince: a certificate that has already
    // expired is the case this exists for, and clamping at zero would render
    // it as expiring today for as long as it sat in the cluster.
    expect(secondsUntil('2026-09-04T13:00:00Z', now)).toBe(3600)
    expect(secondsUntil('2026-09-04T11:00:00Z', now)).toBe(-3600)
    expect(secondsUntil('2026-09-04T12:00:00Z', now)).toBe(0)
  })

  it('is NaN for an empty or unparseable timestamp', () => {
    // A timestamp this cannot read is not a deadline of now.
    expect(secondsUntil('', now)).toBeNaN()
    expect(secondsUntil('not a timestamp', now)).toBeNaN()
  })
})
