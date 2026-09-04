import { render } from '@testing-library/svelte'
import { describe, expect, it, vi } from 'vitest'

import GitOpsDetail from './GitOpsDetail.svelte'

/** An Application managing three kinds, one of which this cluster does not serve. */
const application = {
  metadata: { name: 'shop', namespace: 'argocd' },
  spec: {
    project: 'default',
    source: { repoURL: 'https://github.com/example/shop.git', path: 'deploy', targetRevision: 'main' },
    destination: { server: 'https://kubernetes.default.svc', namespace: 'shop' },
  },
  status: {
    sync: { status: 'OutOfSync', revision: '0123456789abcdef0123456789abcdef01234567' },
    health: { status: 'Degraded' },
    reconciledAt: '2026-09-04T09:05:00Z',
    resources: [
      { group: 'apps', version: 'v1', kind: 'Deployment', namespace: 'shop', name: 'web', status: 'OutOfSync', health: { status: 'Degraded' } },
      { group: 'argoproj.io', version: 'v1alpha1', kind: 'Rollout', namespace: 'shop', name: 'web-rollout', status: 'Synced', health: { status: 'Healthy' } },
      { version: 'v1', kind: 'ConfigMap', namespace: 'other', name: 'shared-config', status: 'Synced' },
    ],
  },
}

const kustomization = {
  metadata: { name: 'apps', namespace: 'flux-system' },
  spec: { interval: '10m', path: './apps', prune: true, sourceRef: { kind: 'GitRepository', name: 'fleet' } },
  status: {
    conditions: [
      { type: 'Ready', status: 'False', reason: 'BuildFailed', message: 'kustomize build failed', lastTransitionTime: '2026-09-04T08:50:00Z' },
    ],
    lastAppliedRevision: 'main@sha1:0123456789abcdef0123456789abcdef01234567',
    inventory: { entries: [{ id: 'shop_web_apps_Deployment', v: 'v1' }] },
  },
}

const helmRelease = {
  metadata: { name: 'podinfo', namespace: 'shop' },
  spec: { interval: '5m', suspend: true, chart: { spec: { chart: 'podinfo', sourceRef: { kind: 'HelmRepository', name: 'podinfo' } } } },
  status: {
    conditions: [{ type: 'Ready', status: 'True', reason: 'ReconciliationSucceeded', message: 'ok' }],
    history: [{ chartVersion: '6.5.4', status: 'deployed' }],
  },
}

/** A cluster serving the built-in kinds and Flux's sources, and not Argo Rollouts. */
function serves(kind: string): string | null {
  return ['Deployment', 'ConfigMap', 'GitRepository', 'HelmRepository'].includes(kind) ? `x/v1/${kind.toLowerCase()}s` : null
}

function linkFor(container: HTMLElement, text: string): HTMLButtonElement | undefined {
  return [...container.querySelectorAll('button')].find(
    (button) => button.textContent?.trim() === text,
  ) as HTMLButtonElement | undefined
}

/** The rendered text with its source line breaks collapsed, so a sentence can be matched as one. */
function textOf(container: HTMLElement): string {
  return (container.textContent ?? '').replace(/\s+/g, ' ')
}

describe('GitOpsDetail', () => {
  it('follows a member by its Kind, verbatim, and leaves an unserved kind as text', () => {
    // THE DEPENDENCY MAP'S RULE, applied to a member list: the click hands
    // the drawer the Kubernetes Kind as Argo CD wrote it, and a kind this
    // cluster's navigator cannot open is a name to read, not a link that
    // fails when followed.
    const onopen = vi.fn()
    const { container } = render(GitOpsDetail, {
      panel: 'argo-application',
      manifest: application,
      namespace: 'argocd',
      canOpen: serves,
      onopen,
    })

    linkFor(container, 'web')?.click()
    expect(onopen).toHaveBeenCalledWith('Deployment', 'web', 'shop')

    expect(linkFor(container, 'web-rollout')).toBeUndefined()
    expect(container.textContent).toContain('web-rollout')
  })

  it('prefixes a member outside the destination namespace and follows it there', () => {
    // Every member is assumed to be in the destination; the one that is not
    // says where it is, and opening it goes to that namespace rather than
    // to the Application's.
    const onopen = vi.fn()
    const { container } = render(GitOpsDetail, {
      panel: 'argo-application',
      manifest: application,
      canOpen: serves,
      onopen,
    })

    linkFor(container, 'other/shared-config')?.click()
    expect(onopen).toHaveBeenCalledWith('ConfigMap', 'shared-config', 'other')
  })

  it('shows Argo CD’s words and says when Argo CD reported them', () => {
    const { container } = render(GitOpsDetail, { panel: 'argo-application', manifest: application })

    expect(textOf(container)).toContain('Sync · OutOfSync')
    expect(textOf(container)).toContain('Health · Degraded')
    expect(textOf(container)).toContain('As reported by Argo CD')
    expect(textOf(container)).toContain('not re-read from the cluster')
  })

  it('shows Flux’s Ready reason, follows the source, and records the revision the inventory is for', () => {
    const onopen = vi.fn()
    const { container } = render(GitOpsDetail, {
      panel: 'flux-kustomization',
      manifest: kustomization,
      namespace: 'flux-system',
      readyTone: 'warn',
      canOpen: serves,
      onopen,
    })

    expect(textOf(container)).toContain('Ready · False · BuildFailed')
    expect(textOf(container)).toContain('kustomize build failed')
    expect(textOf(container)).toContain('As recorded by Flux for revision main@sha1:0123456789abcdef')

    // The source has no namespace of its own, so it is in the Kustomization's.
    linkFor(container, 'GitRepository/fleet')?.click()
    expect(onopen).toHaveBeenCalledWith('GitRepository', 'fleet', 'flux-system')

    // No targetNamespace, so members are assumed to be in flux-system and
    // one in `shop` carries its namespace as a prefix — and opens there.
    linkFor(container, 'shop/web')?.click()
    expect(onopen).toHaveBeenCalledWith('Deployment', 'web', 'shop')
  })

  it('tells the reader a HelmRelease carries no inventory rather than showing an empty list', () => {
    // Null is not empty. The objects exist, inside the Helm release, which
    // Flux stores in a Secret — and the panel says so instead of "nothing".
    const { container } = render(GitOpsDetail, { panel: 'flux-helmrelease', manifest: helmRelease })

    expect(textOf(container)).toContain('Flux keeps no inventory on a HelmRelease')
    expect(textOf(container)).not.toContain('reports nothing')
    expect(textOf(container)).toContain('Suspended')
    expect(textOf(container)).toContain('6.5.4')
  })
})
