/**
 * Which build an object is, and when somebody last changed it — for the
 * Identity section.
 *
 * Both are QUOTATIONS with a stated source, never inferences dressed up as
 * facts: the version is a label an author wrote or a tag an image carries,
 * and the last change is a time the API server recorded.
 */

/** The recommended label an author sets to say which version this is. */
export const VERSION_LABEL = 'app.kubernetes.io/version'

export interface ObjectVersion {
  version: string
  /** Where it came from, for the row's tooltip. */
  source: string
}

interface ContainerLike {
  name?: string
  image?: string
}

/**
 * The tag in an image reference, or null when it names no usable version.
 *
 * A digest pins bytes, not a version, and says nothing a person can read;
 * `latest` is the absence of a version. Both return null rather than a value
 * that would read as an answer. The tag is only what follows the LAST colon
 * after the last slash — `registry:5000/app` has a port, not a tag.
 */
export function imageTag(image: string | undefined): string | null {
  if (!image) return null
  const withoutDigest = image.split('@')[0]
  const lastSlash = withoutDigest.lastIndexOf('/')
  const colon = withoutDigest.lastIndexOf(':')
  if (colon <= lastSlash) return null
  const tag = withoutDigest.slice(colon + 1)
  return tag && tag !== 'latest' ? tag : null
}

/**
 * The object's version: its label when it has one, else its main image's tag.
 *
 * THE MAIN CONTAINER, NOT THE FIRST. A sidecar — a mesh proxy, a log shipper
 * — is routinely listed first and carries somebody else's version. The
 * container named like the application (its `app.kubernetes.io/name` label,
 * or the object's own name) is preferred, and the first only when none is.
 */
export function objectVersion(
  metadata: { name?: string; labels?: Record<string, string> } | undefined,
  containers: ContainerLike[],
): ObjectVersion | null {
  const labelled = metadata?.labels?.[VERSION_LABEL]?.trim()
  if (labelled) return { version: labelled, source: `The ${VERSION_LABEL} label` }

  const appName = metadata?.labels?.['app.kubernetes.io/name']
  const main =
    containers.find((container) => container.name && container.name === appName) ??
    containers.find((container) => container.name && container.name === metadata?.name) ??
    containers[0]
  const tag = imageTag(main?.image)
  if (!tag || !main) return null
  return {
    version: tag,
    source: `The image tag of the ${main.name ?? 'main'} container (${main.image}) — no ${VERSION_LABEL} label is set`,
  }
}

interface ManagedFieldsEntry {
  time?: string
  subresource?: string
}

/**
 * When the object itself was last written, from `metadata.managedFields`.
 *
 * Kubernetes keeps no "updated at" field; managedFields records, per field
 * manager, when that manager last wrote. STATUS WRITES ARE EXCLUDED, and
 * that is the whole of the rule: a controller rewrites a Deployment's status
 * on every reconcile, so counting them would report "updated seconds ago" for
 * an object nobody has touched in a month. What is left is a change to its
 * spec or metadata — an apply, an edit, a scale, a sync.
 *
 * Null when nothing qualifies: an object created and never since changed has
 * a creation time and no update, and saying so is truer than repeating it.
 */
export function lastUpdated(managedFields: ManagedFieldsEntry[] | undefined, created?: string): string | null {
  let latest: string | null = null
  for (const entry of managedFields ?? []) {
    if (!entry.time || entry.subresource) continue
    if (latest === null || Date.parse(entry.time) > Date.parse(latest)) latest = entry.time
  }
  if (latest && created && Date.parse(latest) <= Date.parse(created)) return null
  return latest
}
