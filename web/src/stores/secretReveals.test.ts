import { beforeEach, describe, expect, it, vi } from 'vitest'

// The bindings do not exist outside the Wails runtime, so both calls this
// store makes into the backend are stubbed — see session.test.ts for the
// same pattern.
vi.mock('$lib/api/client', async () => {
  const actual = await vi.importActual<Record<string, unknown>>('$lib/api/client')
  return {
    ...actual,
    revealSecretKey: vi.fn(),
    setSecretKey: vi.fn(),
  }
})

import { revealSecretKey, setSecretKey } from '$lib/api/client'
import { secretReveals } from './secretReveals.svelte'

const mockReveal = vi.mocked(revealSecretKey)
const mockSet = vi.mocked(setSecretKey)

beforeEach(() => {
  // A SINGLETON STORE, so leftover state — and a leftover hide timer — from
  // one test must not answer the next one's `at()`.
  secretReveals.hideAll()
  mockReveal.mockReset()
  mockSet.mockReset()
})

describe('writing a Secret key', () => {
  it('refuses before the key has been revealed', async () => {
    // EDIT REQUIRES REVEAL FIRST. Nothing here has called `reveal`, so
    // `write` must refuse rather than send an operator's typed text to a
    // cluster on the strength of a value nobody has actually looked at.
    await expect(
      secretReveals.write('k', 'dev', 'app', 'creds', 'password', 'new-value'),
    ).rejects.toThrow(/reveal/i)

    expect(mockSet).not.toHaveBeenCalled()
  })

  it('writes and then re-reveals through the same audited read', async () => {
    mockReveal.mockResolvedValueOnce('old-value')
    await secretReveals.reveal('k', 'dev', 'app', 'creds', 'password')
    expect(secretReveals.at('k').value).toBe('old-value')

    mockSet.mockResolvedValueOnce(undefined)
    mockReveal.mockResolvedValueOnce('new-value')

    await secretReveals.write('k', 'dev', 'app', 'creds', 'password', 'new-value')

    expect(mockSet).toHaveBeenCalledWith('dev', 'app', 'creds', 'password', 'new-value')
    // Reveal is called once to establish the precondition and once more by
    // write's own re-reveal.
    expect(mockReveal).toHaveBeenCalledTimes(2)

    // A SUCCESSFUL WRITE RE-REVEALS: what is shown afterwards is whatever
    // the re-read answered with, not the value that was typed — proving the
    // store trusts the cluster's own answer rather than the local draft.
    expect(secretReveals.at('k').value).toBe('new-value')
  })

  it('leaves the shown value alone when the write itself fails', async () => {
    mockReveal.mockResolvedValueOnce('old-value')
    await secretReveals.reveal('k', 'dev', 'app', 'creds', 'password')

    mockSet.mockRejectedValueOnce(new Error('forbidden'))

    await expect(
      secretReveals.write('k', 'dev', 'app', 'creds', 'password', 'new-value'),
    ).rejects.toThrow('forbidden')

    // No re-reveal was attempted after a failed write — a failure re-reads
    // nothing, so whatever was on screen before the attempt stays there.
    expect(mockReveal).toHaveBeenCalledTimes(1)
    expect(secretReveals.at('k').value).toBe('old-value')
  })
})
