<!--
  One cluster's workspace: navigator on the left, the selected kind's list
  filling the rest, details overlaid on top when a row is selected.

  This component owns the toolbar, because every view underneath shares it.
  Only the table itself swaps. Refresh, the theme toggle and Settings live in
  ClusterTabs instead — they apply no matter which tab is in front, so they
  belong to the bar that does not remount when this component does.
-->
<script lang="ts">
  import DetailDrawer from '$lib/components/DetailDrawer.svelte'
  import SessionOverlay from '$lib/components/SessionOverlay.svelte'
  import CreateResourceDialog from '$lib/components/CreateResourceDialog.svelte'
  import BulkActionBar from '$lib/components/BulkActionBar.svelte'
  import BulkActionDialog from '$lib/components/BulkActionDialog.svelte'
  import { escapeUnclaimed } from '$lib/escape'
  import type { BulkActionId } from '$lib/bulk'
  import NamespacesView from './NamespacesView.svelte'
import TimelineView from './TimelineView.svelte'
  import ApplicationsView from './ApplicationsView.svelte'
  import ErrorBanner from '$lib/components/ErrorBanner.svelte'
  import Navigator from '$lib/components/Navigator.svelte'
  import Pagination from '$lib/components/Pagination.svelte'
  import ColumnMenu from '$lib/components/ColumnMenu.svelte'
  import InfoHint from '$lib/components/InfoHint.svelte'
  import ToolbarButton from '$lib/components/ToolbarButton.svelte'
  import { activeTable } from '$stores/activeTable.svelte'
  import { newResourceDialog } from '$stores/newResourceDialog.svelte'
  import { focusFirstRow } from '$lib/components/DataTable.svelte'
  import SearchField from '$lib/components/SearchField.svelte'
  import { preferences } from '$stores/preferences.svelte'
  import { organisation } from '$stores/organisation.svelte'
  import { formatClockTime } from '$lib/format'
  import { shortcut } from '$lib/shortcuts'
  import { toCSV } from '$lib/csv'
  import { buildExportFilename } from '$lib/exportFilename'
  import { ALL_NAMESPACES, saveTextFile } from '$lib/api/client'
  import { toApiError } from '$lib/api/errors'
  import { flash } from '$lib/flash.svelte'
  import { skeletonFor } from '$lib/manifestTemplates'
  import { iconForKind } from '$lib/kindIcons'
  import type { CustomColumnSpec } from '$lib/customColumns'
  import type { ClusterSession } from '$stores/session.svelte'
  import EventsView from './EventsView.svelte'
  import GenericTableView from './GenericTableView.svelte'
  import OverviewView from './OverviewView.svelte'
  import NodesView from './NodesView.svelte'
  import PodsView from './PodsView.svelte'
  import WorkloadsView from './WorkloadsView.svelte'
  import FleetView from './FleetView.svelte'
  import { fleet } from '$stores/fleet.svelte'
  import { PanelLeft, AlertTriangle, Download, Check, Plus, Laptop } from '@lucide/svelte'
  import { onMount } from 'svelte'
  import { sessionLauncher } from '$stores/sessionLauncher.svelte'
  import type { CodingAgent } from '$lib/localShell'
  import {
    DetectAgents,
    LocalShellSupported,
  } from '$lib/wailsjs/go/wails/TerminalAPI'

  interface Props {
    session: ClusterSession
  }

  let { session }: Props = $props()

  let searchField: { focus: () => void } | undefined = $state()

  /**
   * Whether this platform can open a shell on the operator's own machine, and
   * which coding agents are on their PATH.
   *
   * ASKED ONCE, ON MOUNT. Both answers are about the machine rather than about
   * a cluster: the platform does not change while the window is open, and an
   * agent installed mid-session is rare enough not to be worth a poll on a
   * surface that already polls a cluster. Neither call reaches the network.
   */
  let localShellSupported = $state(false)
  let localShellReason = $state('')
  let codingAgents = $state.raw<CodingAgent[]>([])

  onMount(() => {
    void (async () => {
      try {
        const support = await LocalShellSupported()
        localShellSupported = support.supported
        localShellReason = support.reason
        // Only worth asking where a shell can be opened at all — an agent that
        // cannot be launched is not information, it is a list of regrets.
        if (support.supported) codingAgents = await DetectAgents()
      } catch {
        // A local terminal is a convenience beside a cluster client. Failing
        // to establish whether it is available leaves the control absent
        // rather than taking anything else down with it.
        localShellSupported = false
      }
    })()
  })

  /**
   * The object to name in a coding agent's first prompt.
   *
   * The most recently opened object in this tab, resolved through the
   * catalogue so the agent is told the Kubernetes Kind ("Deployment") rather
   * than the catalogue id. Empty when nothing has been opened, which the
   * prompt handles by simply not claiming an object.
   */
  const agentSubject = $derived.by(() => {
    const recent = session.recentObjects[0]
    if (!recent) return { kind: '', namespace: '', name: '' }
    const kind = session.kinds.find((entry) => entry.id === recent.kindId)?.kind ?? ''
    return { kind, namespace: recent.namespace, name: recent.name }
  })

  /** Opens the local-terminal dialog for the tab in front. */
  function onOpenLocalTerminal(): void {
    sessionLauncher.requestLocal({
      clusterId: session.cluster.id,
      agents: codingAgents,
      subject: agentSubject,
    })
  }

  /** The path an export was just saved to, shown in the button's title while
      `exported.on`. Kept even after the flash expires — the button's title
      falls back to it below being a small kindness, not a promise. */
  let exportedPath = $state('')
  const exported = flash(2000)

  /**
   * An annotation column is a new projection — the list has to be ASKED for
   * the key, see $lib/customColumns — so it is re-read at once rather than
   * on the next poll. A label column, or a removal, reads what every row
   * already carries and needs nothing.
   */
  function onColumnsChanged(spec: CustomColumnSpec, change: 'added' | 'removed'): void {
    if (change === 'added' && spec.source === 'annotation') void session.refresh()
  }

  /**
   * The guardrails for the group this cluster sits in.
   *
   * Read fresh on every access rather than cached on connect — same
   * reasoning as DetailDrawer's own copy of this, which this deliberately
   * matches: an operator can change a group's environment or read-only flag
   * in Organise while this tab is open, and the disabled New button has to
   * follow that immediately.
   */
  const groupPlacement = $derived(organisation.placementOf(session.cluster.id))
  const groupName = $derived(
    organisation
      .groupsIn(groupPlacement.project)
      .find((group) => group.id === groupPlacement.group)?.name ?? 'Default',
  )
  const groupSettings = $derived(
    organisation.settingsFor(groupPlacement.project, groupPlacement.group),
  )
  const isProduction = $derived(groupSettings.environment === 'production')
  const productionGroup = $derived(isProduction ? groupName : null)
  const isReadOnly = $derived(groupSettings.readOnly)
  // Matches DetailDrawer's own wording, and the backend's — see
  // app/adapters/wails/errors.go's CodeReadOnly.
  const readOnlyReason = 'This cluster is marked read-only in PodSteer. Change that under Organise.'

  /**
   * The "New <Singular>" dialog, open on whichever kind is currently
   * selected — closing it and switching kinds never leaves it pointed at
   * the wrong one, since it is only ever opened from the kind it names.
   *
   * Visibility lives in $stores/newResourceDialog, not a local `let` —
   * the command palette's own "New <kind>" command is a sibling of this
   * component, not a descendant of it, and needs to open the same dialog.
   */
  const newDialogOpen = $derived(newResourceDialog.open)
  const created = flash(2000)

  /**
   * The bulk action under review, or null when none is. One dialog serves
   * every verb: which rows it acts on is the session's selection, and what
   * it does to each is the plan it fetches on open.
   */
  let bulkAction = $state<BulkActionId | null>(null)

  /**
   * The skeleton the dialog opens with.
   *
   * Live here, but the dialog itself only reads its `seed` prop at the
   * instant `open` becomes true (the same convention ScaleDialog seeds
   * `replicas` from `currentReplicas` with) — so a namespace filter changing
   * while the dialog is already open rewrites what a NEXT opening would seed,
   * never a draft somebody is midway through editing.
   */
  const newSkeleton = $derived(
    session.selectedKind ? skeletonFor(session.selectedKind, session.namespace) : '',
  )

  /** The namespace the kubectl hint shows a `-n` flag for — the same
      "concrete namespace, and only when this kind carries one" rule
      `skeletonFor` itself applies to `metadata.namespace`, so the hint never
      claims a flag the manifest does not actually need. */
  const newNamespaceHint = $derived(
    session.selectedKind?.namespaced && session.namespace !== ALL_NAMESPACES
      ? session.namespace
      : undefined,
  )

  /**
   * Opens the object just created, the same way clicking a fresh row would.
   *
   * Refresh runs BEFORE openDetail: the drawer's own live sections (a
   * workload's replica figures, a pod's findings) come from the list row
   * rather than the manifest alone — see `session.openDetail` — so opening
   * before the list has seen the new object would show a panel missing
   * everything the row supplies.
   */
  async function onResourceCreated(name: string, namespace: string): Promise<void> {
    created.show()
    await session.refresh()
    if (name) await session.openDetail(name, namespace)
  }

  /**
   * Exports whichever table is on screen as CSV, through the same native
   * save dialog every other file PodSteer writes to a chosen path uses.
   *
   * The rows and columns come from `activeTable.exportRows`: registered by
   * whichever view's DataTable is currently mounted, because only that view
   * knows how its own cells are formatted. This function only turns that
   * into text, names the file, and hands it to the dialog.
   */
  async function onExportCSV(): Promise<void> {
    const data = activeTable.exportRows?.()
    if (!data) return

    // Applications and the overview are not entries in the backend's own
    // kind catalogue (see domain/catalog.go), so selectedKind is undefined
    // for them — the overview never reaches here at all, since it renders no
    // DataTable and registers no export.
    const kind =
      session.selectedKind?.singular ??
      (session.viewMode === 'applications'
        ? 'application'
        : session.viewMode === 'fleet'
          ? fleet.tab
          : session.viewMode)
    // A merged table is every open cluster's, so its file is named for all
    // of them rather than for whichever tab happened to be in front.
    const filename = buildExportFilename(
      session.viewMode === 'fleet' ? 'all-clusters' : session.cluster.id,
      kind,
      session.namespace,
    )

    try {
      const path = await saveTextFile(filename, toCSV(data.columns, data.rows))
      // An empty path means the operator cancelled the dialog, which is not
      // an error and says nothing — the same convention as readKubeconfigFile.
      if (path) {
        exportedPath = path
        exported.show()
      }
    } catch (cause) {
      session.error = toApiError(cause)
    }
  }

  /**
   * Poll while this workspace is mounted, at whatever interval Settings says.
   */
  $effect(() => {
    const current = session
    const interval = preferences.effectiveIntervalMs
    current.startAutoRefresh(interval)
    return () => current.stopAutoRefresh()
  })

  /**
   * Cmd+B / Ctrl+B toggles the navigator.
   * Cmd+R / Ctrl+R refreshes.
   * Cmd+K / Ctrl+K focuses the search field.
   * Escape clears the row selection, when nothing nearer has claimed it.
   *
   * Matched against $lib/shortcuts rather than a literal key check, so this
   * handler and ShortcutSheet.svelte read from one table and cannot drift.
   *
   * Escape is the exception, and it goes through $lib/escape's stack: a
   * dialog, a drawer or a menu that is open owns the keystroke, so the
   * selection clears only once there is nothing above the list — one Escape
   * per layer, innermost first. A text field's own Escape (the search box
   * blurs on it) is left alone too.
   */
  function onKeydown(event: KeyboardEvent): void {
    if (event.key === 'Escape') {
      if (!escapeUnclaimed() || session.selection.count === 0) return
      const target = event.target as HTMLElement | null
      if (target?.closest('input, textarea, select, [contenteditable="true"]')) return
      session.selection.clear()
      return
    }

    if (shortcut('toggle-navigator').matches(event)) {
      event.preventDefault()
      preferences.toggleNavigator()
    } else if (shortcut('refresh').matches(event)) {
      event.preventDefault()
      void session.refresh()
    } else if (shortcut('focus-search').matches(event)) {
      event.preventDefault()
      searchField?.focus()
    }
  }
