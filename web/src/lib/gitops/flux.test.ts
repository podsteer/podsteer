import { describe, expect, it } from 'vitest'

import { fluxHelmRelease, fluxKustomization, parseInventoryId } from './flux'

const applied = 'main@sha1:0123456789abcdef0123456789abcdef01234567'
const attempted = 'main@sha1:fedcba9876543210fedcba9876543210fedcba98'

/** A Kustomization whose last build failed, still serving what it applied before. */
const failedKustomization = {
  apiVersion: 'kustomize.toolkit.fluxcd.io/v1',
  kind: 'Kustomization',
  metadata: { name: 'apps', namespace: 'flux-system' },
  spec: {
    interval: '10m0s',
    path: './clusters/prod/apps',
    prune: true,
    sourceRef: { kind: 'GitRepository', name: 'fleet' },
    targetNamespace: 'shop',
  },
  status: {
    conditions: [
      {
        type: 'Ready',
        status: 'False',
        reason: 'BuildFailed',
        message: 'kustomize build failed: accumulating resources: missing file',
        lastTransitionTime: '2026-09-04T08:50:00Z',
      },
      { type: 'Reconciling', status: 'True', reason: 'Progressing', message: 'Reconciliation in progress' },
    ],
    lastAppliedRevision: applied,
    lastAttemptedRevision: attempted,
    inventory: {
      entries: [
        { id: 'shop_web_apps_Deployment', v: 'v1' },
        { id: 'shop_web__Service', v: 'v1' },
        { id: '_shop__Namespace', v: 'v1' },
        { id: '_system__shop-reader_rbac.authorization.k8s.io_ClusterRole', v: 'v1' },
        { id: 'shop_web-cert_cert-manager.io_Certificate', v: 'v1' },
      ],
    },
  },
}

/** A HelmRelease somebody paused, on the v2 API where history replaced lastAppliedRevision. */
const suspendedHelmRelease = {
  apiVersion: 'helm.toolkit.fluxcd.io/v2',
  kind: 'HelmRelease',
  metadata: { name: 'podinfo', namespace: 'shop' },
  spec: {
    interval: '5m',
    suspend: true,
    releaseName: 'podinfo',
    chart: {
      spec: {
        chart: 'podinfo',
        version: '>=6.0.0',
        sourceRef: { kind: 'HelmRepository', name: 'podinfo', namespace: 'flux-system' },
      },
    },
  },
  status: {
    conditions: [
      {
        type: 'Ready',
        status: 'True',
        reason: 'ReconciliationSucceeded',
        message: 'Helm upgrade succeeded for release shop/podinfo.v3 with chart podinfo@6.5.4',
        lastTransitionTime: '2026-09-01T10:00:00Z',
      },
    ],
    lastAttemptedRevision: '6.5.4',
    history: [
      { chartVersion: '6.5.4', appVersion: '6.5.4', status: 'deployed', lastDeployed: '2026-09-01T10:00:00Z' },
      { chartVersion: '6.5.3', appVersion: '6.5.3', status: 'superseded', lastDeployed: '2026-08-20T10:00:00Z' },
    ],
  },
}

describe('parsing a Flux inventory id', () => {
  it('splits namespace, name, group and kind', () => {
    expect(parseInventoryId('shop_web_apps_Deployment')).toEqual({
      namespace: 'shop',
      name: 'web',
      group: 'apps',
      kind: 'Deployment',
    })
  })

  it('reads an empty group segment as the core group', () => {
    // Two underscores in a row are not a typo: a Service has no API group,
    // and the format keeps the empty field rather than dropping it.
    expect(parseInventoryId('shop_web__Service')).toEqual({
      namespace: 'shop',
      name: 'web',
      group: '',
      kind: 'Service',
    })
  })

  it('reads an empty namespace as cluster-scoped', () => {
    expect(parseInventoryId('_shop__Namespace')).toEqual({
      namespace: '',
      name: 'shop',
      group: '',
      kind: 'Namespace',
    })
  })

  it('decodes the double underscore an RBAC name’s colon is written as', () => {
    // cli-utils transcodes the colon because the id was designed to be a
    // ConfigMap key. Read from the outside in, the name is whatever is left
    // in the middle — which is what makes `system:shop-reader` survive.
    expect(parseInventoryId('_system__shop-reader_rbac.authorization.k8s.io_ClusterRole')).toEqual({
      namespace: '',
      name: 'system:shop-reader',
      group: 'rbac.authorization.k8s.io',
      kind: 'ClusterRole',
    })
  })

  it('refuses an id with too few fields', () => {
    expect(parseInventoryId('web')).toBeNull()
    expect(parseInventoryId('shop_web')).toBeNull()
    expect(parseInventoryId('shop_web_apps')).toBeNull()
    expect(parseInventoryId('')).toBeNull()
  })
})

