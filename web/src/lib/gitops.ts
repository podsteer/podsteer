/**
 * Recognising an object that a GitOps controller owns.
 *
 * Editing such an object by hand is not wrong so much as temporary: the
 * controller reconciles against what is in Git, and whatever it finds that
 * does not match gets put back — usually within minutes, sometimes within
 * seconds, and with no notification that it happened. Somebody who does not
 * know that has every reason to believe their change took, because it does
 * take, briefly.
 *
 * So this exists to say who owns the object, before the edit rather than
 * after it.
 */

/** The GitOps controllers PodSteer can recognise. */
export type GitOpsTool = 'argocd' | 'flux'

export interface GitOpsOwner {
  tool: GitOpsTool
  /** Display name of the controller. */
  label: string
  /**
   * What owns it — an Argo CD Application, or a Flux Kustomization or
   * HelmRelease. Empty when the object says it is managed but not by what.
   */
  source: string
  /** The kind of the owning object, for wording that reads naturally. */
  sourceKind: string
}

/** Metadata as it appears in a parsed manifest. */
interface Metadata {
  labels?: Record<string, string>
  annotations?: Record<string, string>
}

/**
 * Identifies the controller managing an object, or null.
 *
 * Deliberately does NOT treat `app.kubernetes.io/instance` as an Argo CD
 * signal on its own, even though Argo CD's default tracking method writes it.
 * Helm writes the same label for its release name, and the two are
 * indistinguishable once written. Measured on a real cluster: of 61
 * deployments, 39 carried Argo CD's own tracking annotation and 40 carried
 * `instance` — so trusting the label would have claimed one object was
 * managed by a controller that had never heard of it. A false "your edit will
 * be reverted" is worse than no warning, because it teaches people to ignore
 * the warning.
 */
export function gitOpsOwner(manifest: unknown): GitOpsOwner | null {
  const metadata = (manifest as { metadata?: Metadata } | null)?.metadata
  if (!metadata) return null

  const labels = metadata.labels ?? {}
  const annotations = metadata.annotations ?? {}

  // --- Argo CD ------------------------------------------------------------
  //
  // The tracking id is Argo CD's own record of ownership and is the only
  // signal that names the owning Application reliably. Its form is
  // `<application>:<group>/<Kind>:<namespace>/<name>`, and the Application is
  // the part before the first colon — which on a real cluster is a different
  // value from the `instance` label beside it, the label naming a parent app.
  const trackingId = annotations['argocd.argoproj.io/tracking-id']
  if (trackingId) {
    return argo(trackingId.split(':')[0] ?? '')
  }

  // The older tracking method, and unambiguous because it is Argo CD's own
  // namespace rather than the shared `app.kubernetes.io` one.
  const argoInstance = labels['argocd.argoproj.io/instance']
  if (argoInstance) return argo(argoInstance)

  if (labels['app.kubernetes.io/managed-by'] === 'argocd') return argo('')

  // --- Flux ---------------------------------------------------------------
  //
  // Flux labels the objects it applies with the Kustomization or HelmRelease
  // responsible, which is both the signal and the answer to "owned by what".
  const kustomization = labels['kustomize.toolkit.fluxcd.io/name']
  if (kustomization) return flux(kustomization, 'Kustomization')

  const helmRelease = labels['helm.toolkit.fluxcd.io/name']
  if (helmRelease) return flux(helmRelease, 'HelmRelease')

  return null
}

function argo(application: string): GitOpsOwner {
  return { tool: 'argocd', label: 'Argo CD', source: application, sourceKind: 'Application' }
}

function flux(source: string, sourceKind: string): GitOpsOwner {
  return { tool: 'flux', label: 'Flux', source, sourceKind }
}

/**
 * One sentence saying what will happen to a hand-made change.
 *
 * Names the controller and, when it is known, the thing that will do the
 * reverting — "reverted by Argo CD" is a warning, "reverted by the
 * authentication-identity-service Application" is somewhere to go and look.
 */
export function revertWarning(owner: GitOpsOwner): string {
  const by = owner.source
    ? `${owner.label} — the ${owner.source} ${owner.sourceKind}`
    : owner.label

  return `This object is managed by ${by}. Changes made here are reverted the next time it reconciles against Git.`
}
