/**
 * The monitoring backends a cluster offers, for Settings → Clusters' picker.
 *
 * `MetricsQueryAPI.Backends(clusterID)` lists every candidate discovery found
 * (the same discovery the chart's automatic pick ranks). It is looked up on
 * the generated module BY NAME at run time, so this file builds against
 * bindings that do not have it yet and starts listing the moment they do;
 * until then `listMetricsBackends` answers null and the picker offers the
 * automatic choice and whatever is already pinned. Pinning is
 * `SetMetricsQuery`'s preferred namespace and service, which exists already.
 */
import * as metricsQuery from '$bindings/metricsqueryapi'
import { toApiError } from '$lib/api/errors'

/** One discovered backend, read defensively: only the names are required. */
export interface BackendCandidate {
  namespace: string
  service: string
  /** "Prometheus", "VictoriaMetrics"… or the raw kind. */
  product: string
  /** What PodSteer knows about it in words — verified for this cluster, a fleet… */
  detail: string
}

type Lister = (clusterID: string) => Promise<unknown>

let lister: Lister | null | undefined

function bound(): Lister | null {
  if (lister !== undefined) return lister
  const name = 'Backends'
  const found = (metricsQuery as unknown as Record<string, unknown>)[name]
  lister = typeof found === 'function' ? (found as Lister) : null
  return lister
}

/** Replaces the lister — a test's fixture, or undefined to look it up again. */
export function setBackendLister(next: Lister | null | undefined): void {
  lister = next
}

function text(value: unknown): string {
  return typeof value === 'string' ? value : ''
}

/** The candidates, or null when this build cannot list them. */
export async function listMetricsBackends(clusterId: string): Promise<BackendCandidate[] | null> {
  const call = bound()
  if (!call) return null
  let raw: unknown
  try {
    raw = await call(clusterId)
  } catch (error) {
    throw toApiError(error)
  }
  const list = Array.isArray(raw) ? raw : Array.isArray((raw as { backends?: unknown })?.backends) ? (raw as { backends: unknown[] }).backends : []
  return list
    .map((item) => {
      const o = (item ?? {}) as Record<string, unknown>
      return {
        namespace: text(o.namespace),
        service: text(o.service),
        product: text(o.product) || text(o.kind),
        detail: text(o.detail) || text(o.message) || text(o.verification),
      }
    })
    .filter((candidate) => candidate.namespace && candidate.service)
}

/** The value a picker option carries: "" for automatic, else namespace/service. */
export function backendValue(namespace: string, service: string): string {
  return namespace && service ? `${namespace}/${service}` : ''
}

/** Back from an option value to what SetMetricsQuery stores. */
export function parseBackendValue(value: string): { preferredNamespace: string; preferredService: string } {
  const slash = value.indexOf('/')
  if (slash <= 0) return { preferredNamespace: '', preferredService: '' }
  return { preferredNamespace: value.slice(0, slash), preferredService: value.slice(slash + 1) }
}

/**
 * The picker's options: the automatic choice first, then every candidate, and
 * the pinned backend even when discovery no longer offers it — so a pin is
 * never silently shown as something else.
 */
export function backendOptions(
  candidates: readonly BackendCandidate[] | null,
  pinned: { preferredNamespace: string; preferredService: string },
): { value: string; label: string; hint?: string }[] {
  const options: { value: string; label: string; hint?: string }[] = [
    { value: '', label: 'Whichever PodSteer finds' },
  ]
  for (const candidate of candidates ?? []) {
    options.push({
      value: backendValue(candidate.namespace, candidate.service),
      label: `${candidate.product || 'Backend'}: ${candidate.service} in ${candidate.namespace}`,
      hint: candidate.detail || undefined,
    })
  }
  const pin = backendValue(pinned.preferredNamespace, pinned.preferredService)
  if (pin && !options.some((option) => option.value === pin)) {
    options.push({
      value: pin,
      label: `${pinned.preferredService} in ${pinned.preferredNamespace}`,
      hint: candidates ? 'pinned, not found now' : 'pinned',
    })
  }
  return options
}
