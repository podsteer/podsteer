import { describe, expect, it } from 'vitest'

import { deviceClass, resourceClaim, resourceClaimTemplate } from './devices'

/**
 * An allocated claim in the shape v1beta1 serves: the request's fields sit on
 * the request itself.
 */
const allocatedClaim = {
  apiVersion: 'resource.k8s.io/v1beta1',
  kind: 'ResourceClaim',
  metadata: { name: 'training-gpu', namespace: 'ml' },
  spec: {
    devices: {
      requests: [
        {
          name: 'gpu',
          deviceClassName: 'nvidia-h100',
          allocationMode: 'ExactCount',
          count: 2,
          adminAccess: false,
          selectors: [
            { cel: { expression: 'device.attributes["gpu.nvidia.com"].memory >= 80' } },
          ],
        },
      ],
      constraints: [{ requests: ['gpu'], matchAttribute: 'gpu.nvidia.com/family' }],
      config: [{ opaque: { driver: 'gpu.nvidia.com', parameters: { sharing: 'none' } } }],
    },
  },
  status: {
    allocation: {
      devices: {
        results: [
          { request: 'gpu', driver: 'gpu.nvidia.com', pool: 'node-7', device: 'gpu-0' },
          {
            request: 'gpu',
            driver: 'gpu.nvidia.com',
            pool: 'node-7',
            device: 'gpu-1',
            adminAccess: true,
          },
        ],
      },
      nodeSelector: {
        nodeSelectorTerms: [
          { matchFields: [{ key: 'metadata.name', operator: 'In', values: ['node-7'] }] },
        ],
      },
      allocationTimestamp: '2026-09-04T10:00:00Z',
    },
    reservedFor: [
      { resource: 'pods', name: 'trainer-0', uid: 'aaaa-bbbb' },
      // Something that is not a core pod: quoted, and not offered as a link.
      { apiGroup: 'example.net', resource: 'trainingjobs', name: 'nightly', uid: 'cccc' },
    ],
    devices: [
      {
        driver: 'gpu.nvidia.com',
        pool: 'node-7',
        device: 'gpu-0',
        conditions: [{ type: 'Ready', status: 'True', reason: 'DeviceReady' }],
        networkData: { ips: ['10.0.0.5/24'], interfaceName: 'eth1', hardwareAddress: '02:42:ac:11' },
      },
    ],
  },
}

/**
 * An unallocated claim in the LATER shape, where the request's fields moved
 * under `exactly` and a prioritised list sits beside it.
 */
const prioritisedClaim = {
  apiVersion: 'resource.k8s.io/v1',
  kind: 'ResourceClaim',
  metadata: { name: 'inference-gpu', namespace: 'ml' },
  spec: {
    devices: {
      requests: [
        {
          name: 'accelerator',
          exactly: {
            deviceClassName: 'nvidia-a100',
            allocationMode: 'All',
            adminAccess: true,
            selectors: [{ cel: { expression: 'device.driver == "gpu.nvidia.com"' } }],
          },
        },
        {
          name: 'fallback',
          firstAvailable: [
            { name: 'big', deviceClassName: 'nvidia-a100', count: 1 },
            { name: 'small', deviceClassName: 'nvidia-t4', count: 4 },
          ],
        },
      ],
      constraints: [{ distinctAttribute: 'kubernetes.io/hostname' }],
    },
  },
  status: {},
}

/**
 * A claim served by a version this file does NOT specifically code for: the
 * earliest shape, which named one resource class on the claim and recorded
 * driver handles rather than devices.
 */
const legacyVersionClaim = {
  apiVersion: 'resource.k8s.io/v1alpha2',
  kind: 'ResourceClaim',
  metadata: { name: 'legacy-gpu', namespace: 'ml' },
  spec: {
    resourceClassName: 'gpu.example.com',
    allocationMode: 'WaitForFirstConsumer',
  },
  status: {
    allocation: {
      resourceHandles: [{ driverName: 'gpu.example.com', data: 'opaque-driver-blob' }],
      availableOnNodes: {
        nodeSelectorTerms: [
          { matchExpressions: [{ key: 'gpu', operator: 'Exists' }] },
        ],
      },
      shareable: true,
    },
    reservedFor: [{ resource: 'pods', name: 'legacy-0', uid: 'dddd' }],
    deallocationRequested: true,
  },
}

