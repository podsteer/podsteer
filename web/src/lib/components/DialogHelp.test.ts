import { render, fireEvent, cleanup } from '@testing-library/svelte'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const findClusterShells = vi.fn()

vi.mock('$lib/api/client', () => ({
  findClusterShells: (...args: unknown[]) => findClusterShells(...args),
}))

import ClusterShellDialog from './ClusterShellDialog.svelte'
import LocalShellDialog from './LocalShellDialog.svelte'
import HelpPanel from './HelpPanel.svelte'
import TerminalMenu from './TerminalMenu.svelte'
import { HELP_TOPICS, helpTopic } from '$lib/help'
import { help } from '$stores/help.svelte'

afterEach(() => {
  cleanup()
  help.close()
})

describe('the help registry', () => {
  it('gives every topic a lede and at least one headed section', () => {
    // A topic with no sections opens an empty drawer, which is worse than no
    // (?) at all: it answers a question by showing that nobody wrote one.
    for (const [id, topic] of Object.entries(HELP_TOPICS)) {
      expect(topic.title, id).not.toBe('')
      expect(topic.lede, id).not.toBe('')
      expect(topic.sections.length, id).toBeGreaterThan(0)
      for (const section of topic.sections) {
        expect(section.heading, id).not.toBe('')
        expect(section.body.length, id).toBeGreaterThan(0)
        for (const paragraph of section.body) expect(paragraph.trim(), id).not.toBe('')
      }
    }
  })

  it('answers for a topic nobody wrote with nothing, rather than with something empty', () => {
    expect(helpTopic('no-such-topic')).toBeNull()
    expect(helpTopic(null)).toBeNull()
  })

  it('still carries what the dialogs used to print', () => {
    // These four sentences were printed down the front of the shell dialogs.
    // Moving prose and deleting it look identical in a screenshot, so what
    // holds them now is asserted rather than assumed.
    const said = (id: string): string =>
      HELP_TOPICS[id as keyof typeof HELP_TOPICS].sections
        .flatMap((section) => section.body)
        .join(' ')

    expect(said('local-shell')).toContain('--context')
    expect(said('local-shell')).toContain('read-only')
    expect(said('cluster-shell')).toContain('unprivileged pod')
    expect(said('cluster-shell')).toContain('restricted')
    expect(said('node-shell')).toContain('nsenter')
  })
})

describe('the help panel', () => {
  it('shows nothing at all until a topic is asked for', () => {
    const { container } = render(HelpPanel)
    expect(container.querySelector('[role="dialog"]')).toBeNull()
  })

  it('renders the open topic, headings and all', async () => {
    render(HelpPanel)
    help.open('evict')
    await vi.waitFor(() => {
      expect(document.querySelector('[role="dialog"]')).toBeTruthy()
    })

    const panel = document.querySelector('[role="dialog"]')!
    expect(panel.getAttribute('aria-label')).toBe('Help: Evict')
    expect(panel.textContent).toContain('What refuses it')
    expect(panel.textContent).toContain('PodDisruptionBudget')
  })

  it('closes, leaving whatever it was opened over alone', async () => {
    render(HelpPanel)
    help.open('drain')
    await vi.waitFor(() => expect(document.querySelector('[role="dialog"]')).toBeTruthy())
    ;(document.querySelector('[aria-label="Close help"]') as HTMLElement).click()
    await vi.waitFor(() => expect(document.querySelector('[role="dialog"]')).toBeNull())
    expect(help.topic).toBeNull()
  })
})

describe('a dialog header', () => {
  const localProps = {
    open: true,
    clusterId: 'plt-euc3-de1-dev-svc-01',
    agents: [],
    onclose: () => {},
    onconfirm: () => {},
  }

  it('carries a close control, not only an invisible way out', async () => {
    // Escape and a click behind the dialog both closed these already, and
    // both are invisible. Somebody looking for the control that dismisses a
    // dialog should find one.
    const onclose = vi.fn()
    const { getByRole } = render(LocalShellDialog, { ...localProps, onclose })

    await fireEvent.click(getByRole('button', { name: 'Close' }))
    expect(onclose).toHaveBeenCalledTimes(1)
  })

  it('opens this dialog’s topic and not another', async () => {
    const { getByRole } = render(LocalShellDialog, localProps)
    await fireEvent.click(getByRole('button', { name: 'Help with Local terminal' }))
    expect(help.topic).toBe('local-shell')
  })
})

describe('what the dialogs print now', () => {
  beforeEach(() => {
    findClusterShells.mockReset()
    findClusterShells.mockResolvedValue({ reusable: [], other: [] })
  })

  it('the local terminal states its context and explains nothing', () => {
    // THE FACT IS WHAT VARIES between one opening and the next. Everything
    // around it — what KUBECONFIG points at, why current-context is left
    // alone, that read-only does not apply — is read once, if ever, and is in
    // the panel.
    const { getByText, queryByText } = render(LocalShellDialog, {
      open: true,
      clusterId: 'plt-euc3-de1-dev-svc-01',
      agents: [],
      onclose: () => {},
      onconfirm: () => {},
    })

    expect(getByText('plt-euc3-de1-dev-svc-01')).toBeTruthy()
    expect(queryByText(/PodSteer never rewrites that file/)).toBeNull()
    expect(queryByText(/does not apply/)).toBeNull()
  })

  it('the in-cluster shell opens on its two fields', () => {
    const { getByLabelText, queryByText } = render(ClusterShellDialog, {
      open: true,
      clusterId: 'dev',
      namespace: 'development',
      onclose: () => {},
      onconfirm: () => {},
      onattach: () => {},
    })

    expect((getByLabelText('Image') as HTMLInputElement).value).toContain('dockydeb')
    expect(queryByText(/ordinary, unprivileged pod/)).toBeNull()
    expect(queryByText(/self-destructs/)).toBeNull()
  })

  it('the terminal menu prints no description under either entry', async () => {
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
    expect(local.getAttribute('title')).toContain('KUBECONFIG')
    expect(cluster.getAttribute('title')).toContain('throwaway pod')
  })
})
