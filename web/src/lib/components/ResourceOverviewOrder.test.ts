/**
 * The Overview tab reads in the manifest's own order.
 *
 * What is wrong and what it is using come first — neither is a field — and
 * from there down the panel follows the manifest: metadata, then spec, then
 * status, with anything the manifest does not contain after it. Asserted on
 * the rendered headings, because the order is a property of the template and
 * nothing else would notice a section drifting.
 */
import { afterEach, describe, expect, it } from 'vitest'
import { cleanup, render } from '@testing-library/svelte'

import ResourceOverview from './ResourceOverview.svelte'

afterEach(cleanup)

function headings(container: HTMLElement): string[] {
  return [...container.querySelectorAll('h3')].map((h) => (h.textContent ?? '').trim().split('\n')[0].trim())
}

/** Asserts `expected` appear in this relative order (others may sit between). */
function inOrder(actual: string[], expected: string[]): void {
  const positions = expected.map((title) => {
    const at = actual.findIndex((heading) => heading.startsWith(title))
    expect(at, `"${title}" in ${JSON.stringify(actual)}`).toBeGreaterThan(-1)
    return at
  })
  expect(positions).toEqual([...positions].sort((a, b) => a - b))
}

const metadata = `metadata:
  name: web
  namespace: shop
  uid: 1
  labels: { app: web }
  annotations: { note: x }`

describe('the Overview tab', () => {
  it('puts a controller\'s metadata before its spec, and its conditions last', () => {
    const manifest = `apiVersion: apps/v1
kind: Deployment
${metadata}
spec:
  replicas: 2
  strategy: { type: RollingUpdate }
  template:
    spec:
      containers: [{ name: app, image: nginx }]
status:
  conditions:
    - { type: Available, status: "True" }`
    const { container } = render(ResourceOverview, {
      manifest,
      kind: 'Deployment',
      selectedWorkload: { kind: 'Deployment', name: 'web', namespace: 'shop', desired: 2, ready: 2 } as never,
    })

    inOrder(headings(container), ['Identity', 'Labels', 'Annotations', 'Replicas', 'Pod template', 'Conditions'])
  })

  it('puts a CronJob\'s schedule before its template, as its spec does', () => {
    const manifest = `apiVersion: batch/v1
kind: CronJob
${metadata}
spec:
  schedule: "*/5 * * * *"
  jobTemplate:
    spec:
      template:
        spec:
          containers: [{ name: app, image: busybox }]`
    const { container } = render(ResourceOverview, {
      manifest,
      kind: 'CronJob',
      selectedWorkload: { kind: 'CronJob', name: 'web', namespace: 'shop' } as never,
    })

    inOrder(headings(container), ['Identity', 'Labels', 'Schedule', 'Pod template'])
  })

  it('reads a pod as metadata, then its containers, then its status', () => {
    const manifest = `apiVersion: v1
kind: Pod
${metadata}
spec:
  containers: [{ name: app, image: nginx }]
status:
  phase: Running
  conditions:
    - { type: Ready, status: "True" }`
    const { container } = render(ResourceOverview, { manifest, kind: 'Pod' })

    inOrder(headings(container), ['Identity', 'Labels', 'Annotations', 'Containers', 'Status', 'Conditions'])
  })
})
