/**
 * The traffic calls into the Go backend, behind a seam.
 *
 * `bound` is the generated `TrafficAPI` — Sources(clusterID) and
 * Traffic(clusterID, namespaces, all, source, window) — unless a test installs
 * a fixture with `setTrafficBindings`; null restores the not-available state.
 * The generated types are wider than the contract (unions typed `string`,
 * slices `| null`), so every answer is narrowed here, once.
 *
 * Nothing here queries on its own: the panel calls these on a gesture only.
 */

import { Sources as bindSources, Traffic as bindTraffic } from "$bindings/trafficapi";
import type * as wails from "$bindings/models";
import { ApiError, toApiError } from "$lib/api/errors";
import type {
  TrafficLayer,
  TrafficSourceName,
  TrafficSources,
  TrafficWindow,
} from "./contract";

export interface TrafficBindings {
  Sources(clusterID: string): Promise<TrafficSources | wails.TrafficSources>;
  Traffic(
    clusterID: string,
    namespaces: string[],
    all: boolean,
    source: string,
    window: string,
  ): Promise<TrafficLayer | wails.TrafficLayer>;
}

const SOURCES = new Set<TrafficSourceName>(["istio", "linkerd", "beyla", "caretta", "hubble"]);
const WINDOWS = new Set<TrafficWindow>(["5m", "15m", "1h"]);

/** The generated sources answer, narrowed: unknown source names are dropped. */
export function normaliseSources(
  s: TrafficSources | wails.TrafficSources | null | undefined,
): TrafficSources {
  return {
    backend: s?.backend ?? "",
    sources: (s?.sources ?? [])
      .filter((x) => SOURCES.has(x.source as TrafficSourceName))
      .map((x) => ({ source: x.source as TrafficSourceName, available: x.available, detail: x.detail ?? "" })),
    status: s?.status ?? "",
    message: s?.message ?? "",
  };
}

/** The generated layer, narrowed to the contract, nils as empty lists. */
export function normaliseLayer(
  l: TrafficLayer | wails.TrafficLayer | null | undefined,
  asked: { source: TrafficSourceName; window: TrafficWindow },
): TrafficLayer {
  return {
    source: SOURCES.has(l?.source as TrafficSourceName) ? (l!.source as TrafficSourceName) : asked.source,
    window: WINDOWS.has(l?.window as TrafficWindow) ? (l!.window as TrafficWindow) : asked.window,
    edges: (l?.edges ?? []).map((e) => ({ ...e, latencyBeyondBuckets: e.latencyBeyondBuckets ?? false })),
    unmapped: l?.unmapped ?? [],
    status: l?.status ?? "",
    message: l?.message ?? "",
    provenance: l?.provenance ?? null,
    expressions: l?.expressions ?? [],
  };
}

/**
 * The traffic backend is not in this build. Carries `internal` because the
 * error-code list is mirrored from Go and checked against it; the
 * `notAvailable` flag is what callers branch on.
 */
export class TrafficNotAvailableError extends ApiError {
  readonly notAvailable = true;

  constructor() {
    super(
      "internal",
      "Observed traffic is not available in this build of PodSteer yet.",
    );
    this.name = "TrafficNotAvailableError";
  }
}

export function isTrafficNotAvailable(
  e: unknown,
): e is TrafficNotAvailableError {
  return e instanceof TrafficNotAvailableError;
}

let bound: TrafficBindings | null = { Sources: bindSources, Traffic: bindTraffic };

/** Installs a fixture (tests) or null (not available); `restoreTrafficBindings` puts the real ones back. */
export function setTrafficBindings(b: TrafficBindings | null): void {
  bound = b;
}

export function restoreTrafficBindings(): void {
  bound = { Sources: bindSources, Traffic: bindTraffic };
}

function need(): TrafficBindings {
  if (!bound) throw new TrafficNotAvailableError();
  return bound;
}

/** Which traffic sources this cluster's metrics backend can answer for. */
export async function trafficSources(
  clusterId: string,
): Promise<TrafficSources> {
  const b = need();
  try {
    return normaliseSources(await b.Sources(clusterId));
  } catch (e) {
    throw toApiError(e);
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
  const b = need();
  try {
    return normaliseLayer(await b.Traffic(clusterId, namespaces, all, source, window), { source, window });
  } catch (e) {
    throw toApiError(e);
  }
}
