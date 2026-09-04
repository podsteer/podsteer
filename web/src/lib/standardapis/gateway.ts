/**
 * What a Gateway API object declares and what its controller made of it.
 *
 * QUOTATION, NOT VERDICT. Everything here is lifted out of the one manifest
 * the drawer already fetched. A route names its parent Gateway and a Gateway
 * names its class; neither is resolved, because resolving one would be a
 * second GET and would turn a panel into a crawler. Accepted, Programmed,
 * ResolvedRefs and whatever else an implementation writes are the
 * controller's own words, and a condition type this file has never heard of
 * is carried through with the rest.
 *
 * THE PER-PARENT ROUTE STATUS IS THE POINT OF THE ROUTE PANEL. A route has no
 * `status.conditions` of its own — the array everything else in the drawer
 * knows how to render. Its status is `status.parents[]`, one entry PER
 * GATEWAY THAT WAS ASKED TO SERVE IT, each with the controller that answered
 * and that controller's own conditions. "This route was rejected by that
 * gateway, and accepted by this one" is a sentence that can only be said in
 * that shape, and it is the reason somebody opens an HTTPRoute at all. See
 * CLAUDE.md.
 *
 * Field names follow the Gateway API types (gateway.networking.k8s.io/v1):
 * https://gateway-api.sigs.k8s.io/reference/spec/
 */

import {
  BACKEND_DEFAULT_KIND,
  PARENT_DEFAULT_KIND,
  conditionsOf,
  namedCondition,
  numberOr,
  selectorTerms,
  type StandardCondition,
  type StandardRef,
} from './panel'

/** An address a Gateway asked for, or one its controller assigned. */
export interface GatewayAddress {
  /** IPAddress, Hostname, NamedAddress, or an implementation's own — verbatim. */
  type: string
  value: string
}

/** One listener of a Gateway, with whatever its controller said back about it. */
export interface GatewayListener {
  name: string
  /** Empty means every hostname, which is the API's meaning and not "unset". */
  hostname: string
  port: number | null
  protocol: string
  /** Terminate or Passthrough, when the listener does TLS at all. */
  tlsMode: string
  /** Where the listener's certificate comes from — a Secret, usually. */
  certificateRefs: StandardRef[]
  /**
   * `allowedRoutes.namespaces.from`, defaulted to Same as the API defaults it.
   *
   * The difference between Same, All and Selector is the whole of whether a
   * route in another namespace can attach, which is the first thing to check
   * when one has not.
   */
  routesFrom: string
  /** The selector's terms, when `from` is Selector. */
  routesSelector: string[]
  /** `allowedRoutes.kinds`, verbatim. Empty means the protocol's own default. */
  routeKinds: string[]
  /** `status.listeners[].attachedRoutes`; null when the controller has said nothing. */
  attachedRoutes: number | null
  /** `status.listeners[].supportedKinds`, which can be narrower than what was asked for. */
  supportedKinds: string[]
  /** This listener's own conditions, which differ from the Gateway's. */
  conditions: StandardCondition[]
  /**
   * Whether the controller reported a listener the spec does not declare.
   *
   * Kept rather than dropped: a status listener with no spec counterpart means
   * the two halves disagree, and a panel that silently showed only the spec
   * would hide it.
   */
  statusOnly: boolean
}

export interface GatewayClassView {
  /** The controller that claims this class. Nothing here checks it is running. */
  controllerName: string
  description: string
  /** The implementation-specific configuration object, when the class names one. */
  parametersRef: StandardRef | null
  conditions: StandardCondition[]
  accepted: StandardCondition | null
}

export interface GatewayView {
  /** Followable to the GatewayClass, which is cluster-scoped. */
  gatewayClassName: string
  /** `spec.addresses` — what was asked for. */
  requestedAddresses: GatewayAddress[]
  /** `status.addresses` — what the controller actually assigned. */
  addresses: GatewayAddress[]
  conditions: StandardCondition[]
  accepted: StandardCondition | null
  programmed: StandardCondition | null
  listeners: GatewayListener[]
}

