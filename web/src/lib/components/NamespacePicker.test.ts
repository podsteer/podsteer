import { cleanup, fireEvent, render } from '@testing-library/svelte'
import { afterEach, describe, expect, it, vi } from 'vitest'

import NamespacePicker, { APPLY_DELAY_MS } from './NamespacePicker.svelte'
import type { NamespaceScope } from '$lib/namespaceScope'

const choices = [{ name: 'billing' }, { name: 'keda' }, { name: 'shop' }, { name: 'old', hint: 'terminating' }]

function mount(value: NamespaceScope, extra: Record<string, unknown> = {}) {
  const onchange = vi.fn()
  const rendered = render(NamespacePicker, { value, choices, onchange, ...extra })
  const trigger = rendered.getByRole('button', { name: /Namespaces/ }) as HTMLButtonElement
  return { ...rendered, onchange, trigger }
}

function boxFor(container: HTMLElement, name: string): HTMLInputElement {
  const label = [...container.querySelectorAll('label')].find((node) => node.textContent?.includes(name))
  return label!.querySelector('input') as HTMLInputElement
}

afterEach(() => {
  cleanup()
  vi.useRealTimers()
})

describe('the namespace picker', () => {
  it('labels the trigger by the rule: All, one, "x +n", "N namespaces"', () => {
    expect(mount({ namespaces: [], all: true }).trigger.textContent).toContain('All namespaces')
    cleanup()
    expect(mount({ namespaces: ['shop'], all: false }).trigger.textContent).toContain('shop')
    cleanup()
    const three = mount({ namespaces: ['billing', 'keda', 'shop'], all: false }).trigger
    expect(three.textContent).toContain('billing +2')
    expect(three.title).toBe('billing, keda, shop')
    cleanup()
    expect(mount({ namespaces: ['a', 'b', 'c', 'd'], all: false }).trigger.textContent).toContain('4 namespaces')
  })

  it('narrows the list by the filter', async () => {
    const { trigger, getByLabelText, container } = mount({ namespaces: [], all: true })
    await fireEvent.click(trigger)
    await fireEvent.input(getByLabelText('Filter namespaces'), { target: { value: 'ke' } })

    const listed = [...container.querySelectorAll('ul[aria-label="Namespaces"] li')].map((li) => li.textContent?.trim())
    expect(listed).toEqual(['keda'])
  })

  it('has no Apply or Cancel: a tick is applied, once ticking pauses', async () => {
    vi.useFakeTimers()
    const { trigger, container, queryByRole, onchange } = mount({ namespaces: [], all: true })
    await fireEvent.click(trigger)
    expect(queryByRole('button', { name: 'Apply' })).toBeNull()
    expect(queryByRole('button', { name: 'Cancel' })).toBeNull()

    // While All is on, ticking one starts a set of one; a second tick soon
    // after replaces the pending change rather than adding a refresh.
    await fireEvent.click(boxFor(container, 'shop'))
    await fireEvent.click(boxFor(container, 'billing'))
    expect(onchange).not.toHaveBeenCalled()

    vi.advanceTimersByTime(APPLY_DELAY_MS)
    expect(onchange).toHaveBeenCalledTimes(1)
    expect(onchange).toHaveBeenCalledWith({ namespaces: ['billing', 'shop'], all: false })
  })

  it('applies All at once, and unticking the last namespace is All', async () => {
    vi.useFakeTimers()
    const { trigger, container, getByLabelText, onchange } = mount({ namespaces: ['shop'], all: false })
    await fireEvent.click(trigger)

    await fireEvent.click(boxFor(container, 'shop'))
    expect(onchange).toHaveBeenLastCalledWith({ namespaces: [], all: true })

    await fireEvent.click(boxFor(container, 'keda'))
    vi.advanceTimersByTime(APPLY_DELAY_MS)
    expect(onchange).toHaveBeenLastCalledWith({ namespaces: ['keda'], all: false })

    await fireEvent.click(getByLabelText('All namespaces'))
    expect(onchange).toHaveBeenLastCalledWith({ namespaces: [], all: true })
  })

  it('draws All as mixed while some namespaces are ticked', async () => {
    const { trigger, getByLabelText } = mount({ namespaces: ['shop'], all: false })
    await fireEvent.click(trigger)
    const all = getByLabelText('All namespaces') as HTMLInputElement
    expect(all.indeterminate).toBe(true)
  })

  it('flushes a pending tick the moment the menu closes, and keeps it', async () => {
    vi.useFakeTimers()
    const { trigger, container, getByLabelText, onchange } = mount({ namespaces: ['shop'], all: false })
    await fireEvent.click(trigger)
    await fireEvent.click(boxFor(container, 'keda'))
    expect(onchange).not.toHaveBeenCalled()

    await fireEvent.keyDown(getByLabelText('Filter namespaces'), { key: 'Escape' })
    expect(onchange).toHaveBeenCalledTimes(1)
    expect(onchange).toHaveBeenCalledWith({ namespaces: ['keda', 'shop'], all: false })
    expect(container.querySelector('[role="dialog"]')).toBeNull()

    // Nothing more goes out when the timer would have fired.
    vi.advanceTimersByTime(APPLY_DELAY_MS * 2)
    expect(onchange).toHaveBeenCalledTimes(1)
  })

  it('sends nothing when the menu closes with nothing changed', async () => {
    const { trigger, getByLabelText, onchange } = mount({ namespaces: ['shop'], all: false })
    await fireEvent.click(trigger)
    await fireEvent.keyDown(getByLabelText('Filter namespaces'), { key: 'Escape' })
    expect(onchange).not.toHaveBeenCalled()
  })

  it('works from the keyboard: open into the filter, move, tick, close', async () => {
    const { trigger, getByLabelText, onchange } = mount({ namespaces: ['shop'], all: false })
    await fireEvent.keyDown(trigger, { key: 'Enter' })

    const filter = getByLabelText('Filter namespaces')
    expect(document.activeElement).toBe(filter)

    // Row 0 is "All namespaces"; ↓ lands on billing.
    await fireEvent.keyDown(filter, { key: 'ArrowDown' })
    await fireEvent.keyDown(filter, { key: ' ' })
    await fireEvent.keyDown(filter, { key: 'Enter' })

    expect(onchange).toHaveBeenCalledWith({ namespaces: ['billing', 'shop'], all: false })
  })

  it('keeps a selected namespace the cluster no longer lists, marked not found', async () => {
    const { trigger, container } = mount({ namespaces: ['gone', 'shop'], all: false })
    await fireEvent.click(trigger)

    const gone = boxFor(container, 'gone')
    expect(gone.checked).toBe(true)
    expect(gone.closest('label')?.textContent).toContain('not found')
  })

  it('shows a phase hint beside a choice', async () => {
    const { trigger, container } = mount({ namespaces: [], all: true })
    await fireEvent.click(trigger)
    expect(boxFor(container, 'old').closest('label')?.textContent).toContain('terminating')
  })

  it('does not open when disabled', async () => {
    const { trigger, container } = mount({ namespaces: [], all: true }, { disabled: true, title: 'Nodes are cluster-scoped' })
    expect(trigger.title).toBe('Nodes are cluster-scoped')
    await fireEvent.click(trigger)
    expect(container.querySelector('[role="dialog"]')).toBeNull()
  })
})

