import { render, fireEvent, cleanup } from '@testing-library/svelte'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'

import TerminalImagesPane from './TerminalImagesPane.svelte'
import {
  preferences,
  DEFAULT_DEBUG_IMAGE,
  DEFAULT_NODE_SHELL_IMAGE,
  DEFAULT_NODE_SHELL_NAMESPACE,
  DEFAULT_CLUSTER_SHELL_IMAGE,
} from '$stores/preferences.svelte'

describe('Settings → Terminal images', () => {
  beforeEach(() => {
    preferences.setDebugImage('')
    preferences.setNodeShellImage('')
    preferences.setNodeShellNamespace('')
    preferences.setClusterShellImage('')
  })

  afterEach(() => {
    // render() appends to document.body and the queries are body-scoped, so
    // without this the second test's field matches the first test's too.
    cleanup()
    preferences.setDebugImage('')
    preferences.setNodeShellImage('')
    preferences.setNodeShellNamespace('')
    preferences.setClusterShellImage('')
  })

  it('shows every image PodSteer puts into a cluster, in one place', () => {
    // The whole reason the pane exists: an operator whose clusters cannot pull
    // from Docker Hub could only find these by opening the dialog that used
    // one, which is not where they are when they hit ImagePullBackOff.
    const { getByLabelText } = render(TerminalImagesPane)

    expect((getByLabelText('Debug container image') as HTMLInputElement).value).toBe(
      DEFAULT_DEBUG_IMAGE,
    )
    expect((getByLabelText('Node shell image') as HTMLInputElement).value).toBe(
      DEFAULT_NODE_SHELL_IMAGE,
    )
    expect((getByLabelText('Namespace for the node-shell pod') as HTMLInputElement).value).toBe(
      DEFAULT_NODE_SHELL_NAMESPACE,
    )
    expect((getByLabelText('In-cluster shell image') as HTMLInputElement).value).toBe(
      DEFAULT_CLUSTER_SHELL_IMAGE,
    )
  })

  it('gives the in-cluster shell no namespace setting, because that one follows the tab', () => {
    // Deliberately absent, unlike the node shell's. Persisting a namespace
    // here would be a fact about a cluster's contents in a store that holds
    // workflow preferences, and it would disagree with the tab the operator is
    // looking at. See $lib/clusterShell.
    const { queryByLabelText } = render(TerminalImagesPane)

    expect(queryByLabelText('Namespace for the in-cluster shell pod')).toBeNull()
  })

  it('commits an edited image to the preference', async () => {
    const { getByLabelText } = render(TerminalImagesPane)
    const field = getByLabelText('Debug container image') as HTMLInputElement

    await fireEvent.input(field, { target: { value: 'registry.internal/debug:1.0.0' } })
    await fireEvent.change(field)

    expect(preferences.debugImage).toBe('registry.internal/debug:1.0.0')
  })

  it('shows the default again when a field is cleared, rather than leaving the box blank', async () => {
    // The setters normalise a blank back to the default. Without the
    // read-back the box would keep showing the blank while the preference had
    // already reverted, which reads as a setting that did not save.
    preferences.setNodeShellNamespace('ops')
    const { getByLabelText } = render(TerminalImagesPane)
    const field = getByLabelText('Namespace for the node-shell pod') as HTMLInputElement
    expect(field.value).toBe('ops')

    await fireEvent.input(field, { target: { value: '   ' } })
    await fireEvent.change(field)

    expect(preferences.nodeShellNamespace).toBe(DEFAULT_NODE_SHELL_NAMESPACE)
    expect(field.value).toBe(DEFAULT_NODE_SHELL_NAMESPACE)
  })

  it('restores every default at once, and offers nothing to press when already there', async () => {
    preferences.setDebugImage('registry.internal/debug:1.0.0')
    preferences.setNodeShellImage('registry.internal/shell:1.0.0')
    preferences.setClusterShellImage('registry.internal/incluster:1.0.0')
    const { getByRole } = render(TerminalImagesPane)

    const restore = getByRole('button', { name: 'Restore defaults' }) as HTMLButtonElement
    expect(restore.disabled).toBe(false)

    await fireEvent.click(restore)

    expect(preferences.debugImage).toBe(DEFAULT_DEBUG_IMAGE)
    expect(preferences.nodeShellImage).toBe(DEFAULT_NODE_SHELL_IMAGE)
    expect(preferences.nodeShellNamespace).toBe(DEFAULT_NODE_SHELL_NAMESPACE)
    expect(preferences.clusterShellImage).toBe(DEFAULT_CLUSTER_SHELL_IMAGE)
    expect(restore.disabled).toBe(true)
  })
})
