import { describe, expect, it } from 'vitest'

import type { ProbeResult, ProbeStep } from '$lib/api/client'
import {
  isProbeableKind,
  outcomeTone,
  probeSubject,
  probeTargets,
  routeLabel,
  stepLabel,
  stepTone,
  takenAgo,
  vantageOptions,
} from './reachability'

const service = {
  metadata: { name: 'web', namespace: 'shop' },
  spec: {
    type: 'ClusterIP',
    clusterIP: '10.96.0.10',
    ports: [
      { name: 'http', port: 80, targetPort: 8080, protocol: 'TCP' },
      { name: 'metrics', port: 9090, protocol: 'TCP' },
      { name: 'dns', port: 53, protocol: 'UDP' },
    ],
  },
}

const pod = {
  metadata: { name: 'web-0', namespace: 'shop' },
  spec: {
    containers: [
      { name: 'app', ports: [{ name: 'http', containerPort: 8080, protocol: 'TCP' }] },
      { name: 'sidecar', ports: [{ containerPort: 8080 }] },
    ],
  },
  status: { podIP: '10.1.2.3' },
}

const ingress = {
  metadata: { name: 'web', namespace: 'shop' },
  spec: {
    tls: [{ hosts: ['shop.example.com'], secretName: 'shop-tls' }],
    rules: [
      {
        host: 'shop.example.com',
        http: { paths: [{ path: '/', backend: { service: { name: 'web', port: { number: 80 } } } }] },
      },
      {
        host: 'shop.example.com',
        http: { paths: [{ path: '/api', backend: { service: { name: 'api', port: { number: 80 } } } }] },
      },
      {
        host: 'plain.example.com',
        http: { paths: [{ path: '/', backend: { service: { name: 'web', port: { number: 80 } } } }] },
      },
      // A rule with no host matches whatever reaches the controller: there is
      // no name to resolve and nothing to aim at.
      { http: { paths: [{ path: '/', backend: { service: { name: 'web', port: { number: 80 } } } }] } },
    ],
  },
}

describe('isProbeableKind', () => {
  it('offers a probe only where there is an address to aim at', () => {
    expect(isProbeableKind('Service')).toBe(true)
    expect(isProbeableKind('Pod')).toBe(true)
    expect(isProbeableKind('Ingress')).toBe(true)
    expect(isProbeableKind('ConfigMap')).toBe(false)
    expect(isProbeableKind(undefined)).toBe(false)
  })
})

describe('probeTargets', () => {
  it('quotes every service port, UDP included', () => {
    const targets = probeTargets('Service', service)

    expect(targets.map((t) => t.port)).toEqual([80, 9090, 53])
    // A UDP port is OFFERED and refused by the backend with the reason,
    // rather than hidden: an operator who cannot find their DNS Service's
    // port reasonably concludes the panel is broken.
    expect(targets[2].protocol).toBe('UDP')
    expect(targets[2].label).toContain('UDP')
  })

  it('names the container on a pod port, because two containers routinely share a number', () => {
    const targets = probeTargets('Pod', pod)

    expect(targets).toHaveLength(2)
    expect(targets[0].label).toContain('app')
    expect(targets[1].label).toContain('sidecar')
    expect(new Set(targets.map((t) => t.key)).size).toBe(2)
  })

  it('offers one option per ingress host, with the port TLS implies', () => {
    const targets = probeTargets('Ingress', ingress)

    expect(targets.map((t) => t.host)).toEqual(['shop.example.com', 'plain.example.com'])
    expect(targets[0].port).toBe(443)
    expect(targets[0].tls).toBe(true)
    expect(targets[1].port).toBe(80)
    expect(targets[1].tls).toBe(false)
  })

  it('returns nothing for a kind with no address and for a missing manifest', () => {
    expect(probeTargets('ConfigMap', { metadata: { name: 'settings' } })).toEqual([])
    expect(probeTargets('Service', null)).toEqual([])
    expect(probeTargets('Service', { spec: {} })).toEqual([])
  })
})

