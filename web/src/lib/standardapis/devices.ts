/**
 * What a Dynamic Resource Allocation object asks for, and what it was given.
 *
 * This is how a GPU workload is actually described now: a pod names a
 * ResourceClaim or a ResourceClaimTemplate, the claim names device requests
 * against a DeviceClass, and the scheduler writes back which driver, pool and
 * device satisfied each request. The generic table can show that such an
 * object exists; only the object itself says which card a pod is holding.
 *
 * READ BY SHAPE, NEVER BY VERSION, and here that is load-bearing rather than
 * tidy. `resource.k8s.io` has been re-cut in nearly every release it has
 * existed for: the earliest versions took ONE class name on the claim, later
 * ones a list of device requests, and later still the request's own fields
 * moved down under `exactly` with a prioritised `firstAvailable` list beside
 * it. Nothing below looks at `apiVersion`. Each field is read from wherever
 * that shape puts it, and an object served by a version nobody here has seen
 * renders as much of itself as its fields allow rather than as an empty panel.
 * Where a version records something these ones do not — an older claim's
 * single class, its resource handles, its shareable flag — it is carried in a
 * field of its own rather than folded into a modern one it does not mean.
 *
 * QUOTATION, NOT VERDICT. An allocation mode, a device name and a driver name
 * are the API's and the driver's own words. Nothing here decides whether a
 * claim is stuck, whether a device is the right one, or what an unallocated
 * claim is waiting for.
 *
 * Field names follow the ResourceClaim, ResourceClaimTemplate and DeviceClass
 * types (resource.k8s.io/v1).
 */

import {
  conditionsOf,
  numberOr,
  type StandardCondition,
  type StandardRef,
} from './panel'

/** One alternative of a prioritised request, tried in the order it is listed. */
export interface DeviceSubRequestView {
  name: string
  deviceClassName: string
  /** ExactCount, All, or whatever a later version adds — verbatim. */
  allocationMode: string
  count: number | null
  /** CEL expressions, exactly as written. */
  selectors: string[]
}

/** One request for devices, from whichever shape this version writes it in. */
export interface DeviceRequestView {
  name: string
  /** Empty when the request is a prioritised list, which carries a class per alternative. */
  deviceClassName: string
  allocationMode: string
  count: number | null
  /**
   * Whether the request asked for administrative access to the device.
   *
   * Worth a row of its own: an admin claim ignores every ordinary claim's
   * access mode, so it is the difference between a device being shared and
   * being taken.
   */
  adminAccess: boolean
  selectors: string[]
  /** `firstAvailable`, in the order the scheduler tries them. */
  alternatives: DeviceSubRequestView[]
}

/** A constraint the allocated set as a whole has to satisfy. */
export interface DeviceConstraintView {
  /** Which requests it binds; empty means all of them, which is the API's meaning. */
  requests: string[]
  /** The attribute every matched device must share. */
  matchAttribute: string
  /** The attribute every matched device must differ on. */
  distinctAttribute: string
}

/** One device the scheduler actually allocated. */
export interface DeviceAllocationView {
  /** The request this satisfies, by name. */
  request: string
  driver: string
  pool: string
  device: string
  adminAccess: boolean
}

/** Something holding the claim — a pod, in practice. */
export interface ClaimConsumerView extends StandardRef {
  /**
   * The PLURAL RESOURCE the reference names ("pods"), verbatim.
   *
   * The API records a resource here and not a Kind, and the two are not
   * mechanically convertible — "endpoints" is its own plural and every
   * irregular case would be a guess. So the resource is quoted as written and
   * `kind` is filled only for the one mapping that is exact, which is also the
   * only one that occurs: a core "pods" is a Pod. Anything else renders as its
   * resource and is not offered as a link, because a link built on a guessed
   * Kind fails when it is followed.
   */
  resource: string
  uid: string
}

/** What one allocated device reports about itself, when its driver reports at all. */
export interface DeviceStatusView {
  driver: string
  pool: string
  device: string
  conditions: StandardCondition[]
  /** `networkData.ips`, for a network device that was given addresses. */
  addresses: string[]
  hardwareAddress: string
  interfaceName: string
}

