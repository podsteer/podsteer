/**
 * What the dialog offers when the server refuses over field ownership.
 *
 * The sentence and the button label differ by WHO owns the field, because
 * what overriding would mean differs: a reconciler takes the field straight
 * back on its next sync, so "Take ownership" would be a claim the code cannot
 * keep.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { render, cleanup, fireEvent } from '@testing-library/svelte'

const applyResource = vi.fn()
vi.mock('$lib/api/client', async () => {
  const actual = await vi.importActual<Record<string, unknown>>('$lib/api/client')
  return { ...actual, applyResource: (...args: unknown[]) => applyResource(...args) }
})

import CreateResourceDialog from './CreateResourceDialog.svelte'

/** The rendered text with its line breaks collapsed, so an assertion about a
    sentence is not defeated by where the markup happened to wrap. */
const words = (element: HTMLElement) => (element.textContent ?? '').replace(/\s+/g, ' ')

const MANIFEST = 'apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: web\n  namespace: shop\n'

function conflict(manager: string, kind: string) {
  return {
    field: '.spec.replicas',
    manager,
    message: `conflict with "${manager}" using apps/v1`,
    kind,
  }
}

function refusal(...conflicts: ReturnType<typeof conflict>[]) {
  return { created: false, kind: 'Deployment', name: 'web', namespace: 'shop', dryRun: false, warnings: [], conflicts, refused: true }
}

function props(overrides: Record<string, unknown> = {}) {
  return {
    open: true,
    clusterId: 'dev',
    kindLabel: 'Deployment',
    verb: 'New' as const,
    seed: MANIFEST,
    isReadOnly: false,
    readOnlyReason: '',
    onclose: () => {},
    oncreated: () => {},
    ...overrides,
  }
}

beforeEach(() => applyResource.mockReset())
afterEach(cleanup)

