import { beforeEach, describe, expect, it, vi } from 'vitest'

const check = vi.fn()
const permitted = vi.fn(async () => true)

vi.mock('$lib/wailsjs/go/wails/UpdateAPI', () => ({
  CheckForUpdate: (force: boolean) => check(force),
  UpdateChecksPermitted: () => permitted(),
}))

const { updates } = await import('./updates.svelte')
const { preferences } = await import('./preferences.svelte')

describe('the update check', () => {
  beforeEach(() => {
    check.mockReset()
    check.mockResolvedValue({ state: 'current', installed: 'v0.1.1', latest: 'v0.1.1', url: '' })
    preferences.updateChecksEnabled = true
    preferences.lastUpdateCheck = 0
    preferences.dismissedUpdate = ''
  })

  it('makes no call at all when it is switched off', async () => {
    // THE HALF THAT MATTERS. An opt-out that is shipped but never asserted has
    // silently broken in k9s, Terraform, dotnet, JetBrains and Docker Desktop.
    // Asserting the state is not enough — the question is whether a request
    // was made.
    preferences.updateChecksEnabled = false

    await updates.refresh(false)
    await updates.refresh(true)

    expect(check).not.toHaveBeenCalled()
  })

  it('does not call again inside the interval', async () => {
    await updates.refresh(false)
    await updates.refresh(false)

    expect(check).toHaveBeenCalledTimes(1)
  })

  it('calls again when the operator asks explicitly', async () => {
    await updates.refresh(false)
    await updates.refresh(true)

    expect(check).toHaveBeenCalledTimes(2)
    expect(check).toHaveBeenLastCalledWith(true)
  })

  it('shows nothing when the release is current', async () => {
    await updates.refresh(false)

    expect(updates.available).toBe(false)
  })

  it('shows the badge when a newer release exists', async () => {
    check.mockResolvedValue({ state: 'available', installed: 'v0.1.1', latest: 'v0.2.0', url: 'x' })

    await updates.refresh(true)

    expect(updates.available).toBe(true)
  })

  it('stays quiet about a version already dismissed', async () => {
    check.mockResolvedValue({ state: 'available', installed: 'v0.1.1', latest: 'v0.2.0', url: 'x' })
    preferences.dismissedUpdate = 'v0.2.0'

    await updates.refresh(true)

    expect(updates.available).toBe(false)
  })

  it('speaks up again for the release after a dismissed one', async () => {
    // Dismissal is per version. Somebody not upgrading today still wants to
    // hear about the next one.
    check.mockResolvedValue({ state: 'available', installed: 'v0.1.1', latest: 'v0.3.0', url: 'x' })
    preferences.dismissedUpdate = 'v0.2.0'

    await updates.refresh(true)

    expect(updates.available).toBe(true)
  })

  it('never surfaces a failure', async () => {
    check.mockRejectedValue(new Error('no route to host'))

    await expect(updates.refresh(true)).resolves.toBeUndefined()
    expect(updates.available).toBe(false)
  })
})
