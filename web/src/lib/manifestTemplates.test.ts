import { describe, expect, it } from 'vitest'
import { parse } from 'yaml'

import type { ResourceKind } from './api/client'
import { skeletonFor } from './manifestTemplates'

/** A `ResourceKind` fixture, defaulting to a namespaced core kind — override
    whatever the test is actually about. Matches the shape `BrowseAPI.ListKinds`
    sends (see `app/adapters/wails/dto_resources.go`). */
function kind(overrides: Partial<ResourceKind> = {}): ResourceKind {
  return {
    id: 'core/v1/configmaps',
    group: '',
    version: 'v1',
    kind: 'ConfigMap',
    namespaced: true,
    category: 'config',
    subcategory: '',
    title: 'ConfigMaps',
    singular: 'ConfigMap',
    rich: false,
    ...overrides,
  } as ResourceKind
}

// Every Kind the skeleton has a real body for, plus one it does not — see
// SPECS in manifestTemplates.ts. Kept as a literal list rather than derived
// from the module, so a typo in a key there shows up as an untested kind
// here instead of a test that quietly covers nothing.
const KNOWN_KINDS = [
  'Deployment',
  'StatefulSet',
  'DaemonSet',
  'Job',
  'CronJob',
  'Service',
  'ConfigMap',
  'Secret',
  'Ingress',
  'NetworkPolicy',
  'PersistentVolumeClaim',
  'ServiceAccount',
  'Role',
  'RoleBinding',
  'HorizontalPodAutoscaler',
  'PodDisruptionBudget',
  'Namespace',
]

describe('skeletonFor', () => {
  it('produces YAML that parses, for every kind it has a body for', () => {
    for (const kindName of KNOWN_KINDS) {
      const manifest = skeletonFor(kind({ kind: kindName, namespaced: kindName !== 'Namespace' }), 'billing')
      expect(() => parse(manifest), `${kindName} did not parse`).not.toThrow()
    }
  })

  it('produces YAML that parses for a kind it does not recognise', () => {
    const widget = kind({
      id: 'widgets.example.com/v1/widgets',
      group: 'widgets.example.com',
      version: 'v1',
      kind: 'Widget',
      namespaced: true,
      category: 'custom-resources',
    })
    expect(() => parse(skeletonFor(widget, 'billing'))).not.toThrow()
  })

  it('takes apiVersion from just the version for a core kind', () => {
    // ConfigMap is served by the core group, which domain.ResourceKind.Group
    // renders as "" — kubectl and the API server both write that as a bare
    // version with no leading slash.
    const manifest = parse(skeletonFor(kind({ group: '', version: 'v1', kind: 'ConfigMap' }), null))
    expect(manifest.apiVersion).toBe('v1')
    expect(manifest.kind).toBe('ConfigMap')
  })

  it('takes apiVersion from group/version for a grouped kind', () => {
    const manifest = parse(
      skeletonFor(
        kind({ group: 'apps', version: 'v1', kind: 'Deployment', namespaced: true }),
        null,
      ),
    )
    expect(manifest.apiVersion).toBe('apps/v1')
    expect(manifest.kind).toBe('Deployment')
  })

  it('carries the group/version through even for an unrecognised kind', () => {
    // The catalog id "group/version/resource" comes from the SAME two
    // fields as the header of a known kind — the fallback earns its apiVersion
    // the same way, it just cannot earn a real spec.
    const manifest = parse(
      skeletonFor(
        kind({ group: 'monitoring.coreos.com', version: 'v1', kind: 'ServiceMonitor', namespaced: true }),
        null,
      ),
    )
    expect(manifest.apiVersion).toBe('monitoring.coreos.com/v1')
    expect(manifest.kind).toBe('ServiceMonitor')
    expect(manifest.spec).toEqual({})
  })

  it('leaves the name blank for the operator to fill in', () => {
    const manifest = parse(skeletonFor(kind(), null))
    expect(manifest.metadata.name).toBe('')
  })

  it('writes the namespace only when the kind is namespaced and one is selected', () => {
    const namespaced = kind({ namespaced: true })

    expect(parse(skeletonFor(namespaced, 'billing')).metadata.namespace).toBe('billing')
    // null: nothing selected yet.
    expect(parse(skeletonFor(namespaced, null)).metadata).not.toHaveProperty('namespace')
    // '': ALL_NAMESPACES, the "every namespace" filter — not a place an
    // object can live.
    expect(parse(skeletonFor(namespaced, '')).metadata).not.toHaveProperty('namespace')
  })

  it('never writes a namespace for a cluster-scoped kind, even with one selected', () => {
    const clusterScoped = kind({
      group: 'rbac.authorization.k8s.io',
      version: 'v1',
      kind: 'ClusterRole',
      namespaced: false,
    })
    expect(parse(skeletonFor(clusterScoped, 'billing')).metadata).not.toHaveProperty('namespace')
  })

  it('keeps the two label lines in a Deployment skeleton in agreement', () => {
    // The one thing that has to be true for the skeleton to actually
    // reconcile: selector.matchLabels is what template.metadata.labels is
    // matched against.
    const manifest = parse(
      skeletonFor(kind({ group: 'apps', version: 'v1', kind: 'Deployment', namespaced: true }), null),
    )
    expect(manifest.spec.selector.matchLabels).toEqual(manifest.spec.template.metadata.labels)
    expect(manifest.spec.template.spec.containers).toHaveLength(1)
    expect(manifest.spec.template.spec.containers[0].image).toBe('nginx:1.27')
  })

  it('nests a CronJob one level deeper than a Job, and both still parse', () => {
    // CronJob does not own pods, it owns Jobs that own pods — see CLAUDE.md,
    // "A CronJob does not own pods" — so its container lives at
    // spec.jobTemplate.spec.template.spec, not spec.template.spec.
    const job = parse(skeletonFor(kind({ group: 'batch', version: 'v1', kind: 'Job', namespaced: true }), null))
    expect(job.spec.template.spec.containers[0].name).toBe('app')

    const cronJob = parse(
      skeletonFor(kind({ group: 'batch', version: 'v1', kind: 'CronJob', namespaced: true }), null),
    )
    expect(cronJob.spec.jobTemplate.spec.template.spec.containers[0].name).toBe('app')
    expect(cronJob.spec.schedule).toBeTruthy()
  })

  it('derives the HorizontalPodAutoscaler apiVersion from the catalog entry, not a hardcoded one', () => {
    // The catalog (app/domain/catalog.go) serves HPA from autoscaling/v2;
    // skeletonFor must read that off the kind it was handed rather than
    // assuming it, or a future catalog change would silently produce a
    // manifest for the wrong API version.
    const manifest = parse(
      skeletonFor(
        kind({ group: 'autoscaling', version: 'v2', kind: 'HorizontalPodAutoscaler', namespaced: true }),
        null,
      ),
    )
    expect(manifest.apiVersion).toBe('autoscaling/v2')
    expect(manifest.spec.metrics[0].type).toBe('Resource')
  })

  it('gives a Secret stringData rather than pre-encoded data', () => {
    const manifest = parse(skeletonFor(kind({ group: '', version: 'v1', kind: 'Secret', namespaced: true }), null))
    expect(manifest.type).toBe('Opaque')
    expect(manifest.stringData).toEqual({ key: 'value' })
  })
})
