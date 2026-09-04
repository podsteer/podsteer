/**
 * ConfigMap contents, read so an environment variable can show its value.
 *
 * A `configMapKeyRef` names a key in an object that is not secret and that
 * anybody who can read the pod can almost always read too — so unlike a
 * Secret, whose value is fetched only on a deliberate click, this is resolved
 * on sight. What the pane showed instead was the reference itself,
 * `<set to the key 'host' of config map 'redis'>`, which is what kubectl
 * prints because kubectl is describing a template. This is describing a
 * running container, and the value is one read away.
 *
 * THE SAME CAVEAT THE SECRET FOOTNOTE MAKES APPLIES HERE, and it is why this
 * is not cached for the life of the session: environment is injected once, at
 * start, and never updated, so a ConfigMap edited since then holds a value
 * the process has never seen. A short window keeps a pane with twenty
 * variables from making twenty reads of the same object, without pretending
 * an hour-old read is current.
 */

import { getManifest } from '$lib/api/client'
import { parse } from 'yaml'

/** How long a read is reused. Long enough for one pane, short enough to be now. */
const WINDOW_MS = 30_000

interface Entry {
  at: number
  data: Promise<Record<string, string>>
}

const cache = new Map<string, Entry>()

/**
 * The `data` of one ConfigMap, or an empty map when it cannot be read.
 *
 * NEVER REJECTS. An account that may read pods and not ConfigMaps is
 * ordinary, and the caller's fallback is to keep printing the reference —
 * which is what it was printing anyway, so a refusal costs nothing and must
 * not surface as an error in a pane about something else.
 */
export function configMapData(
  clusterId: string,
  namespace: string,
  name: string,
): Promise<Record<string, string>> {
  const key = `${clusterId}/${namespace}/${name}`
  const held = cache.get(key)
  if (held && Date.now() - held.at < WINDOW_MS) return held.data

  const data = getManifest(clusterId, 'core/v1/configmaps', namespace, name)
    .then((manifest) => {
      const parsed = parse(manifest) as { data?: Record<string, unknown> } | null
      const entries = Object.entries(parsed?.data ?? {})
      return Object.fromEntries(entries.map(([entry, value]) => [entry, String(value)]))
    })
    .catch(() => ({}) as Record<string, string>)

  cache.set(key, { at: Date.now(), data })
  return data
}

/**
 * Forces a fresh read of one ConfigMap, discarding whatever is cached.
 *
 * Called after a key in it has just been written through SetConfigMapKey: the
 * WINDOW_MS cache exists to stop one pane's twenty variables reading the same
 * object twenty times, not to keep showing what was there before a save this
 * pane itself just made.
 */
export function refreshConfigMap(
  clusterId: string,
  namespace: string,
  name: string,
): Promise<Record<string, string>> {
  cache.delete(`${clusterId}/${namespace}/${name}`)
  return configMapData(clusterId, namespace, name)
}

/**
 * Forgets one cluster's reads, for a tab being closed.
 *
 * Per-cluster rather than wholesale, so closing one tab does not make every
 * other tab re-read the ConfigMaps it already has. It was previously
 * wholesale AND never called from anywhere: a disconnected cluster's
 * ConfigMap contents stayed in memory for the life of the process, which is a
 * poor thing to be true of data read out of somebody's cluster.
 */
export function forgetConfigMaps(clusterId: string): void {
  const prefix = `${clusterId}/`
  for (const key of cache.keys()) {
    if (key.startsWith(prefix)) cache.delete(key)
  }
}
