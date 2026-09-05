<!--
  The merged tables: every open cluster's pods, workloads or events in one
  list, with a column saying which cluster each row came from.

  Nothing is decided here. Each row is the same DTO that cluster's own list
  renders, so the status word, the ready count and the meters are Go's — and
  so is the verdict on a cluster that did not answer (app/domain/fleet.go),
  which the strip above the table repeats without paraphrase. This component
  draws the strip, the chips and the three tables, and hands a click to the
  workspace, which knows how to open an object in another tab.
-->
<script lang="ts">
  import DataTable, { type Column } from '$lib/components/DataTable.svelte'
  import type { CSVExport } from '$stores/activeTable.svelte'
  import MeterBar from '$lib/components/MeterBar.svelte'
  import StatusIndicator from '$lib/components/StatusIndicator.svelte'
  import EmptyState from '$lib/components/EmptyState.svelte'
  import { formatAge, podStatusLabel, podTone, type Tone } from '$lib/format'
  import { preferences } from '$stores/preferences.svelte'
  import { cpuMeter, cpuTitle, memoryMeter, memoryTitle } from '$lib/meter'
  import { POD_STATUS_CHIPS } from '$lib/podStatusFilters'
  import {
    EVENT_CHIPS,
    FLEET_TABS,
    WORKLOAD_CHIPS,
    fleetRowTarget,
    hasClusterTerm,
    toggleClusterTerm,
    type FleetChip,
    type FleetRow,
    type FleetTab,
  } from '$lib/fleet'
  import { iconForKind } from '$lib/kindIcons'
  import { fleet } from '$stores/fleet.svelte'
  import { workspace } from '$stores/workspace.svelte'
  import { fleetTableId, type ClusterSession } from '$stores/session.svelte'
  import type { K8sEvent, Pod, Workload } from '$lib/api/client'
  import { Activity, Box, CircleDot, Server, TriangleAlert } from '@lucide/svelte'

  interface Props {
    session: ClusterSession
  }

  let { session }: Props = $props()

  /** The same denominator the pod list is set to. */
  const byLimit = $derived(preferences.podMeasure === 'limits')

  /** The table showing, for column preferences and the sort — see fleetTableId. */
  const tableId = $derived(fleetTableId(fleet.tab))

  const openCount = $derived(workspace.sessions.length)

  /** The first read has not landed: there is no strip to draw yet. */
  const reading = $derived(fleet.status === 'loading' && fleet.strip.length === 0)

  // The Cluster column sits between the status mark and the name, before
  // anything else, because it is the one column that did not exist on the
  // single-cluster list and the one question every row of this table has
  // to answer first.
  const POD_COLUMNS: Column[] = [
    { id: 'status', label: 'Status', width: 44, icon: CircleDot },
    { id: 'cluster', label: 'Cluster', width: 190 },
    { id: 'name', label: 'Name', width: 300, pinned: true },
    { id: 'namespace', label: 'Namespace', width: 150 },
    { id: 'cpu', label: 'CPU', width: 220, minWidth: 200 },
    { id: 'memory', label: 'Memory', width: 220, minWidth: 200 },
    { id: 'ready', label: 'Ready', width: 80, numeric: true },
    { id: 'restarts', label: 'Restarts', width: 90, numeric: true },
    { id: 'controlledBy', label: 'Controlled By', width: 200, defaultHidden: true },
    { id: 'node', label: 'Node', width: 180 },
    { id: 'age', label: 'Age', width: 80, numeric: true },
  ]

  // No usage meters. A controller's figures are the sum over its pods and
  // cost the namespace's pods and metrics per cluster on every tick — the
  // single-cluster list pays that for one cluster; paying it for every open
  // one would turn the one-request-per-cluster-per-kind rule into a storm.
  const WORKLOAD_COLUMNS: Column[] = [
    { id: 'status', label: 'Status', width: 44, icon: CircleDot },
    { id: 'cluster', label: 'Cluster', width: 190 },
    { id: 'kind', label: 'Kind', width: 130 },
    { id: 'name', label: 'Name', width: 300, pinned: true },
    { id: 'namespace', label: 'Namespace', width: 150 },
    { id: 'ready', label: 'Ready', width: 90, numeric: true },
    { id: 'images', label: 'Images', width: 300 },
    { id: 'age', label: 'Age', width: 80, numeric: true },
  ]

  const EVENT_COLUMNS: Column[] = [
    { id: 'type', label: 'Type', width: 44, icon: CircleDot },
    { id: 'cluster', label: 'Cluster', width: 190 },
    { id: 'reason', label: 'Reason', width: 200, pinned: true },
    { id: 'object', label: 'Object', width: 300 },
    { id: 'namespace', label: 'Namespace', width: 160 },
    { id: 'message', label: 'Message', width: 520 },
    { id: 'count', label: 'Count', width: 96, numeric: true },
    { id: 'age', label: 'Last seen', width: 116, numeric: true },
  ]

  /**
   * How many of the search-filtered rows each chip would add, in one pass —
   * counted against the searched rows rather than the visible ones, for the
   * reason PodsView gives: an unselected chip must show what selecting it
   * would add, not a count its own absence has already shrunk.
   */
  function countChips<T>(rows: readonly T[], chips: readonly FleetChip<T>[]): Record<string, number> {
    const counts: Record<string, number> = {}
    for (const chip of chips) counts[chip.id] = 0
    for (const row of rows) {
      for (const chip of chips) {
        if (chip.predicate(row)) counts[chip.id]++
      }
    }
    return counts
  }

  const podChipCounts = $derived(countChips(session.searchedFleetPods, POD_STATUS_CHIPS))
  const workloadChipCounts = $derived(countChips(session.searchedFleetWorkloads, WORKLOAD_CHIPS))
  const eventChipCounts = $derived(countChips(session.searchedFleetEvents, EVENT_CHIPS))

  /** The row's cluster becomes the tab in front; the row becomes the drawer. */
  function open(row: FleetRow<Pod> | FleetRow<Workload> | FleetRow<K8sEvent>): void {
    void workspace.openInCluster(fleetRowTarget(fleet.tab, row))
  }

  /** A strip chip narrows the table to its cluster through the search box,
      so the filter is visible, editable and removable like any other term. */
  function toggleCluster(cluster: string): void {
    session.setSearch(toggleClusterTerm(session.typedSearch, cluster))
  }

  /** The same colour rule WorkloadsView draws with. */
  function workloadTone(workload: Workload): Tone {
    if (workload.suspended) return 'neutral'
    if (!workload.isHealthy) return workload.readyCount === 0 ? 'error' : 'warning'
    if (workload.isRolling) return 'info'
    return 'success'
  }

  /** The findings worth marking a row for — severity is Go's judgement; this
      only decides which of them earns a glyph. Same rule as PodsView. */
  function alarming(pod: Pod) {
    return (pod.findings ?? []).filter((finding) => finding.severity !== 'info')
  }

  /** Same rule ColumnMenu and DataTable apply, keyed by the table showing. */
  function isColumnVisible(column: Column): boolean {
    const stored = preferences.columns[tableId]?.[column.id]?.hidden
    return column.pinned || (stored === undefined ? !column.defaultHidden : !stored)
  }

  /** Why the table is empty, in words that say which of the three reasons. */
  function emptyDescription(noun: string): string {
    if (session.search) return `Nothing matches "${session.search}".`
    if (fleet.degraded > 0) return 'Some clusters did not answer — the strip above says which, and why.'
    const clusters = `${openCount} open cluster${openCount === 1 ? '' : 's'}`
    return `No ${noun} in this namespace across your ${clusters}.`
  }

  /**
   * The merged table's CSV export: the same text each cell shows, with the
   * cluster as a column like any other — a spreadsheet of every cluster's
   * pods with no column saying whose is the wrong file to hand somebody.
   */
  function exportCSV(): CSVExport {
    switch (fleet.tab) {
      case 'pods': {
        const visible = POD_COLUMNS.filter(isColumnVisible)
        const cell = (pod: FleetRow<Pod>, id: string): string => {
          switch (id) {
            case 'status':
              return podStatusLabel(pod)
            case 'cluster':
              return pod.cluster
            case 'name':
              return pod.name
            case 'namespace':
              return pod.namespace
            case 'cpu':
              return pod.cpu
            case 'memory':
              return pod.memory
            case 'ready':
              return pod.ready
            case 'restarts':
              return String(pod.restarts)
            case 'controlledBy':
              return pod.controlledBy || '—'
            case 'node':
              return pod.nodeName || '—'
            case 'age':
              return formatAge(pod.ageSeconds)
            default:
              return ''
          }
        }
        return {
          columns: visible.map((column) => column.label),
          rows: session.sortedFleetPods.map((pod) => visible.map((column) => cell(pod, column.id))),
        }
      }
      case 'workloads': {
        const visible = WORKLOAD_COLUMNS.filter(isColumnVisible)
        const cell = (workload: FleetRow<Workload>, id: string): string => {
          switch (id) {
            case 'status':
              return workload.status
            case 'cluster':
              return workload.cluster
            case 'kind':
              return workload.kind
            case 'name':
              return workload.name
            case 'namespace':
              return workload.namespace
            case 'ready':
              return workload.ready
            case 'images':
              return (workload.images ?? []).join(', ')
            case 'age':
              return formatAge(workload.ageSeconds)
            default:
              return ''
          }
        }
        return {
          columns: visible.map((column) => column.label),
          rows: session.sortedFleetWorkloads.map((workload) =>
            visible.map((column) => cell(workload, column.id)),
          ),
        }
      }
      case 'events': {
        const visible = EVENT_COLUMNS.filter(isColumnVisible)
        const cell = (event: FleetRow<K8sEvent>, id: string): string => {
          switch (id) {
            case 'type':
              return event.type
            case 'cluster':
              return event.cluster
            case 'reason':
              return event.reason
            case 'object':
              return event.involvedObject
            case 'namespace':
              return event.namespace
            case 'message':
              return event.message
            case 'count':
              return String(event.count)
            case 'age':
              return formatAge(event.ageSeconds)
            default:
              return ''
          }
        }
        return {
          columns: visible.map((column) => column.label),
          rows: session.sortedFleetEvents.map((event) =>
            visible.map((column) => cell(event, column.id)),
          ),
        }
      }
    }
  }
