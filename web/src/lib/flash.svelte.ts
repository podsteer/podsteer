/**
 * A flag that turns itself off, and can be asked again before it has.
 *
 * Five places said "Copied!" by setting a boolean and clearing it from a bare
 * `setTimeout` with no handle. The write after unmount is harmless in Svelte —
 * a dead signal, not a warning — so that is not what this is for. What it is
 * for is the SECOND click: the first timer was still running, so the
 * confirmation from the second copy vanished early, on whatever was left of
 * the first one's second. In the two row menus that timer also CLOSES the
 * menu, so a stale one could shut a menu the operator had just reopened.
 *
 * Re-asking restarts the clock, which is what somebody clicking twice means.
 */
export function flash(ms = 1500): {
  readonly on: boolean
  show: (then?: () => void) => void
  cancel: () => void
} {
  let on = $state(false)
  let timer: ReturnType<typeof setTimeout> | null = null

  function stop(): void {
    if (timer !== null) clearTimeout(timer)
    timer = null
  }

  return {
    get on(): boolean {
      return on
    },
    /** Shows it, and runs `then` when it expires — never when it is restarted. */
    show(then?: () => void): void {
      stop()
      on = true
      timer = setTimeout(() => {
        timer = null
        on = false
        then?.()
      }, ms)
    },
    /** For a component going away, so nothing is left running behind it. */
    cancel(): void {
      stop()
      on = false
    },
  }
}