/** One part of a route match, whichever kind of route wrote it. */
export interface RouteMatchPart {
  /** What is being matched: "path", "method", "header", "query", "service". */
  kind: string
  /** How it matches — Exact, PathPrefix, RegularExpression — verbatim, or empty. */
  type: string
  /** The header's or query parameter's name; empty for a path or a method. */
  name: string
  value: string
}

/** One filter on a rule or on a backend, in the API's own vocabulary. */
export interface RouteFilter {
  /** RequestRedirect, URLRewrite, ExtensionRef, or an implementation's own. */
  type: string
  /**
   * The filter's own fields, flattened to one line.
   *
   * Empty for a filter type this file does not model, which still renders as
   * its type — the manifest tab has the rest, and inventing a summary for a
   * shape nobody here has seen would be a guess with a filter's authority.
   */
  detail: string
}

/** Where a rule sends what it matched. */
export interface RouteBackend extends StandardRef {
  port: number | null
  /**
   * `weight`; null when unset, which the API reads as 1.
   *
   * Null and 0 are opposites and must not collapse: an omitted weight is a
   * backend taking its share, an explicit 0 is a backend taking nothing.
   */
  weight: number | null
  filters: RouteFilter[]
}

export interface RouteRule {
  /** `name`, added in v1.2; empty on every earlier route. */
  name: string
  /** Empty means the rule matches EVERY request, which is the API's meaning. */
  matches: RouteMatchPart[][]
  filters: RouteFilter[]
  backends: RouteBackend[]
}

/** A parent a route asks to be served by, and the section of it. */
export interface RouteParentRef extends StandardRef {
  /** A named listener of the parent Gateway, when the route asked for one. */
  sectionName: string
  port: number | null
}

/** What ONE parent's controller said about this route. */
export interface RouteParentStatus {
  parent: RouteParentRef
  /** The controller that answered — the field that says WHO rejected it. */
  controllerName: string
  conditions: StandardCondition[]
}

export interface GatewayRouteView {
  parents: RouteParentRef[]
  /** Empty means every hostname the parent listener allows. */
  hostnames: string[]
  rules: RouteRule[]
  /** `status.parents` — one entry per parent that answered. */
  parentStatuses: RouteParentStatus[]
}

/** The parts of a GatewayClass manifest this reads. */
interface GatewayClassManifest {
  spec?: {
    controllerName?: string
    description?: string
    parametersRef?: { group?: string; kind?: string; namespace?: string; name?: string }
  }
  status?: { conditions?: unknown }
}

/**
 * Reads a GatewayClass, or null when there is no manifest at all.
 *
 * A class whose controller has not answered comes back with an empty
 * condition list rather than null, so the panel renders what was declared and
 * says nothing was reported — which is the truth of a class installed before
 * its controller.
 */
export function gatewayClass(manifest: unknown): GatewayClassView | null {
  if (!manifest || typeof manifest !== 'object') return null
  const { spec = {}, status = {} } = manifest as GatewayClassManifest

  const conditions = conditionsOf(status.conditions)
  const parameters = spec.parametersRef

  return {
    controllerName: spec.controllerName ?? '',
    description: spec.description ?? '',
    parametersRef: parameters
      ? {
          group: parameters.group ?? '',
          kind: parameters.kind ?? '',
          namespace: parameters.namespace ?? '',
          name: parameters.name ?? '',
        }
      : null,
    conditions,
    accepted: namedCondition(conditions, 'Accepted'),
  }
}

/** The parts of a Gateway manifest this reads. */
interface GatewayManifest {
  spec?: {
    gatewayClassName?: string
    addresses?: { type?: string; value?: string }[]
    listeners?: RawListener[]
  }
  status?: {
    conditions?: unknown
    addresses?: { type?: string; value?: string }[]
    listeners?: RawListenerStatus[]
  }
}

interface RawListener {
  name?: string
  hostname?: string
  port?: number
  protocol?: string
  tls?: { mode?: string; certificateRefs?: RawRef[] }
  allowedRoutes?: {
    namespaces?: { from?: string; selector?: unknown }
    kinds?: { group?: string; kind?: string }[]
  }
}

interface RawListenerStatus {
  name?: string
  attachedRoutes?: number
  supportedKinds?: { group?: string; kind?: string }[]
  conditions?: unknown
}

