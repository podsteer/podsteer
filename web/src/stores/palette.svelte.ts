/**
 * The command palette's state: open/closed, the query, and every result
 * group it renders — Commands, Kinds, Objects, Recents, Clusters,
 * Namespaces.
 *
 * SEARCH WHAT IS ALREADY IN MEMORY FIRST, AND NEVER FAN OUT ACROSS KINDS.
 * The palette must not turn a keystroke into a cluster-wide LIST of every
 * kind — see CLAUDE.md's "the assessment runs every tick" and "nothing
 * on-demand is cached" for why a poll here would repeat the same mistake at
 * a worse scale. So every group but one is built from data the application
 * already has:
 *
 *   - Commands, Kinds, Clusters, Namespaces come from `buildCommands`
 *     ($lib/palette/commands), itself built from the catalogue, the open
 *     tabs and the namespace list — all already loaded by the tab, none of
 *     it fetched here.
 *   - Objects reads whichever list the CURRENT view already polled — pods,
 *     workloads, nodes, namespaces, events, applications, a generic
 *     table's rows, or the merged All-clusters rows while that view is the
 *     one on screen ($stores/fleet) — with no request of its own.
 *   - Recents reads `session.recentObjects`, already in memory.
 *
 * The one exception is a `kind:` pill (or typing a kind's own name followed
 * by a space — see $lib/palette/parse) naming a kind OTHER than the one on
 * screen: that is an explicit request to search a kind nothing has polled,
 * and it costs exactly ONE `ListTable` read, in the tab's current namespace
 * scope, debounced behind the keyboard and cached for as long as this
 * palette instance stays open — see `#scheduleKindSearch` below. A second
 * cluster's objects are never fetched at all; its tab, if open, already
 * polls it, and $stores/palette only ever reads what that tab already has.
 *
 * DECOUPLED FROM $stores/workspace ON PURPOSE. Every command still acts on
 * the real `ClusterSession` and the real workspace, but this module never
 * imports either — `sync()` is handed a snapshot by CommandPalette.svelte's
 * own effect instead, kept in step with $stores/workspace for as long as
 * the palette is mounted. That is what lets a test build the one
 * `ClusterSession` it is actually about — mocking `$lib/api/client` exactly
 * as `session.test.ts` does — without also standing up the whole
 * `Workspace` singleton (its cluster list, its connections) for a question
 * that is really about ranking and grouping.
 */

import {
  ALL_NAMESPACES,
  listTable,
  saveTextFile,
  type ResourceTable,
  type TableRow,
} from '$lib/api/client'
import { toApiError } from '$lib/api/errors'
import { toCSV } from '$lib/csv'
import { buildExportFilename } from '$lib/exportFilename'
import { fleetRowTarget, type FleetTarget } from '$lib/fleet'
import {
  buildCommands,
  type Command,
  type CommandContext,
  type CommandGroup,
  type CommandHandlers,
} from '$lib/palette/commands'
import { parsePaletteQuery } from '$lib/palette/parse'
import { rank } from '$lib/palette/rank'
import { activeTable } from './activeTable.svelte'
import { fleet } from './fleet.svelte'
import { newResourceDialog } from './newResourceDialog.svelte'
import { organiseDialog } from './organiseDialog.svelte'
import { preferences } from './preferences.svelte'
import { RICH_KIND_IDS, workloadKindId, type ClusterSession } from './session.svelte'
import { settingsDialog } from './settingsDialog.svelte'
import { shortcutSheet } from './shortcutSheet.svelte'

/**
 * How long the box waits behind the keyboard before firing the ONE
 * on-demand `ListTable` a scoped kind search costs.
 *
 * Its own constant rather than reusing `ClusterSession`'s
 * `SEARCH_DEBOUNCE_MS` (120ms): that one debounces a client-side filter
 * over rows already in memory, which is cheap enough to run on almost every
 * keystroke. This debounces a NETWORK READ, so it waits for a longer pause
 * — long enough that typing out a kind's name is one request, not one per
 * letter typed after the pill appears.
 */
const KIND_SEARCH_DEBOUNCE_MS = 200

/** Top results shown per group. Eight keeps every group glanceable without
    scrolling on the common case, and a caller asking a more specific
    question (typing more of the name) narrows it further than any limit
    could. */
