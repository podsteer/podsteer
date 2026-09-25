import { describe, expect, it } from 'vitest'

import { gateway, gatewayClass, gatewayRoute } from './gateway'

/** A GatewayClass its controller has accepted — the ordinary case. */
const acceptedClass = {
  apiVersion: 'gateway.networking.k8s.io/v1',
  kind: 'GatewayClass',
  metadata: { name: 'external' },
  spec: {
    controllerName: 'example.net/gateway-controller',
    description: 'The internet-facing class',
    parametersRef: {
      group: 'example.net',
      kind: 'GatewayConfig',
      namespace: 'infra',
      name: 'external-config',
    },
  },
  status: {
    conditions: [
      {
        type: 'Accepted',
        status: 'True',
        reason: 'Accepted',
        message: 'the class has been accepted by the controller',
        lastTransitionTime: '2026-08-01T10:00:00Z',
      },
      { type: 'SupportedVersion', status: 'True', reason: 'SupportedVersion' },
    ],
  },
}

/** A Gateway with two listeners, one of which its controller refused. */
const programmedGateway = {
  apiVersion: 'gateway.networking.k8s.io/v1',
  kind: 'Gateway',
  metadata: { name: 'edge', namespace: 'infra' },
  spec: {
    gatewayClassName: 'external',
    addresses: [{ type: 'IPAddress', value: '203.0.113.10' }],
    listeners: [
      {
        name: 'https',
        hostname: '*.example.com',
        port: 443,
        protocol: 'HTTPS',
        tls: { mode: 'Terminate', certificateRefs: [{ name: 'example-tls' }] },
        allowedRoutes: {
          namespaces: { from: 'Selector', selector: { matchLabels: { 'routes/allowed': 'true' } } },
          kinds: [{ group: 'gateway.networking.k8s.io', kind: 'HTTPRoute' }],
        },
      },
      {
        name: 'http',
        port: 80,
        protocol: 'HTTP',
      },
    ],
  },
  status: {
    addresses: [{ type: 'IPAddress', value: '203.0.113.10' }],
    conditions: [
      {
        type: 'Accepted',
        status: 'True',
        reason: 'Accepted',
        lastTransitionTime: '2026-09-01T08:00:00Z',
      },
      {
        type: 'Programmed',
        status: 'True',
        reason: 'Programmed',
        message: 'the gateway is programmed',
        lastTransitionTime: '2026-09-01T08:00:05Z',
      },
    ],
    listeners: [
      {
        // Deliberately in the opposite order to the spec: the API keys these
        // by name, not by position.
        name: 'http',
        attachedRoutes: 0,
        supportedKinds: [{ kind: 'HTTPRoute' }],
        conditions: [{ type: 'Accepted', status: 'True', reason: 'Accepted' }],
      },
      {
        name: 'https',
        attachedRoutes: 3,
        supportedKinds: [{ kind: 'HTTPRoute' }, { kind: 'GRPCRoute' }],
        conditions: [
          { type: 'Accepted', status: 'True', reason: 'Accepted' },
          {
            type: 'ResolvedRefs',
            status: 'False',
            reason: 'InvalidCertificateRef',
            message: 'Secret infra/example-tls not found',
          },
        ],
      },
      {
        // A listener the controller reports and the spec does not declare.
        name: 'legacy',
        attachedRoutes: 1,
        conditions: [{ type: 'Accepted', status: 'Unknown', reason: 'Pending' }],
      },
    ],
  },
}

