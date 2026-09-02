/**
 * Which layer Escape belongs to.
 *
 * SEVENTEEN COMPONENTS LISTEN FOR ESCAPE ON THE WINDOW, and none of them
 * could tell whether something nearer had a better claim on it. With a row
 * menu open inside the detail drawer, one Escape closed the menu AND the
 * drawer — and the drawer's Escape discards an unsaved YAML draft, so a
 * keystroke aimed at a menu could throw somebody's work away.
 *
 * `event.stopPropagation()` does not fix this, and it is worth saying why:
 * every one of these listeners is on the SAME target, so propagation never
 * happens between them. `stopImmediatePropagation` would work only if the
 * innermost layer's listener were registered first, which is the opposite of
 * the order things mount in.
 *
 * So the layers say so explicitly. Each claims while it is open and releases
 * when it closes, and only the innermost claim acts on Escape.
 */

const stack: symbol[] = []

/** One layer's claim on Escape, held for as long as the layer is open. */
export interface EscapeClaim {
  /** Whether this is the innermost open layer, and so the one Escape means. */
  owns(): boolean
  release(): void
}

/**
 * Claims Escape until released.
 *
 * Held from an `$effect` guarded on the layer being open, so that the
 * effect's own teardown releases it:
 *
 *     let claim = $state<EscapeClaim | null>(null)
 *     $effect(() => {
 *       if (!open) return
 *       const held = escapeLayer()
 *       claim = held
 *       return () => {
 *         held.release()
 *         claim = null
 *       }
 *     })
 */
export function escapeLayer(): EscapeClaim {
  const token = Symbol('escape layer')
  stack.push(token)

  return {
    owns: () => stack.length === 0 || stack[stack.length - 1] === token,
    release(): void {
      const at = stack.lastIndexOf(token)
      // Not necessarily the top: two layers can close in either order, and a
      // component unmounting takes its claim with it wherever it sits.
      if (at !== -1) stack.splice(at, 1)
    },
  }
}

/**
 * Whether a layer holding no claim should act on Escape.
 *
 * True when nothing has claimed it, so a component that has not been
 * converted keeps working exactly as it did rather than going deaf.
 */
export function escapeUnclaimed(): boolean {
  return stack.length === 0
}
