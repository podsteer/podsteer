/**
 * Renders a container's spec the way `kubectl describe` renders it.
 *
 * These are FORMATTERS, not judgements, which is why they live in the
 * frontend at all: every one of them composes fields that are already on
 * screen in the YAML tab into the string an operator recognises. Anything
 * that compares, thresholds or concludes — a probe delay measured against
 * observed startup, a tag identified as mutable — belongs in the Go domain
 * where it can be argued about in a test.
 *
 * The strings deliberately match kubectl's own. Operators have read
 * `delay=10s timeout=1s period=10s #success=1 #failure=3` ten thousand times
 * and can scan it without reading it; inventing a prettier format costs them
 * that and buys nothing.
 */

/** One container's declared port. */
interface Port {
  name?: string
  containerPort: number
  hostPort?: number
  protocol?: string
}

/** A probe, in any of the four shapes Kubernetes allows. */
interface Probe {
  httpGet?: { path?: string; port?: number | string; host?: string; scheme?: string }
  exec?: { command?: string[] }
  tcpSocket?: { host?: string; port?: number | string }
  grpc?: { port?: number; service?: string }
  initialDelaySeconds?: number
  timeoutSeconds?: number
  periodSeconds?: number
  successThreshold?: number
  failureThreshold?: number
}

/** An environment variable, from a literal or from somewhere else. */
interface EnvVar {
  name: string
  value?: string
  valueFrom?: {
    fieldRef?: { apiVersion?: string; fieldPath?: string }
    resourceFieldRef?: { containerName?: string; resource?: string; divisor?: string }
    secretKeyRef?: { name?: string; key?: string; optional?: boolean }
    configMapKeyRef?: { name?: string; key?: string; optional?: boolean }
  }
}

interface VolumeMount {
  name: string
  mountPath: string
  readOnly?: boolean
  subPath?: string
}

/** Ports as "8080/TCP", or named as "http 8080/TCP". */
export function formatPorts(ports: Port[] | undefined): string {
  if (!ports?.length) return ''
  return ports
    .map((port) => {
      const base = `${port.containerPort}/${port.protocol ?? 'TCP'}`
      const named = port.name ? `${port.name} ${base}` : base
      // A host port is worth calling out: it binds the node's own interface
      // and is why a second replica will not schedule beside the first.
      return port.hostPort ? `${named} → host ${port.hostPort}` : named
    })
    .join(', ')
}

/**
 * A probe in kubectl's own words.
 *
 * ALL FOUR HANDLER SHAPES, which is where competitors fall down: Headlamp
 * renders only httpGet and exec — a tcpSocket probe renders as nothing at all
 * and an exec probe as the literal string "undefined" — and neither it nor
 * Freelens nor Octant handles gRPC, which has been a probe type since 1.24.
 * A probe silently missing from a pane is worse than no probe section: it
 * reads as "this container has no liveness check".
 */
export function formatProbe(probe: Probe | undefined): string {
  if (!probe) return ''

  let handler: string
  if (probe.httpGet) {
    const scheme = (probe.httpGet.scheme ?? 'HTTP').toLowerCase()
    const host = probe.httpGet.host ?? ''
    handler = `http-get ${scheme}://${host}:${probe.httpGet.port ?? ''}${probe.httpGet.path ?? ''}`
  } else if (probe.exec) {
    handler = `exec [${(probe.exec.command ?? []).join(' ')}]`
  } else if (probe.tcpSocket) {
    handler = `tcp-socket ${probe.tcpSocket.host ?? ''}:${probe.tcpSocket.port ?? ''}`
  } else if (probe.grpc) {
    handler = `grpc :${probe.grpc.port}${probe.grpc.service ? ` ${probe.grpc.service}` : ''}`
  } else {
    // An unknown handler is a Kubernetes version newer than this code. Say so
    // rather than rendering an empty probe, which reads as "no probe".
    handler = 'unrecognised probe type'
  }

  const timings = [
    `delay=${probe.initialDelaySeconds ?? 0}s`,
    `timeout=${probe.timeoutSeconds ?? 1}s`,
    `period=${probe.periodSeconds ?? 10}s`,
    `#success=${probe.successThreshold ?? 1}`,
    `#failure=${probe.failureThreshold ?? 3}`,
  ].join(' ')

  return `${handler} ${timings}`
}

/**
 * An environment variable's value, or a description of where it comes from.
 *
 * NO SECRET VALUE IS EVER RETURNED HERE, and no API call is made to find one.
 * This is exactly what `kubectl describe` prints — the reference, the key and
 * the source object — and it is the right default for three separate reasons.
 *
 * It is what an operator already recognises. It requires no `get secrets`
 * permission, which many people deliberately do not hold. And resolving
 * eagerly would fire a Secret read for every referenced Secret the moment a
 * pane opened, which is precisely the pattern Kubernetes' own Secret
 * good-practices page tells cluster operators to raise an alert on, and which
 * Falco ships a rule for, enabled, at ERROR severity.
 *
 * There is a correctness argument too, and it is the stronger one. The value
 * in the Secret is NOT necessarily the value in the running process:
 * environment variables are injected when the container starts and are never
 * updated afterwards, so a Secret edited since then has a current value the
 * process has never seen. Showing it labelled as the pod's environment is
 * wrong, not merely risky.
 */
