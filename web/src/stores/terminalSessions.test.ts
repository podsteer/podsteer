import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { terminalSessions, sessionKey, localSessionKey } from './terminalSessions.svelte'

const KEY = sessionKey('c1', 'default', 'api-0', 'app')

describe('terminal session handover', () => {
  beforeEach(() => vi.useFakeTimers())
  afterEach(() => {
    terminalSessions.forget(KEY)
    vi.useRealTimers()
  })

  it('lets a remount claim the session before it is reaped', () => {
    // THE CASE THAT MATTERS. Maximising the pane destroys the component and
    // builds another; without the handover that is a new `kubectl exec`, so a
    // shell somebody had set up — cwd, history, whatever was running — is
    // abandoned by pressing a layout button.
    const stop = vi.fn()
    terminalSessions.offer(KEY, 'sess-1', 'screen', stop)

    const claimed = terminalSessions.take(KEY)

    expect(claimed).toEqual({ id: 'sess-1', buffer: 'screen' })
    vi.advanceTimersByTime(60_000)
    expect(stop).not.toHaveBeenCalled()
  })

  it('stops a session nobody claims', () => {
    // Closing the drawer or switching tabs. Nothing re-attaches, so the exec
    // must not be left running against somebody's production container.
    const stop = vi.fn()
    terminalSessions.offer(KEY, 'sess-2', '', stop)

    vi.advanceTimersByTime(5000)

    expect(stop).toHaveBeenCalledWith('sess-2')
    expect(terminalSessions.take(KEY)).toBeNull()
  })

  it('does not stop it during the grace period', () => {
    const stop = vi.fn()
    terminalSessions.offer(KEY, 'sess-3', '', stop)

    vi.advanceTimersByTime(500)

    expect(stop).not.toHaveBeenCalled()
  })

  it('a claimed session can be offered again', () => {
    // Maximise, then restore: two handovers in a row, and the shell survives
    // both.
    const stop = vi.fn()
    terminalSessions.offer(KEY, 'sess-4', 'a', stop)
    const first = terminalSessions.take(KEY)!

    terminalSessions.offer(KEY, first.id, 'b', stop)
    const second = terminalSessions.take(KEY)

    expect(second).toEqual({ id: 'sess-4', buffer: 'b' })
    vi.advanceTimersByTime(60_000)
    expect(stop).not.toHaveBeenCalled()
  })

  it('forgetting an ended session does not stop it again', () => {
    // The far end hung up, or the operator reconnected deliberately. Reaping
    // would call StopSession on an id the Go side has already dropped.
    const stop = vi.fn()
    terminalSessions.offer(KEY, 'sess-5', '', stop)

    terminalSessions.forget(KEY)
    vi.advanceTimersByTime(60_000)

    expect(stop).not.toHaveBeenCalled()
  })

  it('keeps sessions for different containers apart', () => {
    const other = sessionKey('c1', 'default', 'api-0', 'sidecar')
    const stop = vi.fn()

    terminalSessions.offer(KEY, 'sess-app', '', stop)
    terminalSessions.offer(other, 'sess-side', '', stop)

    expect(terminalSessions.take(KEY)?.id).toBe('sess-app')
    expect(terminalSessions.take(other)?.id).toBe('sess-side')
  })

  it('keeps a shell and an attach session for the same container apart', () => {
    // Shell starts a new process, Attach connects to the running one — two
    // different sessions against the same container, so a mode switch must
    // not collide with, or silently reattach to, the other mode's session.
    const shellKey = sessionKey('c1', 'default', 'api-0', 'app')
    const attachKey = sessionKey('c1', 'default', 'api-0', 'app', 'attach')
    expect(attachKey).not.toBe(shellKey)

    const stop = vi.fn()
    terminalSessions.offer(shellKey, 'sess-shell', '', stop)
    terminalSessions.offer(attachKey, 'sess-attach', '', stop)

    expect(terminalSessions.take(shellKey)?.id).toBe('sess-shell')
    expect(terminalSessions.take(attachKey)?.id).toBe('sess-attach')

    terminalSessions.forget(attachKey)
  })

  it('defaults sessionKey to shell mode, unchanged from before Attach existed', () => {
    expect(sessionKey('c1', 'default', 'api-0', 'app')).toBe(
      sessionKey('c1', 'default', 'api-0', 'app', 'shell'),
    )
  })
})

describe('local session keys', () => {
  it('keeps a local shell apart from a container shell on the same cluster', () => {
    // A local shell has no pod and no container, so without its own key space
    // it would collide with the bare cluster-level key and a remount could
    // re-attach a pane on this machine to an exec in a container.
    expect(localSessionKey('c1', null)).not.toBe(sessionKey('c1', '', '', ''))
  })

  it('keeps a coding agent apart from the plain shell beside it', () => {
    // Two different processes against the same tab. Sharing a key would mean
    // maximising one re-attached to the other.
    expect(localSessionKey('c1', 'claude')).not.toBe(localSessionKey('c1', null))
  })

  it('keeps two agents apart', () => {
    expect(localSessionKey('c1', 'claude')).not.toBe(localSessionKey('c1', 'gemini'))
  })

  it('keeps the same agent apart across cluster tabs', () => {
    // The environment differs by tab — a different context is named in the
    // notice and in the agent's prompt — so these are not one session.
    expect(localSessionKey('c1', 'claude')).not.toBe(localSessionKey('c2', 'claude'))
  })

  it('is stable, so a remount claims the session it left', () => {
    expect(localSessionKey('c1', 'claude')).toBe(localSessionKey('c1', 'claude'))
  })
})
