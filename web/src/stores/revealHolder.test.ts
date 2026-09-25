import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { hideOnWindowBlur, RevealHolder, REVEAL_HIDE_AFTER_MS } from './revealHolder.svelte'

/**
 * THE ONE SET OF TESTS FOR THE REVEAL DISCIPLINE.
 *
 * Both things in PodSteer that put Secret material on screen — a Secret's key
 * and a Helm release's values and notes — go through this holder, so these
 * assertions cover both rather than one of them having its own copy that
 * quietly stops matching. That is the whole reason the holder was extracted:
 * a thirty-second timer that has drifted to never looks exactly like one that
 * has not, right up until a credential is still on screen in a recording.
 */

beforeEach(() => {
  vi.useFakeTimers()
})

afterEach(() => {
  vi.useRealTimers()
})

describe('a revealed value expires', () => {
  it('is gone after the hide interval, not merely hidden', () => {
    const holder = new RevealHolder<string>()
    holder.put('k', 'hunter2', true)

    expect(holder.at('k')).toBe('hunter2')

    vi.advanceTimersByTime(REVEAL_HIDE_AFTER_MS - 1)
    expect(holder.at('k')).toBe('hunter2')

    vi.advanceTimersByTime(1)

    // FORGOTTEN, NOT FLAGGED. `at` returning undefined is the assertion that
    // matters: a holder that kept the material and only stopped painting it
    // would be back to the one-way reveal the doctrine exists to prevent —
    // the value would still be in a heap dump and one line from the screen.
    expect(holder.at('k')).toBeUndefined()
    expect(holder.has('k')).toBe(false)
    expect(holder.size).toBe(0)
  })

  it('leaves a non-expiring entry alone', () => {
    // A loading state and a refusal go under the same key as the value they
    // are waiting for, and neither is material. An error message that
    // removes itself is one the operator never finished reading.
    const holder = new RevealHolder<string>()
    holder.put('k', 'you are not allowed to read this Secret')

    vi.advanceTimersByTime(REVEAL_HIDE_AFTER_MS * 10)

    expect(holder.at('k')).toBe('you are not allowed to read this Secret')
  })

  it('gives a re-revealed key its own full interval rather than the remainder', () => {
    // A write re-reveals through the same audited read, and a second
    // explicit reveal is a second deliberate act. Either inheriting the
    // first timer's remainder would take a value away moments after
    // somebody asked for it.
    const holder = new RevealHolder<string>()
    holder.put('k', 'old', true)

    vi.advanceTimersByTime(REVEAL_HIDE_AFTER_MS - 100)
    holder.put('k', 'new', true)

    vi.advanceTimersByTime(REVEAL_HIDE_AFTER_MS - 100)
    expect(holder.at('k')).toBe('new')

    vi.advanceTimersByTime(100)
    expect(holder.at('k')).toBeUndefined()
  })
})

describe('hiding', () => {
  it('puts one value away and leaves the others', () => {
    const holder = new RevealHolder<string>()
    holder.put('a', 'one', true)
    holder.put('b', 'two', true)

    holder.hide('a')

    expect(holder.at('a')).toBeUndefined()
    expect(holder.at('b')).toBe('two')
  })

  it('cancels the timer it hid, so a later tick cannot reach a re-revealed value', () => {
    // The subtle one. Hide, then reveal again inside the original interval:
    // if `hide` had left the first timer running, the value somebody just
    // asked for would vanish when it fired.
    const holder = new RevealHolder<string>()
    holder.put('k', 'first', true)

    vi.advanceTimersByTime(1_000)
    holder.hide('k')
    holder.put('k', 'second', true)

    vi.advanceTimersByTime(REVEAL_HIDE_AFTER_MS - 1_000)
    expect(holder.at('k')).toBe('second')
  })

  it('empties everything at once', () => {
    const holder = new RevealHolder<string>()
    holder.put('a', 'one', true)
    holder.put('b', 'two')

    holder.hideAll()

    // Both go, expiring or not: a blur is not a timeout, it is somebody
    // pointing a camera at the screen.
    expect(holder.size).toBe(0)
  })
})

describe('a read still in flight', () => {
  it('is invalidated by hideAll, so it cannot land material after a blur', () => {
    // THE BUG THIS EXISTS FOR. Press reveal, alt-tab, the blur handler
    // empties the holder — and then the promise resolves and writes the
    // value straight back, revealed and under a fresh thirty seconds, in a
    // window nobody is looking at. Emptying is not enough on its own,
    // because the read was already in the air.
    const holder = new RevealHolder<string>()
    const issued = holder.claim('k')

    holder.hideAll()

    expect(holder.isCurrent('k', issued)).toBe(false)
  })

  it('is invalidated by a hide of its own key', () => {
    const holder = new RevealHolder<string>()
    const issued = holder.claim('k')

    holder.hide('k')

    expect(holder.isCurrent('k', issued)).toBe(false)
  })

  it('is invalidated by a second read of the same key', () => {
    // Two reveals of one key: the first must not overwrite the second's
    // answer just because it resolved last.
    const holder = new RevealHolder<string>()
    const first = holder.claim('k')
    const second = holder.claim('k')

    expect(holder.isCurrent('k', first)).toBe(false)
    expect(holder.isCurrent('k', second)).toBe(true)
  })

  it('is left alone when a DIFFERENT key is forgotten', () => {
    // The token is per key, so a pane moving between subjects invalidates
    // exactly the read it abandoned and nothing else.
    const holder = new RevealHolder<string>()
    const issued = holder.claim('a')

    holder.hide('b')

    expect(holder.isCurrent('a', issued)).toBe(true)
  })

  it('survives an ordinary put, so a held value is not invalidated by itself', () => {
    const holder = new RevealHolder<string>()
    const issued = holder.claim('k')

    holder.put('k', 'hunter2', true)

    expect(holder.isCurrent('k', issued)).toBe(true)
  })
})

describe('window blur', () => {
  it('empties the holder, which is when a screen share usually starts', () => {
    const holder = new RevealHolder<string>()
    hideOnWindowBlur(holder)

    holder.put('k', 'hunter2', true)
    window.dispatchEvent(new Event('blur'))

    expect(holder.at('k')).toBeUndefined()
  })

  it('takes a non-expiring value with it too', () => {
    // Anything a store chose not to expire is still on screen, and a blur is
    // about what is visible rather than about how long it has been there.
    const holder = new RevealHolder<string>()
    hideOnWindowBlur(holder)

    holder.put('k', 'still on screen')
    window.dispatchEvent(new Event('blur'))

    expect(holder.size).toBe(0)
  })
})