</script>

<!-- One chip per quick filter of the table showing. The pressed colours are
     ToolbarToggle's, as on PodsView, so a chip on and a toolbar icon on read
     as the same state. -->
{#snippet chipRow(
  tab: FleetTab,
  chips: readonly { id: string; label: string }[],
  counts: Record<string, number>,
  noun: string,
)}
  {#each chips as chip (chip.id)}
    {@const pressed = session.fleetChips[tab].includes(chip.id)}
    {@const count = counts[chip.id]}
    <button
      type="button"
      onclick={() => session.toggleFleetChip(tab, chip.id)}
      aria-pressed={pressed}
      title="{pressed ? 'Showing only' : 'Show only'} {chip.label.toLowerCase()} {noun}"
      class="rounded-full border px-2.5 py-1 text-label-small transition-colors duration-100
             {pressed
               ? 'border-primary/40 bg-primary/14 text-primary'
               : 'border-outline-variant/50 text-on-surface-variant hover:bg-surface-container hover:text-on-surface'}"
    >
      {chip.label}
      {#if count > 0}
        <span class="tabular-nums {pressed ? 'text-primary/70' : 'text-on-surface-variant/60'}">
          {count}
        </span>
      {/if}
    </button>
  {/each}
{/snippet}

<div class="flex min-h-0 flex-1 flex-col">
  <!--
    Which table, then which clusters. The strip is the feature's contract
    made visible: one chip per open cluster, in tab order, carrying Go's
    verdict on that cluster's read — read, partial, slow, forbidden,
    unreachable, failed — and, in its tooltip, the reason in the backend's
    own words and how old the rows shown are. A cluster that did not answer
    is a chip here, never an empty table or a spinner over the others.
  -->
  <div
    class="flex flex-wrap items-center gap-2 border-b border-outline-variant/40
           bg-surface-container-low/40 px-4 py-2"
  >
    <div
      role="tablist"
      aria-label="Merged table"
      class="flex shrink-0 items-center gap-0.5 rounded-full bg-surface-container p-0.5"
    >
      {#each FLEET_TABS as tab (tab.id)}
        {@const active = fleet.tab === tab.id}
        <button
          type="button"
          role="tab"
          aria-selected={active}
          onclick={() => void session.selectFleetTab(tab.id)}
          class="rounded-full px-3 py-1 text-label-medium transition-colors duration-100
                 {active
                   ? 'bg-primary/14 text-primary'
                   : 'text-on-surface-variant hover:bg-surface-container-high hover:text-on-surface'}"
        >
          {tab.label}
        </button>
      {/each}
    </div>

    <div class="h-5 w-px shrink-0 bg-outline-variant/60" aria-hidden="true"></div>

    {#if fleet.strip.length === 0}
      <span class="text-body-small text-on-surface-variant/70">
        {reading
          ? `Reading ${openCount} cluster${openCount === 1 ? '' : 's'}…`
          : 'No clusters read yet'}
      </span>
    {/if}

    {#each fleet.strip as entry (entry.cluster)}
      {@const pressed = hasClusterTerm(session.typedSearch, entry.cluster)}
      <button
        type="button"
        onclick={() => toggleCluster(entry.cluster)}
        aria-pressed={pressed}
        title={entry.title}
        class="flex max-w-72 items-center gap-1.5 rounded-full border px-2.5 py-1 text-label-small
               transition-colors duration-100
               {pressed
                 ? 'border-primary/40 bg-primary/14 text-primary'
                 : 'border-outline-variant/50 text-on-surface-variant hover:bg-surface-container hover:text-on-surface'}"
      >
        <StatusIndicator
          tone={entry.tone}
          label={entry.label}
          icon={Server}
          pulse={entry.status === 'slow'}
        />
        <span class="truncate">{entry.cluster}</span>
        <!-- The count when there are rows to count; otherwise the verdict,
             because "0" under a forbidden cluster reads as "no pods". -->
        {#if entry.rows > 0 || entry.status === 'ok'}
          <span
            class="tabular-nums {pressed ? 'text-primary/70' : 'text-on-surface-variant/60'}"
          >
            {entry.rows}{entry.stale ? '*' : ''}
          </span>
        {:else}
          <span class="lowercase {pressed ? 'text-primary/70' : 'text-on-surface-variant/60'}">
            {entry.label}
          </span>
        {/if}
      </button>
    {/each}
  </div>

  <!-- Quick filters, per table. Each SELECTS on a field Go already put on
       the row — see $lib/podStatusFilters and $lib/fleet — never a new
       comparison made here. -->
  <div
    class="flex flex-wrap items-center gap-1.5 border-b border-outline-variant/40
           bg-surface-container-low/40 px-4 py-2"
  >
    {#if fleet.tab === 'pods'}
      {@render chipRow('pods', POD_STATUS_CHIPS, podChipCounts, 'pods')}
    {:else if fleet.tab === 'workloads'}
      {@render chipRow('workloads', WORKLOAD_CHIPS, workloadChipCounts, 'workloads')}
    {:else}
      {@render chipRow('events', EVENT_CHIPS, eventChipCounts, 'events')}
    {/if}
  </div>

  {#if fleet.tab === 'pods'}
    <DataTable
      kindId={tableId}
      columns={POD_COLUMNS}
      isEmpty={session.pagedFleetPods.length === 0}
      sort={session.sort}
      onsort={session.toggleSort}
      exportRows={exportCSV}
    >
      {#snippet empty()}
        <EmptyState
          title={reading ? 'Reading clusters…' : 'No pods across your clusters'}
          description={reading ? undefined : emptyDescription('pods')}
        />
      {/snippet}

      {#snippet rows(isVisible)}
        {#each session.pagedFleetPods as pod (pod.cluster + '/' + pod.namespace + '/' + pod.name)}
          <tr
            class="group/row cursor-pointer border-t border-outline-variant/25 transition-colors duration-75
                   hover:bg-surface-container-low"
            onclick={() => open(pod)}
          >
            {#if isVisible('status')}
              <td class="overflow-hidden py-1.5 pr-3 pl-5">
                <StatusIndicator
                  tone={podTone(pod)}
                  label={podStatusLabel(pod)}
                  icon={Box}
                  pulse={pod.phase === 'Terminating'}
                />
              </td>
            {/if}
            {#if isVisible('cluster')}
              <td class="truncate px-3 py-1.5 text-on-surface-variant" title={pod.cluster}>
                {pod.cluster}
              </td>
            {/if}
            <td class="px-3 py-1.5" title={pod.name}>
              <span class="flex items-center gap-2">
                <span class="truncate font-medium text-on-surface">{pod.name}</span>
                {#if alarming(pod).length > 0}
                  <TriangleAlert
                    class="size-3.5 shrink-0 text-gauge-warn"
                    strokeWidth={2.2}
                    aria-label="{alarming(pod).length} findings"
                  />
                {/if}
              </span>
            </td>
            {#if isVisible('namespace')}
              <td class="truncate px-3 py-1.5">
                <span class="rounded bg-surface-container-high px-1.5 py-0.5 text-body-small text-on-surface-variant">
                  {pod.namespace}
                </span>
              </td>
            {/if}
            {#if isVisible('cpu')}
              {@const cpu = cpuMeter(pod, byLimit)}
              <td class="overflow-hidden px-3 py-1.5 text-on-surface-variant">
                <MeterBar
                  label={pod.cpu}
                  scope="pods"
                  name="CPU"
                  valueWidth="7ch"
                  percent={cpu.percent}
                  measured={pod.hasMetrics}
                  thresholds={cpu.thresholds}
                  absent={cpu.absent}
                  severity={cpu.severity}
                  title={cpuTitle(pod)}
                />
              </td>
            {/if}
            {#if isVisible('memory')}
              {@const memory = memoryMeter(pod, byLimit)}
              <td class="overflow-hidden px-3 py-1.5 text-on-surface-variant">
                <MeterBar
                  label={pod.memory}
                  scope="pods"
                  name="Memory"
                  percent={memory.percent}
                  measured={pod.hasMetrics}
                  thresholds={memory.thresholds}
                  absent={memory.absent}
                  severity={memory.severity}
                  title={memoryTitle(pod)}
                />
              </td>
            {/if}
            {#if isVisible('ready')}
              <td
                class="truncate px-3 py-1.5 text-right tabular-nums
                       {pod.readyContainers === pod.totalContainers
                         ? 'text-on-surface-variant'
                         : 'text-warning font-medium'}"
              >
                {pod.ready}
              </td>
            {/if}
            {#if isVisible('restarts')}
              <td
                class="truncate px-3 py-1.5 text-right tabular-nums
                       {pod.restarts > 0 ? 'text-warning font-medium' : 'text-on-surface-variant'}"
              >
                {pod.restarts}
              </td>
            {/if}
            {#if isVisible('controlledBy')}
              <td class="truncate px-3 py-1.5 text-on-surface-variant" title={pod.controlledBy}>
                {pod.controlledBy || '—'}
              </td>
            {/if}
            {#if isVisible('node')}
              <td class="truncate px-3 py-1.5 text-on-surface-variant" title={pod.nodeName}>
                {pod.nodeName || '—'}
              </td>
            {/if}
            {#if isVisible('age')}
              <td class="truncate px-3 py-1.5 text-right tabular-nums text-on-surface-variant">
                {formatAge(pod.ageSeconds)}
              </td>
            {/if}
          </tr>
        {/each}
      {/snippet}
    </DataTable>
  {:else if fleet.tab === 'workloads'}
    <DataTable
      kindId={tableId}
      columns={WORKLOAD_COLUMNS}
      isEmpty={session.pagedFleetWorkloads.length === 0}
      sort={session.sort}
      onsort={session.toggleSort}
      exportRows={exportCSV}
    >
      {#snippet empty()}
        <EmptyState
          title={reading ? 'Reading clusters…' : 'No workloads across your clusters'}
          description={reading ? undefined : emptyDescription('workloads')}
        />
      {/snippet}

      {#snippet rows(isVisible)}
        {#each session.pagedFleetWorkloads as workload (workload.cluster + '/' + workload.kind + '/' + workload.namespace + '/' + workload.name)}
          {@const KindIcon = iconForKind({ kind: workload.kind })}
          <tr
            class="group/row cursor-pointer border-t border-outline-variant/25 transition-colors duration-75
                   hover:bg-surface-container-low"
            onclick={() => open(workload)}
          >
            {#if isVisible('status')}
              <td class="overflow-hidden py-1.5 pr-3 pl-5">
                <StatusIndicator
                  tone={workloadTone(workload)}
                  label={workload.status}
                  icon={KindIcon}
                  pulse={workload.isRolling}
                />
              </td>
            {/if}
            {#if isVisible('cluster')}
              <td class="truncate px-3 py-1.5 text-on-surface-variant" title={workload.cluster}>
                {workload.cluster}
              </td>
            {/if}
            {#if isVisible('kind')}
              <td class="truncate px-3 py-1.5 text-on-surface-variant">{workload.kind}</td>
            {/if}
            <td class="px-3 py-1.5" title={workload.name}>
              <span class="truncate font-medium text-on-surface">{workload.name}</span>
            </td>
            {#if isVisible('namespace')}
              <td class="truncate px-3 py-1.5">
                <span class="rounded bg-surface-container-high px-1.5 py-0.5 text-body-small text-on-surface-variant">
                  {workload.namespace}
                </span>
              </td>
            {/if}
            {#if isVisible('ready')}
              <td
                class="truncate px-3 py-1.5 text-right tabular-nums
                       {workload.isHealthy ? 'text-on-surface-variant' : 'text-warning font-medium'}"
              >
                {workload.ready}
              </td>
            {/if}
            {#if isVisible('images')}
              <td class="truncate px-3 py-1.5 text-on-surface-variant" title={(workload.images ?? []).join('\n')}>
                {(workload.images ?? []).join(', ')}
              </td>
            {/if}
            {#if isVisible('age')}
              <td class="truncate px-3 py-1.5 text-right tabular-nums text-on-surface-variant">
                {formatAge(workload.ageSeconds)}
              </td>
            {/if}
          </tr>
        {/each}
      {/snippet}
    </DataTable>
  {:else}
    <DataTable
      kindId={tableId}
      columns={EVENT_COLUMNS}
      isEmpty={session.pagedFleetEvents.length === 0}
      sort={session.sort}
      onsort={session.toggleSort}
      exportRows={exportCSV}
    >
      {#snippet empty()}
        <EmptyState
          title={reading ? 'Reading clusters…' : 'No events across your clusters'}
          description={reading ? undefined : emptyDescription('events')}
        />
      {/snippet}

      {#snippet rows(isVisible)}
        {#each session.pagedFleetEvents as event (event.cluster + '/' + event.namespace + '/' + event.name)}
          <tr
            class="group/row cursor-pointer border-t border-outline-variant/25 transition-colors duration-75
                   hover:bg-surface-container-low"
            onclick={() => open(event)}
          >
            {#if isVisible('type')}
              <td class="overflow-hidden py-1.5 pr-3 pl-5">
                <StatusIndicator
                  tone={event.isWarning ? 'warning' : 'neutral'}
                  label={event.type}
                  icon={Activity}
                />
              </td>
            {/if}
            {#if isVisible('cluster')}
              <td class="truncate px-3 py-1.5 text-on-surface-variant" title={event.cluster}>
                {event.cluster}
              </td>
            {/if}
            <td class="px-3 py-1.5" title={event.reason}>
              <span class="truncate font-medium text-on-surface">{event.reason}</span>
            </td>
            {#if isVisible('object')}
              <td class="truncate px-3 py-1.5 text-on-surface-variant" title={event.involvedObject}>
                {event.involvedObject}
              </td>
            {/if}
            {#if isVisible('namespace')}
              <td class="truncate px-3 py-1.5 text-on-surface-variant">{event.namespace}</td>
            {/if}
            {#if isVisible('message')}
              <td class="truncate px-3 py-1.5 text-on-surface-variant" title={event.message} data-selectable>
                {event.message}
              </td>
            {/if}
            {#if isVisible('count')}
              <td class="truncate px-3 py-1.5 text-right tabular-nums text-on-surface-variant">
                {event.count}
              </td>
            {/if}
            {#if isVisible('age')}
              <td class="truncate px-3 py-1.5 text-right tabular-nums text-on-surface-variant">
                {formatAge(event.ageSeconds)}
              </td>
            {/if}
          </tr>
        {/each}
      {/snippet}
    </DataTable>
  {/if}
</div>