const GROUP_LIMIT = 8

export type PaletteGroupName = 'Commands' | 'Kinds' | 'Objects' | 'Recents' | 'Clusters' | 'Namespaces'

const GROUP_ORDER: PaletteGroupName[] = [
  'Commands',
  'Kinds',
  'Objects',
  'Recents',
  'Clusters',
  'Namespaces',
]

/** One row the palette can render and act on, whichever group it came
    from — a command straight from $lib/palette/commands, or an object built
    here from rows already in memory or from the on-demand kind search. */
export interface PaletteEntry {
  id: string
  title: string
  /** A trailing hint — an object's namespace, mainly. */
  detail?: string
  /** The catalogue id, for the navigator glyph and for Tab-accepting a
      Kinds suggestion into a `kind:` pill. Absent for Commands, Recents,
      Clusters and Namespaces entries. */
  kindId?: string
  group: PaletteGroupName
  run: () => void | Promise<void>
}

export interface PaletteResultGroup {
  name: PaletteGroupName
  entries: PaletteEntry[]
}

/** What one already-in-memory (or on-demand-fetched) object contributes to
    the Objects group before it is ranked and turned into a PaletteEntry. */
interface ObjectCandidate {
  label: string
  detail?: string
  kindId: string
  run: () => void | Promise<void>
}

class CommandPaletteStore {
  open = $state(false)
  query = $state('')
  selectedIndex = $state(0)

  /** The active tab's session, or null on the picker. Set by `sync()`. */
  #session = $state.raw<ClusterSession | null>(null)
  /** Every open tab (id only — see $lib/palette/commands' CommandClusterTab). */
  #tabs = $state.raw<{ id: string }[]>([])
  /** `workspace.focus`, handed in by `sync()` rather than imported — see the
      module comment on why this store never imports $stores/workspace. */
  #focusCluster: ((clusterId: string) => void | Promise<void>) | null = $state.raw(null)
  /** `workspace.openInCluster`, handed in the same way and for the same
      reason: a merged-table hit opens in a tab that may not be the one in
      front, and only the workspace knows how to bring one forward. */
  #openInCluster: ((target: FleetTarget) => void | Promise<void>) | null = $state.raw(null)

  /**
   * The one on-demand kind search's state.
   *
   * `#kindSearchCache` is keyed `clusterId|kindId|namespace` and holds
   * either the read in flight or its settled result — storing the PROMISE
   * before awaiting it is what stops a second keystroke inside the same
   * debounce window from starting a second request for the same scope, so
   * "exactly one ListTable call" holds even under a burst of typing.
   * Cleared on every `show()`: its life is this palette instance's, not the
   * application's.
   */
  #kindSearchCache = new Map<string, ResourceTable | Promise<ResourceTable>>()
  #kindSearchTimer: ReturnType<typeof setTimeout> | null = null
  #scopedRows = $state.raw<TableRow[]>([])
  #scopedKind = $state.raw<{ id: string; namespaced: boolean } | null>(null)

  /** The query's grammar — free text plus any `kind:`/`ns:`/`cluster:` pill
      and the leading `>` — see $lib/palette/parse. */
  readonly parsed = $derived(parsePaletteQuery(this.query))

  /**
   * The kind an object search is scoped to right now — from an explicit
   * `kind:` pill, or from typing a kind's own name followed by a space (a
   * shortcut for the pill, so an operator does not have to know the syntax
   * to use it). `undefined` means unscoped: the Objects group searches
   * whatever the current view already has.
   */
  readonly #effectiveKindId = $derived.by((): string | undefined => {
    const session = this.#session
    if (!session) return undefined

    if (this.parsed.kind !== undefined) return this.#resolveKindId(session, this.parsed.kind)

