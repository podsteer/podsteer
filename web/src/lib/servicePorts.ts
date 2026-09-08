/**
 * What a Service's ports are, and which of them PodSteer can forward.
 *
 * A PURE READ OF THE MANIFEST, kept out of the component for the usual
 * reason: whether a port can be forwarded is a claim, the control that says
 * so is a promise, and a promise the backend then refuses is worse than no
 * control at all. Every refusal the Go side can produce for a Service —
 * ExternalName, no selector, not TCP — is decided here so the button is never
 * offered for one of them, and each carries the sentence explaining itself.
 *
 * The resolution of `targetPort` is NOT done here, and cannot be: an absent
 * targetPort defaults to the service port, a numeric one is itself, and a
 * NAMED one only means something against a particular pod's containers. The
 * first two are quoted for the reader; the third stays a name until the
 * backend resolves it against the pod it lands on. See app/domain/serviceforward.go.
 */

/** One row of the Ports section on a Service. */
export interface ServicePortRow {
  /** Stable across re-renders — the name when there is one, else the number. */
  key: string
  /** The port's name, empty on a single-port Service. */
  name: string
  /** What the Service listens on. */
  port: number
  /** TCP unless the Service says otherwise. */
  protocol: string
  /** What it forwards to, as the manifest states it: a number or a name. */
  target: string
  /** The node port, when the Service publishes one. 0 when it does not. */
  nodePort: number
  /** Whether a forward can be offered for this port. */
  forwardable: boolean
  /** Why not, when it cannot. Empty when it can. */
  reason: string
}

interface ServiceManifest {
  spec?: {
    type?: string
    selector?: Record<string, string> | null
    ports?: {
      name?: string
      port?: number
      targetPort?: number | string
      nodePort?: number
      protocol?: string
    }[]
  }
}

/**
 * How the backend is asked for this port: its name when it has one, its
 * number when it does not.
 *
 * Both are accepted, and the name is preferred because it is the stable half
 * — a Service's port number can be changed without the name moving, and a
 * remembered local port is keyed on the name for the same reason.
 */
export function servicePortSelector(row: { name: string; port: number }): string {
  return row.name || String(row.port)
}

/** Identifies a Service's forward, for the store's own key. */
export function serviceForwardKey(
  cluster: string,
  namespace: string,
  service: string,
  servicePort: number,
): string {
  return `${cluster}/${namespace}/service/${service}/${servicePort}`
}

export function servicePortRows(manifest: unknown): ServicePortRow[] {
  const parsed = (manifest ?? null) as ServiceManifest | null
  const ports = parsed?.spec?.ports ?? []

  const externalName = parsed?.spec?.type === 'ExternalName'
  const selector = parsed?.spec?.selector ?? {}
  const selects = Object.keys(selector).length > 0

  return ports
    .filter((port) => typeof port.port === 'number')
    .map((port, index) => {
      const protocol = port.protocol ?? 'TCP'
      const number = port.port as number

      return {
        key: port.name || `${number}-${index}`,
        name: port.name ?? '',
        port: number,
        protocol,
        target: targetText(port.targetPort, number),
        nodePort: typeof port.nodePort === 'number' ? port.nodePort : 0,
        ...verdict(externalName, selects, protocol),
      }
    })
}

/**
 * The three refusals, in the order they stop being about this port.
 *
 * ExternalName first because it is a fact about the whole Service, then the
 * selector, then the protocol — which is the only one of the three that can
 * differ between two ports of the same object.
 */
function verdict(
  externalName: boolean,
  selects: boolean,
  protocol: string,
): { forwardable: boolean; reason: string } {
  if (externalName) {
    return {
      forwardable: false,
      reason: 'An ExternalName Service is a DNS alias — there is no pod behind it to forward to.',
    }
  }
  if (!selects) {
    return {
      forwardable: false,
      reason:
        'This Service selects no pods. Its endpoints are managed by hand, and a forward needs a pod PodSteer can find.',
    }
  }
  if (protocol.toUpperCase() !== 'TCP') {
    return {
      forwardable: false,
      reason: `Kubernetes port-forward carries TCP only, and this port is ${protocol.toUpperCase()}.`,
    }
  }
  return { forwardable: true, reason: '' }
}

/**
 * What the port forwards to, said the way the manifest says it.
 *
 * An ABSENT targetPort is printed as the service port rather than left blank
 * or shown as 0: that is what Kubernetes does with it, and a blank here reads
 * as "nothing is listening", which is a different and alarming claim.
 */
function targetText(target: number | string | undefined, servicePort: number): string {
  if (typeof target === 'number' && target > 0) return String(target)
  if (typeof target === 'string' && target.trim() !== '' && target !== '0') return target
  return String(servicePort)
}