/** An HTTPRoute one Gateway accepted and another refused. */
const acceptedRoute = {
  apiVersion: 'gateway.networking.k8s.io/v1',
  kind: 'HTTPRoute',
  metadata: { name: 'checkout', namespace: 'shop' },
  spec: {
    parentRefs: [
      { name: 'edge', namespace: 'infra', sectionName: 'https' },
      { name: 'internal', kind: 'Gateway', namespace: 'infra' },
    ],
    hostnames: ['shop.example.com'],
    rules: [
      {
        name: 'api',
        matches: [
          { path: { type: 'PathPrefix', value: '/api' }, method: 'POST' },
          { headers: [{ name: 'x-canary', value: 'yes' }] },
        ],
        filters: [
          {
            type: 'RequestHeaderModifier',
            requestHeaderModifier: {
              set: [{ name: 'x-env', value: 'prod' }],
              remove: ['x-debug'],
            },
          },
          {
            type: 'RequestRedirect',
            requestRedirect: { scheme: 'https', statusCode: 301 },
          },
        ],
        backendRefs: [
          { name: 'checkout', port: 8080, weight: 90 },
          { name: 'checkout-canary', port: 8080, weight: 0 },
          { group: 'example.net', kind: 'Backend', name: 'legacy', namespace: 'old' },
        ],
      },
      {
        // No matches at all, which the API reads as every request.
        backendRefs: [{ name: 'fallback', port: 80 }],
      },
    ],
  },
  status: {
    parents: [
      {
        parentRef: { name: 'edge', namespace: 'infra', sectionName: 'https' },
        controllerName: 'example.net/gateway-controller',
        conditions: [
          { type: 'Accepted', status: 'True', reason: 'Accepted' },
          { type: 'ResolvedRefs', status: 'True', reason: 'ResolvedRefs' },
        ],
      },
      {
        parentRef: { name: 'internal', namespace: 'infra' },
        controllerName: 'other.example/gateway-controller',
        conditions: [
          {
            type: 'Accepted',
            status: 'False',
            reason: 'NotAllowedByListeners',
            message: 'no listener on this Gateway accepts routes from namespace shop',
          },
        ],
      },
    ],
  },
}

/** A GRPCRoute — the same object apart from the inside of a match. */
const grpcRoute = {
  apiVersion: 'gateway.networking.k8s.io/v1',
  kind: 'GRPCRoute',
  metadata: { name: 'orders', namespace: 'shop' },
  spec: {
    parentRefs: [{ name: 'edge', namespace: 'infra' }],
    rules: [
      {
        matches: [
          {
            method: { type: 'Exact', service: 'shop.Orders', method: 'Place' },
            headers: [{ type: 'RegularExpression', name: 'x-tenant', value: '^acme-.*' }],
          },
        ],
        backendRefs: [{ name: 'orders', port: 9090 }],
      },
    ],
  },
}

describe('reading a GatewayClass', () => {
  it('quotes the controller, the description and the Accepted condition', () => {
    const view = gatewayClass(acceptedClass)

    expect(view?.controllerName).toBe('example.net/gateway-controller')
    expect(view?.description).toBe('The internet-facing class')
    expect(view?.accepted).toMatchObject({ status: 'True', reason: 'Accepted' })
    expect(view?.conditions).toHaveLength(2)
  })

  it('carries the parameters reference by its own Kind, and never resolves it', () => {
    expect(gatewayClass(acceptedClass)?.parametersRef).toEqual({
      group: 'example.net',
      kind: 'GatewayConfig',
      namespace: 'infra',
      name: 'external-config',
    })
  })

  it('reads a class its controller has never answered about', () => {
    // Installed ahead of its controller: everything declared is there and
    // nothing is reported, which is not the same as a rejection.
    const view = gatewayClass({ spec: { controllerName: 'example.net/none' } })

    expect(view?.controllerName).toBe('example.net/none')
    expect(view?.conditions).toEqual([])
    expect(view?.accepted).toBeNull()
    expect(view?.parametersRef).toBeNull()
  })

  it('answers null only when there is no manifest at all', () => {
    expect(gatewayClass(null)).toBeNull()
    expect(gatewayClass('not an object')).toBeNull()
    expect(gatewayClass({})).not.toBeNull()
  })
})