export function formatEnvValue(variable: EnvVar, pod?: PodManifest): string {
  if (variable.value !== undefined) return variable.value

  const from = variable.valueFrom
  if (!from) return ''

  // The downward API is not a mystery: every path it can carry is a field of
  // the pod this pane is already showing, so it is RESOLVED rather than
  // printed as the path. `<metadata.name>` is what kubectl prints because
  // kubectl is describing a template; this is describing a running pod, and
  // the running pod knows its own name.
  //
  // Still a quotation, not a conclusion — the value is lifted verbatim out of
  // the manifest on screen. Anything that will not resolve keeps the path, so
  // an unfamiliar field degrades to what kubectl would have said.
  if (from.fieldRef?.fieldPath) {
    const resolved = resolveFieldPath(pod, from.fieldRef.fieldPath)
    if (resolved !== null) return resolved
  }
  if (from.resourceFieldRef?.resource) {
    const resolved = resolveResourceField(pod, from.resourceFieldRef)
    if (resolved !== null) return resolved
  }

  if (from.secretKeyRef) {
    // kubectl's exact wording, including the asymmetry with config maps
    // below — "in secret" against "of config map". It is upstream's, not a
    // typo here.
    return `<set to the key '${from.secretKeyRef.key}' in secret '${from.secretKeyRef.name}'>`
  }
  if (from.configMapKeyRef) {
    return `<set to the key '${from.configMapKeyRef.key}' of config map '${from.configMapKeyRef.name}'>`
  }
  if (from.fieldRef) {
    return `<${from.fieldRef.fieldPath}>`
  }
  if (from.resourceFieldRef) {
    return `<${from.resourceFieldRef.resource}${
      from.resourceFieldRef.containerName ? ` of ${from.resourceFieldRef.containerName}` : ''
    }>`
  }
  return ''
}

/** The parts of a pod manifest the downward API can name. */
export interface PodManifest {
  metadata?: {
    name?: string
    namespace?: string
    uid?: string
    labels?: Record<string, string>
    annotations?: Record<string, string>
  }
  spec?: {
    nodeName?: string
    serviceAccountName?: string
    containers?: { name?: string; resources?: ContainerResources }[]
    initContainers?: { name?: string; resources?: ContainerResources }[]
  }
  status?: { podIP?: string; hostIP?: string }
}

interface ContainerResources {
  requests?: Record<string, string>
  limits?: Record<string, string>
}

/**
 * The value behind a downward-API field path.
 *
 * Only the paths Kubernetes actually allows in a fieldRef, matched exactly
 * rather than by walking the object: a general path walker would happily
 * resolve `spec.containers[0].image`, which the API server would have
 * REFUSED — so the pane would be showing a value the pod could never have
 * been given. Null for anything unrecognised, which keeps the path on screen.
 */
function resolveFieldPath(pod: PodManifest | undefined, path: string): string | null {
  if (!pod) return null

  const keyed = /^metadata\.(labels|annotations)\['(.+)'\]$/.exec(path)
  if (keyed) {
    const source = keyed[1] === 'labels' ? pod.metadata?.labels : pod.metadata?.annotations
    return source?.[keyed[2]] ?? null
  }

  switch (path) {
    case 'metadata.name':
      return pod.metadata?.name ?? null
    case 'metadata.namespace':
      return pod.metadata?.namespace ?? null
    case 'metadata.uid':
      return pod.metadata?.uid ?? null
    case 'spec.nodeName':
      return pod.spec?.nodeName ?? null
    case 'spec.serviceAccountName':
      return pod.spec?.serviceAccountName ?? null
    case 'status.podIP':
    case 'status.podIPs':
      return pod.status?.podIP ?? null
    case 'status.hostIP':
      return pod.status?.hostIP ?? null
    default:
      return null
  }
}

/**
 * The value behind a resourceFieldRef — a container's own request or limit.
 *
 * The container name is optional in the spec and means "this one", but this
 * has no way to know which one is being rendered, so an unnamed reference is
 * left as the path rather than guessed at.
 */
function resolveResourceField(
  pod: PodManifest | undefined,
  ref: { containerName?: string; resource?: string; divisor?: string },
): string | null {
  if (!pod || !ref.containerName || !ref.resource) return null

  const containers = [...(pod.spec?.containers ?? []), ...(pod.spec?.initContainers ?? [])]
  const container = containers.find((entry) => entry.name === ref.containerName)
  if (!container) return null

  const [scope, resource] = ref.resource.split('.')
  const declared =
    scope === 'limits' ? container.resources?.limits : container.resources?.requests
  const quantity = declared?.[resource]
  if (quantity === undefined) return null

  // THE DIVISOR IS THE POINT OF THE REFERENCE, AND IT WAS IGNORED. Kubernetes
  // divides the quantity by the divisor and rounds UP to a whole number, so
  // the standard `limits.memory` with `divisor: 1Mi` — how everybody feeds a
  // heap size to a JVM — gave the process `128` while this pane printed
  // `128Mi`, and `limits.cpu: 500m` with no divisor gives the process `1`
  // while this printed `500m`. Two different numbers under one name, with
  // nothing marking which was which.
  return divideQuantity(quantity, ref.divisor)
}