const template = {
  apiVersion: 'resource.k8s.io/v1',
  kind: 'ResourceClaimTemplate',
  metadata: { name: 'gpu-template', namespace: 'ml' },
  spec: {
    metadata: { labels: { app: 'trainer' } },
    spec: {
      devices: {
        requests: [{ name: 'gpu', exactly: { deviceClassName: 'nvidia-h100', count: 1 } }],
      },
    },
  },
}

describe('reading a ResourceClaim', () => {
  it('quotes the device requests, their mode and their CEL selectors', () => {
    const view = resourceClaim(allocatedClaim)

    expect(view?.requests).toHaveLength(1)
    expect(view?.requests[0]).toMatchObject({
      name: 'gpu',
      deviceClassName: 'nvidia-h100',
      allocationMode: 'ExactCount',
      count: 2,
      adminAccess: false,
    })
    expect(view?.requests[0].selectors).toEqual([
      'device.attributes["gpu.nvidia.com"].memory >= 80',
    ])
  })

  it('names a config’s driver and never its opaque parameters', () => {
    // The parameters belong to the driver and mean nothing here, so printing
    // them would be printing something this cannot label.
    expect(resourceClaim(allocatedClaim)?.configDrivers).toEqual(['gpu.nvidia.com'])
  })

  it('reads the allocation result device by device', () => {
    const view = resourceClaim(allocatedClaim)

    expect(view?.allocated).toBe(true)
    expect(view?.allocations).toEqual([
      { request: 'gpu', driver: 'gpu.nvidia.com', pool: 'node-7', device: 'gpu-0', adminAccess: false },
      { request: 'gpu', driver: 'gpu.nvidia.com', pool: 'node-7', device: 'gpu-1', adminAccess: true },
    ])
    expect(view?.nodeSelector).toEqual(['metadata.name In (node-7)'])
    expect(view?.allocationTimestamp).toBe('2026-09-04T10:00:00Z')
  })

  it('resolves only a core pod consumer to a Kind, and quotes the rest', () => {
    // "pods" to Pod is the one exact mapping; a resource is not a Kind, and a
    // link built on a guessed one fails when it is followed.
    const reserved = resourceClaim(allocatedClaim)?.reservedFor

    expect(reserved?.[0]).toMatchObject({ kind: 'Pod', resource: 'pods', name: 'trainer-0' })
    expect(reserved?.[1]).toMatchObject({
      kind: '',
      group: 'example.net',
      resource: 'trainingjobs',
      name: 'nightly',
    })
  })

  it('carries what a device’s own driver reported about it', () => {
    const status = resourceClaim(allocatedClaim)?.deviceStatuses[0]

    expect(status).toMatchObject({ driver: 'gpu.nvidia.com', pool: 'node-7', device: 'gpu-0' })
    expect(status?.addresses).toEqual(['10.0.0.5/24'])
    expect(status?.interfaceName).toBe('eth1')
    expect(status?.conditions[0]).toMatchObject({ type: 'Ready', status: 'True' })
  })

  it('reads the later shape, where a request’s fields moved under `exactly`', () => {
    const view = resourceClaim(prioritisedClaim)

    expect(view?.requests[0]).toMatchObject({
      name: 'accelerator',
      deviceClassName: 'nvidia-a100',
      allocationMode: 'All',
      adminAccess: true,
    })
    expect(view?.requests[0].selectors).toEqual(['device.driver == "gpu.nvidia.com"'])
  })

  it('keeps a prioritised request’s alternatives in the order the scheduler tries them', () => {
    expect(resourceClaim(prioritisedClaim)?.requests[1]).toMatchObject({
      name: 'fallback',
      // A prioritised request carries no class of its own; each alternative
      // does, so inventing one here would name a class the object does not.
      deviceClassName: '',
      alternatives: [
        { name: 'big', deviceClassName: 'nvidia-a100', allocationMode: '', count: 1, selectors: [] },
        { name: 'small', deviceClassName: 'nvidia-t4', allocationMode: '', count: 4, selectors: [] },
      ],
    })
  })

  it('separates an unallocated claim from an allocation of nothing', () => {
    const view = resourceClaim(prioritisedClaim)

    expect(view?.allocated).toBe(false)
    expect(view?.allocations).toEqual([])
    expect(view?.reservedFor).toEqual([])
    expect(view?.shareable).toBeNull()
    expect(view?.deallocationRequested).toBe(false)
  })

  it('reads a claim from a version it was not written for', () => {
    // resource.k8s.io has been re-cut in nearly every release, so the parser
    // reads by SHAPE and never by apiVersion. The earliest shape named ONE
    // class on the claim and recorded driver handles rather than devices: it
    // renders as much as it says, in that version's own words, instead of as
    // an empty panel.
    const view = resourceClaim(legacyVersionClaim)

    expect(view?.resourceClassName).toBe('gpu.example.com')
    expect(view?.claimAllocationMode).toBe('WaitForFirstConsumer')
    // Not folded into `requests`, which would claim a DeviceClass this object
    // does not name.
    expect(view?.requests).toEqual([])
    expect(view?.allocated).toBe(true)
    expect(view?.allocations).toEqual([
      { request: '', driver: 'gpu.example.com', pool: '', device: '', adminAccess: false },
    ])
    expect(view?.nodeSelector).toEqual(['gpu Exists'])
    expect(view?.shareable).toBe(true)
    expect(view?.deallocationRequested).toBe(true)
    expect(view?.reservedFor[0]).toMatchObject({ kind: 'Pod', name: 'legacy-0' })
  })

  it('keeps an unknown allocation mode as itself', () => {
    const view = resourceClaim({
      spec: { devices: { requests: [{ name: 'x', allocationMode: 'SomethingNewIn134' }] } },
    })

    expect(view?.requests[0].allocationMode).toBe('SomethingNewIn134')
  })

  it('reads a claim with no status at all', () => {
    const view = resourceClaim({ spec: { devices: { requests: [{ name: 'gpu' }] } } })

    expect(view?.allocated).toBe(false)
    expect(view?.deviceStatuses).toEqual([])
    expect(view?.requests[0].count).toBeNull()
  })

  it('answers null only when there is no manifest at all', () => {
    expect(resourceClaim(null)).toBeNull()
    expect(resourceClaim('not an object')).toBeNull()
    expect(resourceClaim({})).not.toBeNull()
  })
})

