/**
 * Skeleton manifests for creating a new object from the navigator.
 *
 * `skeletonFor` is what the editor opens with when there is no object yet —
 * modelled on what `kubectl create --dry-run=client -o yaml` prints for a
 * kind it has a generator for: the minimum a real API server will accept,
 * `metadata.name` left blank for the operator to fill in, and nothing
 * invented that Kubernetes does not also require. Comments are placed the
 * way kubectl's own generators would place them if they had any — one line,
 * next to the field it is about — never a paragraph.
 *
 * Everything here is a STARTING POINT, not a validated request: the object
 * this produces still goes through the same `updateResource` apply path as a
 * hand-written manifest, and the server has the final say.
 */

import { ALL_NAMESPACES, type ResourceKind } from './api/client'

/** The API group/version string as the API server writes it. See
    `domain.ResourceKind.GroupVersion` on the Go side, which this mirrors. */
function apiVersion(kind: ResourceKind): string {
  return kind.group ? `${kind.group}/${kind.version}` : kind.version
}

/**
 * `apiVersion`, `kind` and `metadata`, common to every skeleton.
 *
 * The namespace line is omitted for a cluster-scoped kind, and also when no
 * concrete namespace is selected — "all namespaces" is a filter, not a place
 * an object can live, so writing it in would mean copying ALL_NAMESPACES'
 * empty string into a manifest as if it meant something.
 */
function header(kind: ResourceKind, namespace: string | null): string {
  const lines = [`apiVersion: ${apiVersion(kind)}`, `kind: ${kind.kind}`, 'metadata:', '  name: "" # name required']
  if (kind.namespaced && namespace && namespace !== ALL_NAMESPACES) {
    lines.push(`  namespace: ${namespace}`)
  }
  return lines.join('\n')
}

/**
 * Shifts every line of a shared fragment right by `spaces`.
 *
 * CONTAINER and SELECTOR_AND_TEMPLATE_LABELS are spliced in under a
 * `containers:` (or `spec:`) key that sits at a different column depending on
 * the kind — CronJob nests a pod spec one level deeper than the rest, because
 * it does not own pods, it owns Jobs that do (see CLAUDE.md). Writing the
 * fragment once at column zero and indenting it per call site is what keeps
 * the two copies from drifting apart the way two hand-indented ones would.
 */
function indent(block: string, spaces: number): string {
  const pad = ' '.repeat(spaces)
  return block
    .split('\n')
    .map((line) => pad + line)
    .join('\n')
}

/** One container's worth of requests/limits and a probe placeholder — the
    part of a workload skeleton that repeats across every controller that
    runs pods. Column zero; see `indent`. */
const CONTAINER = `- name: app
  image: nginx:1.27 # replace with the image to run
  resources:
    requests:
      cpu: 100m
      memory: 128Mi
    limits:
      cpu: 500m
      memory: 256Mi
  readinessProbe:
    httpGet:
      path: /healthz # replace with a real endpoint
      port: 8080
    initialDelaySeconds: 5`

/** `spec.selector` and `spec.template.metadata`, shared by the three
    controllers that select pods by a label rather than by a claim
    (Deployment, StatefulSet, DaemonSet). The two `app: my-app` lines must
    agree with EACH OTHER, not with `metadata.name` — Kubernetes never
    requires the third. */
const SELECTOR_AND_TEMPLATE_LABELS = `  selector:
    matchLabels:
      app: my-app # must match spec.template.metadata.labels below
  template:
    metadata:
      labels:
        app: my-app`

/** Specs for the kinds common enough to be worth a real starting point.
    Keyed by `Kind` — the CamelCase name, e.g. "Deployment" — because that is
    what identifies a well-known shape independent of which API group serves
    it. Everything not listed here falls through to the generic body below. */
