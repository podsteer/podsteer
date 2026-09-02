/**
 * A node's taints, in the form the tolerations opposite them are written in.
 *
 * THE PANEL SHOWED ONE HALF OF THIS CONVERSATION. A pod's panel renders its
 * tolerations in full, including the toleration seconds several clients drop
 * — and the node's panel never showed the taints those tolerations exist to
 * tolerate. Every scheduling question ("why is this pod not here", "why is
 * nothing scheduling on that node") needs both sides.
 *
 * Written the way `kubectl describe node` writes them, and the way a
 * toleration is written in the pod panel beside it, so the two can be read
 * against each other without translating.
 */

interface Taint {
  key?: string
  value?: string
  effect?: string
  timeAdded?: string
}

interface NodeManifest {
  spec?: { taints?: Taint[]; unschedulable?: boolean }
}

/**
 * One taint as `key=value:Effect`, or `key:Effect` when it carries no value.
 *
 * The effect is the half that decides what happens — NoSchedule keeps new
 * pods off, NoExecute evicts the ones already there — so it is never omitted.
 */
export function formatTaint(taint: Taint): string {
  const key = taint.key ?? ''
  const effect = taint.effect ? `:${taint.effect}` : ''
  return taint.value ? `${key}=${taint.value}${effect}` : `${key}${effect}`
}

/** Every taint on a node, formatted. */
export function nodeTaints(manifest: unknown): string[] {
  const taints = (manifest as NodeManifest | null)?.spec?.taints ?? []
  return taints.map(formatTaint)
}

/**
 * Whether the node has been cordoned.
 *
 * Its own fact rather than an inference from the taints, even though
 * cordoning adds one: an operator who ran `kubectl cordon` wants to see that
 * they did, and reading it back out of a taint list is asking them to decode
 * their own action.
 */
export function isCordoned(manifest: unknown): boolean {
  return Boolean((manifest as NodeManifest | null)?.spec?.unschedulable)
}
