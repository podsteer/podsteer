import { describe, expect, it } from 'vitest'

import { gitOpsPanelFor, secondsSince } from './panel'

describe('choosing a GitOps panel', () => {
  it('selects by group and kind together', () => {
    expect(gitOpsPanelFor('argoproj.io', 'Application')).toBe('argo-application')
    expect(gitOpsPanelFor('kustomize.toolkit.fluxcd.io', 'Kustomization')).toBe('flux-kustomization')
    expect(gitOpsPanelFor('helm.toolkit.fluxcd.io', 'HelmRelease')).toBe('flux-helmrelease')
  })

  it('does not match the same Kind in another group', () => {
    // "Application" is also a kind in app.k8s.io and core.oam.dev, and
    // "Kustomization" is kustomize's own config kind. Neither carries the
    // status this panel reads, so neither gets it.
    expect(gitOpsPanelFor('app.k8s.io', 'Application')).toBeNull()
    expect(gitOpsPanelFor('core.oam.dev', 'Application')).toBeNull()
    expect(gitOpsPanelFor('kustomize.config.k8s.io', 'Kustomization')).toBeNull()
  })

  it('selects nothing for a built-in kind or an unknown one', () => {
    expect(gitOpsPanelFor('apps', 'Deployment')).toBeNull()
    expect(gitOpsPanelFor('', 'Pod')).toBeNull()
    expect(gitOpsPanelFor(undefined, undefined)).toBeNull()
    expect(gitOpsPanelFor('argoproj.io', 'Rollout')).toBeNull()
  })
})

describe('measuring an age from a timestamp', () => {
  const now = Date.parse('2026-09-04T10:00:00Z')

  it('counts whole seconds since the timestamp', () => {
    expect(secondsSince('2026-09-04T09:55:00Z', now)).toBe(300)
  })

  it('never goes negative for a timestamp from the future', () => {
    // Clock skew between the controller and this machine is ordinary; an
    // age of "-3s" is not.
    expect(secondsSince('2026-09-04T10:00:03Z', now)).toBe(0)
  })

  it('answers NaN for anything it cannot read, which renders as an em dash', () => {
    expect(secondsSince('', now)).toBeNaN()
    expect(secondsSince('yesterday', now)).toBeNaN()
  })
})
