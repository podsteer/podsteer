import { describe, expect, it, vi } from 'vitest'

import { managementFromMarker, resolveManagement } from './gitopsChain'
import { managementWarning } from './gitops'

// The three objects below are transcribed from a real Argo CD-managed
// workload — the metadata is what the cluster actually returned, trimmed to
// the keys these rules read. The pod's emptiness is the point: it carries no
// GitOps marker of any kind, which is why the question cannot be answered
// from the object in front of the operator.

const TRACKING = 'workspace-delta-processor:apps/Deployment:synapctx/workspace-delta-processor'

const deployment = {
  kind: 'Deployment',
  metadata: {
    name: 'workspace-delta-processor',
    annotations: { 'argocd.argoproj.io/tracking-id': TRACKING },
    labels: { 'app.kubernetes.io/managed-by': 'argocd' },
  },
}

const replicaSet = {
  kind: 'ReplicaSet',
  metadata: {
    name: 'workspace-delta-processor-64c9c878b4',
    // COPIED, NOT EARNED. The Deployment controller puts a Deployment's
    // annotations on every ReplicaSet it creates, so this names a Deployment.
    annotations: { 'argocd.argoproj.io/tracking-id': TRACKING },
    labels: { 'app.kubernetes.io/name': 'workspace-delta-processor', 'pod-template-hash': '64c9c878b4' },
  },
  ownerReferences: undefined,
}

const pod = {
  kind: 'Pod',
  metadata: {
    name: 'workspace-delta-processor-64c9c878b4-pzv7j',
    labels: { 'app.kubernetes.io/name': 'workspace-delta-processor', 'pod-template-hash': '64c9c878b4' },
    annotations: { 'prometheus.io/scrape': 'true' },
    ownerReferences: [
      { apiVersion: 'apps/v1', kind: 'ReplicaSet', name: 'workspace-delta-processor-64c9c878b4', controller: true },
    ],
  },
}

const withOwner = (object: object, owner: object) => ({
  ...object,
  metadata: { ...(object as { metadata: object }).metadata, ownerReferences: [owner] },
})

describe('resolving what holds an object in Git', () => {
  it('answers for the managed object itself without a read', async () => {
    const read = vi.fn()

    const found = await resolveManagement(deployment, 'synapctx', read)

    expect(found?.through).toBe('direct')
    expect(found?.owner.source).toBe('workspace-delta-processor')
    expect(read).not.toHaveBeenCalled()
  })

  it('walks from a pod to the Deployment its spec comes from', async () => {
    const read = vi.fn().mockResolvedValue(
      withOwner(replicaSet, { apiVersion: 'apps/v1', kind: 'Deployment', name: 'workspace-delta-processor', controller: true }),
    )

    const found = await resolveManagement(pod, 'synapctx', read)

    expect(found?.through).toBe('inherited')
    // NAMES THE DEPLOYMENT, not the ReplicaSet it read. The ReplicaSet is an
    // implementation detail of the Deployment; sending somebody to look at it
    // would be sending them to the wrong object.
    expect(found?.controller).toEqual({ kind: 'Deployment', name: 'workspace-delta-processor' })
    expect(read).toHaveBeenCalledExactlyOnceWith('apps/v1/replicasets', 'synapctx', 'workspace-delta-processor-64c9c878b4')
  })

  it('does not call a ReplicaSet managed when its marker names a Deployment', async () => {
    // The bug this closes: the ReplicaSet holds a perfectly accurate tracking
    // id ABOUT SOMETHING ELSE. Read as its own, it warned that Argo CD would
    // revert an edit to a ReplicaSet — which Argo CD never looks at.
    const found = await resolveManagement(replicaSet, 'synapctx', vi.fn())

    expect(found?.through).toBe('inherited')
    expect(found?.controller).toEqual({ kind: 'Deployment', name: 'workspace-delta-processor' })
  })

  it('says nothing when the object is managed by nobody', async () => {
    const orphan = { kind: 'Pod', metadata: { name: 'debug', labels: {}, annotations: {} } }

    expect(await resolveManagement(orphan, 'default', vi.fn())).toBeNull()
  })

  it('says nothing rather than guessing when the walk cannot read', async () => {
    // An account that may not list ReplicaSets. Unknown is not "no", but a
    // warning nobody can verify is one people learn to dismiss.
    const read = vi.fn().mockRejectedValue(new Error('[forbidden] no'))

    expect(await resolveManagement(pod, 'synapctx', read)).toBeNull()
  })

  it('does not follow a kind it has no path for', async () => {
    const owned = withOwner(pod, { apiVersion: 'argoproj.io/v1alpha1', kind: 'Rollout', name: 'web', controller: true })
    const read = vi.fn()

    expect(await resolveManagement(owned, 'synapctx', read)).toBeNull()
    expect(read).not.toHaveBeenCalled()
  })

  it('follows a Job up to its CronJob', async () => {
    const job = withOwner(pod, { apiVersion: 'batch/v1', kind: 'Job', name: 'refresh-29816280', controller: true })
    const read = vi.fn().mockResolvedValue({
      kind: 'Job',
      metadata: {
        name: 'refresh-29816280',
        labels: { 'kustomize.toolkit.fluxcd.io/name': 'apps' },
      },
    })

    const found = await resolveManagement(job, 'synapctx', read)

    expect(found?.owner.tool).toBe('flux')
    expect(found?.through).toBe('inherited')
    expect(found?.controller).toEqual({ kind: 'Job', name: 'refresh-29816280' })
  })
})

describe('what the warning says', () => {
  it('promises a revert only for the object the controller applies', async () => {
    const direct = await resolveManagement(deployment, 'synapctx', vi.fn())

    expect(managementWarning(direct!)).toContain('reverted')
  })

  it('does NOT promise a revert for a pod', async () => {
    // MEASURED, not assumed: fifteen minutes after an in-place resize the pod
    // still held the new figures and the Application still read Synced, with
    // self-heal on. Argo CD reconciles the Deployment, and a pod resize does
    // not change the Deployment. What ends the change is the replacement.
    const read = vi.fn().mockResolvedValue(
      withOwner(replicaSet, { apiVersion: 'apps/v1', kind: 'Deployment', name: 'workspace-delta-processor', controller: true }),
    )
    const inherited = await resolveManagement(pod, 'synapctx', read)
    const sentence = managementWarning(inherited!)

    expect(sentence).not.toContain('reverted')
    expect(sentence).toContain('stays on this object')
    expect(sentence).toContain('workspace-delta-processor Deployment')
  })
})

describe('a list row, which cannot afford a read', () => {
  it('reads a Deployment row as managed', () => {
    expect(managementFromMarker(deployment)?.through).toBe('direct')
  })

  it('does not warn that a ReplicaSet row will be reverted', () => {
    // The row carries the Deployment's annotation. Before the kind and name
    // were part of the evidence, every ReplicaSet in the list claimed Argo CD
    // would revert an edit to it — and Argo CD never looks at a ReplicaSet.
    const found = managementFromMarker(replicaSet)

    expect(found?.through).toBe('inherited')
    expect(managementWarning(found!)).not.toContain('reverted')
  })

  it('says nothing about an unmarked row rather than walking', () => {
    // A pod's row has no marker and this makes no reads, so the honest
    // answer is nothing — resolveManagement is what walks.
    expect(managementFromMarker(pod)).toBeNull()
  })
})
