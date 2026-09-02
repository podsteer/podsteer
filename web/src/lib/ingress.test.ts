import { describe, expect, it } from 'vitest'

import { ingressAddresses, ingressCertificates, ingressRoutes, isOpenable } from './ingress'

const ingress = {
  spec: {
    tls: [{ hosts: ['app.example.com'], secretName: 'app-tls' }],
    rules: [
      {
        host: 'app.example.com',
        http: {
          paths: [
            { path: '/', pathType: 'Prefix', backend: { service: { name: 'web', port: { number: 80 } } } },
            { path: '/api', pathType: 'Prefix', backend: { service: { name: 'api', port: { name: 'http' } } } },
          ],
        },
      },
      {
        host: 'plain.example.com',
        http: { paths: [{ path: '/', backend: { service: { name: 'web', port: { number: 80 } } } }] },
      },
    ],
  },
  status: { loadBalancer: { ingress: [{ hostname: 'a1b2.elb.amazonaws.com' }] } },
}

describe('what an Ingress serves', () => {
  it('builds an address somebody can open', () => {
    // The one thing a reader wants from an Ingress — "what is the address" —
    // is the one thing a table of hosts, paths and backends does not say.
    const routes = ingressRoutes(ingress)

    expect(routes.map((route) => route.url)).toEqual([
      'https://app.example.com/',
      'https://app.example.com/api',
      'http://plain.example.com/',
    ])
  })

  it('takes the scheme from the certificate, not from a guess', () => {
    // A host listed under spec.tls is served over https by definition; one
    // that is not, is not. Assuming https because it looks like a website is
    // how a client sends somebody to a port nothing is listening on.
    const routes = ingressRoutes(ingress)

    expect(routes[0].secure).toBe(true)
    expect(routes[2].secure).toBe(false)
  })

  it('reads a TLS entry with no hosts as covering all of them', () => {
    // Kubernetes' own meaning for the omitted list, and the opposite of how
    // an empty cell reads.
    const routes = ingressRoutes({
      spec: {
        tls: [{ secretName: 'wildcard' }],
        rules: [{ host: 'anything.example.com', http: { paths: [{ path: '/' }] } }],
      },
    })

    expect(routes[0].secure).toBe(true)
  })

  it('applies a wildcard certificate to one label, not any depth', () => {
    // *.example.com secures api.example.com and does NOT secure
    // a.b.example.com. That is the TLS rule rather than a convenience.
    const spec = {
      tls: [{ hosts: ['*.example.com'], secretName: 'star' }],
      rules: [
        { host: 'api.example.com', http: { paths: [{ path: '/' }] } },
        { host: 'a.b.example.com', http: { paths: [{ path: '/' }] } },
      ],
    }

    const routes = ingressRoutes({ spec })
    expect(routes[0].secure).toBe(true)
    expect(routes[1].secure).toBe(false)
  })

  it('names the backend the way kubectl does', () => {
    const routes = ingressRoutes(ingress)

    expect(routes[0].backend).toBe('web:80')
    expect(routes[1].backend).toBe('api:http')
  })

  it('says so when a rule matches any host', () => {
    // A rule with no host matches whatever reaches the controller, which is
    // not an address anybody can click — so it is not rendered as a URL with
    // an empty authority.
    const routes = ingressRoutes({ spec: { rules: [{ http: { paths: [{ path: '/health' }] } }] } })

    expect(routes[0].url).toBe('/health (any host)')
    expect(isOpenable(routes[0])).toBe(false)
  })

  it('shows a default backend as the route it is', () => {
    // An Ingress with no rules and a default backend sends everything to one
    // service. That is a legitimate shape, and rendering nothing for it says
    // the Ingress does nothing.
    const routes = ingressRoutes({
      spec: { defaultBackend: { service: { name: 'fallback', port: { number: 8080 } } } },
    })

    expect(routes).toHaveLength(1)
    expect(routes[0].backend).toBe('fallback:8080')
  })

  it('reports the certificates and where it is published', () => {
    expect(ingressCertificates(ingress)).toEqual([
      { secretName: 'app-tls', hosts: ['app.example.com'] },
    ])
    expect(ingressAddresses(ingress)).toEqual(['a1b2.elb.amazonaws.com'])
  })

  it('has no address until a controller gives it one', () => {
    // Ordinary rather than an error: an Ingress nothing is watching, or one
    // still being provisioned, has no address — which is itself the answer to
    // "why does this not work".
    expect(ingressAddresses({ spec: {} })).toEqual([])
    expect(ingressRoutes(null)).toEqual([])
  })
})
