/**
 * What the decoded ownership ledger tells an operator.
 *
 * The panel exists because the raw record does not answer the question the
 * toggle's own tooltip promises to answer. These assertions are about that
 * promise: the manager is named, what kind of thing it is is said in the same
 * words a conflict dialog would use, and the two operations of one manager
 * stay two rows — because the server treats them as two managers, and that is
 * the whole reason an apply can conflict with PodSteer's own earlier edit.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { render, cleanup, fireEvent, screen } from '@testing-library/svelte'

const fieldOwnership = vi.fn()
vi.mock(
  '$lib/bindings/github.com/podsteer/podsteer/app/adapters/wails/managementapi',
  async () => ({ FieldOwnership: (...args: unknown[]) => fieldOwnership(...args) }),
)

import FieldOwnershipPanel from './FieldOwnershipPanel.svelte'

const words = (element: HTMLElement) => (element.textContent ?? '').replace(/\s+/g, ' ')

function owner(overrides: Record<string, unknown> = {}) {
  return {
    manager: 'argocd-controller',
    kind: 'gitops',
    operation: 'Apply',
    subresource: '',
    updatedAt: '2026-09-11T15:17:59Z',
    fields: ['.spec.replicas'],
    ...overrides,
  }
}

/** Lets the panel's guarded await settle. */
const settle = () => new Promise((resolve) => setTimeout(resolve, 0))

beforeEach(() => {
  fieldOwnership.mockReset()
})

afterEach(() => cleanup())

describe('FieldOwnershipPanel', () => {
  it('names the manager and says what kind of thing it is', async () => {
    fieldOwnership.mockResolvedValue([owner()])
    const { container } = render(FieldOwnershipPanel, { manifest: 'apiVersion: v1\n' })
    await settle()

    const text = words(container as unknown as HTMLElement)
    expect(text).toContain('argocd-controller')
    expect(text).toContain('a reconciler')
  })

  it('keeps the two operations of one manager apart', async () => {
    // The trap this whole increment is about: the server keys an entry on
    // name AND operation, so podsteer/Update and podsteer/Apply are two
    // managers. Collapsing them would hide why an apply ever conflicts with
    // PodSteer's own earlier edit.
    fieldOwnership.mockResolvedValue([
      owner({ manager: 'podsteer', kind: 'podsteer', operation: 'Update', fields: ['.data.one'] }),
      owner({ manager: 'podsteer', kind: 'podsteer', operation: 'Apply', fields: ['.data.two'] }),
    ])
    render(FieldOwnershipPanel, { manifest: 'apiVersion: v1\n' })
    await settle()

    expect(screen.getAllByRole('button', { expanded: false })).toHaveLength(2)
    const text = words(document.body)
    expect(text).toContain('Update')
    expect(text).toContain('Apply')
  })

  it('shows a manager’s fields only when asked', async () => {
    fieldOwnership.mockResolvedValue([
      owner({ fields: ['.spec.template.spec.containers[name="web"].image'] }),
    ])
    render(FieldOwnershipPanel, { manifest: 'apiVersion: v1\n' })
    await settle()

    // Collapsed by default: a Deployment's ledger runs to dozens of paths,
    // and opening the toggle must not bury the manifest underneath them.
    expect(words(document.body)).not.toContain('containers[name="web"]')

    await fireEvent.click(screen.getAllByRole('button')[0])
    expect(words(document.body)).toContain('.spec.template.spec.containers[name="web"].image')
  })

  it('says plainly that an object has no record, rather than looking broken', async () => {
    fieldOwnership.mockResolvedValue([])
    render(FieldOwnershipPanel, { manifest: 'apiVersion: v1\n' })
    await settle()

    expect(words(document.body)).toContain('no ownership record')
  })

  it('reports a failure to decode without claiming the object has no owners', async () => {
    fieldOwnership.mockRejectedValue(new Error('[internal] reading the ownership record'))
    render(FieldOwnershipPanel, { manifest: 'apiVersion: v1\n' })
    await settle()

    expect(screen.getByRole('alert')).toBeTruthy()
    expect(words(document.body)).not.toContain('no ownership record')
  })

  it('ignores an answer for a manifest it has already left', async () => {
    // The drawer re-seeds this when the operator selects another object. A
    // late answer describing the previous one would be a panel that names the
    // wrong owners for the object on screen.
    let releaseFirst: (value: unknown) => void = () => {}
    fieldOwnership.mockImplementationOnce(
      () => new Promise((resolve) => (releaseFirst = resolve)),
    )
    fieldOwnership.mockResolvedValueOnce([owner({ manager: 'kubectl', kind: 'kubectl' })])

    const { rerender } = render(FieldOwnershipPanel, { manifest: 'first' })
    await rerender({ manifest: 'second' })
    await settle()

    releaseFirst([owner({ manager: 'argocd-controller', kind: 'gitops' })])
    await settle()

    expect(words(document.body)).toContain('kubectl')
    expect(words(document.body)).not.toContain('argocd-controller')
  })
})
