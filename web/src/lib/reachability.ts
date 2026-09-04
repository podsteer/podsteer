/**
 * What a reachability panel offers, and how it reads what came back.
 *
 * The rules that decide whether a probe is possible, what it does and what its
 * result means all live in `app/domain/reachability.go`, where they can be
 * argued with in a test. Everything here is the other half: choosing which
 * ports and vantages to OFFER out of the manifest already on screen, and
 * turning a finished result into words. That is quotation and presentation,
 * which is exactly the line ResourceOverview already sits on.
 *
 * One rule runs through all of it: A RESULT ALWAYS SAYS WHERE IT CAME FROM.
 * "Reachable" on its own is the least useful true statement this feature could
 * make — the API server's proxy reaching a Service says nothing about whether
 * a workload can, and a container reaching it says nothing about whether you
 * can. So there is no code path here that renders an outcome without its
 * vantage beside it.
 */

import type { ProbeResult, ProbeStep, ProbeSubjectInput } from '$lib/api/client'
import { ingressRoutes } from '$lib/ingress'

/** A vantage a probe can be performed from. */
export type Vantage = 'local' | 'in_cluster'

/** One port on one object, offered as something to probe. */
export interface ProbeTarget {
  /** A stable key for the option, unique within one object. */
  key: string
  /** What the option says in the list. */
  label: string
  /** The port to aim at. */
  port: number
  /** The port's name, which is the only hint about the protocol. */
  portName: string
  /** The port's protocol, empty meaning TCP as Kubernetes defines it. */
  protocol: string
  /** The Ingress host this option is for, empty for anything else. */
  host: string
  /** Whether TLS terminates for that host. */
  tls: boolean
}

/** How a vantage is offered, and what an answer from it would mean. */
export interface VantageOption {
  vantage: Vantage
  label: string
  /**
   * One line saying what a success from here actually establishes. Not
   * decoration: it is the difference between the two vantages, and it is the
   * whole reason both exist.
   */
  meaning: string
  /** Whether this vantage can answer for this kind at all. */
  available: boolean
  /** Why not, when it cannot. Shown instead of the control. */
  unavailableReason: string
}

interface PortSpec {
  name?: string
  port?: number
  targetPort?: number | string
  containerPort?: number
  protocol?: string
}

interface ProbeManifest {
  metadata?: { name?: string; namespace?: string }
  spec?: {
    type?: string
    clusterIP?: string
    ports?: PortSpec[]
    containers?: { name?: string; ports?: PortSpec[] }[]
  }
  status?: { podIP?: string }
}

/** The kinds a probe is offered on. Nothing else has an address to aim at. */
export function isProbeableKind(kind: string | undefined): boolean {
  return kind === 'Service' || kind === 'Pod' || kind === 'Ingress'
}

/**
 * Every port on this object worth offering, quoted out of the manifest.
 *
 * A UDP port is offered and refused by the backend rather than hidden, on
 * purpose: an operator looking for a DNS Service's port and not finding it
 * would reasonably conclude the panel is broken, where a refusal naming the
 * protocol answers the question they actually had.
 */
export function probeTargets(kind: string | undefined, manifest: unknown): ProbeTarget[] {
  const parsed = (manifest ?? null) as ProbeManifest | null
  if (!parsed) return []

  switch (kind) {
    case 'Service':
      return servicePorts(parsed)
    case 'Pod':
      return podPorts(parsed)
    case 'Ingress':
      return ingressTargets(manifest)
    default:
      return []
  }
}

function servicePorts(manifest: ProbeManifest): ProbeTarget[] {
  return (manifest.spec?.ports ?? [])
    .filter((port) => typeof port.port === 'number')
    .map((port, index) => ({
      key: `svc-${port.name ?? index}-${port.port}`,
      label: portLabel(port.name, port.port as number, port.protocol),
      port: port.port as number,
      portName: port.name ?? '',
      protocol: port.protocol ?? '',
      host: '',
      tls: false,
    }))
}

/**
 * A pod's ports are its CONTAINERS' declared ports, and the container's name
 * rides the label — two containers routinely declare 8080, and an option list
 * that said "8080" twice would be one nobody could choose from.
 */
function podPorts(manifest: ProbeManifest): ProbeTarget[] {
  const targets: ProbeTarget[] = []

  for (const container of manifest.spec?.containers ?? []) {
    for (const [index, port] of (container.ports ?? []).entries()) {
      if (typeof port.containerPort !== 'number') continue
      targets.push({
        key: `pod-${container.name ?? index}-${port.containerPort}`,
        label: `${container.name ?? 'container'} · ${portLabel(port.name, port.containerPort, port.protocol)}`,
        port: port.containerPort,
        portName: port.name ?? '',
        protocol: port.protocol ?? '',
        host: '',
        tls: false,
      })
    }
  }

  return targets
}

/**
 * An Ingress is probed by HOST, not by port: the port follows from whether
 * TLS terminates for that host, which is what `ingressRoutes` already decides
 * by reading `spec.tls` rather than guessing.
 *
 * One option per distinct host. The paths under it are a routing question, and
 * a connect attempt cannot tell one path from another.
 */
function ingressTargets(manifest: unknown): ProbeTarget[] {
  const seen = new Set<string>()
  const targets: ProbeTarget[] = []

  for (const route of ingressRoutes(manifest)) {
    // A rule with no host matches whatever reaches the controller. There is no
    // name to resolve and nothing to aim at, so it is not offered.
    if (!route.host || seen.has(route.host)) continue
    seen.add(route.host)

    targets.push({
      key: `ing-${route.host}`,
      label: route.secure ? `${route.host} (https)` : `${route.host} (http)`,
      port: route.secure ? 443 : 80,
      portName: route.secure ? 'https' : 'http',
      protocol: 'TCP',
      host: route.host,
      tls: route.secure,
    })
  }

  return targets
}

