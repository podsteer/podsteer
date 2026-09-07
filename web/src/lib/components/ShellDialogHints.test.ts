import { render, fireEvent, cleanup } from '@testing-library/svelte'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const findClusterShells = vi.fn()

vi.mock('$lib/api/client', () => ({
  findClusterShells: (...args: unknown[]) => findClusterShells(...args),
}))

import ClusterShellDialog from './ClusterShellDialog.svelte'
import LocalShellDialog from './LocalShellDialog.svelte'
import TerminalMenu from './TerminalMenu.svelte'

/**
 * Opens the hint the given icon carries and hands back what it says.
 *
 * The hint is the same InfoHint the toolbar's search field uses: a button that
 * opens a `role="tooltip"` panel, so a test asks for it exactly as somebody
 * using a screen reader would.
 */
async function hintText(button: HTMLElement): Promise<string> {
  await fireEvent.click(button)
  return document.querySelector('[role="tooltip"]')?.textContent?.trim() ?? ''
}

describe('the terminal menu is its entries and nothing else', () => {
  afterEach(cleanup)

  it('prints no description under either entry', async () => {
    // The menu carried a sentence under each name — four lines of prose in
    // front of somebody who had already decided to open a terminal, repeating
    // what the dialog behind the entry says properly.
    const { getByRole } = render(TerminalMenu, {
      localSupported: true,
      localReason: '',
      readOnly: false,
      readOnlyReason: '',
      onlocal: () => {},
      oncluster: () => {},
    })
    await fireEvent.click(getByRole('button', { name: 'Terminal' }))

    const local = getByRole('menuitem', { name: /Local shell/ })
    const cluster = getByRole('menuitem', { name: /In-cluster shell/ })

    expect(local.textContent?.trim()).toBe('Local shell')
    expect(cluster.textContent?.trim()).toBe('In-cluster shell')

    // And the sentence is not lost: it answers a hesitation, from the title,
    // rather than interrupting the decision.
    expect(local.getAttribute('title')).toContain('KUBECONFIG')
    expect(cluster.getAttribute('title')).toContain('throwaway pod')
  })
})

describe('the local terminal dialog', () => {
  afterEach(cleanup)

  const props = {
    open: true,
    clusterId: 'plt-euc3-de1-dev-svc-01',
    agents: [],
    onclose: () => {},
    onconfirm: () => {},
  }

  it('states which context the shell is told, and nothing else about it', () => {
    // THE FACT IS WHAT VARIES between one opening and the next. Everything
    // around it — what KUBECONFIG is set to, why current-context is left
    // alone, that read-only does not apply — is read once, if ever.
    const { getByText, queryByText } = render(LocalShellDialog, props)

    expect(getByText('plt-euc3-de1-dev-svc-01')).toBeTruthy()
    expect(queryByText(/PodSteer never rewrites that file/)).toBeNull()
    expect(queryByText(/does not apply/)).toBeNull()
  })

  it('keeps the kubeconfig caveat one press away', async () => {
    // It is the caveat this dialog exists for: the shell is TOLD the context,
    // and kubectl in the operator's other terminals must not change target
    // because a pane was opened here.
    const { getByRole } = render(LocalShellDialog, props)

    const text = await hintText(getByRole('button', { name: 'How the context is set' }))
    expect(text).toContain('--context')
    expect(text).toContain('never rewrites')
  })

  it('keeps the read-only limit one press away too', async () => {
    // PodSteer's read-only setting guards PodSteer's own writes. A shell the
    // operator opened with their own credentials is not something it polices,
    // and somebody who assumes otherwise has assumed a protection they do not
    // have — so the sentence has to survive being moved.
    const { getByRole } = render(LocalShellDialog, props)

    const text = await hintText(getByRole('button', { name: 'What a local terminal is' }))
    expect(text).toContain('read-only')
    expect(text).toContain('KUBECONFIG')
  })

  it('says so plainly when no cluster tab is open', () => {
    // Nothing to state and nothing to explain: the shell gets the kubeconfig
    // unchanged, which is one line rather than a hint.
    const { getByText } = render(LocalShellDialog, { ...props, clusterId: '' })
    expect(getByText(/kubeconfig is passed through unchanged/)).toBeTruthy()
  })
})

describe('the in-cluster shell dialog', () => {
  beforeEach(() => {
    findClusterShells.mockReset()
    findClusterShells.mockResolvedValue({ reusable: [], other: [] })
  })
  afterEach(cleanup)

  const props = {
    open: true,
    clusterId: 'dev',
    namespace: 'development',
    onclose: () => {},
    onconfirm: () => {},
    onattach: () => {},
  }

  it('opens on the two fields and the namespace it inherited', () => {
    const { getByLabelText, queryByText } = render(ClusterShellDialog, props)

    expect((getByLabelText('Image') as HTMLInputElement).value).toContain('dockydeb')
    expect(queryByText(/ordinary, unprivileged pod/)).toBeNull()
    expect(queryByText(/self-destructs/)).toBeNull()
  })

  it('keeps what the pod is, and how long it lives, one press away', async () => {
    // The lifetime is the half somebody acts on: a pod that outlives its pane
    // is a pod somebody has to go and find.
    const { getByRole } = render(ClusterShellDialog, props)

    const text = await hintText(getByRole('button', { name: 'What an in-cluster shell is' }))
    expect(text).toContain('unprivileged pod')
    expect(text).toContain('self-destructs')
  })

  it('keeps the admission requirements beside the image they constrain', async () => {
    // The image field is where somebody types the thing that gets refused, so
    // the note about `restricted` belongs on that field and not at the foot of
    // the dialog.
    const { getByRole } = render(ClusterShellDialog, props)

    const text = await hintText(getByRole('button', { name: 'What this image has to satisfy' }))
    expect(text).toContain('restricted')
    expect(text).toContain('non-root')
  })
})