describe('reading a Gateway', () => {
  it('quotes Accepted and Programmed, and both sets of addresses', () => {
    const view = gateway(programmedGateway)

    expect(view?.gatewayClassName).toBe('external')
    expect(view?.accepted).toMatchObject({ status: 'True', reason: 'Accepted' })
    expect(view?.programmed).toMatchObject({ status: 'True', message: 'the gateway is programmed' })
    expect(view?.addresses).toEqual([{ type: 'IPAddress', value: '203.0.113.10' }])
    expect(view?.requestedAddresses).toEqual([{ type: 'IPAddress', value: '203.0.113.10' }])
  })

  it('joins listeners by name rather than by position', () => {
    // The status listed them in the opposite order to the spec, which the API
    // permits: a positional join would have given the HTTPS listener the HTTP
    // one's attached-route count.
    const view = gateway(programmedGateway)
    const https = view?.listeners.find((listener) => listener.name === 'https')

    expect(https?.port).toBe(443)
    expect(https?.protocol).toBe('HTTPS')
    expect(https?.attachedRoutes).toBe(3)
    expect(https?.supportedKinds).toEqual(['HTTPRoute', 'GRPCRoute'])
  })

  it('carries a listener’s own conditions, including a refusal', () => {
    const https = gateway(programmedGateway)?.listeners.find(
      (listener) => listener.name === 'https',
    )

    expect(https?.conditions).toHaveLength(2)
    expect(https?.conditions[1]).toMatchObject({
      type: 'ResolvedRefs',
      status: 'False',
      reason: 'InvalidCertificateRef',
    })
  })

  it('defaults a certificate reference to a Secret and allowed routes to Same', () => {
    const view = gateway(programmedGateway)
    const https = view?.listeners.find((listener) => listener.name === 'https')
    const http = view?.listeners.find((listener) => listener.name === 'http')

    expect(https?.certificateRefs).toEqual([
      { group: '', kind: 'Secret', namespace: '', name: 'example-tls' },
    ])
    expect(https?.routesFrom).toBe('Selector')
    expect(https?.routesSelector).toEqual(['routes/allowed=true'])
    // No allowedRoutes block at all: the CRD's default is Same, and leaving
    // the row blank would read as "anywhere".
    expect(http?.routesFrom).toBe('Same')
  })

  it('keeps a listener the status reports and the spec does not declare', () => {
    const legacy = gateway(programmedGateway)?.listeners.find(
      (listener) => listener.name === 'legacy',
    )

    expect(legacy?.statusOnly).toBe(true)
    expect(legacy?.attachedRoutes).toBe(1)
    // Nothing is invented for a listener with no spec: the default `from`
    // would be a claim about a declaration that does not exist.
    expect(legacy?.routesFrom).toBe('')
  })

  it('separates an unset attached-route count from a count of zero', () => {
    const view = gateway({
      spec: { gatewayClassName: 'external', listeners: [{ name: 'http', port: 80 }] },
    })

    expect(view?.listeners[0].attachedRoutes).toBeNull()
    expect(
      gateway(programmedGateway)?.listeners.find((listener) => listener.name === 'http')
        ?.attachedRoutes,
    ).toBe(0)
  })

  it('reads a Gateway with no status at all', () => {
    const view = gateway({ spec: { gatewayClassName: 'external', listeners: [{ name: 'http' }] } })

    expect(view?.accepted).toBeNull()
    expect(view?.programmed).toBeNull()
    expect(view?.addresses).toEqual([])
    expect(view?.listeners).toHaveLength(1)
  })
})

