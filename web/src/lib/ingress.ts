/**
 * What an Ingress actually serves, as somewhere you can go.
 *
 * An Ingress is a routing table written as a nested list, and every client in
 * this category renders it as one: hosts in one column, paths in another,
 * backends in a third, and the reader assembles the URL in their head. The one
 * thing they came for — "what is the address, can I open it" — is the one
 * thing none of them says.
 *
 * COMPOSED FROM QUOTED FIELDS, NOT CONCLUDED. Every part of a URL here is
 * lifted verbatim out of the manifest; the only judgement is the scheme, and
 * that is read off `spec.tls` rather than guessed — an Ingress whose host is
 * listed there is served over https by definition, and one that is not is
 * not. This is the same kind of composition container.ts does when it builds
 * `http-get http://:8080/healthz` out of a probe.
 */

interface IngressManifest {
  spec?: {
    ingressClassName?: string
    defaultBackend?: Backend
    tls?: { hosts?: string[]; secretName?: string }[]
    rules?: {
      host?: string
      http?: { paths?: { path?: string; pathType?: string; backend?: Backend }[] }
    }[]
  }
  status?: {
    loadBalancer?: { ingress?: { ip?: string; hostname?: string }[] }
  }
}

interface Backend {
  service?: { name?: string; port?: { number?: number; name?: string } }
  resource?: { kind?: string; name?: string }
}

/** One address this Ingress answers on. */
export interface IngressRoute {
  /** The whole thing, ready to open. */
  url: string
  host: string
  path: string
  /** How the path is matched: Prefix, Exact, or ImplementationSpecific. */
  pathType: string
  /** Where it sends the request, in kubectl's form: "service:port". */
  backend: string
  /** Whether TLS terminates here, which is what decides the scheme. */
  secure: boolean
}

/** The certificates this Ingress terminates with. */
export interface IngressCertificate {
  /** The Secret holding the certificate and key. */
  secretName: string
  /**
   * The hosts it covers. EMPTY MEANS EVERY HOST, which is what Kubernetes
   * means by an omitted list here — not "no hosts", which is how an empty
   * cell would read.
   */
  hosts: string[]
}

/**
 * Every address the Ingress serves.
 *
 * A rule with no host matches any name that reaches the controller, which is
 * an address nobody can click — so it is rendered as the wildcard it is
 * rather than as a URL with an empty authority.
 */
export function ingressRoutes(manifest: unknown): IngressRoute[] {
  const spec = (manifest as IngressManifest | null)?.spec
  if (!spec) return []

  const secured = securedHosts(spec.tls)
  const routes: IngressRoute[] = []

  for (const rule of spec.rules ?? []) {
    const host = rule.host ?? ''
    const secure = isSecure(host, secured)

    for (const entry of rule.http?.paths ?? []) {
      const path = entry.path ?? '/'
      routes.push({
        url: host ? `${secure ? 'https' : 'http'}://${host}${path}` : `${path} (any host)`,
        host,
        path,
        pathType: entry.pathType ?? 'ImplementationSpecific',
        backend: backendOf(entry.backend),
        secure,
      })
    }
  }

  // A default backend with no rules is a legitimate Ingress: everything that
  // reaches it goes to one service. Shown as such rather than as nothing.
  if (routes.length === 0 && spec.defaultBackend) {
    routes.push({
      url: 'anything (default backend)',
      host: '',
      path: '/',
      pathType: 'ImplementationSpecific',
      backend: backendOf(spec.defaultBackend),
      secure: false,
    })
  }

  return routes
}

/** The certificates, if it terminates any. */
export function ingressCertificates(manifest: unknown): IngressCertificate[] {
  const tls = (manifest as IngressManifest | null)?.spec?.tls ?? []

  return tls
    .filter((entry) => entry.secretName)
    .map((entry) => ({ secretName: entry.secretName ?? '', hosts: entry.hosts ?? [] }))
}

/**
 * Where the controller has published it, from `status.loadBalancer`.
 *
 * Empty is ordinary and is not an error: an Ingress with no controller
 * watching it, or one still being provisioned, has no address yet — which is
 * itself the answer to "why does this not work".
 */
export function ingressAddresses(manifest: unknown): string[] {
  const published = (manifest as IngressManifest | null)?.status?.loadBalancer?.ingress ?? []

  return published
    .map((entry) => entry.hostname || entry.ip || '')
    .filter((address) => address !== '')
}

/** Whether a URL can be opened, as opposed to described. */
export function isOpenable(route: IngressRoute): boolean {
  return route.host !== ''
}

function securedHosts(
  tls: { hosts?: string[]; secretName?: string }[] | undefined,
): Set<string> | 'all' {
  const entries = tls ?? []
  if (entries.length === 0) return new Set()

  // An entry with no hosts covers every host in the Ingress, which is the
  // API's own meaning and not an omission.
  if (entries.some((entry) => (entry.hosts ?? []).length === 0)) return 'all'

  return new Set(entries.flatMap((entry) => entry.hosts ?? []))
}

function isSecure(host: string, secured: Set<string> | 'all'): boolean {
  if (secured === 'all') return true
  if (secured.has(host)) return true

  // A wildcard certificate covers one label, not any depth: *.example.com
  // secures api.example.com and does not secure a.b.example.com. That is the
  // TLS rule, not a convenience.
  const parent = host.replace(/^[^.]+\./, '')
  return parent !== host && secured.has(`*.${parent}`)
}

function backendOf(backend: Backend | undefined): string {
  if (backend?.service?.name) {
    const port = backend.service.port
    const suffix = port?.number ?? port?.name
    return suffix ? `${backend.service.name}:${suffix}` : backend.service.name
  }
  if (backend?.resource?.name) {
    return `${backend.resource.kind ?? 'Resource'}/${backend.resource.name}`
  }
  return '—'
}
