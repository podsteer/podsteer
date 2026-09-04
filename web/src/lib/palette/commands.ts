/**
 * The command palette's registry of app commands.
 *
 * A FUNCTION OF CONTEXT, NOT A STANDING LIST. A cluster's kinds, its open
 * tabs and its namespaces all change while the application runs, and a
 * command pointing at one that no longer exists is worse than a shorter
 * list — so `buildCommands` is called fresh every time `$stores/palette`
 * needs it, over whatever the workspace looks like right now.
 *
 * Every `run()` is bound to a handler the CALLER supplies, and nothing here
 * imports a store, `$lib/api/client`, or a Wails binding — the same reason
 * `$lib/query` stays a plain module: a table of commands is worth arguing
 * over in a test without a live cluster, a live workspace or a live
 * preferences store behind it.
 *
 * `scope` reuses the exact vocabulary `$lib/shortcuts` already established
 * for the same distinction: GLOBAL commands are offered from any tab,
 * including the cluster picker; CLUSTER commands need the tab in front to
 * act on, so $stores/palette drops them entirely when there is none.
 */

import type { ShortcutScope } from '$lib/shortcuts'

/** Which result group a command renders under in the palette. Objects and
    Recents are not built here — they come from rows already in memory or
    from the on-demand kind search, neither of which is a fixed command —
    see $stores/palette for those two. */
export type CommandGroup = 'Commands' | 'Kinds' | 'Clusters' | 'Namespaces'

export interface Command {
  /** Unique within one buildCommands() call. */
  id: string
  title: string
  /** Extra words fuzzy-matched alongside the title but never shown — e.g. a
      kind's API group, so "networking" finds Ingress. See
      $lib/palette/rank. */
  keywords: string[]
  group: CommandGroup
  scope: ShortcutScope
  /** The catalogue id a Kinds-group command navigates to, carried so the
      palette can draw the same glyph the navigator uses and so Tab can turn
      the highlighted suggestion into a `kind:` pill — see
      $stores/palette's acceptKindPill. Absent for every other group. */
  kindId?: string
  run: () => void | Promise<void>
}

/** The minimum a kind needs to become a "Go to <kind>" command. Matches the
    shape of `ResourceKind` closely enough that a caller can pass one
    straight through, without this module importing `$lib/api/client` for a
    type it only reads three fields of. */
export interface CommandKind {
  id: string
  title: string
  singular: string
  group: string
}

/** One other open cluster tab, a target for "Switch to <cluster>". */
export interface CommandClusterTab {
  id: string
}

export interface CommandContext {
  /** Whether a cluster tab is in front right now. Every 'cluster'-scoped
      command is omitted entirely when this is false — there is nothing
      for it to act on from the picker. */
  hasActiveCluster: boolean
  /** The active cluster's browsable kinds — including the Overview and
      Applications pseudo-kinds, which are not in the backend's own
      catalogue (see CLAUDE.md) and so are passed in by the caller
      alongside the real ones. Empty when there is no active cluster. */
  kinds: CommandKind[]
  /** Every OPEN cluster tab other than the active one — switching to the
      tab already in front is not a command. */
  otherClusterTabs: CommandClusterTab[]
  /** The active cluster's namespace names, already loaded by the tab — see
      $stores/palette's own comment on why nothing here triggers a
      namespace LIST. */
  namespaces: string[]
  /** Whether the active tab is currently showing every namespace, so "All
      namespaces" is offered as a command only when it would change
      anything. */
  showsAllNamespaces: boolean
  /** The selected kind's singular name, for "New <kind>" — undefined for a
      pseudo-kind, or before kinds have loaded, mirroring the exact gate
      ClusterWorkspace.svelte's own New button applies to
      `session.selectedKind`. */
  selectedKindSingular?: string
  /** Whether the toolbar's Export CSV control is currently usable — a
      mounted table with at least one visible row, the same gate
      ClusterWorkspace.svelte applies. */
  canExportCSV: boolean
}

