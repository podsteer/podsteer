import { describe, expect, it } from 'vitest'
import { parse } from 'yaml'

import { stripForDuplicate } from './duplicate'

/** A Deployment carrying every field this module is supposed to remove,
    alongside labels, annotations and spec content it is supposed to leave
    alone — so a test asserting one removal can also assert nothing else
    moved. */
const FULL_MANIFEST = `apiVersion: apps/v1
kind: Deployment
metadata:
  name: web
  namespace: billing
  uid: 3f1c2b4a-0000-0000-0000-000000000000
  resourceVersion: "489213"
  generation: 4
  creationTimestamp: "2024-03-01T12:00:00Z"
  selfLink: /apis/apps/v1/namespaces/billing/deployments/web
  labels:
    app: web
    tier: backend
  annotations:
    kubectl.kubernetes.io/last-applied-configuration: '{"apiVersion":"apps/v1","kind":"Deployment"}'
    custom.io/owner: team-payments
  ownerReferences:
    - apiVersion: apps/v1
      kind: ReplicaSet
      name: web-abc123
      uid: aaaa-bbbb
  managedFields:
    - manager: kubectl-client-side-apply
      operation: Update
spec:
  replicas: 3
  selector:
    matchLabels:
      app: web
  template:
    metadata:
      labels:
        app: web
    spec:
      containers:
        - name: app
          image: web:1.2.3 # pinned by the release pipeline
status:
  readyReplicas: 3
  conditions:
    - type: Available
      status: "True"
`

describe('stripForDuplicate', () => {
  it('removes status entirely', () => {
    const result = parse(stripForDuplicate(FULL_MANIFEST))
    expect(result).not.toHaveProperty('status')
  })

  it('removes the server-assigned metadata fields', () => {
    const result = parse(stripForDuplicate(FULL_MANIFEST))
    for (const field of [
      'uid',
      'resourceVersion',
      'creationTimestamp',
      'generation',
      'managedFields',
      'selfLink',
      'ownerReferences',
    ]) {
      expect(result.metadata, `metadata.${field} should be gone`).not.toHaveProperty(field)
    }
  })

  it('removes only kubectl’s last-applied-configuration annotation', () => {
    const result = parse(stripForDuplicate(FULL_MANIFEST))
    expect(result.metadata.annotations).not.toHaveProperty(
      'kubectl.kubernetes.io/last-applied-configuration',
    )
    // The annotation next to it is an operator's own, not the server's, and
    // has no reason to disappear along with it.
    expect(result.metadata.annotations['custom.io/owner']).toBe('team-payments')
  })

  it('clears the name and leaves a comment saying to pick one', () => {
    const output = stripForDuplicate(FULL_MANIFEST)
    expect(output).toMatch(/name:\s*(""|'')\s*#.*name/i)
    expect(parse(output).metadata.name).toBe('')
  })

  it('preserves labels, the surviving annotation, and the whole spec verbatim', () => {
    const result = parse(stripForDuplicate(FULL_MANIFEST))
    expect(result.metadata.labels).toEqual({ app: 'web', tier: 'backend' })
    expect(result.metadata.namespace).toBe('billing')
    expect(result.spec).toEqual({
      replicas: 3,
      selector: { matchLabels: { app: 'web' } },
      template: { metadata: { labels: { app: 'web' } }, spec: { containers: [{ name: 'app', image: 'web:1.2.3' }] } },
    })
    // Not just the value — the comment sitting on it, since "verbatim" means
    // more than the parsed data.
    expect(stripForDuplicate(FULL_MANIFEST)).toContain('# pinned by the release pipeline')
  })

  it('removes a Service’s allocated ClusterIP, and only for a Service', () => {
    const service = `apiVersion: v1
kind: Service
metadata:
  name: web
spec:
  type: ClusterIP
  clusterIP: 10.96.0.5
  clusterIPs:
    - 10.96.0.5
  selector:
    app: web
  ports:
    - port: 80
`
    const result = parse(stripForDuplicate(service))
    expect(result.spec).not.toHaveProperty('clusterIP')
    expect(result.spec).not.toHaveProperty('clusterIPs')
    expect(result.spec.selector).toEqual({ app: 'web' })

    // A Deployment has no clusterIP to begin with; the field list is keyed
    // by kind so this must be a no-op rather than an error for anything else.
    expect(() => stripForDuplicate(FULL_MANIFEST)).not.toThrow()
  })

  it('removes a bound PersistentVolumeClaim’s volumeName', () => {
    const pvc = `apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: data
spec:
  accessModes:
    - ReadWriteOnce
  resources:
    requests:
      storage: 10Gi
  volumeName: pvc-3f1c2b4a
  storageClassName: gp3
`
    const result = parse(stripForDuplicate(pvc))
    expect(result.spec).not.toHaveProperty('volumeName')
    expect(result.spec.storageClassName).toBe('gp3')
  })

  it('removes a Pod’s nodeName', () => {
    const pod = `apiVersion: v1
kind: Pod
metadata:
  name: web-abc123
spec:
  nodeName: node-3
  containers:
    - name: app
      image: web:1.2.3
`
    const result = parse(stripForDuplicate(pod))
    expect(result.spec).not.toHaveProperty('nodeName')
    expect(result.spec.containers).toEqual([{ name: 'app', image: 'web:1.2.3' }])
  })

  it('leaves a manifest with none of these fields alone but for the name', () => {
    // No status, no server-assigned metadata, no annotations map at all —
    // the ordinary shape of something a person wrote by hand rather than one
    // read back from a live cluster. Nothing here should throw reaching for
    // a field that was never there.
    const minimal = `apiVersion: v1
kind: ConfigMap
metadata:
  name: settings
data:
  color: blue
`
    const result = parse(stripForDuplicate(minimal))
    expect(result).toEqual({ apiVersion: 'v1', kind: 'ConfigMap', metadata: { name: '' }, data: { color: 'blue' } })
  })

  it('returns manifests it cannot parse unchanged, rather than losing them', () => {
    const broken = 'metadata:\n  name: [oops'
    expect(stripForDuplicate(broken)).toBe(broken)
  })
})
