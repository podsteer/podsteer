/**
 * The traffic calls into the Go backend, behind a seam.
 *
 * The Wails bindings for `TrafficAPI` do not exist yet (they are generated
 * once the backend lands), so nothing imports `$bindings/trafficapi` and
 * the calls throw a typed not-available error until the bindings are wired.
 * To wire them, replace `bound` with:
 *
 *   import { Sources, Traffic } from '$bindings/trafficapi'
 *   let bound: TrafficBindings | null = { Sources, Traffic }
 *
 * The positional signatures mirror the Go ones exactly:
 *   TrafficAPI.Sources(clusterID)
 *   TrafficAPI.Traffic(clusterID, namespaces, all, source, window)
 *
 * Nothing here queries on its own: the panel calls these on a gesture only.
 */

import { ApiError, toApiError } from '$lib/api/errors'
import type { TrafficLayer, TrafficSourceName, TrafficSources, TrafficWindow } from './contract'

export interface TrafficBindings {
  Sources(clusterID: string): Promise<TrafficSources>
  Traffic(
    clusterID: string,
    namespaces: string[],
    all: boolean,
    source: string,
    window: string,
  ): Promise<TrafficLayer>
}

/**
 * The traffic backend is not in this build. Carries `internal` because the
 * error-code list is mirrored from Go and checked against it; the
 * `notAvailable` flag is what callers branch on.
 */
export class TrafficNotAvailableError extends ApiError {
  readonly notAvailable = true

  constructor() {
    super('internal', 'Observed traffic is not available in this build of PodSteer yet.')
    this.name = 'TrafficNotAvailableError'
  }
}

export function isTrafficNotAvailable(e: unknown): e is TrafficNotAvailableError {
  return e instanceof TrafficNotAvailableError
}

let bound: TrafficBindings | null = null

/** Installs bindings (or a fixture in tests); null restores the not-available state. */
export function setTrafficBindings(b: TrafficBindings | null): void {
  bound = b
}

function need(): TrafficBindings {
  if (!bound) throw new TrafficNotAvailableError()
  return bound
}

/** Which traffic sources this cluster's metrics backend can answer for. */
export async function trafficSources(clusterId: string): Promise<TrafficSources> {
  const b = need()
  try {
    return await b.Sources(clusterId)
  } catch (e) {
    throw toApiError(e)
  }
}

/** The observed traffic for a scope from one source over one window. */
export async function traffic(
  clusterId: string,
  namespaces: string[],
  all: boolean,
  source: TrafficSourceName,
  window: TrafficWindow,
): Promise<TrafficLayer> {
  const b = need()
  try {
    return await b.Traffic(clusterId, namespaces, all, source, window)
  } catch (e) {
    throw toApiError(e)
  }
}