interface RawRef {
  group?: string
  kind?: string
  namespace?: string
  name?: string
  port?: number
  weight?: number
  sectionName?: string
  filters?: RawFilter[]
}

/**
 * Reads a Gateway, or null when there is no manifest at all.
 *
 * The listeners are joined BY NAME, which is what the API keys them by: the
 * spec declares them and the status answers about them, and neither array is
 * required to be in the same order or to be complete. A status entry naming a
 * listener the spec does not declare is kept and flagged rather than dropped,
 * because the two halves disagreeing is a fact and hiding it is not.
 */
export function gateway(manifest: unknown): GatewayView | null {
  if (!manifest || typeof manifest !== 'object') return null
  const { spec = {}, status = {} } = manifest as GatewayManifest

  const conditions = conditionsOf(status.conditions)
  const reported = new Map<string, RawListenerStatus>()
  for (const entry of status.listeners ?? []) {
    if (entry && typeof entry === 'object') reported.set(entry.name ?? '', entry)
  }

  const listeners: GatewayListener[] = (spec.listeners ?? []).map((listener) => {
    const name = listener.name ?? ''
    const answer = reported.get(name)
    reported.delete(name)
    return listenerOf(listener, answer, false)
  })

  // Whatever the status named and the spec did not, in the order the
  // controller wrote it.
  for (const answer of reported.values()) {
    listeners.push(listenerOf({ name: answer.name }, answer, true))
  }

  return {
    gatewayClassName: spec.gatewayClassName ?? '',
    requestedAddresses: addressesOf(spec.addresses),
    addresses: addressesOf(status.addresses),
    conditions,
    accepted: namedCondition(conditions, 'Accepted'),
    programmed: namedCondition(conditions, 'Programmed'),
    listeners,
  }
}

function addressesOf(addresses: { type?: string; value?: string }[] | undefined): GatewayAddress[] {
  return (addresses ?? [])
    .filter((address) => address && typeof address === 'object')
    .map((address) => ({ type: address.type ?? '', value: address.value ?? '' }))
}

function listenerOf(
  listener: RawListener,
  answer: RawListenerStatus | undefined,
  statusOnly: boolean,
): GatewayListener {
  const namespaces = listener.allowedRoutes?.namespaces
  return {
    name: listener.name ?? '',
    hostname: listener.hostname ?? '',
    port: numberOr(listener.port),
    protocol: listener.protocol ?? '',
    tlsMode: listener.tls?.mode ?? '',
    certificateRefs: (listener.tls?.certificateRefs ?? []).map((ref) => ({
      group: ref.group ?? '',
      // A certificate reference defaults to a core Secret, which is what every
      // implementation actually reads.
      kind: ref.kind || 'Secret',
      namespace: ref.namespace ?? '',
      name: ref.name ?? '',
    })),
    // Same is the API's own default and is what a listener with no
    // allowedRoutes block enforces, so quoting it is more honest than leaving
    // the row blank and letting it read as "anywhere".
    routesFrom: namespaces?.from || (statusOnly ? '' : 'Same'),
    routesSelector: selectorTerms(namespaces?.selector),
    routeKinds: (listener.allowedRoutes?.kinds ?? []).map((kind) => kind.kind ?? ''),
    attachedRoutes: numberOr(answer?.attachedRoutes),
    supportedKinds: (answer?.supportedKinds ?? []).map((kind) => kind.kind ?? ''),
    conditions: conditionsOf(answer?.conditions),
    statusOnly,
  }
}

/** The parts of an HTTPRoute or GRPCRoute manifest this reads. */
interface RouteManifest {
  spec?: {
    parentRefs?: RawRef[]
    hostnames?: string[]
    rules?: RawRule[]
  }
  status?: {
    parents?: { parentRef?: RawRef; controllerName?: string; conditions?: unknown }[]
  }
}

interface RawRule {
  name?: string
  matches?: RawMatch[]
  filters?: RawFilter[]
  backendRefs?: RawRef[]
}