/** The half of a claim that a ResourceClaimTemplate also carries. */
export interface ClaimSpecView {
  requests: DeviceRequestView[]
  constraints: DeviceConstraintView[]
  /** The drivers named in `devices.config`; the parameters themselves are opaque. */
  configDrivers: string[]
  /**
   * `spec.resourceClassName` — the ONE class the first versions of this group
   * took instead of a request list. Empty on every current version.
   */
  resourceClassName: string
  /** `spec.allocationMode`, from the versions that put it on the claim itself. */
  claimAllocationMode: string
}

export interface ResourceClaimView extends ClaimSpecView {
  /** Whether the scheduler has written an allocation at all. */
  allocated: boolean
  allocations: DeviceAllocationView[]
  allocationTimestamp: string
  /** The allocation's node selector, one line per expression. */
  nodeSelector: string[]
  /** An older version's `shareable`; null when the object does not carry it. */
  shareable: boolean | null
  deallocationRequested: boolean
  reservedFor: ClaimConsumerView[]
  deviceStatuses: DeviceStatusView[]
}

export interface ResourceClaimTemplateView {
  /** What every claim generated from this template will ask for. */
  spec: ClaimSpecView
}

export interface DeviceClassView {
  /** CEL expressions a device must satisfy, verbatim. */
  selectors: string[]
  configDrivers: string[]
  /** The extended resource name this class can be requested as, when it has one. */
  extendedResourceName: string
  /** An older version's `suitableNodes`, one line per expression. */
  suitableNodes: string[]
}

interface RawSelector {
  cel?: { expression?: string }
}

interface RawConfig {
  opaque?: { driver?: string }
}

interface RawSubRequest {
  name?: string
  deviceClassName?: string
  allocationMode?: string
  count?: number
  selectors?: RawSelector[]
}

interface RawRequest extends RawSubRequest {
  adminAccess?: boolean
  /** The later shape: the request's own fields, one level down. */
  exactly?: RawSubRequest & { adminAccess?: boolean }
  /** The prioritised shape: alternatives tried in order. */
  firstAvailable?: RawSubRequest[]
}

interface RawClaimSpec {
  devices?: {
    requests?: RawRequest[]
    constraints?: { requests?: string[]; matchAttribute?: string; distinctAttribute?: string }[]
    config?: RawConfig[]
  }
  /** The earliest shape: one class, named on the claim itself. */
  resourceClassName?: string
  allocationMode?: string
}

interface RawAllocation {
  devices?: {
    results?: {
      request?: string
      driver?: string
      pool?: string
      device?: string
      adminAccess?: boolean
    }[]
  }
  nodeSelector?: unknown
  allocationTimestamp?: string
  /** The earliest shape's equivalents. */
  resourceHandles?: { driverName?: string; data?: string }[]
  availableOnNodes?: unknown
  shareable?: boolean
}

interface ResourceClaimManifest {
  spec?: RawClaimSpec
  status?: {
    allocation?: RawAllocation
    reservedFor?: { apiGroup?: string; resource?: string; name?: string; uid?: string }[]
    deallocationRequested?: boolean
    devices?: {
      driver?: string
      pool?: string
      device?: string
      conditions?: unknown
      networkData?: { ips?: string[]; hardwareAddress?: string; interfaceName?: string }
    }[]
  }
}

/**
 * The resource name a consumer reference has to carry for it to be followable.
 *
 * ONE exact mapping rather than a rule: see `ClaimConsumerView.resource`.
 */
const POD_RESOURCE = 'pods'

/**
 * Reads a ResourceClaim, or null when there is no manifest at all.
 *
 * A claim the scheduler has not allocated comes back with `allocated: false`
 * and an empty allocation list, which the panel says in words. That is a
 * different fact from an allocation of nothing, and it is not a verdict about
 * why: a claim can be unallocated because no pod has asked for it yet, which
 * is the ordinary state of a claim created ahead of its workload.
 */
