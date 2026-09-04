import { describe, expect, it } from 'vitest'

import { argoApplication, argoConditionTone, argoTone, shortRevision } from './argo'

const sha = '0123456789abcdef0123456789abcdef01234567'
const olderSha = 'fedcba9876543210fedcba9876543210fedcba98'

/** A synced, healthy Application with automated sync — the ordinary case. */
const healthy = {
  apiVersion: 'argoproj.io/v1alpha1',
  kind: 'Application',
  metadata: { name: 'shop', namespace: 'argocd' },
  spec: {
    project: 'default',
    source: {
      repoURL: 'https://github.com/example/shop.git',
      path: 'deploy/overlays/prod',
      targetRevision: 'main',
    },
    destination: { server: 'https://kubernetes.default.svc', namespace: 'shop' },
    syncPolicy: { automated: { prune: true, selfHeal: true } },
  },
  status: {
    sync: { status: 'Synced', revision: sha },
    health: { status: 'Healthy' },
    operationState: {
      phase: 'Succeeded',
      message: 'successfully synced (all tasks run)',
      startedAt: '2026-09-04T09:00:00Z',
      finishedAt: '2026-09-04T09:00:07Z',
      syncResult: { revision: sha },
      operation: { sync: { revision: sha } },
    },
    reconciledAt: '2026-09-04T09:05:00Z',
    resources: [
      { group: 'apps', version: 'v1', kind: 'Deployment', namespace: 'shop', name: 'web', status: 'Synced', health: { status: 'Healthy' } },
      { version: 'v1', kind: 'Service', namespace: 'shop', name: 'web', status: 'Synced', health: { status: 'Healthy' } },
      { group: 'rbac.authorization.k8s.io', version: 'v1', kind: 'ClusterRole', name: 'shop-reader', status: 'Synced' },
    ],
  },
}

/** An Application whose last sync failed, with the conditions Argo CD raises for it. */
const degraded = {
  apiVersion: 'argoproj.io/v1alpha1',
  kind: 'Application',
  metadata: { name: 'shop', namespace: 'argocd' },
  spec: {
    project: 'default',
    source: { repoURL: 'https://charts.example.com', chart: 'shop', targetRevision: '2.4.1' },
    destination: { name: 'in-cluster', namespace: 'shop' },
    // Present and empty: automated with neither option, which is how the
    // CRD spells "on".
    syncPolicy: { automated: {} },
  },
  status: {
    sync: { status: 'OutOfSync', revision: '2.4.1' },
    health: { status: 'Degraded' },
    operationState: {
      phase: 'Failed',
      message: 'one or more objects failed to apply',
      startedAt: '2026-09-04T09:00:00Z',
      finishedAt: '2026-09-04T09:00:03Z',
      syncResult: { revision: '2.4.1' },
    },
    reconciledAt: '2026-09-04T09:05:00Z',
    conditions: [
      { type: 'SyncError', message: 'Failed sync attempt to 2.4.1: one or more objects failed to apply', lastTransitionTime: '2026-09-04T09:00:03Z' },
      { type: 'SharedResourceWarning', message: 'ConfigMap/shop-config is part of applications shop and shop-canary', lastTransitionTime: '2026-09-03T12:00:00Z' },
    ],
    resources: [
      {
        group: 'apps', version: 'v1', kind: 'Deployment', namespace: 'shop', name: 'web',
        status: 'OutOfSync',
        health: { status: 'Degraded', message: 'Deployment "web" exceeded its progress deadline' },
      },
      { group: 'argoproj.io', version: 'v1alpha1', kind: 'Rollout', namespace: 'shop', name: 'web-rollout', status: 'Synced', health: { status: 'Progressing' } },
      { version: 'v1', kind: 'ConfigMap', namespace: 'shop', name: 'old-config', status: 'OutOfSync', requiresPruning: true },
    ],
  },
}