interface RawMatch {
  path?: { type?: string; value?: string }
  /** A string on an HTTPRoute, an object on a GRPCRoute — the one shape difference. */
  method?: string | { type?: string; service?: string; method?: string }
  headers?: { type?: string; name?: string; value?: string }[]
  queryParams?: { type?: string; name?: string; value?: string }[]
}

interface RawFilter {
  type?: string
  requestRedirect?: RawRedirect
  urlRewrite?: RawRewrite
  requestMirror?: { backendRef?: RawRef; percent?: number }
  requestHeaderModifier?: RawHeaderModifier
  responseHeaderModifier?: RawHeaderModifier
  extensionRef?: { group?: string; kind?: string; name?: string }
}

interface RawRedirect {
  scheme?: string
  hostname?: string
  port?: number
  statusCode?: number
  path?: { type?: string; replaceFullPath?: string; replacePrefixMatch?: string }
}

interface RawRewrite {
  hostname?: string
  path?: { type?: string; replaceFullPath?: string; replacePrefixMatch?: string }
}

interface RawHeaderModifier {
  set?: { name?: string; value?: string }[]
  add?: { name?: string; value?: string }[]
  remove?: string[]
}

/**
 * Reads an HTTPRoute or a GRPCRoute, or null when there is no manifest at all.
 *
 * ONE PARSER FOR BOTH, because they are the same object apart from the inside
 * of a match: HTTP matches a path, a method and query parameters, gRPC matches
 * a service and a method, and both match headers. Everything a panel actually
 * arranges — parents, hostnames, rules, filters, backends, and the per-parent
 * status — is field for field identical, and two copies of it would drift the
 * first time either was touched.
 */
export function gatewayRoute(manifest: unknown): GatewayRouteView | null {
  if (!manifest || typeof manifest !== 'object') return null
  const { spec = {}, status = {} } = manifest as RouteManifest

  return {
    parents: (spec.parentRefs ?? []).map(parentRefOf),
    hostnames: spec.hostnames ?? [],
    rules: (spec.rules ?? []).map((rule) => ({
      name: rule.name ?? '',
      matches: (rule.matches ?? []).map(matchPartsOf),
      filters: (rule.filters ?? []).map(filterOf),
      backends: (rule.backendRefs ?? []).map((backend) => ({
        group: backend.group ?? '',
        // The CRD's own default, and the one every ordinary route relies on.
        kind: backend.kind || BACKEND_DEFAULT_KIND,
        namespace: backend.namespace ?? '',
        name: backend.name ?? '',
        port: numberOr(backend.port),
        weight: numberOr(backend.weight),
        filters: (backend.filters ?? []).map(filterOf),
      })),
    })),
    parentStatuses: (status.parents ?? [])
      .filter((entry) => entry && typeof entry === 'object')
      .map((entry) => ({
        parent: parentRefOf(entry.parentRef ?? {}),
        controllerName: entry.controllerName ?? '',
        conditions: conditionsOf(entry.conditions),
      })),
  }
}

function parentRefOf(ref: RawRef): RouteParentRef {
  return {
    group: ref.group ?? '',
    // Gateway is the CRD default, and a parent reference that leaned on it
    // would otherwise render with no kind and be unfollowable.
    kind: ref.kind || PARENT_DEFAULT_KIND,
    namespace: ref.namespace ?? '',
    name: ref.name ?? '',
    sectionName: ref.sectionName ?? '',
    port: numberOr(ref.port),
  }
}

/**
 * One match flattened to its parts, in the order they read in.
 *
 * An EMPTY array is meaningful and is not the same as no match block: a rule
 * with no matches matches every request the parent sends it, which the panel
 * says in words rather than leaving as a blank row.
 */