export function resourceClaim(manifest: unknown): ResourceClaimView | null {
  if (!manifest || typeof manifest !== 'object') return null
  const { spec = {}, status = {} } = manifest as ResourceClaimManifest
  const allocation = status.allocation

  return {
    ...claimSpecOf(spec),
    allocated: !!allocation,
    allocations: allocationsOf(allocation),
    allocationTimestamp: allocation?.allocationTimestamp ?? '',
    // `availableOnNodes` is what the earliest versions called the same field.
    nodeSelector: nodeSelectorTerms(allocation?.nodeSelector ?? allocation?.availableOnNodes),
    shareable: typeof allocation?.shareable === 'boolean' ? allocation.shareable : null,
    deallocationRequested: status.deallocationRequested === true,
    reservedFor: (status.reservedFor ?? [])
      .filter((consumer) => consumer && typeof consumer === 'object')
      .map((consumer) => {
        const group = consumer.apiGroup ?? ''
        const resource = consumer.resource ?? ''
        return {
          group,
          kind: !group && resource === POD_RESOURCE ? 'Pod' : '',
          // A consumer is always in the claim's own namespace; the reference
          // does not repeat it, and the panel supplies it when following.
          namespace: '',
          name: consumer.name ?? '',
          resource,
          uid: consumer.uid ?? '',
        }
      }),
    deviceStatuses: (status.devices ?? [])
      .filter((device) => device && typeof device === 'object')
      .map((device) => ({
        driver: device.driver ?? '',
        pool: device.pool ?? '',
        device: device.device ?? '',
        conditions: conditionsOf(device.conditions),
        addresses: device.networkData?.ips ?? [],
        hardwareAddress: device.networkData?.hardwareAddress ?? '',
        interfaceName: device.networkData?.interfaceName ?? '',
      })),
  }
}

/**
 * Reads a ResourceClaimTemplate, or null when there is no manifest at all.
 *
 * The template's `spec.spec` IS a claim spec, so it is read by the same
 * function — a template that diverged from the claim it stamps out would be
 * two descriptions of one thing. A template has no status of its own: the
 * claims it generates carry theirs.
 */
export function resourceClaimTemplate(manifest: unknown): ResourceClaimTemplateView | null {
  if (!manifest || typeof manifest !== 'object') return null
  const { spec = {} } = manifest as { spec?: { spec?: RawClaimSpec } }

  return { spec: claimSpecOf(spec.spec ?? {}) }
}

/**
 * Reads a DeviceClass, or null when there is no manifest at all.
 *
 * A class is entirely a selector and a configuration: nothing about it is
 * allocated, so there is no status to read and no state to be in.
 */
export function deviceClass(manifest: unknown): DeviceClassView | null {
  if (!manifest || typeof manifest !== 'object') return null
  const { spec = {} } = manifest as {
    spec?: {
      selectors?: RawSelector[]
      config?: RawConfig[]
      extendedResourceName?: string
      suitableNodes?: unknown
    }
  }

  return {
    selectors: expressionsOf(spec.selectors),
    configDrivers: driversOf(spec.config),
    extendedResourceName: spec.extendedResourceName ?? '',
    suitableNodes: nodeSelectorTerms(spec.suitableNodes),
  }
}

/** The half of a claim a template shares, read from whichever shape wrote it. */
function claimSpecOf(spec: RawClaimSpec): ClaimSpecView {
  return {
    requests: (spec.devices?.requests ?? [])
      .filter((request) => request && typeof request === 'object')
      .map(requestOf),
    constraints: (spec.devices?.constraints ?? [])
      .filter((constraint) => constraint && typeof constraint === 'object')
      .map((constraint) => ({
        requests: constraint.requests ?? [],
        matchAttribute: constraint.matchAttribute ?? '',
        distinctAttribute: constraint.distinctAttribute ?? '',
      })),
    configDrivers: driversOf(spec.devices?.config),
    resourceClassName: spec.resourceClassName ?? '',
    claimAllocationMode: spec.allocationMode ?? '',
  }
}

/**
 * One request, taking each field from wherever this version puts it.
 *
 * The flat shape and the `exactly` shape carry the same fields at different
 * depths, so the flat one is preferred and `exactly` fills in what it did not
 * write. That order matters rather than the reverse: a version that writes the
 * field at the top level does not also write it underneath, so whichever is
 * present is the one the API server served.
 */
