import { render, fireEvent, cleanup } from '@testing-library/svelte'
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from 'vitest'

import Select from './Select.svelte'

/**
 * The dropdown's keyboard, which is the reason it is not a native <select>.
 *
 * A native select gets arrows, Home/End and type-ahead from the platform for
 * nothing. This component re-implements all three, so they are the part of it
 * that can silently regress: the styling is visible the moment anybody opens
 * the app, and the type-ahead buffer is not.
 */
const OPTIONS = [
  { value: 'default', label: 'default' },
  { value: 'kube-system', label: 'kube-system' },
  { value: 'kube-public', label: 'kube-public' },
  { value: 'monitoring', label: 'monitoring' },
  { value: 'production', label: 'production' },
]

const props = {
  label: 'Namespace',
  value: 'default',
  options: OPTIONS,
}

/** The option the arrow keys are on, read the way a screen reader reads it. */
function activeLabel(panel: HTMLElement): string {
  const id = panel.getAttribute('aria-activedescendant')
  return id ? (panel.ownerDocument.getElementById(id)?.textContent?.trim() ?? '') : ''
}

/**
 * Opens the panel the way a keyboard does, and hands back the listbox.
 *
 * The casts are here because the query helper's return type is a union wide
 * enough to include `null` and a promise; every call below is a synchronous
 * get that throws rather than returning either, so narrowing at the one place
 * that shares them beats repeating it in fifteen tests.
 */
async function openPanel(getByRole: ReturnType<typeof render>['getByRole']): Promise<HTMLElement> {
  await fireEvent.keyDown(getByRole('button') as HTMLElement, { key: 'Enter' })
  return getByRole('listbox') as HTMLElement
}

