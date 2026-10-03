import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { notices } from './notices.svelte'

beforeEach(() => {
  vi.useFakeTimers()
  notices.clear()
})
afterEach(() => {
  notices.clear()
  vi.useRealTimers()
})

describe('notices', () => {
  it('goes away by itself', () => {
    notices.post('one', 1000)
    expect(notices.items).toHaveLength(1)
    vi.advanceTimersByTime(1001)
    expect(notices.items).toHaveLength(0)
  })

  it('collapses the same message into one and restarts its clock', () => {
    notices.post('same', 1000)
    vi.advanceTimersByTime(800)
    notices.post('same', 1000)
    vi.advanceTimersByTime(800)
    expect(notices.items).toHaveLength(1)
    vi.advanceTimersByTime(300)
    expect(notices.items).toHaveLength(0)
  })

  it('shows at most three, dropping the oldest', () => {
    for (const message of ['a', 'b', 'c', 'd']) notices.post(message)
    expect(notices.items.map((notice) => notice.message)).toEqual(['b', 'c', 'd'])
  })

  it('ignores blank messages and can be dismissed', () => {
    notices.post('   ')
    expect(notices.items).toHaveLength(0)
    notices.post('x')
    const [only] = notices.items
    notices.dismiss(only!.id)
    expect(notices.items).toHaveLength(0)
  })
})
