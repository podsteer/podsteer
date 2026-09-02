import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { flash } from './flash.svelte'

describe('a confirmation that turns itself off', () => {
  beforeEach(() => vi.useFakeTimers())
  afterEach(() => vi.useRealTimers())

  it('restarts the clock when asked again', () => {
    // THE BUG: two copies in quick succession left the first timer running,
    // so the second "Copied!" vanished on whatever was left of the first
    // one's second rather than showing for its own.
    const copied = flash(1000)

    copied.show()
    vi.advanceTimersByTime(900)
    copied.show()

    vi.advanceTimersByTime(900)
    expect(copied.on).toBe(true)

    vi.advanceTimersByTime(200)
    expect(copied.on).toBe(false)
  })

  it('runs the follow-up once, and not for a restart', () => {
    // In the row menus the follow-up CLOSES the menu, so a stale timer could
    // shut one the operator had just reopened.
    const then = vi.fn()
    const copied = flash(1000)

    copied.show(then)
    vi.advanceTimersByTime(900)
    copied.show(then)
    vi.advanceTimersByTime(1100)

    expect(then).toHaveBeenCalledTimes(1)
  })

  it('leaves nothing running when cancelled', () => {
    const then = vi.fn()
    const copied = flash(1000)

    copied.show(then)
    copied.cancel()
    vi.advanceTimersByTime(5000)

    expect(copied.on).toBe(false)
    expect(then).not.toHaveBeenCalled()
  })
})
