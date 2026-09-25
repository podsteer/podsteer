/**
 * Finding the GitOps controller above an object that carries no marker.
 *
 * A POD IS NEVER MARKED. Argo CD writes its tracking annotation on the object
 * it applies — the Deployment — and the pods that Deployment eventually
 * produces inherit its template's labels and nothing else. Measured on a real
 * cluster: the Deployment carried `argocd.argoproj.io/tracking-id` and
 * `app.kubernetes.io/managed-by: argocd`; its pod carried neither, only
 * `app.kubernetes.io/name`, a version, and a pod-template-hash.
 *
 * So the question "is what I am about to change actually held in Git" cannot
 * be answered from the object in front of the operator. It is answered by
 * walking up.
 *
 * TWO HOPS AT MOST, which is not a limit so much as the shape of Kubernetes:
 * a pod's controller is a ReplicaSet and the ReplicaSet's is a Deployment; a
 * scheduled pod's controller is a Job and the Job's is a CronJob; a
 * StatefulSet and a DaemonSet own their pods directly. Nothing owns a
 * Deployment, a CronJob, a StatefulSet or a DaemonSet, so there is no third
 * hop to look for. The Kubernetes adapter's own ownerChain says the same
 * thing about the same walk.
 */

import { gitOpsOwner, type GitOpsManagement, type GitOpsOwner } from './gitops'

/** Reads one object's manifest. Injected so the walk is testable. */
export type ManifestReader = (kindId: string, namespace: string, name: string) => Promise<unknown>

/** The controller reference as it appears in a parsed manifest. */
interface OwnerReference {
  apiVersion?: string
  kind?: string
  name?: string
  controller?: boolean
}

/**
 * The kinds worth following, and the resource name each one is served under.
 *
 * AN ALLOWLIST, NOT A PLURALISER. Deriving `rollouts` from `Rollout` by adding
 * an `s` is right often enough to be dangerous: it would send a read for a
 * kind this map has never seen, on a group it has not checked, and a 404 on
 * an invented path is a worse answer than not walking. Every kind here owns
 * pods or owns something that does.
 */
const FOLLOWABLE: Record<string, string> = {
  ReplicaSet: 'apps/v1/replicasets',
  Deployment: 'apps/v1/deployments',
  StatefulSet: 'apps/v1/statefulsets',
  DaemonSet: 'apps/v1/daemonsets',
  Job: 'batch/v1/jobs',
  CronJob: 'batch/v1/cronjobs',
}

/** The controlling owner reference, or null. */
function controllerOf(manifest: unknown): OwnerReference | null {
  const owners = (manifest as { metadata?: { ownerReferences?: OwnerReference[] } } | null)?.metadata
    ?.ownerReferences
  if (!Array.isArray(owners)) return null
  return owners.find((owner) => owner.controller === true) ?? null
}

/**
 * Reports which GitOps controller holds this object's spec, if any.
 *
 * The object itself is checked first, so a Deployment answers without a read.
 * Only when it is unmarked does this walk, and each hop is one GET of an
 * object the operator can already see.
 *
 * A FAILED READ IS NOT AN ANSWER OF "NO". An account that may not list
 * ReplicaSets gets null here, which means "unknown" and shows no warning —
 * the same choice gitOpsOwner makes about an ambiguous label, and for the
 * same reason: a warning that fires when nobody can check it is a warning
 * people learn to dismiss.
 */
export async function resolveManagement(
  manifest: unknown,
  namespace: string,
  read: ManifestReader,
): Promise<GitOpsManagement | null> {
  const own = gitOpsOwner(manifest)
  if (own) return classify(manifest, own)

  let current = manifest
  for (let hop = 0; hop < 2; hop++) {
    const controller = controllerOf(current)
    const kindId = controller?.kind ? FOLLOWABLE[controller.kind] : undefined
    if (!controller?.name || !kindId) return null

    let above: unknown
    try {
      above = await read(kindId, namespace, controller.name)
    } catch {
      return null
    }

    const owner = gitOpsOwner(above)
    if (owner) {
      // The object above may itself be carrying a copied annotation — a
      // ReplicaSet holding its Deployment's tracking id is the ordinary case
      // — so what the marker NAMES wins over what is holding it.
      const named = classify(above, owner)
      return {
        owner,
        through: 'inherited',
        controller: named.controller ?? { kind: controller.kind ?? '', name: controller.name },
      }
    }
    current = above
  }
  return null
}

/**
 * What an object's OWN marker says, with no read and no walk.
 *
 * For a list row, which has labels and annotations and cannot afford a read
 * per line. It answers the copied-annotation case — a ReplicaSet row holding
 * its Deployment's tracking id — and nothing else; an unmarked row is null
 * here even when something above it is managed.
 */
export function managementFromMarker(manifest: unknown): GitOpsManagement | null {
  const owner = gitOpsOwner(manifest)
  return owner ? classify(manifest, owner) : null
}

/**
 * Decides whether an object's own marker is about itself.
 *
 * A tracking id that names a different object is a COPY: the Deployment
 * controller puts a Deployment's annotations on the ReplicaSets it creates,
 * so the marker travels one hop down while the ownership does not. Comparing
 * the two is the whole check, and it needs no read.
 */
function classify(manifest: unknown, owner: GitOpsOwner): GitOpsManagement {
  const object = manifest as { kind?: string; metadata?: { name?: string } } | null
  const target = owner.target

  const namesSomethingElse =
    target !== null &&
    Boolean(object?.kind) &&
    (target.kind !== object?.kind || target.name !== object?.metadata?.name)

  if (namesSomethingElse) {
    return { owner, through: 'inherited', controller: { kind: target.kind, name: target.name } }
  }
  return { owner, through: 'direct', controller: null }
}