describe('reading a ResourceClaimTemplate', () => {
  it('reads the nested claim spec with the same parser', () => {
    const view = resourceClaimTemplate(template)

    expect(view?.spec.requests[0]).toMatchObject({
      name: 'gpu',
      deviceClassName: 'nvidia-h100',
      count: 1,
    })
  })

  it('reads a template whose spec is empty', () => {
    expect(resourceClaimTemplate({ spec: {} })?.spec.requests).toEqual([])
    expect(resourceClaimTemplate({})?.spec.constraints).toEqual([])
  })

  it('answers null only when there is no manifest at all', () => {
    expect(resourceClaimTemplate(null)).toBeNull()
    expect(resourceClaimTemplate([])).not.toBeNull()
  })
})

describe('reading a DeviceClass', () => {
  it('quotes the CEL selectors and the drivers its config belongs to', () => {
    const view = deviceClass({
      apiVersion: 'resource.k8s.io/v1',
      kind: 'DeviceClass',
      metadata: { name: 'nvidia-h100' },
      spec: {
        selectors: [{ cel: { expression: 'device.driver == "gpu.nvidia.com"' } }],
        config: [{ opaque: { driver: 'gpu.nvidia.com', parameters: { mode: 'exclusive' } } }],
        extendedResourceName: 'nvidia.com/h100',
      },
    })

    expect(view?.selectors).toEqual(['device.driver == "gpu.nvidia.com"'])
    expect(view?.configDrivers).toEqual(['gpu.nvidia.com'])
    expect(view?.extendedResourceName).toBe('nvidia.com/h100')
    expect(view?.suitableNodes).toEqual([])
  })

  it('reads a class from a version that still carried suitableNodes', () => {
    const view = deviceClass({
      apiVersion: 'resource.k8s.io/v1alpha3',
      spec: {
        suitableNodes: {
          nodeSelectorTerms: [
            { matchExpressions: [{ key: 'accelerator', operator: 'In', values: ['h100'] }] },
          ],
        },
      },
    })

    expect(view?.suitableNodes).toEqual(['accelerator In (h100)'])
    expect(view?.selectors).toEqual([])
  })

  it('reads a class that selects on nothing', () => {
    const view = deviceClass({ spec: {} })

    expect(view?.selectors).toEqual([])
    expect(view?.configDrivers).toEqual([])
    expect(view?.extendedResourceName).toBe('')
  })

  it('answers null only when there is no manifest at all', () => {
    expect(deviceClass(undefined)).toBeNull()
    expect(deviceClass({})).not.toBeNull()
  })
})