function requestOf(request: RawRequest): DeviceRequestView {
  const exactly = request.exactly ?? {}
  return {
    name: request.name ?? '',
    deviceClassName: request.deviceClassName ?? exactly.deviceClassName ?? '',
    allocationMode: request.allocationMode ?? exactly.allocationMode ?? '',
    count: numberOr(request.count ?? exactly.count),
    adminAccess: (request.adminAccess ?? exactly.adminAccess) === true,
    selectors: expressionsOf(request.selectors ?? exactly.selectors),
    alternatives: (request.firstAvailable ?? [])
      .filter((alternative) => alternative && typeof alternative === 'object')
      .map((alternative) => ({
        name: alternative.name ?? '',
        deviceClassName: alternative.deviceClassName ?? '',
        allocationMode: alternative.allocationMode ?? '',
        count: numberOr(alternative.count),
        selectors: expressionsOf(alternative.selectors),
      })),
  }
}

/**
 * The devices an allocation records, from either shape.
 *
 * The current one names a request, a driver, a pool and a device. The earliest
 * one recorded `resourceHandles`, which named only a driver and an opaque blob
 * of its own — so those come through with the driver filled in and the rest
 * empty, which is exactly as much as that shape said.
 */
function allocationsOf(allocation: RawAllocation | undefined): DeviceAllocationView[] {
  if (!allocation) return []

  const results = (allocation.devices?.results ?? [])
    .filter((result) => result && typeof result === 'object')
    .map((result) => ({
      request: result.request ?? '',
      driver: result.driver ?? '',
      pool: result.pool ?? '',
      device: result.device ?? '',
      adminAccess: result.adminAccess === true,
    }))
  if (results.length > 0) return results

  return (allocation.resourceHandles ?? [])
    .filter((handle) => handle && typeof handle === 'object')
    .map((handle) => ({
      request: '',
      driver: handle.driverName ?? '',
      pool: '',
      device: '',
      adminAccess: false,
    }))
}

/** The CEL expressions of a selector list, dropping any that carries none. */
function expressionsOf(selectors: RawSelector[] | undefined): string[] {
  return (selectors ?? [])
    .map((selector) => selector?.cel?.expression ?? '')
    .filter(Boolean)
}

/**
 * The drivers a config list names.
 *
 * The driver and NOT its parameters: `opaque.parameters` is an arbitrary blob
 * whose meaning belongs to the driver, and a panel that printed it would be
 * printing something it cannot label. The manifest tab has it in full.
 */
function driversOf(config: RawConfig[] | undefined): string[] {
  const drivers: string[] = []
  for (const entry of config ?? []) {
    const driver = entry?.opaque?.driver
    if (driver && !drivers.includes(driver)) drivers.push(driver)
  }
  return drivers
}

interface RawNodeSelector {
  nodeSelectorTerms?: {
    matchExpressions?: { key?: string; operator?: string; values?: string[] }[]
    matchFields?: { key?: string; operator?: string; values?: string[] }[]
  }[]
}

/**
 * A node selector as one line per expression, in the syntax `kubectl` prints.
 *
 * Terms are ORed and the expressions within a term are ANDed, which no flat
 * list can show — so this is deliberately a list of the constraints in force
 * rather than a claim about how they combine. A single-term selector, which is
 * what a driver writes when it pins a claim to the node holding the device, is
 * exact either way.
 */
function nodeSelectorTerms(selector: unknown): string[] {
  if (!selector || typeof selector !== 'object') return []
  const { nodeSelectorTerms: terms = [] } = selector as RawNodeSelector

  const lines: string[] = []
  for (const term of terms) {
    if (!term || typeof term !== 'object') continue
    for (const expression of [...(term.matchExpressions ?? []), ...(term.matchFields ?? [])]) {
      if (!expression || typeof expression !== 'object') continue
      const key = expression.key ?? ''
      const operator = expression.operator ?? ''
      const values = (expression.values ?? []).join(', ')
      lines.push(values ? `${key} ${operator} (${values})` : `${key} ${operator}`.trim())
    }
  }
  return lines
}
