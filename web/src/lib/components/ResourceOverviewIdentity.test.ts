import { afterEach, describe, expect, it } from 'vitest'
import { cleanup, render } from '@testing-library/svelte'

import ResourceOverview from './ResourceOverview.svelte'

afterEach(cleanup)

const text = (container: HTMLElement) => (container.textContent ?? '').replace(/\s+/g, ' ')

describe('a DaemonSet', () => {
  it('reports its own counts, not a Deployment\'s zeroes', () => {
    const manifest = `apiVersion: apps/v1
kind: DaemonSet
metadata: { name: hcloud-csi-node, namespace: kube-system }
spec:
  template:
    spec:
      containers: [{ name: csi, image: hetznercloud/hcloud-csi-driver:v2.13.0 }]
status:
  desiredNumberScheduled: 18
  currentNumberScheduled: 18
  numberReady: 18
  numberAvailable: 18
  updatedNumberScheduled: 18`
    const { container } = render(ResourceOverview, {
      manifest,
      kind: 'DaemonSet',
      selectedWorkload: { kind: 'DaemonSet', name: 'hcloud-csi-node', namespace: 'kube-system' } as never,
    })

    const words = text(container)
    expect(words).toContain('Desired 18')
    expect(words).toContain('Ready 18')
    expect(words).toContain('Available 18')
    expect(words).not.toContain('Desired 0')
  })
})

describe('Identity', () => {
  it('shows the version label after the namespace, and the last change', () => {
    const manifest = `apiVersion: apps/v1
kind: Deployment
metadata:
  name: web
  namespace: shop
  creationTimestamp: "2026-01-01T00:00:00Z"
  labels: { app.kubernetes.io/version: v1.0.0-dev-65 }
  managedFields:
    - { manager: argocd-controller, operation: Apply, time: "2026-09-20T00:00:00Z" }
    - { manager: kube-controller-manager, operation: Update, subresource: status, time: "2026-09-24T00:00:00Z" }
spec:
  replicas: 1
  template:
    spec:
      containers: [{ name: web, image: shop/web:9.9.9 }]`
    const { container } = render(ResourceOverview, {
      manifest,
      kind: 'Deployment',
      selectedWorkload: { kind: 'Deployment', name: 'web', namespace: 'shop' } as never,
    })

    const words = text(container)
    expect(words).toMatch(/Namespace shop Version v1\.0\.0-dev-65 Created .* Updated /)
  })

  it('falls back to the image tag when there is no version label', () => {
    const manifest = `apiVersion: apps/v1
kind: DaemonSet
metadata: { name: kube-proxy, namespace: kube-system }
spec:
  template:
    spec:
      containers: [{ name: kube-proxy, image: registry.k8s.io/kube-proxy:v1.32.7 }]`
    const { container } = render(ResourceOverview, {
      manifest,
      kind: 'DaemonSet',
      selectedWorkload: { kind: 'DaemonSet', name: 'kube-proxy', namespace: 'kube-system' } as never,
    })

    expect(text(container)).toContain('Version v1.32.7')
  })
})

describe('a CronJob schedule', () => {
  it('is said in words beside the expression', () => {
    const manifest = `apiVersion: batch/v1
kind: CronJob
metadata: { name: watch, namespace: shop }
spec:
  schedule: "*/5 * * * *"
  jobTemplate: { spec: { template: { spec: { containers: [{ name: watch, image: alpine/k8s:1.29.4 }] } } } }`
    const { container } = render(ResourceOverview, {
      manifest,
      kind: 'CronJob',
      selectedWorkload: { kind: 'CronJob', name: 'watch', namespace: 'shop' } as never,
    })

    expect(text(container)).toContain('*/5 * * * * (Every 5 minutes)')
  })
})
