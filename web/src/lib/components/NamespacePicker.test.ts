import { cleanup, fireEvent, render } from '@testing-library/svelte'
import { afterEach, describe, expect, it, vi } from 'vitest'

import NamespacePicker from './NamespacePicker.svelte'
import type { NamespaceScope } from '$lib/namespaceScope'

const choices = [{ name: 'billing' }, { name: 'keda' }, { name: 'shop' }, { name: 'old', hint: 'terminating' }]

function mount(value: NamespaceScope, extra: Record<string, unknown> = {}) {
  const onapply = vi.fn()
  const rendered = render(NamespacePicker, { value, choices, onapply, ...extra })
  const trigger = rendered.getByRole('button', { name: /Namespaces/ }) as HTMLButtonElement
  return { ...rendered, onapply, trigger }
}

function boxFor(container: HTMLElement, name: string): HTMLInputElement {
  const label = [...container.querySelectorAll('label')].find((node) => node.textContent?.includes(name))
  return label!.querySelector('input') as HTMLInputElement
}

afterEach(cleanup)

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

  it('disables the list while All is ticked, and enables it when All is cleared', async () => {
    const { trigger, container, getByLabelText } = mount({ namespaces: [], all: true })
    await fireEvent.click(trigger)

    expect(boxFor(container, 'keda').disabled).toBe(true)
    await fireEvent.click(getByLabelText('All namespaces'))
    expect(boxFor(container, 'keda').disabled).toBe(false)
  })

  it('will not apply an empty set, and applies a ticked one sorted', async () => {
    const { trigger, container, getByLabelText, getByRole, onapply } = mount({ namespaces: [], all: true })
    await fireEvent.click(trigger)
    await fireEvent.click(getByLabelText('All namespaces'))

    const apply = getByRole('button', { name: 'Apply' }) as HTMLButtonElement
    expect(apply.disabled).toBe(true)

    await fireEvent.click(boxFor(container, 'shop'))
    await fireEvent.click(boxFor(container, 'billing'))
    expect(apply.disabled).toBe(false)
    await fireEvent.click(apply)

    expect(onapply).toHaveBeenCalledWith({ namespaces: ['billing', 'shop'], all: false })
  })

  it('changes nothing on Cancel', async () => {
    const { trigger, container, getByRole, onapply } = mount({ namespaces: ['shop'], all: false })
    await fireEvent.click(trigger)
    await fireEvent.click(boxFor(container, 'keda'))
    await fireEvent.click(getByRole('button', { name: 'Cancel' }))

    expect(onapply).not.toHaveBeenCalled()
    expect(container.querySelector('[role="dialog"]')).toBeNull()
  })

  it('works from the keyboard: open into the filter, move, tick, apply', async () => {
    const { trigger, getByLabelText, onapply } = mount({ namespaces: ['shop'], all: false })
    await fireEvent.keyDown(trigger, { key: 'Enter' })

    const filter = getByLabelText('Filter namespaces')
    expect(document.activeElement).toBe(filter)

    // Row 0 is "All namespaces"; ↓ lands on billing.
    await fireEvent.keyDown(filter, { key: 'ArrowDown' })
    await fireEvent.keyDown(filter, { key: ' ' })
    await fireEvent.keyDown(filter, { key: 'Enter' })

    expect(onapply).toHaveBeenCalledWith({ namespaces: ['billing', 'shop'], all: false })
  })

  it('cancels on Escape', async () => {
    const { trigger, getByLabelText, onapply, container } = mount({ namespaces: ['shop'], all: false })
    await fireEvent.keyDown(trigger, { key: 'Enter' })
    await fireEvent.keyDown(getByLabelText('Filter namespaces'), { key: 'Escape' })

    expect(onapply).not.toHaveBeenCalled()
    expect(container.querySelector('[role="dialog"]')).toBeNull()
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
