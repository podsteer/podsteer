/**
 * Tracks whether the window is in native fullscreen.
 *
 * On macOS with an inset title bar, clicking the green button does not just
 * enlarge the window the way it does for an app with a visible title bar —
 * it enters true fullscreen, which takes the traffic lights (and the space
 * they were reserved for in the tab bar) with it. The header needs to know
 * that happened so it can reclaim that space instead of leaving it blank.
 *
 * Wails v2 has no push event for this — `WindowIsFullscreen` is a one-shot
 * poll, not a subscription — so the best available signal is the DOM's own
 * `resize` event, which the webview does receive when the native window
 * changes size for any reason, fullscreen included. A resize re-checks the
 * poll; nothing here claims that is instantaneous, only that it lands within
 * one animation frame of the OS finishing its transition. Those resizes are
 * coalesced to one poll per frame — see #onResize.
 */
import { WindowIsFullscreen } from '$lib/wailsjs/runtime/runtime'

class WindowState {
  isFullscreen = $state(false)

  constructor() {
    if (typeof window === 'undefined') return
    void this.#check()
    window.addEventListener('resize', this.#onResize)
  }

  /**
   * Pending re-check, so a drag costs one poll rather than one per frame.
   *
   * Dragging a window edge on macOS emits resize at the display's refresh
   * rate, and each one was an IPC round trip to the Go side asking a question
   * whose answer changes at most twice in a session. Coalescing to one check
   * per animation frame keeps the response indistinguishable — the frame is
   * the soonest anything could be painted with the answer anyway — while
   * removing about fifty-nine of every sixty calls.
   */
  #pending = 0

  #onResize = (): void => {
    if (this.#pending) return
    this.#pending = requestAnimationFrame(() => {
      this.#pending = 0
      void this.#check()
    })
  }

  async #check(): Promise<void> {
    try {
      this.isFullscreen = await WindowIsFullscreen()
    } catch {
      // The runtime bridge is not ready yet on the very first paint — the
      // next resize (or the app's own layout settling) checks again.
    }
  }
}

/** The application-wide window state. A module singleton for the same reason
    `workspace` and `preferences` are: one desktop window, one truth. */
export const windowState = new WindowState()