/**
 * Applies a downward-API divisor, the way the kubelet does.
 *
 * The default divisor is `1` for every resource — which for CPU means whole
 * cores and for memory means bytes — and the result is always rounded up to
 * an integer. Anything this cannot parse is returned as written rather than
 * guessed at: a wrong number here is worse than an unconverted one, because
 * it looks like the value the process actually received.
 */
function divideQuantity(quantity: string, divisor: string | undefined): string {
  const value = parseQuantity(quantity)
  if (value === null) return quantity

  const by = divisor ? parseQuantity(divisor) : 1
  if (by === null || by <= 0) return quantity

  return String(Math.ceil(value / by))
}

/** Kubernetes quantity suffixes, as multipliers of the base unit. */
const QUANTITY_SUFFIXES: Record<string, number> = {
  n: 1e-9,
  u: 1e-6,
  m: 1e-3,
  '': 1,
  k: 1e3,
  M: 1e6,
  G: 1e9,
  T: 1e12,
  P: 1e15,
  E: 1e18,
  Ki: 1024,
  Mi: 1024 ** 2,
  Gi: 1024 ** 3,
  Ti: 1024 ** 4,
  Pi: 1024 ** 5,
  Ei: 1024 ** 6,
}

/** One Kubernetes quantity as a number, or null when it is not one. */
function parseQuantity(raw: string): number | null {
  const match = /^(\d+(?:\.\d+)?)([a-zA-Z]*)$/.exec(raw.trim())
  if (!match) return null

  const multiplier = QUANTITY_SUFFIXES[match[2]]
  if (multiplier === undefined) return null
  return Number(match[1]) * multiplier
}

/** Whether a variable's value came from a Secret, for the redaction badge. */
export function isFromSecret(variable: EnvVar): boolean {
  return Boolean(variable.valueFrom?.secretKeyRef)
}

/**
 * How confident we are that a LITERAL value is a credential.
 *
 * This is where real leaks live. Everybody thinks about `secretKeyRef`; a
 * plaintext `value:` holding an AWS key or a JWT is in the pod spec itself,
 * readable by anyone who can get the pod, and no tool in this category masks
 * it — because it is not "a secret" as far as Kubernetes is concerned.
 *
 * TWO CONFIDENCES, BECAUSE ONE OF THEM IS A GUESS AND THE PANE SAID IT WAS A
 * FACT. A PEM header or an AWS key id is what it looks like. A NAME matching
 * /secret|token|.../ is a hint about the name, not about the value, and it
 * caught things like `SECRET_MANAGER_ENDPOINT=https://vault.internal.example`
 * — masked, and captioned "a literal credential, written into the pod spec in
 * the clear", which is a false accusation about somebody's workload with no
 * way to see the value and disagree.
 *
 * Deliberately conservative in both tiers: masking half an environment as
 * suspected secrets trains people to reveal everything by reflex, which is
 * worse than the occasional credential going unmasked.
 */
export type Sensitivity = 'certain' | 'suspected'

export function sensitivity(variable: EnvVar): Sensitivity | null {
  const value = variable.value
  if (!value || value.length < 16) return null

  // Unmistakable shapes: a PEM block, an AWS access key, a JWT.
  if (/^-----BEGIN /.test(value)) return 'certain'
  if (/^(AKIA|ASIA)[0-9A-Z]{16}/.test(value)) return 'certain'
  if (/^eyJ[A-Za-z0-9_-]{10,}\./.test(value)) return 'certain'

  // A name that announces itself, holding something long enough to be real.
  if (!/(password|passwd|secret|token|apikey|api_key|private_key)/i.test(variable.name)) {
    return null
  }
  // ...but not when the value announces it is something else. A URL, a path,
  // a host list and a boolean are none of them credentials however the
  // variable is named, and calling them credentials is what made the caption
  // untrue.
  if (/^[a-z][a-z0-9+.-]*:\/\//i.test(value)) return null
  if (/^\//.test(value)) return null
  if (/[\s,]/.test(value)) return null
  return 'suspected'
}

/** Whether a literal value should be masked at all. */
export function looksSensitive(variable: EnvVar): boolean {
  return sensitivity(variable) !== null
}

/** Mounts as "/etc/config from settings (ro)", kubectl's own form. */
export function formatMount(mount: VolumeMount): string {
  const mode = mount.readOnly ? 'ro' : 'rw'
  const sub = mount.subPath ? `,path="${mount.subPath}"` : ''
  return `${mount.mountPath} from ${mount.name} (${mode}${sub})`
}