export interface CommandHandlers {
  goToKind: (kindId: string) => void | Promise<void>
  focusCluster: (clusterId: string) => void | Promise<void>
  setNamespace: (namespace: string) => void | Promise<void>
  openSettings: () => void
  openOrganise: () => void
  openShortcutSheet: () => void
  refresh: () => void | Promise<void>
  toggleNavigator: () => void
  exportCSV: () => void
  newResource: () => void
}

/**
 * Builds every command the palette can offer right now.
 *
 * Order within a group does not matter here — $stores/palette re-ranks
 * every group against whatever the operator typed, and an empty query's own
 * tie-break (recency, then alphabetical — see `$lib/palette/rank`) is what
 * decides what shows first before anything is typed.
 */
export function buildCommands(context: CommandContext, handlers: CommandHandlers): Command[] {
  const commands: Command[] = []

  for (const kind of context.kinds) {
    commands.push({
      id: `kind:${kind.id}`,
      title: kind.title,
      keywords: [kind.singular, kind.group].filter(Boolean),
      group: 'Kinds',
      scope: 'cluster',
      kindId: kind.id,
      run: () => handlers.goToKind(kind.id),
    })
  }

  for (const tab of context.otherClusterTabs) {
    commands.push({
      id: `cluster:${tab.id}`,
      title: tab.id,
      keywords: [],
      group: 'Clusters',
      scope: 'global',
      run: () => handlers.focusCluster(tab.id),
    })
  }

  if (!context.showsAllNamespaces) {
    commands.push({
      id: 'namespace:',
      title: 'All namespaces',
      keywords: [],
      group: 'Namespaces',
      scope: 'cluster',
      run: () => handlers.setNamespace(''),
    })
  }
  for (const namespace of context.namespaces) {
    commands.push({
      id: `namespace:${namespace}`,
      title: namespace,
      keywords: [],
      group: 'Namespaces',
      scope: 'cluster',
      run: () => handlers.setNamespace(namespace),
    })
  }

  commands.push(
    {
      id: 'action:settings',
      title: 'Open Settings',
      keywords: ['preferences', 'theme', 'threshold'],
      group: 'Commands',
      scope: 'global',
      run: handlers.openSettings,
    },
    {
      id: 'action:organise',
      title: 'Open Organise',
      keywords: ['projects', 'groups', 'read-only'],
      group: 'Commands',
      scope: 'global',
      run: handlers.openOrganise,
    },
    {
      id: 'action:shortcut-sheet',
      title: 'Show keyboard shortcuts',
      keywords: ['help', 'keys'],
      group: 'Commands',
      scope: 'global',
      run: handlers.openShortcutSheet,
    },
  )

  // Everything past here needs a cluster tab in front to act on — dropped
  // entirely rather than offered disabled, the same reasoning
  // $lib/shortcuts documents for its own 'cluster' scope.
  if (!context.hasActiveCluster) return commands

  commands.push(
    {
      id: 'action:refresh',
      title: 'Refresh',
      keywords: ['reload'],
      group: 'Commands',
      scope: 'cluster',
      run: handlers.refresh,
    },
    {
      id: 'action:toggle-navigator',
      title: 'Toggle navigator',
      keywords: ['sidebar', 'hide', 'show'],
      group: 'Commands',
      scope: 'cluster',
      run: handlers.toggleNavigator,
    },
  )

  if (context.canExportCSV) {
    commands.push({
      id: 'action:export-csv',
      title: 'Export CSV',
      keywords: ['download', 'save'],
      group: 'Commands',
      scope: 'cluster',
      run: handlers.exportCSV,
    })
  }

  if (context.selectedKindSingular) {
    commands.push({
      id: 'action:new-resource',
      title: `New ${context.selectedKindSingular}`,
      keywords: ['create', 'apply'],
      group: 'Commands',
      scope: 'cluster',
      run: handlers.newResource,
    })
  }

  return commands
}