function matchPartsOf(match: RawMatch): RouteMatchPart[] {
  const parts: RouteMatchPart[] = []
  if (!match || typeof match !== 'object') return parts

  if (match.path) {
    parts.push({
      kind: 'path',
      // PathPrefix is the CRD default for a path match that names no type.
      type: match.path.type || 'PathPrefix',
      name: '',
      value: match.path.value ?? '',
    })
  }

  if (typeof match.method === 'string') {
    // An HTTPRoute's method is a bare verb.
    parts.push({ kind: 'method', type: '', name: '', value: match.method })
  } else if (match.method && typeof match.method === 'object') {
    // A GRPCRoute's is a service and a method, either of which may be absent —
    // an absent one matches everything at that level.
    const { type = '', service = '', method = '' } = match.method
    if (service) parts.push({ kind: 'service', type, name: '', value: service })
    if (method) parts.push({ kind: 'method', type, name: '', value: method })
  }

  for (const header of match.headers ?? []) {
    if (!header || typeof header !== 'object') continue
    // Exact is the CRD default for a header and a query match alike.
    parts.push({
      kind: 'header',
      type: header.type || 'Exact',
      name: header.name ?? '',
      value: header.value ?? '',
    })
  }

  for (const query of match.queryParams ?? []) {
    if (!query || typeof query !== 'object') continue
    parts.push({
      kind: 'query',
      type: query.type || 'Exact',
      name: query.name ?? '',
      value: query.value ?? '',
    })
  }

  return parts
}

/**
 * A filter as its type and one line of its own fields.
 *
 * The TYPE is always carried, including one this does not model, because the
 * type alone answers "what is this rule doing to the request". The detail is
 * built only for the filter types the API defines; anything else renders as
 * its bare type rather than as a summary invented for a shape nobody here has
 * seen.
 */
function filterOf(filter: RawFilter): RouteFilter {
  const type = filter?.type ?? ''
  return { type, detail: filterDetail(filter) }
}

function filterDetail(filter: RawFilter): string {
  if (!filter || typeof filter !== 'object') return ''

  if (filter.requestRedirect) return redirectDetail(filter.requestRedirect)
  if (filter.urlRewrite) return rewriteDetail(filter.urlRewrite)

  if (filter.requestMirror) {
    const backend = filter.requestMirror.backendRef
    const target = backend ? `${backend.kind || BACKEND_DEFAULT_KIND}/${backend.name ?? ''}` : ''
    const percent = numberOr(filter.requestMirror.percent)
    return percent === null ? target : `${target} · ${percent}%`
  }

  const modifier = filter.requestHeaderModifier ?? filter.responseHeaderModifier
  if (modifier) return headerModifierDetail(modifier)

  if (filter.extensionRef) {
    const { group = '', kind = '', name = '' } = filter.extensionRef
    return [group, kind, name].filter(Boolean).join('/')
  }

  return ''
}

function redirectDetail(redirect: RawRedirect): string {
  const parts: string[] = []
  if (redirect.scheme) parts.push(`${redirect.scheme}://`)
  if (redirect.hostname) parts.push(redirect.hostname)
  const port = numberOr(redirect.port)
  if (port !== null) parts.push(`:${port}`)
  const path = pathReplacement(redirect.path)
  if (path) parts.push(path)
  const status = numberOr(redirect.statusCode)
  // The status code is what makes a redirect permanent or not, which is the
  // half of it anybody actually asks about.
  if (status !== null) parts.push(`(${status})`)
  return parts.join(' ')
}

function rewriteDetail(rewrite: RawRewrite): string {
  const parts: string[] = []
  if (rewrite.hostname) parts.push(rewrite.hostname)
  const path = pathReplacement(rewrite.path)
  if (path) parts.push(path)
  return parts.join(' ')
}

function pathReplacement(
  path: { type?: string; replaceFullPath?: string; replacePrefixMatch?: string } | undefined,
): string {
  if (!path) return ''
  if (path.replaceFullPath !== undefined) return `→ ${path.replaceFullPath}`
  if (path.replacePrefixMatch !== undefined) return `prefix → ${path.replacePrefixMatch}`
  // A path block that carries only a type says which kind of replacement was
  // asked for and nothing about the value; the type is still worth showing.
  return path.type ?? ''
}

function headerModifierDetail(modifier: RawHeaderModifier): string {
  const parts: string[] = []
  for (const header of modifier.set ?? []) parts.push(`set ${header.name ?? ''}: ${header.value ?? ''}`)
  for (const header of modifier.add ?? []) parts.push(`add ${header.name ?? ''}: ${header.value ?? ''}`)
  for (const name of modifier.remove ?? []) parts.push(`remove ${name}`)
  return parts.join(' · ')
}