const SPECS: Record<string, string> = {
  Deployment: `spec:
  replicas: 1
${SELECTOR_AND_TEMPLATE_LABELS}
    spec:
      containers:
${indent(CONTAINER, 8)}`,

  StatefulSet: `spec:
  serviceName: "" # the headless Service that governs this StatefulSet
  replicas: 1
${SELECTOR_AND_TEMPLATE_LABELS}
    spec:
      containers:
${indent(CONTAINER, 8)}`,

  // No replicas: a DaemonSet runs exactly one pod per matching node.
  DaemonSet: `spec:
${SELECTOR_AND_TEMPLATE_LABELS}
    spec:
      containers:
${indent(CONTAINER, 8)}`,

  Job: `spec:
  template:
    spec:
      containers:
${indent(CONTAINER, 8)}
      restartPolicy: Never # a Job's pod is replaced, never restarted in place; OnFailure also valid`,

  // Nests one level deeper than Job — spec.jobTemplate.spec.template.spec —
  // because a CronJob does not own pods, it owns Jobs that own pods.
  CronJob: `spec:
  schedule: "*/5 * * * *" # cron schedule, in the cluster's time zone
  jobTemplate:
    spec:
      template:
        spec:
          containers:
${indent(CONTAINER, 12)}
          restartPolicy: Never`,

  Service: `spec:
  type: ClusterIP
  selector:
    app: my-app # the pods this Service routes to
  ports:
    - port: 80
      targetPort: 8080
      protocol: TCP`,

  ConfigMap: `data:
  key: value`,

  // stringData is written as plain text; the server base64-encodes it into
  // .data on apply, the same as `kubectl create secret generic`.
  Secret: `type: Opaque
stringData:
  key: value`,

  Ingress: `spec:
  ingressClassName: "" # the cluster's IngressClass, e.g. nginx
  rules:
    - host: example.com
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: my-service # the Service this path routes to
                port:
                  number: 80`,

  // Denies all inbound traffic to the selected pods — the empty podSelector
  // means every pod in the namespace — and adds no ingress rule, which is
  // what makes it deny-all rather than a no-op. Add a `from:` under
  // `ingress:` to allow specific traffic back in.
  NetworkPolicy: `spec:
  podSelector: {} # every pod in the namespace
  policyTypes:
    - Ingress`,

  PersistentVolumeClaim: `spec:
  accessModes:
    - ReadWriteOnce
  resources:
    requests:
      storage: 1Gi
  # storageClassName: "" # omit to use the cluster's default StorageClass`,

  ServiceAccount: '',

  Role: `rules:
  - apiGroups: [""]
    resources: ["pods"]
    verbs: ["get", "list", "watch"]`,

  RoleBinding: `subjects:
  - kind: ServiceAccount
    name: "" # the account being granted this role
    namespace: "" # that account's own namespace
roleRef:
  kind: Role # or ClusterRole
  name: "" # the Role being bound
  apiGroup: rbac.authorization.k8s.io`,

  HorizontalPodAutoscaler: `spec:
  scaleTargetRef:
    apiVersion: apps/v1
    kind: Deployment # or StatefulSet
    name: "" # the workload to scale
  minReplicas: 1
  maxReplicas: 5
  metrics:
    - type: Resource
      resource:
        name: cpu
        target:
          type: Utilization
          averageUtilization: 80`,

  PodDisruptionBudget: `spec:
  minAvailable: 1 # or maxUnavailable, not both
  selector:
    matchLabels:
      app: my-app # the workload this budget protects`,

  // No spec: kubectl's own generator emits none, and Namespace has nothing
  // worth prefilling — `spec.finalizers` is set by controllers, not by hand.
  Namespace: '',
}

/**
 * A YAML manifest to seed the "New <Kind>" editor with.
 *
 * Everything a real object of this kind needs to be ACCEPTED, and nothing
 * more — an operator fills in the name and whatever the comments call out,
 * then Apply sends it through the same `updateResource` path as any other
 * edit. A kind this function does not recognise — every custom resource, and
 * any built-in it has not been taught — gets an empty `spec` and says so,
 * because guessing a schema PodSteer was never told would be worse than
 * leaving it blank.
 */
export function skeletonFor(kind: ResourceKind, namespace: string | null): string {
  const body = SPECS[kind.kind]

  if (body === undefined) {
    return `${header(kind, namespace)}
spec: {} # PodSteer does not know this kind's schema — the server will validate this on apply
`
  }

  if (body === '') {
    return `${header(kind, namespace)}\n`
  }

  return `${header(kind, namespace)}\n${body}\n`
}
