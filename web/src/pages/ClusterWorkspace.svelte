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
  import ErrorBanner from '$lib/components/ErrorBanner.svelte'
  import Navigator from '$lib/components/Navigator.svelte'
  import Pagination from '$lib/components/Pagination.svelte'
  import { focusFirstRow } from '$lib/components/DataTable.svelte'
  import SearchField from '$lib/components/SearchField.svelte'
  import { preferences } from '$stores/preferences.svelte'
  import { formatClockTime } from '$lib/format'
  import type { ClusterSession } from '$stores/session.svelte'
  import EventsView from './EventsView.svelte'
  import GenericTableView from './GenericTableView.svelte'
  import OverviewView from './OverviewView.svelte'
  import NodesView from './NodesView.svelte'
  import PodsView from './PodsView.svelte'
  import WorkloadsView from './WorkloadsView.svelte'
  import { PanelLeft, AlertTriangle } from '@lucide/svelte'

  interface Props {
    session: ClusterSession
  }

  let { session }: Props = $props()

  let searchField: { focus: () => void } | undefined = $state()

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
   */
  function onKeydown(event: KeyboardEvent): void {
    const accel = event.metaKey || event.ctrlKey
    if (!accel) return

    const key = event.key.toLowerCase()
    if (key === 'b') {
      event.preventDefault()
      preferences.toggleNavigator()
    } else if (key === 'r') {
      event.preventDefault()
      void session.refresh()
    } else if (key === 'k') {
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
        title="{preferences.navigatorCollapsed ? 'Show' : 'Hide'} sidebar  ⌘B"
        class="state-layer grid size-8 shrink-0 place-items-center rounded-full
               text-on-surface-variant transition-colors duration-100 hover:bg-surface-container hover:text-on-surface"
      >
        <PanelLeft class="size-[18px]" strokeWidth={1.8} />
      </button>

      <!-- Resource title and count -->
      <div class="min-w-0 shrink">
        <div class="flex items-baseline gap-2">
          <h2 class="truncate text-title-medium font-semibold text-on-surface">
            {session.isList ? (session.selectedKind?.title ?? 'Resources') : session.cluster.id}
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
        <!-- Search -->
        <SearchField
          bind:this={searchField}
          value={session.search}
          placeholder="Search {session.selectedKind?.title.toLowerCase() ?? 'resources'}…"
          onchange={session.setSearch}
          onnext={focusFirstRow}
          class="min-w-40 flex-1"
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
    {:else if session.viewMode === 'pods'}
      <PodsView {session} />
    {:else if session.viewMode === 'nodes'}
      <NodesView {session} />
    {:else if session.viewMode === 'events'}
      <EventsView {session} />
    {:else if session.viewMode === 'workloads'}
      <WorkloadsView {session} />
    {:else}
      <GenericTableView {session} />
    {/if}
  </section>
</div>

<DetailDrawer {session} />
