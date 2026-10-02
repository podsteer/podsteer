/**
 * With several namespaces selected (or All), "New <kind>" asks which one: an
 * object lives in exactly one, and guessing would create it somewhere the
 * operator did not mean.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { cleanup, fireEvent, render } from '@testing-library/svelte'

const applyResource = vi.fn()
vi.mock('$lib/api/client', async () => {
  const actual = await vi.importActual<Record<string, unknown>>('$lib/api/client')
  return { ...actual, applyResource: (...args: unknown[]) => applyResource(...args) }
})

import CreateResourceDialog from './CreateResourceDialog.svelte'

const SKELETON = 'apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: cfg\ndata: {}\n'

function props(overrides: Record<string, unknown> = {}) {
  return {
    open: true,
    clusterId: 'dev',
    kindLabel: 'ConfigMap',
    verb: 'New' as const,
    seed: SKELETON,
    isReadOnly: false,
    readOnlyReason: '',
    onclose: () => {},
    oncreated: () => {},
    ...overrides,
  }
}

async function expandCommands(container: HTMLElement): Promise<void> {
  for (const link of container.querySelectorAll<HTMLButtonElement>('button[aria-expanded="false"]')) {
    if ((link.textContent ?? '').includes('kubectl equivalent')) await fireEvent.click(link)
  }
}

const apply = (getAllByRole: (role: string, options: { name: string }) => HTMLElement[]) =>
  getAllByRole('button', { name: 'Apply' }).at(-1) as HTMLButtonElement

beforeEach(() => {
  applyResource.mockReset()
  applyResource.mockResolvedValue({ created: true, refused: false, conflicts: [] })
})
afterEach(cleanup)

describe('creating with several namespaces selected', () => {
  it('asks, with nothing chosen and Apply disabled, then writes the choice into the manifest and the hint', async () => {
    const { getAllByRole, getByRole, getByText, container } = render(
      CreateResourceDialog,
      props({ namespaceChoices: ['billing', 'shop'] }),
    )

    expect(apply(getAllByRole).disabled).toBe(true)
    expect(container.textContent).toContain('choose the one to create this in')

    await fireEvent.click(getByRole('button', { name: /Namespace/ }))
    await fireEvent.click(getByText('shop'))

    expect(apply(getAllByRole).disabled).toBe(false)
    await expandCommands(container)
    expect(container.textContent).toContain('kubectl --context dev -n shop apply')

    await fireEvent.click(apply(getAllByRole))
    expect(applyResource).toHaveBeenCalledWith('dev', expect.stringContaining('  namespace: shop'))
  })

  it('does not ask when exactly one is selected: the skeleton already names it', async () => {
    const seeded = SKELETON.replace('  name: cfg\n', '  name: cfg\n  namespace: shop\n')
    const { getAllByRole, queryByRole, container } = render(CreateResourceDialog, props({ seed: seeded, namespace: 'shop' }))

    expect(queryByRole('button', { name: /Namespace/ })).toBeNull()
    expect(apply(getAllByRole).disabled).toBe(false)
    await expandCommands(container)
    expect(container.textContent).toContain('kubectl --context dev -n shop apply')
  })
})
