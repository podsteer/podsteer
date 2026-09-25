/**
 * The overlay's cluster scoping.
 *
 * `sessionLauncher` is a module singleton; this overlay is not. App.svelte
 * keys the workspace on the cluster id, so the overlay is destroyed and
 * rebuilt on every tab switch, and until these two rules existed the
 * singleton's state walked across that boundary: a node-shell dialog opened on
 * production reappeared under staging's tab, and confirming it created a
 * privileged pod on production.
 *
 * Neither test renders a dialog — the point is what is NOT shown, and what is
 * dropped when the tab goes.
 */
import { afterEach, describe, expect, it } from 'vitest'
import { render, cleanup } from '@testing-library/svelte'
import SessionOverlay from './SessionOverlay.svelte'
import { sessionLauncher } from '$stores/sessionLauncher.svelte'

const PROD_NODE_SHELL = {
  clusterId: 'prod-eu',
  node: 'ip-10-0-1-9',
  readOnly: false,
  productionGroup: 'Production',
}

afterEach(() => {
  cleanup()
  sessionLauncher.leave()
})

describe('the session overlay', () => {
  it('shows nothing for a dialog that belongs to another cluster', () => {
    sessionLauncher.requestNodeShell(PROD_NODE_SHELL)

    const { container } = render(SessionOverlay, { clusterId: 'staging' })

    // The launcher still holds it — this overlay simply is not the one that
    // may render or confirm it.
    expect(sessionLauncher.pending?.clusterId).toBe('prod-eu')
    expect(container.querySelector('*')).toBeNull()
  })

  it('empties the launcher when the workspace rendering it goes away', () => {
    const { unmount } = render(SessionOverlay, { clusterId: 'staging' })
    sessionLauncher.requestNodeShell(PROD_NODE_SHELL)

    unmount()

    // Switching tabs must not carry a dialog — or a terminal whose pod the
    // unmount has already deleted — into the next cluster's workspace.
    expect(sessionLauncher.pending).toBeNull()
    expect(sessionLauncher.running).toBeNull()
  })
})