    const words = this.parsed.text.split(' ').filter(Boolean)
    // A space has to have been typed — otherwise "Deploy" mid-word would
    // scope to Deployments before the operator has finished typing it, and
    // every keystroke after would be read as an object search rather than
    // as more of the kind's own name.
    if (words.length < 2) return undefined
    return this.#resolveKindId(session, words[0])
  })

  /** Whatever free text is left once a scoping kind name — pill or
      auto-detected — has been read out of it. */
  readonly #objectSearchText = $derived.by((): string => {
    if (this.parsed.kind !== undefined) return this.parsed.text
    if (this.#effectiveKindId === undefined) return this.parsed.text
    const [, ...rest] = this.parsed.text.split(' ').filter(Boolean)
    return rest.join(' ')
  })

  /** Exact, case-insensitive match only — a `kind:` pill typed by hand or
      Tab-accepted from the Kinds group both name a kind precisely, and
      resolving a partial one fuzzily here would make the auto-detect
      trigger on ordinary search words that happen to share a prefix with a
      kind's title. */
  #resolveKindId(session: ClusterSession, needle: string): string | undefined {
    if (!needle) return undefined
    const lower = needle.toLowerCase()
    return session.kinds.find(
      (kind) =>
        kind.kind.toLowerCase() === lower ||
        kind.singular.toLowerCase() === lower ||
        kind.title.toLowerCase() === lower ||
        kind.id.toLowerCase() === lower,
    )?.id
  }

  readonly #commandContext = $derived.by((): CommandContext => {
    const session = this.#session
    return {
      hasActiveCluster: session !== null,
      kinds: session
        ? session.kinds.map((kind) => ({
            id: kind.id,
            title: kind.title,
            singular: kind.singular,
            group: kind.group,
          }))
        : [],
      otherClusterTabs: this.#tabs
        .filter((tab) => tab.id !== session?.cluster.id)
        .map((tab) => ({ id: tab.id })),
      namespaces: session ? session.namespaces.map((namespace) => namespace.name) : [],
      showsAllNamespaces: session ? session.namespace === ALL_NAMESPACES : true,
      selectedKindSingular: session?.selectedKind?.singular,
      canExportCSV: activeTable.present && (session?.visibleCount ?? 0) > 0,
    }
  })

  /** Bound once — every handler closes over `this` and reads live state
      (`this.#session`) at call time, never a snapshot taken here. */
  #handlers: CommandHandlers = {
    goToKind: (kindId) => this.#session?.selectKind(kindId),
    focusCluster: (clusterId) => this.#focusCluster?.(clusterId),
    setNamespace: (namespace) => this.#session?.selectNamespace(namespace),
    openSettings: () => settingsDialog.show(),
    openOrganise: () => organiseDialog.show(),
    openShortcutSheet: () => shortcutSheet.show(),
    refresh: () => this.#session?.refresh(),
    toggleNavigator: () => preferences.toggleNavigator(),
    exportCSV: () => void this.#exportCSV(),
    newResource: () => newResourceDialog.show(),
  }

  readonly #allCommands = $derived.by((): Command[] =>
    buildCommands(this.#commandContext, this.#handlers),
  )

  /** Every result group with at least one entry, in display order. Empty
      groups are dropped rather than shown as an empty heading. */
  readonly groups = $derived.by((): PaletteResultGroup[] => {
    const parsed = this.parsed
    const commandsGroup: PaletteResultGroup = {
      name: 'Commands',
      entries: this.#commandBucket('Commands', parsed.text),
    }

    // "> " restricts the whole palette to actions, on purpose — see
    // $lib/palette/parse's own comment on the leading '>'.
    if (parsed.commandsOnly) {
      return commandsGroup.entries.length > 0 ? [commandsGroup] : []
    }

    const groups: PaletteResultGroup[] = [
      commandsGroup,
      { name: 'Kinds', entries: this.#commandBucket('Kinds', parsed.text) },
      this.#objectsGroup(),
      this.#recentsGroup(),
      { name: 'Clusters', entries: this.#commandBucket('Clusters', parsed.cluster ?? parsed.text) },
      {
        name: 'Namespaces',
        entries: this.#commandBucket('Namespaces', parsed.namespace ?? parsed.text),
      },
    ]

    return groups.filter((group) => group.entries.length > 0)
  })

  /** Every entry, in the same order the palette renders its groups —
      what ↑/↓ actually walks. */
  readonly flatEntries = $derived(
    GROUP_ORDER.flatMap(
      (name) => this.groups.find((group) => group.name === name)?.entries ?? [],
    ),
  )

  #commandBucket(group: CommandGroup, text: string): PaletteEntry[] {
    const candidates = this.#allCommands
      .filter((command) => command.group === group)
      .map((command) => ({ label: command.title, keywords: command.keywords, command }))

    return rank(text, candidates)
      .slice(0, GROUP_LIMIT)
      .map(({ command }) => ({
        id: command.id,
        title: command.title,
        kindId: command.kindId,
        group,
        run: command.run,
      }))
  }

  /**
   * The Objects group: rows to search by name, from whichever source is
   * currently in scope.
   *
   * Unscoped, or scoped to the kind already on screen, this is the current
   * view's own already-polled rows — no request at all. Scoped to a
   * DIFFERENT kind, this is `#scopedRows`, the on-demand read's result —
   * see `#scheduleKindSearch`.
   */
  #objectsGroup(): PaletteResultGroup {
    const session = this.#session
    if (!session) return { name: 'Objects', entries: [] }

    const kindId = this.#effectiveKindId
    const usingCurrentView = kindId === undefined || kindId === session.selectedKindId
    const candidates = usingCurrentView ? this.#currentViewObjects(session) : this.#scopedObjects()
    const text = usingCurrentView ? this.parsed.text : this.#objectSearchText

    const ranked = rank(text, candidates)
    return {
      name: 'Objects',
      entries: ranked.slice(0, GROUP_LIMIT).map((candidate) => ({
        id: `object:${candidate.kindId}:${candidate.detail ?? ''}:${candidate.label}`,
        title: candidate.label,
        detail: candidate.detail,
        kindId: candidate.kindId,
        group: 'Objects',
        run: candidate.run,
      })),
    }
  }

  /** The current view's own rows, already fetched by the tab's own poll —
      one branch per `ViewMode`, because each keeps its rows under a
      differently-shaped field (see ClusterSession). Applications route
      through `session.openApplication`, not `openObject` — an application
      is not a Kubernetes object with a manifest to fetch, see
      ClusterSession's own doc comment on it. */
  #currentViewObjects(session: ClusterSession): ObjectCandidate[] {
    const kindId = session.selectedKindId
    const open =
      (name: string, namespace: string, namespaced: boolean) => (): Promise<void> =>
        session.openObject(kindId, name, namespace, namespaced)

    switch (session.viewMode) {
      case 'pods':
        return session.pods.map((pod) => ({
          label: pod.name,
          detail: pod.namespace,
          kindId,
          run: open(pod.name, pod.namespace, true),
        }))
      case 'nodes':
        return session.nodes.map((node) => ({
          label: node.name,
          kindId,
          run: open(node.name, '', false),
        }))
      case 'workloads':
        return session.workloads.map((workload) => ({
          label: workload.name,
          detail: workload.namespace,
          kindId,
          run: open(workload.name, workload.namespace, true),
        }))
      case 'namespaces':
        return session.namespaceRows.map((row) => ({
          label: row.name,
          kindId,
          run: open(row.name, '', false),
        }))
      case 'events':
        return session.events.map((event) => ({
          label: event.involvedObject,
          detail: event.namespace,
          kindId,
          run: open(event.involvedObject, event.namespace, true),
        }))
      case 'applications':
        return session.applications.map((application) => ({
          label: application.instance,
          detail: application.namespace,
          kindId,
          run: () => session.openApplication(application),
        }))
      case 'table':
        return (session.table?.rows ?? []).map((row) => ({
          label: row.name,
          detail: row.namespace || undefined,
          kindId,
          run: open(row.name, row.namespace, session.selectedKind?.namespaced ?? true),
        }))
      case 'fleet': {
        // The merged rows — already in memory, because this view is the one
        // on screen and its own poll is what filled them. Nothing is fetched
        // across clusters for a keystroke; a hit opens in its own cluster's
        // tab, through the workspace.
        const openInCluster = this.#openInCluster
        if (!openInCluster) return []
        switch (fleet.tab) {
          case 'pods':
            return fleet.podRows.map((pod) => ({
              label: pod.name,
              detail: `${pod.cluster} · ${pod.namespace}`,
              kindId: RICH_KIND_IDS.pods,
              run: () => openInCluster(fleetRowTarget('pods', pod)),
            }))
          case 'workloads':
            return fleet.workloadRows.map((workload) => ({
              label: workload.name,
              detail: `${workload.cluster} · ${workload.namespace}`,
              kindId: workloadKindId(workload.kind) ?? kindId,
              run: () => openInCluster(fleetRowTarget('workloads', workload)),
            }))
          case 'events':
            return fleet.eventRows.map((event) => ({
              label: event.involvedObject,
              detail: `${event.cluster} · ${event.namespace}`,
              kindId: RICH_KIND_IDS.events,
              run: () => openInCluster(fleetRowTarget('events', event)),
            }))
        }
      }
      default:
        // 'overview' has no rows — it is an assessment, not a list.
        return []
    }
  }

  /** The on-demand kind search's own rows, mapped the same shape the
      in-memory branches produce. */
  #scopedObjects(): ObjectCandidate[] {
    const session = this.#session
    const scoped = this.#scopedKind
    if (!session || !scoped) return []
    return this.#scopedRows.map((row) => ({
      label: row.name,
      detail: row.namespace || undefined,
      kindId: scoped.id,
      run: () => session.openObject(scoped.id, row.name, row.namespace, scoped.namespaced),
    }))
  }

  #recentsGroup(): PaletteResultGroup {
    const session = this.#session
    if (!session) return { name: 'Recents', entries: [] }

    const candidates = session.recentObjects.map((recent, index) => ({
      label: recent.name,
      // Most-recent-first order is preserved as the tie-break for an empty
      // query — see $lib/palette/rank's own recency rule.
      recency: -index,
      keywords: [recent.namespace],
      recent,
    }))

    return {
      name: 'Recents',
      entries: rank(this.parsed.text, candidates)
        .slice(0, GROUP_LIMIT)
        .map(({ recent }) => ({
          id: `recent:${recent.kindId}:${recent.namespace}:${recent.name}`,
          title: recent.name,
          detail: recent.namespace || undefined,
          kindId: recent.kindId,
          group: 'Recents',
          run: () =>
            session.openObject(
              recent.kindId,
              recent.name,
              recent.namespace,
              this.#namespacedFor(recent.kindId),
            ),
        })),
    }
  }

  #namespacedFor(kindId: string): boolean {
    return this.#session?.kinds.find((kind) => kind.id === kindId)?.namespaced ?? true
  }

  /**
   * Points the palette at the live application state.
   *
   * Called from CommandPalette.svelte's own `$effect`, kept in step with
   * $stores/workspace for as long as the palette is MOUNTED, not only at
   * `show()` — a tab switched or closed while the palette is open must
   * stop pointing commands at a session that just went away.
   */
  sync(
    session: ClusterSession | null,
    tabs: { id: string }[],
    focusCluster: (clusterId: string) => void | Promise<void>,
    openInCluster: ((target: FleetTarget) => void | Promise<void>) | null = null,
  ): void {
    this.#session = session
    this.#tabs = tabs
    this.#focusCluster = focusCluster
    this.#openInCluster = openInCluster
  }

  show = (): void => {
    this.open = true
    this.query = ''
    this.selectedIndex = 0
    // The on-demand search's cache and its debounce timer are this palette
    // INSTANCE's, not the application's — a fresh open starts fresh.
    this.#kindSearchCache.clear()
    this.#scopedRows = []
    this.#scopedKind = null
    if (this.#kindSearchTimer) {
      clearTimeout(this.#kindSearchTimer)
      this.#kindSearchTimer = null
    }
  }

  hide = (): void => {
    this.open = false
    if (this.#kindSearchTimer) {
      clearTimeout(this.#kindSearchTimer)
      this.#kindSearchTimer = null
    }
  }

  setQuery = (text: string): void => {
    this.query = text
    this.selectedIndex = 0
    this.#scheduleKindSearch()
  }

  moveSelection = (delta: number): void => {
    const count = this.flatEntries.length
    if (count === 0) {
      this.selectedIndex = 0
      return
    }
    this.selectedIndex = (this.selectedIndex + delta + count) % count
  }

  /** Runs whatever is highlighted, and closes the palette — every command
      here either navigates somewhere else in the application or opens
      another dialog, and leaving the palette open on top of either reads as
      the action not having worked. */
  runSelected = (): void => {
    const entry = this.flatEntries[this.selectedIndex]
    if (!entry) return
    this.hide()
    void entry.run()
  }

  /**
   * Turns the highlighted Kinds suggestion into a `kind:` pill, the way
   * Select.svelte's own Tab-to-accept works — but here Tab COMMITS a
   * scope rather than closing anything, so the palette stays open for the
   * operator to keep typing what they are actually looking for.
   *
   * A no-op once a `kind:` pill is already in the query: Tab accepting a
   * SECOND time would either do nothing useful or silently replace what was
   * already scoped, neither of which a repeated keypress should do.
   */
  acceptKindPill = (): void => {
    if (this.parsed.kind !== undefined) return
    const top = this.groups.find((group) => group.name === 'Kinds')?.entries[0]
    if (!top?.kindId) return
    this.setQuery(`kind:${top.kindId} `)
  }

  /**
   * Schedules — or joins — the palette's one on-demand `ListTable` read.
   *
   * A no-op whenever the scoped kind is unset or is the one already on
   * screen: `#objectsGroup` reads the current view's in-memory rows in
   * both of those cases, and firing a request nothing needs would be
   * exactly the fan-out this store exists to avoid.
   */
  #scheduleKindSearch(): void {
    if (this.#kindSearchTimer) {
      clearTimeout(this.#kindSearchTimer)
      this.#kindSearchTimer = null
    }

    const session = this.#session
    const kindId = this.#effectiveKindId
    if (!session || !kindId || kindId === session.selectedKindId) return

    const cacheKey = `${session.cluster.id}|${kindId}|${session.namespace}`
    const cached = this.#kindSearchCache.get(cacheKey)
    if (cached) {
      void this.#adoptCachedResult(kindId, cached)
      return
    }

    this.#kindSearchTimer = setTimeout(() => {
      this.#kindSearchTimer = null
      void this.#runKindSearch(session, kindId, cacheKey)
    }, KIND_SEARCH_DEBOUNCE_MS)
  }

  async #adoptCachedResult(
    kindId: string,
    cached: ResourceTable | Promise<ResourceTable>,
  ): Promise<void> {
    const table = cached instanceof Promise ? await cached : cached
    // The operator may have moved on to a different scope while this was
    // resolving — a stale table must not overwrite whatever is current.
    if (this.#effectiveKindId !== kindId) return
    this.#scopedKind = { id: kindId, namespaced: table.namespaced }
    this.#scopedRows = table.rows
  }

  async #runKindSearch(session: ClusterSession, kindId: string, cacheKey: string): Promise<void> {
    const request = listTable(session.cluster.id, kindId, session.namespace)
    // Stored BEFORE the await: a second keystroke inside this same debounce
    // window that resolves to the SAME scope joins this read via the
    // `cached` branch above instead of starting a second one — this is what
    // makes "exactly one ListTable call" hold under a burst of typing, not
    // only under a single keystroke.
    this.#kindSearchCache.set(cacheKey, request)

    try {
      const table = await request
      // Replaced with the settled value, so a later re-scope to this same
      // kind within THIS instance reads it back synchronously.
      this.#kindSearchCache.set(cacheKey, table)
      if (this.#effectiveKindId === kindId) {
        this.#scopedKind = { id: kindId, namespaced: table.namespaced }
        this.#scopedRows = table.rows
      }
    } catch {
      // NEVER CACHED. The same rule readcache.go and every other cache in
      // this application holds to: a permission granted a moment after a
      // refusal must not be hidden behind yesterday's failure for the rest
      // of this palette instance's life.
      this.#kindSearchCache.delete(cacheKey)
    }
  }

  async #exportCSV(): Promise<void> {
    const session = this.#session
    const data = activeTable.exportRows?.()
    if (!session || !data) return

    const kind =
      session.selectedKind?.singular ??
      (session.viewMode === 'applications' ? 'application' : session.viewMode)
    const filename = buildExportFilename(session.cluster.id, kind, session.namespace)

    try {
      await saveTextFile(filename, toCSV(data.columns, data.rows))
    } catch (cause) {
      session.error = toApiError(cause)
    }
  }
}

/** The application-wide command palette. A module singleton for the same
    reason `workspace`, `preferences` and `shortcutSheet` are: one desktop
    window, one palette, one truth about what is in it. */
export const palette = new CommandPaletteStore()