describe('reading a route', () => {
  it('defaults a parent reference’s Kind to Gateway, as the CRD does', () => {
    const view = gatewayRoute(acceptedRoute)

    expect(view?.parents[0]).toEqual({
      group: '',
      kind: 'Gateway',
      namespace: 'infra',
      name: 'edge',
      sectionName: 'https',
      port: null,
    })
    expect(view?.parents[1].kind).toBe('Gateway')
    expect(view?.hostnames).toEqual(['shop.example.com'])
  })

  it('reads each rule’s matches, with the CRD defaults the match relies on', () => {
    const rule = gatewayRoute(acceptedRoute)?.rules[0]

    expect(rule?.name).toBe('api')
    expect(rule?.matches[0]).toEqual([
      { kind: 'path', type: 'PathPrefix', name: '', value: '/api' },
      { kind: 'method', type: '', name: '', value: 'POST' },
    ])
    // A header match that names no type is Exact by the CRD's default.
    expect(rule?.matches[1]).toEqual([
      { kind: 'header', type: 'Exact', name: 'x-canary', value: 'yes' },
    ])
  })

  it('reads a rule with no matches as the empty list it is', () => {
    // The API reads an empty match list as every request, which the panel says
    // in words — so the parser must not invent a match here.
    expect(gatewayRoute(acceptedRoute)?.rules[1].matches).toEqual([])
  })

  it('defaults a backend to a Service and keeps an explicit weight of zero', () => {
    const backends = gatewayRoute(acceptedRoute)?.rules[0].backends

    expect(backends?.[0]).toMatchObject({ kind: 'Service', name: 'checkout', port: 8080, weight: 90 })
    // Zero and unset are opposites: zero takes the backend out of the pool,
    // unset is the API's default of 1.
    expect(backends?.[1].weight).toBe(0)
    expect(backends?.[2]).toMatchObject({ group: 'example.net', kind: 'Backend', namespace: 'old' })
    expect(gatewayRoute(acceptedRoute)?.rules[1].backends[0].weight).toBeNull()
  })

  it('summarises the filter types it models and quotes one it does not', () => {
    const filters = gatewayRoute(acceptedRoute)?.rules[0].filters

    expect(filters?.[0]).toEqual({
      type: 'RequestHeaderModifier',
      detail: 'set x-env: prod · remove x-debug',
    })
    expect(filters?.[1]).toEqual({ type: 'RequestRedirect', detail: 'https:// (301)' })

    // An extension nobody here has modelled still renders as its own type
    // rather than being dropped or given an invented summary.
    const unknown = gatewayRoute({
      spec: { rules: [{ filters: [{ type: 'ExampleCorsFilter', exampleCors: { origins: ['*'] } }] }] },
    })
    expect(unknown?.rules[0].filters[0]).toEqual({ type: 'ExampleCorsFilter', detail: '' })
  })

  it('carries the per-parent status, which is where a rejection actually lives', () => {
    // The whole reason the route panel exists: a route has no conditions of
    // its own, and "accepted by this Gateway, refused by that one" can only be
    // said per parent, with the controller that answered named.
    const statuses = gatewayRoute(acceptedRoute)?.parentStatuses

    expect(statuses).toHaveLength(2)
    expect(statuses?.[0].controllerName).toBe('example.net/gateway-controller')
    expect(statuses?.[0].conditions[0]).toMatchObject({ type: 'Accepted', status: 'True' })
    expect(statuses?.[1].parent.name).toBe('internal')
    expect(statuses?.[1].conditions[0]).toMatchObject({
      type: 'Accepted',
      status: 'False',
      reason: 'NotAllowedByListeners',
      message: 'no listener on this Gateway accepts routes from namespace shop',
    })
  })

  it('reads a route no controller has answered about', () => {
    const view = gatewayRoute({ spec: { parentRefs: [{ name: 'edge' }], rules: [] } })

    expect(view?.parentStatuses).toEqual([])
    expect(view?.rules).toEqual([])
    expect(view?.hostnames).toEqual([])
  })

  it('reads a GRPCRoute’s service-and-method match through the same parser', () => {
    const rule = gatewayRoute(grpcRoute)?.rules[0]

    expect(rule?.matches[0]).toEqual([
      { kind: 'service', type: 'Exact', name: '', value: 'shop.Orders' },
      { kind: 'method', type: 'Exact', name: '', value: 'Place' },
      { kind: 'header', type: 'RegularExpression', name: 'x-tenant', value: '^acme-.*' },
    ])
    expect(rule?.backends[0]).toMatchObject({ kind: 'Service', name: 'orders', port: 9090 })
  })

  it('answers null only when there is no manifest at all', () => {
    expect(gatewayRoute(null)).toBeNull()
    expect(gatewayRoute(42)).toBeNull()
    expect(gatewayRoute({})).not.toBeNull()
  })
})