describe('a refused apply', () => {
  it('names the owner and does not read as a failure', async () => {
    applyResource.mockResolvedValue(refusal(conflict('kubectl', 'kubectl')))

    const { container, getByText } = render(CreateResourceDialog, props())
    await fireEvent.click(getByText('Apply'))

    expect(words(container)).toContain('.spec.replicas')
    expect(words(container)).toContain('kubectl')
    expect(words(container)).toContain('nothing was changed')
    // The dialog stays open: there is a decision to make.
    expect(container.querySelector('[role="alert"]')).toBeNull()
  })

  it('keeps the override button at its own width', async () => {
    // A flex column stretches its children, which turned this into a
    // full-width bar reading "Take ownership" — an action styled like a page
    // control. The container has to opt out.
    applyResource.mockResolvedValue(refusal(conflict('kubectl', 'kubectl')))

    const { getByText } = render(CreateResourceDialog, props())
    await fireEvent.click(getByText('Apply'))

    const row = getByText('Take ownership').closest('div')
    expect(row?.className).toContain('items-start')
  })

  it('offers to take a field from a person', async () => {
    applyResource.mockResolvedValue(refusal(conflict('kubectl', 'kubectl')))

    const { getByText } = render(CreateResourceDialog, props())
    await fireEvent.click(getByText('Apply'))

    expect(getByText('Take ownership')).toBeTruthy()
  })

  it('shows the server-side flags the dialog actually sends', async () => {
    // A plain `kubectl apply` is a client-side apply and would neither merge
    // the same way nor produce this conflict. The hint has to say what was
    // sent, and gain --force-conflicts once there is something to override.
    applyResource.mockResolvedValue(refusal(conflict('kubectl', 'kubectl')))

    const { container, getByText } = render(CreateResourceDialog, props())
    expect(words(container)).toContain('--server-side --field-manager=podsteer')
    expect(words(container)).not.toContain('--force-conflicts')

    await fireEvent.click(getByText('Apply'))

    // THE FORCED COMMAND BELONGS BESIDE THE BUTTON THAT FORCES. This used to
    // put --force-conflicts into the hint next to Apply the moment a conflict
    // existed — describing a command Apply does not send, before the operator
    // had chosen anything.
    // KubectlHint renders the command into a monospaced paragraph.
    const commands = [...container.querySelectorAll('p.font-mono')].map((node) =>
      (node.textContent ?? '').replace(/\s+/g, ' '),
    )
    const forced = commands.filter((command) => command.includes('--force-conflicts'))
    const plain = commands.filter(
      (command) => command.includes('--server-side') && !command.includes('--force-conflicts'),
    )

    expect(plain.length, 'a non-forcing command for Apply').toBeGreaterThan(0)
    expect(forced.length, 'a forcing command for Override').toBeGreaterThan(0)
  })

  it('does not say "some of these" about one field', async () => {
    applyResource.mockResolvedValue(refusal(conflict('argocd-controller', 'gitops')))

    const { container, getByText } = render(CreateResourceDialog, props())
    await fireEvent.click(getByText('Apply'))

    expect(words(container)).toContain('A reconciler owns this field.')
    expect(words(container)).not.toContain('some of these')
  })

  it('refuses to call it ownership when a reconciler will take it back', async () => {
    // THE LABEL IS THE POINT. Argo CD reverts on its next sync, so "Take
    // ownership" would be a claim this cannot keep.
    applyResource.mockResolvedValue(refusal(conflict('argocd-controller', 'gitops')))

    const { container, getByText } = render(CreateResourceDialog, props())
    await fireEvent.click(getByText('Apply'))

    expect(getByText('Override anyway')).toBeTruthy()
    expect(words(container)).toContain('undone on its next sync')
  })

  it('sends the confirmed set back when the operator overrides', async () => {
    const owned = conflict('kubectl', 'kubectl')
    applyResource
      .mockResolvedValueOnce(refusal(owned))
      .mockResolvedValueOnce({ created: false, kind: 'Deployment', name: 'web', namespace: 'shop', dryRun: false, warnings: [], conflicts: [], refused: false })

    const { getByText } = render(CreateResourceDialog, props())
    await fireEvent.click(getByText('Apply'))
    await fireEvent.click(getByText('Take ownership'))

    // The plain apply carries no confirmed set — it defaults — and the
    // override carries exactly what was shown.
    expect(applyResource.mock.calls[0][3] ?? []).toEqual([])
    expect(applyResource.mock.calls[1][3]).toEqual([owned])
  })

  it('reports the object with its namespace after a forced apply', async () => {
    // THE BUG THIS GUARDS. handleOverride called oncreated(name, '') — the
    // right name and NO namespace — so after a successful override the drawer
    // opened on an object it could not fetch: "No manifest available", and a
    // red "The requested resource no longer exists" over a list where the
    // object was plainly still running. handleApply had always passed both.
    const created = vi.fn()
    applyResource
      .mockResolvedValueOnce(refusal(conflict('kubectl', 'kubectl')))
      .mockResolvedValueOnce({
        created: false, kind: 'Deployment', name: 'web', namespace: 'shop',
        dryRun: false, warnings: [], conflicts: [], refused: false,
      })

    const { getByText } = render(CreateResourceDialog, props({ oncreated: created }))
    await fireEvent.click(getByText('Apply'))
    await fireEvent.click(getByText('Take ownership'))

    expect(created).toHaveBeenCalledWith('web', 'shop')
  })

  it('reports the namespace on an ordinary apply too', async () => {
    const created = vi.fn()
    applyResource.mockResolvedValue({
      created: true, kind: 'Deployment', name: 'web', namespace: 'shop',
      dryRun: false, warnings: [], conflicts: [], refused: false,
    })

    const { getByText } = render(CreateResourceDialog, props({ oncreated: created }))
    await fireEvent.click(getByText('Apply'))

    expect(created).toHaveBeenCalledWith('web', 'shop')
  })

  it('gates the override behind the object name on a production cluster', async () => {
    applyResource.mockResolvedValue(refusal(conflict('kubectl', 'kubectl')))

    const { getByText, container } = render(
      CreateResourceDialog,
      props({ productionGroup: 'Production' }),
    )
    await fireEvent.click(getByText('Apply'))

    const button = getByText('Take ownership').closest('button')
    expect(button?.disabled).toBe(true)

    // The label names the object, which is what must be typed.
    expect(words(container)).toContain('Type web to override')

    // BY TEST ID, because the dialog has more than one text input and the
    // first one is not this. Selecting positionally typed into the wrong
    // field and made the gate look broken when it was not.
    const input = container.querySelector('[data-testid="override-confirm"]') as HTMLInputElement
    await fireEvent.input(input, { target: { value: 'web' } })

    expect(getByText('Take ownership').closest('button')?.disabled).toBe(false)
  })
})