</script>

<svelte:window onkeydown={onKeydown} />

<div class="flex min-h-0 flex-1">
  {#if !preferences.navigatorCollapsed}
    <Navigator {session} />
  {/if}

  <section class="flex min-h-0 min-w-0 flex-1 flex-col bg-surface">
    <!-- Toolbar: fixed to the same height as the navigator's namespace bar,
         and bordered the same way, so the two form one continuous line. -->
    <div class="flex h-14 shrink-0 items-center gap-3 border-b border-outline-variant/60 px-4">
      <!-- Sidebar toggle -->
      <button
        type="button"
        onclick={preferences.toggleNavigator}
        aria-label={preferences.navigatorCollapsed ? 'Show sidebar' : 'Hide sidebar'}
        aria-pressed={!preferences.navigatorCollapsed}
        title="{preferences.navigatorCollapsed ? 'Show' : 'Hide'} sidebar  {shortcut(
          'toggle-navigator',
        ).keys}"
        class="state-layer grid size-8 shrink-0 place-items-center rounded-full
               text-on-surface-variant transition-colors duration-100 hover:bg-surface-container hover:text-on-surface"
      >
        <PanelLeft class="size-[18px]" strokeWidth={1.8} />
      </button>

      <!-- Resource title and count -->
      <div class="min-w-0 shrink">
        <div class="flex items-baseline gap-2">
          <h2 class="truncate text-title-medium font-semibold text-on-surface">
            {session.viewMode === 'fleet'
              ? 'All clusters'
              : session.viewMode === 'timeline'
                ? 'Timeline'
                : session.isList
                  ? (session.selectedKind?.title ?? 'Resources')
                  : session.cluster.id}
          </h2>
          {#if session.isList}
            <span class="rounded-full bg-surface-container-high px-2 py-0.5 text-label-small
                         tabular-nums text-on-surface-variant">
              {session.visibleCount}
            </span>
          {:else if session.lastRefreshedAt}
            <span class="text-body-small text-on-surface-variant/60">
              assessed {formatClockTime(session.lastRefreshedAt)}
            </span>
          {/if}
          {#if session.viewMode === 'pods' && session.podSummary.unhealthy > 0}
            <span class="flex items-center gap-1 rounded-full bg-warning-container px-2 py-0.5
                         text-label-small text-on-warning-container">
              <AlertTriangle class="size-3" strokeWidth={2} />
              {session.podSummary.unhealthy} unhealthy
            </span>
          {/if}
        </div>
      </div>

      {#if session.isList}
        <!-- Search. Bound to the TYPED text rather than the debounced term:
             the field has to keep up with the keyboard even though the table
             follows a beat behind it. -->
        <SearchField
          bind:this={searchField}
          value={session.typedSearch}
          placeholder="Search {session.viewMode === 'fleet'
            ? 'all clusters'
            : (session.selectedKind?.title.toLowerCase() ?? 'resources')}…"
          onchange={session.setSearch}
          onnext={focusFirstRow}
          invalid={Boolean(session.searchError)}
          description={session.searchError ?? session.searchDescription}
          class="min-w-40 flex-1"
        />

        <!-- The lightest affordance for a grammar nobody is expected to
             already know: an icon that says nothing until asked, next to a
             field that otherwise looks like the plain-substring box it
             always was. -->
        <InfoHint
          label="Search syntax"
          text={'-term negates. re:pattern or /pattern/ is a regex. key=value, key!=value ' +
            'and label:key select on labels. cluster:name selects a cluster. ' +
            '"quoted phrases" keep spaces in one term.'}
        />

        <div class="h-5 w-px shrink-0 bg-outline-variant/60" aria-hidden="true"></div>

        <!-- Pagination: consolidated here rather than under the table, so
             every per-view control lives in one row. -->
        <Pagination
          totalCount={session.visibleCount}
          pageStart={session.pageStart}
          currentPage={session.currentPage}
          pageCount={session.pageCount}
          onpage={session.goToPage}
        />

        <!-- Create, for every kind the navigator can select — including a
             custom resource and anything served by the generic table, since
             both go through the same `updateResource` apply path as an edit.
             Gated on `selectedKind` rather than on `activeTable.present`
             (Export CSV's own gate): a table registers its export only once
             mounted, but creating an object needs nothing from the table
             that is about to show it. Applications and the overview have no
             `selectedKind` — they are pinned ids, not catalog entries — so
             neither offers this; Namespaces IS a catalog entry and does. -->
        {#if session.selectedKind}
          <div class="h-5 w-px shrink-0 bg-outline-variant/60" aria-hidden="true"></div>

          <ToolbarButton
            icon={created.on ? Check : Plus}
            label="New {session.selectedKind.singular}"
            title={isReadOnly ? readOnlyReason : created.on ? 'Created' : 'New ' + session.selectedKind.singular}
            active={created.on}
            disabled={isReadOnly}
            onclick={newResourceDialog.show}
          />
        {/if}

        <!-- The column chooser, moved out of the table header's trailing cell.
             There it scrolled away with a wide table, which is the one case it
             is wanted in. Behind its own divider because it is not part of the
             pager: everything left of the rule moves you through the rows,
             this changes what a row shows.

             Conditional on a table having registered, so it does not appear
             over the overview — which is an assessment, not a list, and has no
             columns to choose between. -->
        {#if activeTable.present}
          <div class="h-5 w-px shrink-0 bg-outline-variant/60" aria-hidden="true"></div>

          <ColumnMenu
            kindId={activeTable.kindId}
            columns={activeTable.columns}
            keys={session.metadataKeysOnScreen}
            onchange={onColumnsChanged}
          />

          <ToolbarButton
            icon={exported.on ? Check : Download}
            label="Export CSV"
            title={session.visibleCount === 0
              ? 'No rows to export'
              : exported.on
                ? `Saved to ${exportedPath}`
                : 'Export CSV'}
            active={exported.on}
            disabled={session.visibleCount === 0}
            onclick={onExportCSV}
          />
        {/if}
      {/if}

      <!-- A shell on THIS machine, not in the cluster.
           OUTSIDE the list-only controls above, and last in the row: it is
           scoped to the cluster TAB rather than to whatever kind is selected —
           KUBECONFIG and the tab's context are the whole of what PodSteer
           contributes to it — so it belongs on the overview and the
           all-clusters view exactly as much as on a table.

           Absent where the platform cannot open one, with the reason in the
           title rather than a control that does nothing. -->
      {#if localShellSupported}
        <div class="ms-auto h-5 w-px shrink-0 bg-outline-variant/60" aria-hidden="true"></div>

        <ToolbarButton
          icon={Laptop}
          label="Local terminal"
          title="Open a shell on this machine, with KUBECONFIG set for this cluster"
          onclick={onOpenLocalTerminal}
        />
      {:else if localShellReason}
        <div class="ms-auto h-5 w-px shrink-0 bg-outline-variant/60" aria-hidden="true"></div>

        <ToolbarButton
          icon={Laptop}
          label="Local terminal"
          title={localShellReason}
          disabled
          onclick={() => {}}
        />
      {/if}
    </div>

    {#if session.error}
      <div class="px-4 pt-2">
        <ErrorBanner
          error={session.error}
          onretry={session.refresh}
          ondismiss={() => (session.error = null)}
        />
      </div>
    {/if}

    <!-- The view for the selected kind -->
    {#if session.viewMode === 'overview'}
      <OverviewView {session} />
    {:else if session.viewMode === 'fleet'}
      <FleetView {session} />
    {:else if session.viewMode === 'timeline'}
      <TimelineView {session} />
    {:else if session.viewMode === 'pods'}
      <PodsView {session} />
    {:else if session.viewMode === 'nodes'}
      <NodesView {session} />
    {:else if session.viewMode === 'events'}
      <EventsView {session} />
    {:else if session.viewMode === 'namespaces'}
      <NamespacesView {session} />
    {:else if session.viewMode === 'applications'}
      <ApplicationsView {session} />
    {:else if session.viewMode === 'workloads'}
      <WorkloadsView {session} />
    {:else}
      <GenericTableView {session} />
    {/if}
  </section>
</div>

<!-- Over the list, while rows are ticked. The session's selection is
     cleared on every kind or namespace change, so the bar can never name a
     count from a list that is no longer on screen. -->
{#if session.isList}
  <BulkActionBar {session} {isReadOnly} {readOnlyReason} onaction={(action) => (bulkAction = action)} />
{/if}

<BulkActionDialog
  open={bulkAction !== null}
  action={bulkAction ?? 'delete'}
  {session}
  {productionGroup}
  onclose={() => (bulkAction = null)}
/>

<DetailDrawer {session} />

<!-- The debug-container and node-shell dialogs and their terminals, mounted
     once here so they can be launched from the drawer or the node row menu and
     outlive whichever surface launched them. -->
<SessionOverlay />

{#if session.selectedKind}
  <CreateResourceDialog
    open={newDialogOpen}
    icon={iconForKind(session.selectedKind)}
    kindLabel={session.selectedKind.singular}
    verb="New"
    seed={newSkeleton}
    clusterId={session.cluster.id}
    namespace={newNamespaceHint}
    {productionGroup}
    {isReadOnly}
    {readOnlyReason}
    onclose={newResourceDialog.hide}
    oncreated={onResourceCreated}
  />
{/if}
