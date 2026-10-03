/**
 * Transient, non-blocking notices — a line that says something happened in the
 * background and goes away by itself.
 *
 * FOR WHAT NOBODY ASKED ABOUT. A button the operator pressed reports its own
 * failure where they are looking (ErrorBanner, a form's error line). This is
 * for the rest: a promise nothing awaited that rejected, a port-forward that
 * gave up while another tab was in front. It never blocks, never takes focus,
 * and never stacks without bound — three at a time, the same message
 * collapsing into one that restarts its clock.
 *
 * NOT THE DESKTOP NOTIFICATION. `$stores/notifications` posts to the operating
 * system for a new critical finding while the window is hidden; this is inside
 * the window and for the opposite situation.
 */

export interface Notice {
  readonly id: number
  readonly message: string
}

/** How many are shown at once. The oldest is dropped for a new one. */
const MAX_VISIBLE = 3
const DEFAULT_TTL_MS = 8000

class Notices {
  items = $state.raw<Notice[]>([])

  #next = 1
  #timers = new Map<number, ReturnType<typeof setTimeout>>()

  /** Shows a message. The same message already showing is kept and its clock restarted. */
  post(message: string, ttlMs = DEFAULT_TTL_MS): void {
    const text = message.trim()
    if (text === '') return

    const existing = this.items.find((notice) => notice.message === text)
    if (existing) {
      this.#arm(existing.id, ttlMs)
      return
    }

    const notice: Notice = { id: this.#next++, message: text }
    const next = [...this.items, notice]
    // Dropped from the front, with its timer, so a flood cannot leak handles.
    while (next.length > MAX_VISIBLE) {
      const dropped = next.shift()
      if (dropped) this.#disarm(dropped.id)
    }
    this.items = next
    this.#arm(notice.id, ttlMs)
  }

  dismiss(id: number): void {
    this.#disarm(id)
    this.items = this.items.filter((notice) => notice.id !== id)
  }

  /** Clears everything and every timer. For tests and teardown. */
  clear(): void {
    for (const timer of this.#timers.values()) clearTimeout(timer)
    this.#timers.clear()
    this.items = []
  }

  #arm(id: number, ttlMs: number): void {
    this.#disarm(id)
    this.#timers.set(
      id,
      setTimeout(() => this.dismiss(id), ttlMs),
    )
  }

  #disarm(id: number): void {
    const timer = this.#timers.get(id)
    if (timer !== undefined) clearTimeout(timer)
    this.#timers.delete(id)
  }
}

export const notices = new Notices()
