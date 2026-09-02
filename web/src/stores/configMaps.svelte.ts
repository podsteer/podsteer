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

/** Forgets everything, for a cluster being disconnected. */
export function forgetConfigMaps(): void {
  cache.clear()
}
