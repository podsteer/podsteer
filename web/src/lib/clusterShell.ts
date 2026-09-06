/**
 * Pure helpers for the in-cluster shell dialog.
 *
 * Kept out of the component for the reason `debugShell.ts` is: the two rules
 * that matter here — which namespace, and which pods may be offered — are
 * decisions rather than rendering, and a decision belongs somewhere it can be
 * argued with in a test.
 */

import { ALL_NAMESPACES } from './api/client'
import { DEFAULT_CLUSTER_SHELL_IMAGE } from '$stores/preferences.svelte'

/** What a confirmed in-cluster shell dialog asks for. */
export interface ClusterShellRequest {
  image: string
  namespace: string
}

/**
 * The namespace the dialog opens on, given the tab's current filter.
 *
 * DEFAULTS TO THE TAB'S NAMESPACE so the two terminals and the rest of the
 * interface agree about where somebody is working. When the tab is on "All
 * namespaces" there IS NO ANSWER — a pod lives in exactly one namespace — so
 * this returns the empty string and the dialog asks.
 *
 * It deliberately does NOT fall back to a system namespace. That is where the
 * node shell's own default points (kube-system, where admission is already
 * permissive and where a node shell has to be), and it is the wrong answer for
 * this: an operator looking at their application's namespace would get a pod
 * in kube-system without being told, in the one namespace they are least
 * likely to be permitted to create one in.
 */
export function clusterShellNamespaceFor(tabNamespace: string): string {
  return tabNamespace === ALL_NAMESPACES ? '' : tabNamespace.trim()
}

/**
 * Normalises the dialog's inputs.
 *
 * A blank image falls back to the default rather than being sent as an empty
 * string the backend would reject. The namespace is NOT given a default: see
 * clusterShellNamespaceFor. A blank one comes back blank, and `canOpen` below
 * is what stops it being sent.
 */
export function clusterShellRequest(image: string, namespace: string): ClusterShellRequest {
  return {
    image: image.trim() || DEFAULT_CLUSTER_SHELL_IMAGE,
    namespace: namespace.trim(),
  }
}

/**
 * Whether the dialog may be confirmed.
 *
 * The namespace is the only thing that can be missing — the image always
 * resolves to a default — and it is what keeps "ask rather than guess" true in
 * the interface as well as in Go.
 */
export function canOpenClusterShell(namespace: string): boolean {
  return namespace.trim() !== ''
}

/**
 * The sentence shown when the tab is on "All namespaces".
 *
 * Says what is missing and why, rather than leaving a disabled button with no
 * explanation beside it.
 */
export const CLUSTER_SHELL_NAMESPACE_PROMPT =
  'This tab is showing every namespace, and a pod lives in exactly one — name the namespace to open the shell in.'

/**
 * The sentence shown beside pods PodSteer created here that are NOT running.
 *
 * They are reported and never offered: an attach to an exited pod fails for a
 * reason the offer gave nobody a way to see. Saying so is what explains a
 * namespace that has been accumulating them.
 */
export function exitedShellsNote(count: number): string {
  if (count <= 0) return ''
  const plural = count === 1 ? 'pod' : 'pods'
  const verb = count === 1 ? 'is' : 'are'
  return `${count} shell ${plural} PodSteer created here ${verb} no longer running, so ${count === 1 ? 'it' : 'they'} cannot be attached to. They are deleted an hour after they were created.`
}
