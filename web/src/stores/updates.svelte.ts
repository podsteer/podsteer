/**
 * Whether a newer PodSteer has been published.
 *
 * WHY THIS EXISTS AT ALL, given that PodSteer used to promise it would never
 * check: a security fix only protects people who install it, and Homebrew is
 * pull rather than push — a user who never runs `brew upgrade` is never told
 * anything, and Linux and Windows have no package manager in the picture.
 *
 * WHAT MAKES IT DEFENSIBLE is all in what it does NOT do. It sends no
 * identifier — no version, no platform, no machine id — because the comparison
 * happens in Go on what comes back. It asks GitHub rather than anything
 * PodSteer operates, so there is no dataset here for anyone to hold. It runs at
 * most once a day, never on the startup path, and not at all when the operator
 * has switched it off or an administrator has set PODSTEER_UPDATE_CHECK=false.
 *
 * OFF MEANS NOTHING HAPPENS. There is no timer that runs and then checks a
 * flag: when the preference is off, `refresh` returns before touching the
 * bridge, and the Go service refuses independently. Both are asserted.
 */

import { CheckForUpdate, RecallUpdate, UpdateChecksPermitted } from '$bindings/updateapi'
import type * as wails from '$bindings/models'
import { preferences } from './preferences.svelte'

export type UpdateStatus = wails.UpdateStatus

/** How long a result stands before another check is worth making. */
const CHECK_INTERVAL_MS = 24 * 60 * 60 * 1000

/**
 * How long after launch the first check waits.
 *
 * NEVER ON THE STARTUP PATH. k9s made itself unstartable when api.github.com
 * was unreachable; nothing here may sit between the operator and their
 * clusters. By the time this fires the window has been usable for a minute.
 */
const FIRST_CHECK_DELAY_MS = 60_000

class Updates {
  /** The last result, or null if nothing has been checked this session. */
  status = $state.raw<UpdateStatus | null>(null)

  /** Whether the machine permits checking at all, regardless of preference. */
  permitted = $state<boolean>(true)

  /** True while a check the operator asked for is in flight. */
  checking = $state<boolean>(false)

  #timer: number | null = null

  /** A newer release exists and the operator has not dismissed it. */
  readonly available = $derived(
    preferences.updateChecksEnabled &&
      this.status?.state === 'available' &&
      this.status.latest !== preferences.dismissedUpdate,
  )

  /**
   * Starts the once-a-day cycle, after a delay.
   *
   * Called once from the application root. Safe to call when the check is
   * off — it schedules nothing in that case.
   */
  start(): void {
    void this.#permission()
    void this.#recall()
    if (this.#timer !== null) return

    this.#timer = window.setTimeout(() => {
      void this.refresh(false)
      // Re-armed rather than an interval, so a long-running window keeps
      // checking daily without stacking timers if one is slow.
      this.#timer = window.setInterval(() => void this.refresh(false), CHECK_INTERVAL_MS)
    }, FIRST_CHECK_DELAY_MS)
  }

  /** Stops the cycle. */
  stop(): void {
    if (this.#timer === null) return
    window.clearTimeout(this.#timer)
    window.clearInterval(this.#timer)
    this.#timer = null
  }

  /**
   * Shows what an earlier run found, without asking anybody.
   *
   * The daily gate survives a restart and the answer used to not: a relaunch
   * inside the day skipped the check and then had nothing to show, so an
   * update found yesterday disappeared until the next check — every other day
   * for somebody who quits PodSteer each evening. Go re-judges the remembered
   * release against the running build, which may have been upgraded since.
   */
  async #recall(): Promise<void> {
    if (!preferences.updateChecksEnabled || !preferences.lastUpdateLatest) return
    try {
      const recalled = await RecallUpdate(preferences.lastUpdateLatest, preferences.lastUpdateURL)
      // A real check that landed first wins.
      this.status ??= recalled
    } catch {
      // Nothing to show is the same as before this existed.
    }
  }

  async #permission(): Promise<void> {
    try {
      this.permitted = await UpdateChecksPermitted()
    } catch {
      // The binding is unavailable, which means so is the check.
      this.permitted = false
    }
  }

  /**
   * Asks whether a newer release exists.
   *
   * `force` is somebody pressing "Check now", which skips the interval but not
   * the preference: a button that fires a request the setting forbids is the
   * bug this whole design is arranged to prevent.
   */
  async refresh(force: boolean): Promise<void> {
    if (!preferences.updateChecksEnabled) return

    const since = Date.now() - preferences.lastUpdateCheck
    if (!force && preferences.lastUpdateCheck > 0 && since < CHECK_INTERVAL_MS) return

    if (force) this.checking = true
    try {
      const status = await CheckForUpdate(force)
      this.status = status
      const found = status.state === 'available' || status.state === 'current'
      preferences.markUpdateChecked(Date.now(), found ? status.latest : '', found ? status.url : '')
    } catch {
      // Never surfaced. Being unable to reach GitHub says nothing about the
      // cluster the operator is working on.
      this.status = null
    } finally {
      this.checking = false
    }
  }
}

export const updates = new Updates()