describe('the dropdown keyboard', () => {
  beforeAll(() => {
    // The panel scrolls the highlighted row into view on every move. The DOM
    // these tests run against has no layout and therefore no implementation of
    // it, and an absent method here would fail every keyboard test for a
    // reason that has nothing to do with the keyboard.
    if (!Element.prototype.scrollIntoView) {
      Element.prototype.scrollIntoView = () => {}
    }
  })

  beforeEach(() => {
    vi.useFakeTimers()
  })

  afterEach(() => {
    // render() appends to document.body and the queries are body-scoped, so a
    // panel left behind is found by the next test's getByRole('listbox').
    cleanup()
    vi.useRealTimers()
  })

  it('names the trigger with what the field is and what it is set to', () => {
    const { getByRole } = render(Select, { ...props, value: 'production' })

    expect(getByRole('button').textContent).toContain('Namespace')
    expect(getByRole('button').textContent).toContain('production')
  })

  it('opens on Enter with the chosen option marked and highlighted', async () => {
    const { getByRole, getAllByRole } = render(Select, { ...props, value: 'monitoring' })

    const panel = await openPanel(getByRole)

    expect(getByRole('button').getAttribute('aria-expanded')).toBe('true')
    const selected = getAllByRole('option').filter(
      (option) => option.getAttribute('aria-selected') === 'true',
    )
    expect(selected).toHaveLength(1)
    expect(selected[0].textContent).toContain('monitoring')
    expect(activeLabel(panel)).toContain('monitoring')
  })

  it('points the trigger at the panel it opened', async () => {
    const { getByRole } = render(Select, props)

    const panel = await openPanel(getByRole)

    expect(getByRole('button').getAttribute('aria-controls')).toBe(panel.id)
  })

  it('moves the highlight with the arrows without choosing anything', async () => {
    const onchange = vi.fn()
    const { getByRole } = render(Select, { ...props, onchange })

    const panel = await openPanel(getByRole)
    await fireEvent.keyDown(panel, { key: 'ArrowDown' })
    await fireEvent.keyDown(panel, { key: 'ArrowDown' })

    expect(activeLabel(panel)).toContain('kube-public')
    expect(onchange).not.toHaveBeenCalled()
  })

  it('stops the highlight at both ends rather than wrapping', async () => {
    const { getByRole } = render(Select, props)

    const panel = await openPanel(getByRole)
    await fireEvent.keyDown(panel, { key: 'ArrowUp' })
    expect(activeLabel(panel)).toContain('default')

    await fireEvent.keyDown(panel, { key: 'End' })
    await fireEvent.keyDown(panel, { key: 'ArrowDown' })
    expect(activeLabel(panel)).toContain('production')
  })

  it('jumps to the ends of the list with Home and End', async () => {
    const { getByRole } = render(Select, { ...props, value: 'kube-public' })

    const panel = await openPanel(getByRole)
    await fireEvent.keyDown(panel, { key: 'End' })
    expect(activeLabel(panel)).toContain('production')

    await fireEvent.keyDown(panel, { key: 'Home' })
    expect(activeLabel(panel)).toContain('default')
  })

  it('chooses the highlighted option on Enter and closes', async () => {
    const onchange = vi.fn()
    const { getByRole, queryByRole } = render(Select, { ...props, onchange })

    const panel = await openPanel(getByRole)
    await fireEvent.keyDown(panel, { key: 'ArrowDown' })
    await fireEvent.keyDown(panel, { key: 'Enter' })

    expect(onchange).toHaveBeenCalledWith('kube-system')
    expect(queryByRole('listbox')).toBeNull()
  })

  it('abandons on Escape without choosing', async () => {
    const onchange = vi.fn()
    const { getByRole, queryByRole } = render(Select, { ...props, onchange })

    const panel = await openPanel(getByRole)
    await fireEvent.keyDown(panel, { key: 'ArrowDown' })
    await fireEvent.keyDown(panel, { key: 'Escape' })

    expect(queryByRole('listbox')).toBeNull()
    expect(onchange).not.toHaveBeenCalled()
  })

  it('keeps Escape to itself, so a dialog behind it does not close too', async () => {
    const { getByRole } = render(Select, props)
    const panel = await openPanel(getByRole)

    // Listening only once the panel is up: the Enter that opened it is a key
    // press the window is entitled to see, and it is Escape that must not
    // travel — a dialog listening on the window would otherwise close along
    // with the panel, one press doing two things nobody asked for.
    const seen = vi.fn()
    window.addEventListener('keydown', seen)
    await fireEvent.keyDown(panel, { key: 'Escape' })
    window.removeEventListener('keydown', seen)

    expect(seen).not.toHaveBeenCalled()
  })

  it('jumps to the first option starting with a typed letter', async () => {
    const { getByRole } = render(Select, props)

    const panel = await openPanel(getByRole)
    await fireEvent.keyDown(panel, { key: 'm' })

    expect(activeLabel(panel)).toContain('monitoring')
  })

  it('narrows on the letters typed together rather than restarting on each', async () => {
    const { getByRole } = render(Select, props)

    const panel = await openPanel(getByRole)
    await fireEvent.keyDown(panel, { key: 'k' })
    expect(activeLabel(panel)).toContain('kube-system')

    // "ku" then "kub" then "kube-p" — a buffer, not four separate jumps.
    for (const key of ['u', 'b', 'e', '-', 'p']) {
      await fireEvent.keyDown(panel, { key })
    }
    expect(activeLabel(panel)).toContain('kube-public')
  })

  it('advances to the next match when one letter is repeated', async () => {
    const { getByRole } = render(Select, props)

    const panel = await openPanel(getByRole)
    await fireEvent.keyDown(panel, { key: 'k' })
    expect(activeLabel(panel)).toContain('kube-system')

    // Past the pause, so this is a fresh single letter rather than "kk" — and
    // a fresh single letter searches from AFTER the current row.
    vi.advanceTimersByTime(900)
    await fireEvent.keyDown(panel, { key: 'k' })
    expect(activeLabel(panel)).toContain('kube-public')
  })

  it('starts a new search once typing has paused', async () => {
    const { getByRole } = render(Select, props)

    const panel = await openPanel(getByRole)
    await fireEvent.keyDown(panel, { key: 'm' })
    expect(activeLabel(panel)).toContain('monitoring')

    vi.advanceTimersByTime(900)
    await fireEvent.keyDown(panel, { key: 'd' })
    expect(activeLabel(panel)).toContain('default')
  })

  it('leaves the highlight alone when nothing matches what was typed', async () => {
    const { getByRole } = render(Select, props)

    const panel = await openPanel(getByRole)
    await fireEvent.keyDown(panel, { key: 'z' })

    expect(activeLabel(panel)).toContain('default')
  })

  it('follows the highlighted option when the list is rebuilt beneath it', async () => {
    const { getByRole, rerender } = render(Select, props)

    const panel = await openPanel(getByRole)
    await fireEvent.keyDown(panel, { key: 'End' })
    expect(activeLabel(panel)).toContain('production')

    // What a caller's `onopen` does: a namespace created since the tab
    // connected arrives in sorted position and pushes everything after it
    // down. The highlight has to stay on the option, not on the index.
    await rerender({
      ...props,
      options: [{ value: 'argocd', label: 'argocd' }, ...OPTIONS],
    })

    expect(activeLabel(panel)).toContain('production')
  })

  it('records the placement it settled on, which is what starts the entrance', async () => {
    const { getByRole } = render(Select, props)

    const panel = await openPanel(getByRole)

    expect(panel.dataset.placement).toBe('below')
  })
})
