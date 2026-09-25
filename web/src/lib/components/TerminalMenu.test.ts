import { render, fireEvent, cleanup } from '@testing-library/svelte'
import { afterEach, describe, expect, it, vi } from 'vitest'

import TerminalMenu from './TerminalMenu.svelte'

const READ_ONLY_REASON = 'This cluster is marked read-only in PodSteer. Change that under Organise.'

function open(props: Partial<Parameters<typeof render>[1]> = {}) {
  const result = render(TerminalMenu, {
    localSupported: true,
    localReason: '',
    readOnly: false,
    readOnlyReason: READ_ONLY_REASON,
    onlocal: () => {},
    oncluster: () => {},
    ...(props as Record<string, unknown>),
  })
  return result
}

describe('the toolbar terminal control', () => {
  afterEach(cleanup)

  it('reads as a menu rather than a button, which is what the chevron is for', async () => {
    // It grew a second entry, so it must not look like a control that does
    // something. `aria-haspopup` is the half a screen reader gets; the chevron
    // is the half everyone else does, and the two have to agree.
    const { getByRole } = open()
    const trigger = getByRole('button', { name: 'Terminal' })

    expect(trigger.getAttribute('aria-haspopup')).toBe('menu')
    expect(trigger.getAttribute('aria-expanded')).toBe('false')

    await fireEvent.click(trigger)
    expect(trigger.getAttribute('aria-expanded')).toBe('true')
    expect(getByRole('menu', { name: 'Terminal' })).toBeTruthy()
  })

  it('offers both terminals, local first', async () => {
    const { getByRole, getAllByRole } = open()
    await fireEvent.click(getByRole('button', { name: 'Terminal' }))

    const items = getAllByRole('menuitem')
    expect(items).toHaveLength(2)
    expect(items[0].textContent).toContain('Local shell')
    expect(items[1].textContent).toContain('In-cluster shell')
  })

  it('closes when an entry is chosen, and calls exactly that entry', async () => {
    const onlocal = vi.fn()
    const oncluster = vi.fn()
    const { getByRole } = open({ onlocal, oncluster })
    const trigger = getByRole('button', { name: 'Terminal' })

    await fireEvent.click(trigger)
    await fireEvent.click(getByRole('menuitem', { name: /In-cluster shell/ }))

    expect(oncluster).toHaveBeenCalledTimes(1)
    expect(onlocal).not.toHaveBeenCalled()
    expect(trigger.getAttribute('aria-expanded')).toBe('false')
  })

  it('disables the in-cluster entry on a read-only cluster and leaves the local one alone', async () => {
    // The split is the read-only guard's own doctrine rather than an
    // inconsistency: an in-cluster shell creates a pod, which is a write; a
    // shell on the operator's own machine with their own credentials is not
    // something this application can or should police.
    const { getByRole } = open({ readOnly: true })
    await fireEvent.click(getByRole('button', { name: 'Terminal' }))

    const cluster = getByRole('menuitem', { name: /In-cluster shell/ }) as HTMLButtonElement
    const local = getByRole('menuitem', { name: /Local shell/ }) as HTMLButtonElement

    expect(cluster.disabled).toBe(true)
    expect(cluster.title).toBe(READ_ONLY_REASON)
    expect(local.disabled).toBe(false)
  })

  it('keeps a disabled local entry with its reason rather than removing it', async () => {
    // Windows has no pseudo-terminal. An absent control teaches nothing; a
    // disabled one carries the sentence that explains it.
    const reason = 'A local shell needs a pseudo-terminal, which this build does not provide'
    const { getByRole } = open({ localSupported: false, localReason: reason })
    await fireEvent.click(getByRole('button', { name: 'Terminal' }))

    const local = getByRole('menuitem', { name: /Local shell/ }) as HTMLButtonElement
    expect(local.disabled).toBe(true)
    expect(local.title).toBe(reason)
    // And the in-cluster one still works: it reaches the cluster, not this
    // machine, so a platform without a pty has nothing to do with it.
    expect((getByRole('menuitem', { name: /In-cluster shell/ }) as HTMLButtonElement).disabled).toBe(
      false,
    )
  })
})