function portLabel(name: string | undefined, port: number, protocol: string | undefined): string {
  const suffix = protocol && protocol.toUpperCase() !== 'TCP' ? ` ${protocol.toUpperCase()}` : ''
  return name ? `${name} · ${port}${suffix}` : `${port}${suffix}`
}

/**
 * The subject the backend plans a probe from — every field a quotation of the
 * manifest already on screen, so choosing a target costs no request.
 */
export function probeSubject(
  kind: string,
  manifest: unknown,
  target: ProbeTarget,
): ProbeSubjectInput {
  const parsed = (manifest ?? null) as ProbeManifest | null

  return {
    kind,
    namespace: parsed?.metadata?.namespace ?? '',
    name: parsed?.metadata?.name ?? '',
    serviceType: parsed?.spec?.type ?? '',
    clusterIp: parsed?.spec?.clusterIP ?? '',
    podIp: parsed?.status?.podIP ?? '',
    host: target.host,
    port: target.port,
    portName: target.portName,
    protocol: target.protocol,
    tls: target.tls,
  } as ProbeSubjectInput
}

/**
 * Which vantages this kind can be asked from, and what each one would mean.
 *
 * The Ingress case is the one worth reading. Its local vantage is not merely
 * unimplemented: reaching a public host would mean opening a connection to
 * something that is not an API server, and the only outbound destinations
 * PodSteer has are the clusters the kubeconfig names and, for the update
 * check, GitHub. A browser is the tool for that address, and the panel says so
 * rather than leaving an empty control an operator would keep pressing.
 */
export function vantageOptions(kind: string | undefined): VantageOption[] {
  const local: VantageOption = {
    vantage: 'local',
    label: 'From this machine',
    meaning: '',
    available: true,
    unavailableReason: '',
  }
  const inCluster: VantageOption = {
    vantage: 'in_cluster',
    label: 'From inside the cluster',
    meaning:
      'Runs one bounded connect attempt in a container you choose, so cluster DNS and any NetworkPolicy in the path are exercised the way a workload experiences them.',
    available: true,
    unavailableReason: '',
  }

  switch (kind) {
    case 'Service':
      local.meaning =
        "Asks the API server's own service proxy to fetch the port. A success means the endpoints answer the API server — not that anything else in the cluster may reach them."
      return [local, inCluster]

    case 'Pod':
      local.meaning =
        'Opens an ephemeral port-forward through the API server, connects to it, and tears it down again. A real connection from here, to that one pod.'
      return [local, inCluster]

    case 'Ingress':
      local.available = false
      local.unavailableReason =
        'Reaching a public address would mean connecting to a host that is not an API server, and PodSteer only ever talks to the clusters your kubeconfig names. Open it in a browser, or probe from inside the cluster.'
      return [local, inCluster]

    default:
      return []
  }
}

/** How an outcome should read, for the panel's own colouring. */
export type OutcomeTone = 'good' | 'bad' | 'unknown'

/**
 * A failed probe is an ANSWER, not an error, so it is toned as a finding
 * rather than as something broken. What is toned "unknown" is the probe that
 * never got far enough to say — which is a different thing again from a
 * target that refused.
 */
export function outcomeTone(outcome: string): OutcomeTone {
  switch (outcome) {
    case 'reachable':
      return 'good'
    case 'refused':
    case 'name_not_resolved':
      return 'bad'
    default:
      return 'unknown'
  }
}

/** The heading one step goes under. */
export function stepLabel(step: ProbeStep): string {
  switch (step.name) {
    case 'dns':
      return 'Name resolution'
    case 'connect':
      return 'Connection'
    case 'http':
      return 'HTTP request'
    default:
      return step.name
  }
}

/**
 * Whether a step should read as a problem.
 *
 * SKIPPED IS NOT A FAILURE, and keeping those apart is the point of having
 * three statuses rather than a boolean: an address literal has nothing to
 * resolve, and rendering that as a failed resolution would send somebody to
 * look at their cluster's DNS over a step nothing performed.
 */
export function stepTone(step: ProbeStep): OutcomeTone {
  switch (step.status) {
    case 'ok':
      return 'good'
    case 'failed':
      return 'bad'
    default:
      return 'unknown'
  }
}

/** Where an answer came from, in a form that fits above the steps. */
export function routeLabel(result: ProbeResult): string {
  switch (result.route) {
    case 'service_proxy':
      return "through the API server's service proxy"
    case 'port_forward':
      return 'through an ephemeral port-forward'
    case 'exec':
      return 'from inside a container'
    default:
      return ''
  }
}

/**
 * The line under a result saying how long ago it was taken.
 *
 * A probe never repeats itself, so an answer on screen is always from some
 * moment in the past, and saying which is the same job `SeriesResult.spanSeconds`
 * does for the sampled charts: the alternative is a panel that quietly implies
 * it is live.
 */
export function takenAgo(takenAt: number, now: number): string {
  const seconds = Math.max(0, Math.round((now - takenAt) / 1000))
  if (seconds < 5) return 'just now'
  if (seconds < 60) return `${seconds}s ago`

  const minutes = Math.round(seconds / 60)
  if (minutes < 60) return `${minutes}m ago`

  return `${Math.round(minutes / 60)}h ago`
}