describe('closing from outside the panel', () => {
  it('closes on Escape even when focus is not inside it, keeping what was ticked', async () => {
    const { trigger, onchange, container } = mount({ namespaces: ['shop'], all: false })
    await fireEvent.click(trigger)
    await fireEvent.click(boxFor(container, 'keda'))
    ;(document.activeElement as HTMLElement | null)?.blur()

    await fireEvent.keyDown(window, { key: 'Escape' })

    expect(onchange).toHaveBeenCalledWith({ namespaces: ['keda', 'shop'], all: false })
    expect(container.querySelector('[role="dialog"]')).toBeNull()
  })

  it('closes when focus moves to something outside the picker', async () => {
    const outside = document.createElement('button')
    document.body.appendChild(outside)
    const { trigger, getByLabelText, container } = mount({ namespaces: ['shop'], all: false })
    await fireEvent.click(trigger)

    await fireEvent.focusOut(getByLabelText('Filter namespaces'), { relatedTarget: outside })

    expect(container.querySelector('[role="dialog"]')).toBeNull()
    outside.remove()
  })

  it('stays open while focus moves within it', async () => {
    const { trigger, getByLabelText, container } = mount({ namespaces: ['shop'], all: false })
    await fireEvent.click(trigger)

    await fireEvent.focusOut(getByLabelText('Filter namespaces'), {
      relatedTarget: getByLabelText('All namespaces'),
    })

    expect(container.querySelector('[role="dialog"]')).not.toBeNull()
  })
})
