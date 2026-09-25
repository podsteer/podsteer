import { describe, expect, it } from 'vitest'

import {
  canOpenClusterShell,
  clusterShellNamespaceFor,
  clusterShellRequest,
  exitedShellsNote,
} from './clusterShell'
import { ALL_NAMESPACES } from './api/client'
import { DEFAULT_CLUSTER_SHELL_IMAGE, DEFAULT_NODE_SHELL_NAMESPACE } from '$stores/preferences.svelte'

describe('which namespace an in-cluster shell opens in', () => {
  it("defaults to the tab's namespace, so the two terminals agree with the rest of the interface", () => {
    expect(clusterShellNamespaceFor('shop')).toBe('shop')
  })

  it('has NO answer when the tab is on every namespace, and says so by being empty', () => {
    // A pod lives in exactly one namespace, so "all namespaces" is not a
    // scope this has. The dialog asks; nothing here guesses.
    expect(clusterShellNamespaceFor(ALL_NAMESPACES)).toBe('')
    expect(canOpenClusterShell(clusterShellNamespaceFor(ALL_NAMESPACES))).toBe(false)
  })

  it('never falls back to a system namespace, which is the node shell’s setting and the wrong answer here', () => {
    // The node shell's default is kube-system, where admission is permissive
    // and where a node shell has to be. Reusing it here would put a pod in the
    // one namespace an operator is least likely to be permitted to create one
    // in, without being told.
    expect(clusterShellNamespaceFor(ALL_NAMESPACES)).not.toBe(DEFAULT_NODE_SHELL_NAMESPACE)
    expect(clusterShellRequest('', '').namespace).toBe('')
  })

  it('will not confirm on whitespace, which would reach the backend as "all namespaces"', () => {
    expect(canOpenClusterShell('   ')).toBe(false)
    expect(clusterShellRequest('', '  shop  ').namespace).toBe('shop')
    expect(canOpenClusterShell('shop')).toBe(true)
  })
})

describe('which image an in-cluster shell runs', () => {
  it('falls back to the nonroot default rather than sending a blank the backend would reject', () => {
    expect(clusterShellRequest('', 'shop').image).toBe(DEFAULT_CLUSTER_SHELL_IMAGE)
    expect(clusterShellRequest('   ', 'shop').image).toBe(DEFAULT_CLUSTER_SHELL_IMAGE)
  })

  it("defaults to the NONROOT build, because Pod Security's restricted profile refuses a root container", () => {
    expect(DEFAULT_CLUSTER_SHELL_IMAGE).toContain('-nonroot')
  })

  it('keeps an air-gapped mirror exactly as typed', () => {
    expect(clusterShellRequest(' registry.internal/shell:1.0.0 ', 'shop').image).toBe(
      'registry.internal/shell:1.0.0',
    )
  })
})

describe('what is said about pods PodSteer created that are not running', () => {
  it('says nothing when there are none', () => {
    expect(exitedShellsNote(0)).toBe('')
  })

  it('names how many, so a namespace accumulating them is explained rather than mysterious', () => {
    expect(exitedShellsNote(1)).toContain('1 shell pod')
    expect(exitedShellsNote(3)).toContain('3 shell pods')
    // And it says they cannot be attached to — the reason they are reported
    // rather than offered.
    expect(exitedShellsNote(3)).toContain('cannot be attached to')
  })
})