describe('reading a Flux Kustomization', () => {
  it('quotes the Ready condition with Flux’s reason and message', () => {
    // Flux's own conclusion in Flux's own words. Its colour is not decided
    // here — that goes through the domain like every other condition.
    const kustomization = fluxKustomization(failedKustomization)

    expect(kustomization?.ready).toEqual({
      status: 'False',
      reason: 'BuildFailed',
      message: 'kustomize build failed: accumulating resources: missing file',
      since: '2026-09-04T08:50:00Z',
    })
  })

  it('reads the source, path, interval and pruning', () => {
    const kustomization = fluxKustomization(failedKustomization)

    expect(kustomization?.source).toEqual({ kind: 'GitRepository', name: 'fleet', namespace: '' })
    expect(kustomization?.path).toBe('./clusters/prod/apps')
    expect(kustomization?.interval).toBe('10m0s')
    expect(kustomization?.prune).toBe(true)
    expect(kustomization?.suspended).toBe(false)
    expect(kustomization?.targetNamespace).toBe('shop')
  })

  it('keeps the applied and the attempted revision apart', () => {
    // After a failed build the live state is still the OLD revision, and
    // the one Flux tried is a different string. Both are quoted; whether
    // the gap matters is for the reader.
    const kustomization = fluxKustomization(failedKustomization)

    expect(kustomization?.lastAppliedRevision).toBe(applied)
    expect(kustomization?.lastAttemptedRevision).toBe(attempted)
  })

  it('lists the inventory as members, core kinds and RBAC names included', () => {
    const members = fluxKustomization(failedKustomization)?.inventory ?? []

    expect(members.map((member) => `${member.group || 'core'}/${member.kind} ${member.namespace || '-'}/${member.name}`)).toEqual([
      'apps/Deployment shop/web',
      'core/Service shop/web',
      'core/Namespace -/shop',
      'rbac.authorization.k8s.io/ClusterRole -/system:shop-reader',
      'cert-manager.io/Certificate shop/web-cert',
    ])
    // Flux records identity and a version, and no per-member state.
    expect(members[0]).toMatchObject({ version: 'v1', sync: '', health: '', requiresPruning: false })
  })

  it('drops an inventory entry it cannot parse rather than showing half of it', () => {
    const kustomization = fluxKustomization({
      status: { inventory: { entries: [{ id: 'garbage' }, { id: 'shop_web_apps_Deployment', v: 'v1' }] } },
    })

    expect(kustomization?.inventory?.map((member) => member.name)).toEqual(['web'])
  })

  it('tells no inventory from an empty one', () => {
    // Never applied is null; applied nothing is []. A panel that showed
    // both as "no resources" would be right about one of them.
    expect(fluxKustomization({ spec: {}, status: {} })?.inventory).toBeNull()
    expect(fluxKustomization({ status: { inventory: { entries: [] } } })?.inventory).toEqual([])
    expect(fluxKustomization({ status: { inventory: {} } })?.inventory).toEqual([])
  })

  it('reports no Ready condition as null rather than as a status', () => {
    expect(fluxKustomization({ spec: {}, status: { conditions: [{ type: 'Reconciling', status: 'True' }] } })?.ready).toBeNull()
    expect(fluxKustomization({ spec: {} })?.ready).toBeNull()
  })

  it('answers null for no manifest', () => {
    expect(fluxKustomization(null)).toBeNull()
    expect(fluxKustomization(42)).toBeNull()
  })
})

describe('reading a Flux HelmRelease', () => {
  it('reads the suspended flag, the chart and its source', () => {
    const release = fluxHelmRelease(suspendedHelmRelease)

    expect(release?.suspended).toBe(true)
    expect(release?.chart).toBe('podinfo')
    expect(release?.version).toBe('>=6.0.0')
    expect(release?.source).toEqual({ kind: 'HelmRepository', name: 'podinfo', namespace: 'flux-system' })
    expect(release?.interval).toBe('5m')
    expect(release?.releaseName).toBe('podinfo')
  })

  it('takes the deployed chart from the newest history entry on the v2 API', () => {
    // v2 dropped lastAppliedRevision; the history is newest first, so the
    // first entry is what is deployed, with Helm's own status for it.
    const release = fluxHelmRelease(suspendedHelmRelease)

    expect(release?.lastAppliedRevision).toBe('6.5.4')
    expect(release?.appVersion).toBe('6.5.4')
    expect(release?.releaseStatus).toBe('deployed')
    expect(release?.lastDeployed).toBe('2026-09-01T10:00:00Z')
    expect(release?.lastAttemptedRevision).toBe('6.5.4')
  })

  it('prefers lastAppliedRevision when an older controller still writes it', () => {
    const release = fluxHelmRelease({
      spec: {},
      status: { lastAppliedRevision: '6.5.3', history: [{ chartVersion: '6.5.4' }] },
    })

    expect(release?.lastAppliedRevision).toBe('6.5.3')
  })

  it('reads a chartRef as the source when there is no chart template', () => {
    // The v2 way of pointing at an OCIRepository or HelmChart directly.
    const release = fluxHelmRelease({
      spec: { chartRef: { kind: 'OCIRepository', name: 'podinfo', namespace: 'flux-system' } },
    })

    expect(release?.source).toEqual({ kind: 'OCIRepository', name: 'podinfo', namespace: 'flux-system' })
    expect(release?.chart).toBe('OCIRepository/podinfo')
  })

  it('carries the Ready condition through unchanged', () => {
    expect(fluxHelmRelease(suspendedHelmRelease)?.ready).toEqual({
      status: 'True',
      reason: 'ReconciliationSucceeded',
      message: 'Helm upgrade succeeded for release shop/podinfo.v3 with chart podinfo@6.5.4',
      since: '2026-09-01T10:00:00Z',
    })
  })

  it('has no inventory, because Flux keeps none on a HelmRelease', () => {
    // Null, not empty: the objects exist and belong to the Helm release,
    // which lives in a Secret this panel deliberately does not read.
    expect(fluxHelmRelease(suspendedHelmRelease)?.inventory).toBeNull()
  })

  it('answers null for no manifest', () => {
    expect(fluxHelmRelease(undefined)).toBeNull()
  })
})
