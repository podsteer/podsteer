import { describe, expect, it, vi } from 'vitest'

import { buildCommands, type CommandContext, type CommandHandlers } from './commands'

function context(overrides: Partial<CommandContext> = {}): CommandContext {
  return {
    hasActiveCluster: true,
    kinds: [],
    otherClusterTabs: [],
    namespaces: [],
    showsAllNamespaces: true,
    selectedKindSingular: undefined,
    canExportCSV: false,
    ...overrides,
  }
}

function handlers(): CommandHandlers {
  return {
    goToKind: vi.fn(),
    focusCluster: vi.fn(),
    setNamespace: vi.fn(),
    openSettings: vi.fn(),
    openOrganise: vi.fn(),
    openShortcutSheet: vi.fn(),
    refresh: vi.fn(),
    toggleNavigator: vi.fn(),
    exportCSV: vi.fn(),
    newResource: vi.fn(),
  }
}

describe('buildCommands', () => {
  it('offers the always-available global commands even with no cluster open', () => {
    const commands = buildCommands(context({ hasActiveCluster: false }), handlers())
    const titles = commands.map((c) => c.title)
    expect(titles).toEqual(
      expect.arrayContaining(['Open Settings', 'Open Organise', 'Show keyboard shortcuts']),
    )
    expect(commands.every((c) => c.scope === 'global')).toBe(true)
  })

  it('omits every cluster-scoped command when there is no active cluster', () => {
    const commands = buildCommands(
      context({ hasActiveCluster: false, canExportCSV: true, selectedKindSingular: 'Pod' }),
      handlers(),
    )
    expect(commands.some((c) => c.id === 'action:refresh')).toBe(false)
    expect(commands.some((c) => c.id === 'action:toggle-navigator')).toBe(false)
    expect(commands.some((c) => c.id === 'action:export-csv')).toBe(false)
    expect(commands.some((c) => c.id === 'action:new-resource')).toBe(false)
  })

  it('offers refresh and toggle-navigator whenever a cluster is active', () => {
    const commands = buildCommands(context(), handlers())
    expect(commands.some((c) => c.id === 'action:refresh')).toBe(true)
    expect(commands.some((c) => c.id === 'action:toggle-navigator')).toBe(true)
  })

  it('offers Export CSV only when the context says it is usable', () => {
    expect(buildCommands(context({ canExportCSV: false }), handlers()).some((c) => c.id === 'action:export-csv')).toBe(false)
    expect(buildCommands(context({ canExportCSV: true }), handlers()).some((c) => c.id === 'action:export-csv')).toBe(true)
  })

  it('offers "New <kind>" only once a kind is selected, named after it', () => {
    expect(
      buildCommands(context({ selectedKindSingular: undefined }), handlers()).some(
        (c) => c.id === 'action:new-resource',
      ),
    ).toBe(false)

    const commands = buildCommands(context({ selectedKindSingular: 'Deployment' }), handlers())
    const newResource = commands.find((c) => c.id === 'action:new-resource')
    expect(newResource?.title).toBe('New Deployment')
  })

  it('builds one "Go to <kind>" command per kind, carrying the kind id for the glyph and Tab-accept', () => {
    const commands = buildCommands(
      context({
        kinds: [
          { id: 'apps/v1/deployments', title: 'Deployments', singular: 'Deployment', group: 'apps' },
          { id: 'core/v1/pods', title: 'Pods', singular: 'Pod', group: '' },
        ],
      }),
      handlers(),
    )
    const kindCommands = commands.filter((c) => c.group === 'Kinds')
    expect(kindCommands).toHaveLength(2)
    expect(kindCommands.map((c) => c.kindId)).toEqual(
      expect.arrayContaining(['apps/v1/deployments', 'core/v1/pods']),
    )
    expect(kindCommands.every((c) => c.scope === 'cluster')).toBe(true)
  })

  it('runs goToKind with the kind id when a Kinds command is invoked', async () => {
    const h = handlers()
    const commands = buildCommands(
      context({ kinds: [{ id: 'apps/v1/deployments', title: 'Deployments', singular: 'Deployment', group: 'apps' }] }),
      h,
    )
    await commands.find((c) => c.group === 'Kinds')?.run()
    expect(h.goToKind).toHaveBeenCalledWith('apps/v1/deployments')
  })

  it('builds one "switch to" command per OTHER open cluster tab', () => {
    const commands = buildCommands(context({ otherClusterTabs: [{ id: 'staging' }, { id: 'prod' }] }), handlers())
    const clusterCommands = commands.filter((c) => c.group === 'Clusters')
    expect(clusterCommands.map((c) => c.title).sort()).toEqual(['prod', 'staging'])
    // Global, not cluster-scoped: switching tabs has to work from the picker.
    expect(clusterCommands.every((c) => c.scope === 'global')).toBe(true)
  })

  it('runs focusCluster with the tab id when a Clusters command is invoked', async () => {
    const h = handlers()
    const commands = buildCommands(context({ otherClusterTabs: [{ id: 'staging' }] }), h)
    await commands.find((c) => c.group === 'Clusters')?.run()
    expect(h.focusCluster).toHaveBeenCalledWith('staging')
  })

  it('builds a "set namespace" command per namespace, plus "All namespaces" unless already showing it', () => {
    const withFilter = buildCommands(
      context({ namespaces: ['default', 'kube-system'], showsAllNamespaces: false }),
      handlers(),
    )
    const namespaceCommands = withFilter.filter((c) => c.group === 'Namespaces')
    expect(namespaceCommands.map((c) => c.title).sort()).toEqual(['All namespaces', 'default', 'kube-system'])

    const showingAll = buildCommands(
      context({ namespaces: ['default'], showsAllNamespaces: true }),
      handlers(),
    )
    expect(showingAll.filter((c) => c.group === 'Namespaces').map((c) => c.title)).toEqual(['default'])
  })

  it('runs setNamespace with the empty string for "All namespaces"', async () => {
    const h = handlers()
    const commands = buildCommands(context({ showsAllNamespaces: false }), h)
    await commands.find((c) => c.title === 'All namespaces')?.run()
    expect(h.setNamespace).toHaveBeenCalledWith('')
  })

  it('gives every command a unique id', () => {
    const commands = buildCommands(
      context({
        kinds: [{ id: 'core/v1/pods', title: 'Pods', singular: 'Pod', group: '' }],
        otherClusterTabs: [{ id: 'staging' }],
        namespaces: ['default'],
        showsAllNamespaces: false,
        canExportCSV: true,
        selectedKindSingular: 'Pod',
      }),
      handlers(),
    )
    const ids = commands.map((c) => c.id)
    expect(new Set(ids).size).toBe(ids.length)
  })
})
