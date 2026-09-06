/**
 * The reveal discipline, once.
 *
 * ONE IMPLEMENTATION AND ONE SET OF TESTS, WHICH IS THE ENTIRE REASON THIS
 * FILE EXISTS. Two things in PodSteer now put Secret material on screen on an
 * explicit request — a Secret's key, and a Helm release's values and notes —
 * and decision 3 requires the same three controls on both: re-hideable,
 * expiring after thirty seconds, and hidden again on window blur, which is
 * when a screen share usually starts. Written twice, those two would drift,
 * and the way they would drift is invisible: nothing on screen looks
 * different when a timer is thirty seconds in one pane and never in another,
 * and nobody notices until a value is still sitting there in a recording.
 *
 * WHAT IS HERE IS ONLY THE DISCIPLINE. The holder knows nothing about
 * Secrets, Helm, or how a value was fetched — it holds a keyed value, starts
 * a timer, and forgets. Everything about WHICH read is made, what it returns
 * and what refusing looks like stays with the store that owns it, because
 * those differ and the timing does not.
 *
 * HIDING FORGETS, IT DOES NOT MERELY STOP RENDERING. `hide` deletes the value
 * rather than flipping a flag, so a value that has expired is gone from this
 * process and showing it again costs another deliberate, audited read. A
 * store that kept the material and only stopped painting it would be back to
 * the one-way reveal this exists to prevent — the value would still be in a
 * heap dump, in a crash report, and one line of code away from the screen.
 *
 * IN MEMORY AND NOWHERE ELSE. Not persisted, not written to disk, and cleared
 * wholesale when the window is not being looked at.
 */

/**
 * How long a revealed value stays on screen.
 *
 * Thirty seconds, from decision 3, and it is a shared constant rather than a
 * number in each store precisely so the two cannot disagree.
 */
export const REVEAL_HIDE_AFTER_MS = 30_000

/**
 * Holds values that are on screen right now, and takes them away again.
 *
 * Generic over what is held so the Secret store can hold a value-or-refusal
 * record and the Helm store can hold the two payload fields that need a
 * timer, without either of them owning a timer of its own.
 */
export class RevealHolder<T> {
  /**
   * Reactive, because the panes that read this render straight out of it —
   * a row's value cell, a payload tab — rather than copying it out once.
   */
  #shown = $state<Record<string, T>>({})
  #timers = new Map<string, ReturnType<typeof setTimeout>>()
  #hideAfterMs: number

  constructor(hideAfterMs: number = REVEAL_HIDE_AFTER_MS) {
    this.#hideAfterMs = hideAfterMs
  }

  /** What is held for one key, or undefined when nothing is. */
  at(key: string): T | undefined {
    return this.#shown[key]
  }

  /** Whether anything is held for one key. */
  has(key: string): boolean {
    return this.#shown[key] !== undefined
  }

  /**
   * Puts a value in the holder.
   *
   * `expiring` decides whether the re-hide timer starts, and the distinction
   * is load-bearing rather than a convenience: a store puts a LOADING or a
   * REFUSAL under the same key as the value it is waiting for, and neither
   * of those should vanish after thirty seconds — an error message that
   * removes itself is one the operator never finished reading. Only actual
   * material expires.
   *
   * Re-putting a key restarts its timer, which is what makes a re-read after
   * a write, or a second explicit reveal, get its own full thirty seconds
   * rather than inheriting the remainder of the first.
   */
  put(key: string, value: T, expiring = false): void {
    this.#clearTimer(key)
    this.#shown[key] = value

    if (expiring) {
      this.#timers.set(
        key,
        setTimeout(() => this.hide(key), this.#hideAfterMs),
      )
    }
  }

  /** Puts one value away, and forgets it. Any read in flight for this key is
      invalidated with it, so it cannot arrive a moment later and undo this. */
  hide = (key: string): void => {
    this.claim(key)
    this.#clearTimer(key)
    delete this.#shown[key]
  }

  /**
   * Puts everything away.
   *
   * Called when the window loses focus, which in practice is the moment
   * somebody alt-tabs to start a screen share or accepts a call. It costs a
   * click to get back and removes the failure mode where a credential sits
   * revealed behind a window nobody remembers is open.
   */
  hideAll = (): void => {
    // BEFORE emptying, so a read already in the air is invalidated rather
    // than allowed to write material back into a window nobody is looking
    // at. Emptying alone is not enough — that was the bug.
    this.#invalidateAll()
    for (const timer of this.#timers.values()) clearTimeout(timer)
    this.#timers.clear()
    this.#shown = {}
  }

  /** How many values are held. For tests and for nothing else. */
  get size(): number {
    return Object.keys(this.#shown).length
  }

  /**
   * A token for one key that any later `put`, `hide` or `hideAll` invalidates.
   *
   * THIS IS WHAT STOPS AN IN-FLIGHT READ LANDING MATERIAL ON A SCREEN
   * SOMEBODY HAS ALREADY LOOKED AWAY FROM, and it is a real bug rather than
   * a theoretical one: press reveal, alt-tab, the blur handler empties the
   * holder, and then the promise resolves and writes the value straight back
   * in — revealed, under a fresh thirty seconds, in exactly the state the
   * blur rule exists to prevent. Every store here awaits a network call
   * before it has anything to put, so every one of them needs this.
   *
   * A caller takes a token BEFORE its await and checks `isCurrent` after; a
   * stale token means something happened while the read was in flight — a
   * blur, a hide, a second reveal, a switch to another subject — and the
   * answer is dropped rather than shown. It is per KEY as well as global,
   * so a store that moves between subjects (the Helm pane switching
   * revisions) cannot have an abandoned read repopulate the one it left.
   */
  claim(key: string): number {
    const next = (this.#generations.get(key) ?? 0) + 1
    this.#generations.set(key, next)
    return next
  }

  /** Whether a token taken before an await is still the current one. */
  isCurrent(key: string, token: number): boolean {
    return this.#generations.get(key) === token
  }

  #generations = new Map<string, number>()

  /** Invalidates every outstanding token. Used by hideAll, so a blur cannot
      be undone by a read that was already in the air. */
  #invalidateAll(): void {
    for (const key of this.#generations.keys()) {
      this.#generations.set(key, (this.#generations.get(key) ?? 0) + 1)
    }
  }

  #clearTimer(key: string): void {
    const timer = this.#timers.get(key)
    if (timer) {
      clearTimeout(timer)
      this.#timers.delete(key)
    }
  }
}

/**
 * Empties a holder whenever the window loses focus.
 *
 * A FUNCTION RATHER THAN SOMETHING THE CONSTRUCTOR DOES, so a holder can be
 * built in a test without registering a listener on a window that outlives
 * it — and so the registration is one visible line in each store rather than
 * a side effect of construction that a reader has to know about.
 *
 * Guarded on `window` because the stores are imported by unit tests and by
 * anything else that runs outside a browser.
 */
export function hideOnWindowBlur(holder: { hideAll: () => void }): void {
  if (typeof window === 'undefined') return
  window.addEventListener('blur', () => holder.hideAll())
}