describe('reading an Argo CD Application', () => {
  it('quotes sync and health in Argo CD’s own words', () => {
    // The controller's verdicts, shown as the controller wrote them: no
    // re-derivation from the members, no PodSteer opinion layered on top.
    const application = argoApplication(healthy)

    expect(application?.sync).toEqual({ status: 'Synced', revision: sha })
    expect(application?.health).toEqual({ status: 'Healthy', message: '' })
    expect(application?.reconciledAt).toBe('2026-09-04T09:05:00Z')
  })

  it('reads the source, destination and sync policy', () => {
    const application = argoApplication(healthy)

    expect(application?.project).toBe('default')
    expect(application?.sources).toEqual([
      { repoURL: 'https://github.com/example/shop.git', path: 'deploy/overlays/prod', chart: '', targetRevision: 'main' },
    ])
    expect(application?.destination).toEqual({ server: 'https://kubernetes.default.svc', name: '', namespace: 'shop' })
    expect(application?.syncPolicy).toEqual({ automated: true, prune: true, selfHeal: true })
  })

  it('reads a chart source and a destination addressed by cluster name', () => {
    const application = argoApplication(degraded)

    expect(application?.sources[0]).toEqual({ repoURL: 'https://charts.example.com', path: '', chart: 'shop', targetRevision: '2.4.1' })
    expect(application?.destination).toEqual({ server: '', name: 'in-cluster', namespace: 'shop' })
    // `automated: {}` is on, with neither option — the CRD's own spelling.
    expect(application?.syncPolicy).toEqual({ automated: true, prune: false, selfHeal: false })
  })

  it('treats a missing sync policy as manual', () => {
    const application = argoApplication({ spec: { source: {}, destination: {} } })

    expect(application?.syncPolicy.automated).toBe(false)
  })

  it('prefers spec.sources over spec.source when both are present', () => {
    // The CRD's rule for a multi-source Application: once the plural is set,
    // the singular is ignored. The revisions come back one per source.
    const application = argoApplication({
      spec: {
        source: { repoURL: 'https://ignored.example.com', path: '.' },
        sources: [
          { repoURL: 'https://github.com/example/values.git', path: 'values', targetRevision: 'main' },
          { repoURL: 'https://charts.example.com', chart: 'shop', targetRevision: '2.4.1' },
        ],
      },
      status: { sync: { status: 'Synced', revisions: [sha, '2.4.1'] } },
    })

    expect(application?.sources.map((source) => source.repoURL)).toEqual([
      'https://github.com/example/values.git',
      'https://charts.example.com',
    ])
    expect(application?.sync.revision).toBe(`${sha}, 2.4.1`)
  })

  it('lists every member with the state Argo CD reported for it', () => {
    // Membership is the controller's own `status.resources`, verbatim —
    // including a cluster-scoped member with no namespace and one that is
    // live but gone from Git.
    const members = argoApplication(degraded)?.resources ?? []

    expect(members.map((member) => `${member.kind}/${member.name}`)).toEqual([
      'Deployment/web',
      'Rollout/web-rollout',
      'ConfigMap/old-config',
    ])
    expect(members[0]).toMatchObject({
      group: 'apps',
      version: 'v1',
      namespace: 'shop',
      sync: 'OutOfSync',
      health: 'Degraded',
      healthMessage: 'Deployment "web" exceeded its progress deadline',
      requiresPruning: false,
    })
    expect(members[2]).toMatchObject({ group: '', sync: 'OutOfSync', health: '', requiresPruning: true })

    const clusterScoped = argoApplication(healthy)?.resources[2]
    expect(clusterScoped).toMatchObject({ kind: 'ClusterRole', namespace: '', name: 'shop-reader' })
  })

  it('keeps each condition’s message with its type', () => {
    // Argo CD's conditions have no status field: the type is the severity
    // and the message is the whole content, which is why the generic
    // conditions list — "undefined · message" — is the wrong shape for them.
    const conditions = argoApplication(degraded)?.conditions ?? []

    expect(conditions).toEqual([
      { type: 'SyncError', message: 'Failed sync attempt to 2.4.1: one or more objects failed to apply', lastTransitionTime: '2026-09-04T09:00:03Z' },
      { type: 'SharedResourceWarning', message: 'ConfigMap/shop-config is part of applications shop and shop-canary', lastTransitionTime: '2026-09-03T12:00:00Z' },
    ])
    expect(argoConditionTone('SyncError')).toBe('critical')
    expect(argoConditionTone('SharedResourceWarning')).toBe('warn')
    expect(argoConditionTone('SomethingElse')).toBeUndefined()
  })

  it('records the last sync operation, or that there was none', () => {
    expect(argoApplication(healthy)?.operation).toEqual({
      phase: 'Succeeded',
      message: 'successfully synced (all tasks run)',
      revision: sha,
      startedAt: '2026-09-04T09:00:00Z',
      finishedAt: '2026-09-04T09:00:07Z',
    })

    // An Application nothing has synced yet has no operation to show, and
    // that is a fact about it rather than a missing field.
    expect(argoApplication({ spec: {}, status: { sync: { status: 'OutOfSync' } } })?.operation).toBeNull()
  })

  it('falls back to the requested revision while an operation is still running', () => {
    // A running sync has an operation but no result yet; the revision it was
    // asked for is the only one there is.
    const application = argoApplication({
      status: { operationState: { phase: 'Running', operation: { sync: { revision: olderSha } } } },
    })

    expect(application?.operation).toMatchObject({ phase: 'Running', revision: olderSha })
  })

  it('says nothing where Argo CD has said nothing', () => {
    // A just-created Application has a spec and no status. Empty strings
    // rather than a crash or a null, so the panel renders "Unknown" — the
    // truth of it — instead of disappearing.
    const application = argoApplication({ spec: { source: {}, destination: {} } })

    expect(application?.sync.status).toBe('')
    expect(application?.health.status).toBe('')
    expect(application?.resources).toEqual([])
    expect(application?.conditions).toEqual([])
  })

  it('answers null for no manifest', () => {
    expect(argoApplication(null)).toBeNull()
    expect(argoApplication('not an object')).toBeNull()
  })

  it('grades words the way Argo CD’s own interface does, and leaves unknown words plain', () => {
    // Argo CD's grading, not ours: amber for OutOfSync and Missing, red for
    // Degraded. A word the table does not know is uncoloured rather than
    // guessed at.
    expect(argoTone('Synced')).toBeUndefined()
    expect(argoTone('Healthy')).toBeUndefined()
    expect(argoTone('Progressing')).toBeUndefined()
    expect(argoTone('Suspended')).toBeUndefined()
    expect(argoTone('Unknown')).toBeUndefined()
    expect(argoTone('OutOfSync')).toBe('warn')
    expect(argoTone('Missing')).toBe('warn')
    expect(argoTone('Degraded')).toBe('critical')
    expect(argoTone('Failed')).toBe('critical')
    expect(argoTone('')).toBeUndefined()
    expect(argoTone('Degradedish')).toBeUndefined()
  })

  it('shortens a full git SHA and nothing else', () => {
    expect(shortRevision(sha)).toBe('0123456')
    expect(shortRevision('2.4.1')).toBe('2.4.1')
    expect(shortRevision('main')).toBe('main')
    expect(shortRevision('')).toBe('')
  })
})
