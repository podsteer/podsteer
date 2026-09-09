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
  /**
   * The object the controller actually applies, when it says so.
   *
   * ARGO CD'S TRACKING ID NAMES ITS TARGET, and that is not always the object
   * carrying it. The Deployment controller copies a Deployment's annotations
   * onto every ReplicaSet it creates, so a ReplicaSet ends up holding a
   * tracking id that reads `…:apps/Deployment:synapctx/web` — a perfectly
   * accurate statement about a Deployment, found on something else. Reading
   * it as "this ReplicaSet is managed by Argo CD" produced a warning that was
   * wrong twice over: Argo CD never touches the ReplicaSet, and what really
   * undoes an edit to one is the Deployment controller.
   *
   * Null for the older signals — the `argocd.argoproj.io/instance` label and
   * `app.kubernetes.io/managed-by` — which name no target, so nothing can be
   * compared and the object is taken at its word.
   */
  target: { kind: string; name: string; namespace: string } | null
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
    return argo(trackingId.split(':')[0] ?? '', parseTarget(trackingId))
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

function argo(application: string, target: GitOpsOwner['target'] = null): GitOpsOwner {
  return { tool: 'argocd', label: 'Argo CD', source: application, sourceKind: 'Application', target }
}

function flux(source: string, sourceKind: string): GitOpsOwner {
  return { tool: 'flux', label: 'Flux', source, sourceKind, target: null }
}

/**
 * Reads the object out of a tracking id.
 *
 * The form is `<application>:<group>/<Kind>:<namespace>/<name>`, and the
 * group is optional for core kinds — `web:/Service:platform/web`. Anything
 * that does not parse returns null, which means "no target to compare" and
 * leaves the object taken at its word.
 */
function parseTarget(trackingId: string): GitOpsOwner['target'] {
  const parts = trackingId.split(':')
  if (parts.length < 3) return null

  const kind = parts[1]?.split('/').pop() ?? ''
  const [namespace, name] = (parts[2] ?? '').split('/')
  if (!kind || !name) return null

  return { kind, name, namespace: namespace ?? '' }
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

/**
 * How an object comes to be under a GitOps controller.
 *
 * `direct` is the object the controller applies — a Deployment with Argo CD's
 * tracking annotation on it. `inherited` is everything below that: a POD
 * carries no GitOps marker at all (measured on a real cluster: the Deployment
 * had the tracking id, its pods had `app.kubernetes.io/name` and a
 * pod-template-hash and nothing else), so the only way to know a pod's spec
 * comes from Git is to ask what controls it.
 *
 * THE TWO CASES NEED DIFFERENT SENTENCES, and getting that wrong is how a
 * warning stops being read. A change to the Deployment is reverted, usually
 * within seconds where self-heal is on. A change to one of its pods is NOT
 * reverted — Argo CD reconciles the Deployment, and an in-place resize does
 * not change the Deployment, so the Application stays Synced and the pod
 * keeps the new figures. It is lost later, when something replaces the pod.
 */
export interface GitOpsManagement {
  owner: GitOpsOwner
  through: 'direct' | 'inherited'
  /** The controller carrying the marker. Empty when `through` is direct. */
  controller: { kind: string; name: string } | null
}

/** One sentence for either case, saying what actually happens. */
export function managementWarning(management: GitOpsManagement): string {
  if (management.through === 'direct') return revertWarning(management.owner)

  const { owner, controller } = management
  const by = owner.source ? `${owner.label} — the ${owner.source} ${owner.sourceKind}` : owner.label
  const above = controller ? `the ${controller.name} ${controller.kind}` : 'its controller'

  // DELIBERATELY NOT "will be reverted". The controller does not watch this
  // object, so the change stands; what ends it is the replacement, which
  // comes from Git.
  return `This belongs to ${above}, which is managed by ${by}. A change here stays on this object, but its replacement comes from Git — so the next rollout, restart or eviction brings back the figures Git holds.`
}
