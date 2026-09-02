/**
 * Following a name in a detail pane to the object it names.
 *
 * A panel is full of names that are really addresses — the node a pod is on,
 * the ConfigMap a volume mounts, the Secret an environment variable comes
 * from — and left as text every one of them is something to copy into a
 * search box.
 *
 * THE UNDEFINED RETURN IS THE POINT. A reference is only followable if this
 * cluster serves that kind and this account may list it: a CRD removed since
 * the object was written, or nodes an operator cannot read, must render as
 * plain text rather than as a link that fails when followed. Offering a dead
 * link is worse than offering none, so the decision is made once, here,
 * rather than in each pane's own copy of the rule.
 */

/** Resolves a Kubernetes Kind to this cluster's navigator id, or null. */
export type ServesKind = (kindName: string) => string | null

/** Opens an object in the list it belongs to. */
export type OpenObject = (kindName: string, name: string, namespace: string) => void

/**
 * Builds a pane's `follow` — the function that turns a reference into a click
 * handler, or into nothing at all.
 */
export function follower(canOpen?: ServesKind, onopen?: OpenObject) {
  return function follow(
    kindName: string,
    name: string,
    namespace = '',
  ): (() => void) | undefined {
    if (!name || !onopen || !canOpen?.(kindName)) return undefined
    return () => onopen(kindName, name, namespace)
  }
}