describe('probeSubject', () => {
  it('carries the facts the backend plans from, all of them quoted', () => {
    const [target] = probeTargets('Service', service)
    const subject = probeSubject('Service', service, target)

    expect(subject).toMatchObject({
      kind: 'Service',
      namespace: 'shop',
      name: 'web',
      serviceType: 'ClusterIP',
      clusterIp: '10.96.0.10',
      port: 80,
      portName: 'http',
      protocol: 'TCP',
    })
  })

  it('carries a pod IP for a pod and a host for an ingress', () => {
    const podSubject = probeSubject('Pod', pod, probeTargets('Pod', pod)[0])
    expect(podSubject.podIp).toBe('10.1.2.3')
    expect(podSubject.host).toBe('')

    const ingressSubject = probeSubject('Ingress', ingress, probeTargets('Ingress', ingress)[0])
    expect(ingressSubject.host).toBe('shop.example.com')
    expect(ingressSubject.tls).toBe(true)
  })
})

describe('vantageOptions', () => {
  it('says what a success from each vantage actually establishes', () => {
    const [local, inCluster] = vantageOptions('Service')

    expect(local.meaning).toContain('API server')
    expect(local.meaning).toContain('not that anything else in the cluster may reach them')
    expect(inCluster.meaning).toContain('NetworkPolicy')
    // The two must never read the same, or the panel has stopped making the
    // distinction it exists to make.
    expect(local.meaning).not.toEqual(inCluster.meaning)
  })

  it('refuses the local vantage for an ingress and says why', () => {
    const [local, inCluster] = vantageOptions('Ingress')

    expect(local.available).toBe(false)
    expect(local.unavailableReason).toContain('not an API server')
    expect(local.unavailableReason).toContain('browser')
    expect(inCluster.available).toBe(true)
  })

  it('offers both vantages for a pod, and none for anything else', () => {
    expect(vantageOptions('Pod').every((option) => option.available)).toBe(true)
    expect(vantageOptions('ConfigMap')).toEqual([])
  })
})

describe('outcomeTone', () => {
  it('tones a refusal as an answer and an unfinished probe as unknown', () => {
    expect(outcomeTone('reachable')).toBe('good')
    expect(outcomeTone('refused')).toBe('bad')
    // A name that did not resolve is a bad answer, not an unknown one — the
    // probe ran and told us something.
    expect(outcomeTone('name_not_resolved')).toBe('bad')
    expect(outcomeTone('unknown')).toBe('unknown')
    expect(outcomeTone('something new')).toBe('unknown')
  })
})

describe('stepTone', () => {
  const step = (status: string): ProbeStep => ({ name: 'dns', status, detail: '' }) as ProbeStep

  it('never renders a skipped step as a failure', () => {
    expect(stepTone(step('ok'))).toBe('good')
    expect(stepTone(step('failed'))).toBe('bad')
    // An address literal has nothing to resolve. Toned as a failure it would
    // send somebody to look at their cluster's DNS over a step nothing did.
    expect(stepTone(step('skipped'))).toBe('unknown')
  })
})

describe('stepLabel', () => {
  it('keeps resolution and connection under separate headings', () => {
    expect(stepLabel({ name: 'dns' } as ProbeStep)).toBe('Name resolution')
    expect(stepLabel({ name: 'connect' } as ProbeStep)).toBe('Connection')
    expect(stepLabel({ name: 'http' } as ProbeStep)).toBe('HTTP request')
    // A step from a newer build renders as itself rather than vanishing.
    expect(stepLabel({ name: 'tls' } as ProbeStep)).toBe('tls')
  })
})

describe('routeLabel', () => {
  it('names how the answer was reached, which decides what it is evidence of', () => {
    expect(routeLabel({ route: 'service_proxy' } as ProbeResult)).toContain('API server')
    expect(routeLabel({ route: 'port_forward' } as ProbeResult)).toContain('port-forward')
    expect(routeLabel({ route: 'exec' } as ProbeResult)).toContain('container')
    expect(routeLabel({ route: '' } as ProbeResult)).toBe('')
  })
})

describe('takenAgo', () => {
  it('says how old an answer is, because a probe never repeats itself', () => {
    const now = 1_000_000

    expect(takenAgo(now, now)).toBe('just now')
    expect(takenAgo(now - 20_000, now)).toBe('20s ago')
    expect(takenAgo(now - 120_000, now)).toBe('2m ago')
    expect(takenAgo(now - 7_200_000, now)).toBe('2h ago')
  })

  it('never reports a negative age from a clock that moved', () => {
    expect(takenAgo(2_000, 1_000)).toBe('just now')
  })
})
