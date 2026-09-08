import { describe, expect, it } from 'vitest'

import { serviceForwardKey, servicePortRows, servicePortSelector } from './servicePorts'

const clusterIP = (ports: unknown[], extra: Record<string, unknown> = {}) => ({
  spec: { type: 'ClusterIP', selector: { app: 'web' }, ports, ...extra },
})

describe('servicePortRows', () => {
  it('quotes a named port and the number it targets', () => {
    const [row] = servicePortRows(
      clusterIP([{ name: 'http', port: 80, targetPort: 8080, protocol: 'TCP' }]),
    )

    expect(row).toMatchObject({
      name: 'http',
      port: 80,
      protocol: 'TCP',
      target: '8080',
      forwardable: true,
      reason: '',
    })
  })

  it('prints an absent targetPort as the service port, which is what it means', () => {
    // THE TRAP THIS ASSERTS AGAINST. An absent targetPort defaults to the
    // service port; printing it as blank or as 0 would say nothing is
    // listening, which is a different and much more alarming claim.
    const [row] = servicePortRows(clusterIP([{ port: 5432 }]))
    expect(row.target).toBe('5432')
  })

  it('leaves a named targetPort as a name, because only a pod can resolve it', () => {
    const [row] = servicePortRows(clusterIP([{ name: 'web', port: 80, targetPort: 'http' }]))
    expect(row.target).toBe('http')
  })

  it('reads a zero targetPort as absent rather than as port zero', () => {
    // Both shapes reach the frontend: an unset intstr serialises as 0 through
    // one path and as "0" through another, and neither is a port.
    expect(servicePortRows(clusterIP([{ port: 443, targetPort: 0 }]))[0].target).toBe('443')
    expect(servicePortRows(clusterIP([{ port: 443, targetPort: '0' }]))[0].target).toBe('443')
  })

  it('carries a node port when the Service publishes one', () => {
    const [row] = servicePortRows(
      clusterIP([{ name: 'http', port: 80, nodePort: 31234 }], { type: 'NodePort' }),
    )
    expect(row.nodePort).toBe(31234)
  })
})

describe('which ports can actually be forwarded', () => {
  it('refuses UDP by naming the transport, not by hiding the port', () => {
    // The port is still worth listing — somebody looking for a DNS Service's
    // 53 and not finding it would conclude the panel is broken. What it does
    // not get is a button the backend would refuse.
    const [row] = servicePortRows(clusterIP([{ name: 'dns', port: 53, protocol: 'UDP' }]))

    expect(row.forwardable).toBe(false)
    expect(row.reason).toContain('TCP only')
    expect(row.reason).toContain('UDP')
  })

  it('refuses every port of an ExternalName Service', () => {
    const rows = servicePortRows({
      spec: { type: 'ExternalName', externalName: 'db.example.com', ports: [{ port: 5432 }] },
    })

    expect(rows[0].forwardable).toBe(false)
    expect(rows[0].reason).toContain('DNS alias')
  })

  it('refuses a Service with no selector, whose endpoints are set by hand', () => {
    const rows = servicePortRows({ spec: { type: 'ClusterIP', ports: [{ port: 5432 }] } })

    expect(rows[0].forwardable).toBe(false)
    expect(rows[0].reason).toContain('selects no pods')
  })

  it('allows a headless Service, which still selects pods', () => {
    // clusterIP: None changes how it is ADDRESSED, not whether there are pods
    // behind it — and a forward lands on a pod either way.
    const rows = servicePortRows({
      spec: { clusterIP: 'None', selector: { app: 'db' }, ports: [{ port: 5432 }] },
    })

    expect(rows[0].forwardable).toBe(true)
  })

  it('returns nothing for a manifest that is not a Service, rather than throwing', () => {
    expect(servicePortRows(null)).toEqual([])
    expect(servicePortRows({})).toEqual([])
    expect(servicePortRows({ spec: { ports: [{ name: 'broken' }] } })).toEqual([])
  })
})

describe('how a port is asked for', () => {
  it('sends the name when there is one and the number when there is not', () => {
    expect(servicePortSelector({ name: 'http', port: 80 })).toBe('http')
    expect(servicePortSelector({ name: '', port: 80 })).toBe('80')
  })

  it('keys a Service forward by the cluster, so two clusters do not share one', () => {
    expect(serviceForwardKey('prod', 'web', 'api', 80)).not.toBe(
      serviceForwardKey('staging', 'web', 'api', 80),
    )
  })
})
