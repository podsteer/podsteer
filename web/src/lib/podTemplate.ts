/**
 * The pod template a controller carries.
 *
 * A CronJob nests it one level deeper than the rest, under the Job it
 * creates — which is most of the difference between the two kinds, and the
 * reason this is a lookup rather than a field access at the call site. Get it
 * wrong for CronJobs and the panel shows nothing at all, on exactly the kind
 * whose template matters most: between runs there are no pods to open, so the
 * template is the only description of what it does.
 *
 * FROM THE OBJECT'S OWN MANIFEST, NEVER FROM A LIST. A ReplicaSet read out of
 * the watch store has had its template stripped to the container images, so a
 * panel sourcing one from there would show a container with no environment,
 * no volumes and no probes — and would be right about the images, which is
 * the kind of wrong that looks fine. See CLAUDE.md.
 */

/** The parts of a pod template anything here reads. */
export interface PodTemplate {
  metadata?: { labels?: Record<string, string> }
  spec?: {
    containers?: Record<string, unknown>[]
    initContainers?: Record<string, unknown>[]
    volumes?: Record<string, unknown>[]
  }
}

interface Manifest {
  spec?: {
    template?: PodTemplate
    jobTemplate?: { spec?: { template?: PodTemplate } }
  }
}

/**
 * The template inside a controller's manifest, or null when there is none.
 *
 * Null rather than an empty template, so a caller renders nothing rather than
 * an empty section: a kind this does not know about has not got a template
 * with no containers in it, it has one this cannot find.
 */
export function podTemplateOf(manifest: unknown, kind: string | undefined): PodTemplate | null {
  const spec = (manifest as Manifest | null)?.spec
  if (!spec) return null

  const template = kind === 'CronJob' ? spec.jobTemplate?.spec?.template : spec.template
  return template ?? null
}
