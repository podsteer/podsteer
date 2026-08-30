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
    resourceFieldRef?: { containerName?: string; resource?: string }
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
export function formatEnvValue(variable: EnvVar): string {
  if (variable.value !== undefined) return variable.value

  const from = variable.valueFrom
  if (!from) return ''

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

/** Whether a variable's value came from a Secret, for the redaction badge. */
export function isFromSecret(variable: EnvVar): boolean {
  return Boolean(variable.valueFrom?.secretKeyRef)
}

/**
 * Whether a LITERAL value looks like a credential somebody pasted in.
 *
 * This is where real leaks live. Everybody thinks about `secretKeyRef`; a
 * plaintext `value:` holding an AWS key or a JWT is in the pod spec itself,
 * readable by anyone who can get the pod, and no tool in this category masks
 * it — because it is not "a secret" as far as Kubernetes is concerned.
 *
 * Deliberately conservative. Matching a few unmistakable shapes and a high
 * entropy threshold means the occasional credential slips through masked as
 * ordinary text, which is the acceptable failure; masking half the
 * environment as suspected secrets would train people to reveal everything by
 * reflex, which is the unacceptable one.
 */
export function looksSensitive(variable: EnvVar): boolean {
  const value = variable.value
  if (!value || value.length < 16) return false

  // Unmistakable prefixes: a PEM block, an AWS access key, a JWT.
  if (/^-----BEGIN /.test(value)) return true
  if (/^(AKIA|ASIA)[0-9A-Z]{16}/.test(value)) return true
  if (/^eyJ[A-Za-z0-9_-]{10,}\./.test(value)) return true

  // A name that announces itself, holding something long enough to be real.
  if (/(password|passwd|secret|token|apikey|api_key|private_key)/i.test(variable.name)) return true

  return false
}

/** Mounts as "/etc/config from settings (ro)", kubectl's own form. */
export function formatMount(mount: VolumeMount): string {
  const mode = mount.readOnly ? 'ro' : 'rw'
  const sub = mount.subPath ? `,path="${mount.subPath}"` : ''
  return `${mount.mountPath} from ${mount.name} (${mode}${sub})`
}
