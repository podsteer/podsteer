<!--
  The pod list: the view an operator opens first and stares at longest.

  Every derived value here — the status word, the ready count, CPU and memory,
  the controller — was computed in Go. The table renders; it does not decide
  what "healthy" means.
-->
<script lang="ts">
  import DataTable, { type Column } from '$lib/components/DataTable.svelte'
  import type { CSVExport } from '$stores/activeTable.svelte'
  import MeterBar from '$lib/components/MeterBar.svelte'
  import StatusIndicator from '$lib/components/StatusIndicator.svelte'
  import EmptyState from '$lib/components/EmptyState.svelte'
  import { formatAge, podStatusLabel, podTone } from '$lib/format'
  import { preferences } from '$stores/preferences.svelte'
  import { cpuMeter, cpuTitle, memoryMeter, memoryTitle } from '$lib/meter'
  import { POD_STATUS_CHIPS } from '$lib/podStatusFilters'
  import RowMenu, { type RowAction } from '$lib/components/RowMenu.svelte'
  import CustomCells from '$lib/components/CustomCells.svelte'
  import { customCell, parseCustomColumnId, toColumns } from '$lib/customColumns'
  import RowSelect from '$lib/components/RowSelect.svelte'
  import { rowKey } from '$lib/bulk'
  import { get as kubectlGet } from '$lib/kubectl'
  import type { ClusterSession } from '$stores/session.svelte'
  import type { Pod } from '$lib/api/client'
  import { Box, CircleDot, TriangleAlert, Plug, Loader } from '@lucide/svelte'
  import { forwards } from '$stores/forwards.svelte'

  interface Props {
    session: ClusterSession
  }

  let { session }: Props = $props()

  /**
   * How many of the search-filtered pods each chip would add, in one pass —
   * six separate `.filter(...).length` calls would be six passes over the
   * same rows for six numbers that were always going to be read together.
   *
   * Counted against `session.searchedPods` (search applied, chips not yet)
   * rather than `session.visiblePods`, so a chip that is not selected still
   * shows what selecting it would add instead of counting against a list its
   * own selection has already shrunk.
   */
  const chipCounts = $derived.by(() => {
    const counts: Record<string, number> = {}
    for (const chip of POD_STATUS_CHIPS) counts[chip.id] = 0
    for (const pod of session.searchedPods) {
      for (const chip of POD_STATUS_CHIPS) {
        if (chip.predicate(pod)) counts[chip.id]++
      }
    }
    return counts
  })

  const COLUMNS: Column[] = [
    { id: 'select', label: 'Select', width: 40, pinned: true, select: true },
    { id: 'status', label: 'Status', width: 44, icon: CircleDot },
    { id: 'name', label: 'Name', width: 320, pinned: true },
    { id: 'namespace', label: 'Namespace', width: 150 },
    { id: 'cpu', label: 'CPU', width: 220, minWidth: 200 },
    { id: 'memory', label: 'Memory', width: 220, minWidth: 200 },
    { id: 'ready', label: 'Ready', width: 80, numeric: true },
    { id: 'restarts', label: 'Restarts', width: 90, numeric: true },
    { id: 'controlledBy', label: 'Controlled By', width: 200 },
    { id: 'node', label: 'Node', width: 180 },
    { id: 'qos', label: 'QoS', width: 90 },
    { id: 'ip', label: 'IP', width: 120, defaultHidden: true },
    { id: 'age', label: 'Age', width: 80, numeric: true },
  ]

  /** The built-in columns, then the operator's own — see $lib/customColumns. */
  const columns = $derived<Column[]>([...COLUMNS, ...toColumns(session.customColumns)])

  /**
   * The findings worth marking a row for.
   *
   * Computed in Go and merely filtered here: severity is the domain's
   * judgement, and this only decides which of those judgements earns a glyph
   * in a dense list.
   */
  function alarming(pod: Pod) {
    return (pod.findings ?? []).filter((finding) => finding.severity !== 'info')
  }

  /**
   * Whether the bars divide by limits rather than requests.
   *
   * When they do, the bar and the operator's thresholds share one denominator
   * at last, so the meter can be drawn as a proper gauge — segmented, with
   * the two lines marked on the track exactly as the node list draws them.
   * When they do not, the fill answers a question the thresholds are not
   * about, so the lines are left unmarked and only the colour carries them.
   */
  /** What every pod row offers from its menu — just the one thing so far. */
  function actionsFor(pod: Pod): RowAction[] {
    return [
      {
        label: 'Copy as kubectl',
        kind: 'copy',
        onclick: () => copyKubectl(kubectlGet(session.cluster.id, 'pods', pod.name, pod.namespace)),
      },
    ]
  }

  /**
   * Silent about failure, like every other copy control in the application:
   * the clipboard is a permissioned API that can simply refuse, and there is
   * nothing useful to say about that here.
   */
  function copyKubectl(command: string): void {
    void navigator.clipboard?.writeText(command).catch(() => {})
  }

  const byLimit = $derived(preferences.podMeasure === 'limits')

  /**
   * Which rows are on screen, in display order — what a shift-click ranges
   * across and the header checkbox selects. Published from here because only
   * the view knows the page it is drawing; see $lib/selection.
   */
  $effect(() => {
    session.selection.visible = session.pagedPods.map((pod) => rowKey(pod.namespace, pod.name))
    return () => {
      session.selection.visible = []
    }
  })

  /** Whether an operator has not hidden this column — the same rule
      ColumnMenu and DataTable itself apply, repeated here rather than asked
      of either: the export has to decide it independently of what is
      currently mounted, from the same preferences they both read. */
  function isColumnVisible(column: Column): boolean {
    const stored = preferences.columns[session.selectedKindId]?.[column.id]?.hidden
    return column.pinned || (stored === undefined ? !column.defaultHidden : !stored)
  }

  /**
   * The pod list's CSV export.
   *
   * Every field here is the same text its cell shows — a status word rather
   * than the bare phase, a meter's underlying quantity with its unit rather
   * than the percentage, an age already coarsened — never a number the
   * operator would have to re-derive what the column meant.
   */
  function exportCSV(): CSVExport {
    // The tick box is a control, not a column with text in it.
    const visible = columns.filter((column) => !column.select && isColumnVisible(column))

    function cell(pod: Pod, id: string): string {
      const custom = parseCustomColumnId(id)
      if (custom) return customCell(pod, custom)
      switch (id) {
        case 'status':
          return podStatusLabel(pod)
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
        case 'qos':
          return pod.qosClass || '—'
        case 'ip':
          return pod.podIp || '—'
        case 'age':
          return formatAge(pod.ageSeconds)
        default:
          return ''
      }
    }

    return {
      columns: visible.map((column) => column.label),
      rows: session.sortedPods.map((pod) => visible.map((column) => cell(pod, column.id))),
    }
  }
</script>

<div class="flex min-h-0 flex-1 flex-col">
  <!--
    Status quick-filters. Each one SELECTS on a field the domain already
    computed — see $lib/podStatusFilters for exactly which — never a new
    comparison made here. Chips OR together (any one matching is enough) and
    AND with the text search above, same as k9s's "/-l label" and every
    competitor's status filter.

    Not a <PaneToolbar>: that shell is for the full-height text panes (YAML,
    logs), and this is a row of toggles above a table, closer kin to
    OverviewView's trend tabs than to a find box. The pressed/unpressed
    colours are lifted from ToolbarToggle regardless, so a chip on and a
    toolbar icon on read as the same state.
  -->
  <div
    class="flex flex-wrap items-center gap-1.5 border-b border-outline-variant/40
           bg-surface-container-low/40 px-4 py-2"
  >
    {#each POD_STATUS_CHIPS as chip (chip.id)}
      {@const pressed = session.podStatusFilters.includes(chip.id)}
      {@const count = chipCounts[chip.id]}
      <button
        type="button"
        onclick={() => session.togglePodStatusFilter(chip.id)}
        aria-pressed={pressed}
        title="{pressed ? 'Showing only' : 'Show only'} {chip.label.toLowerCase()} pods"
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
  </div>

  <DataTable
    kindId={session.selectedKindId}
    {columns}
    isEmpty={session.pagedPods.length === 0}
    sort={session.sort}
    onsort={session.toggleSort}
    exportRows={exportCSV}
    selectAll={{
      checked: session.selection.allVisibleSelected,
      indeterminate: session.selection.someVisibleSelected,
      ontoggle: () => session.selection.toggleAllVisible(),
    }}
  >
    {#snippet empty()}
      <EmptyState
        title="No pods here"
        description={session.search
          ? `Nothing matches "${session.search}".`
          : 'This namespace is not running any pods you can see.'}
      />
    {/snippet}

    {#snippet rows(isVisible)}
      {#each session.pagedPods as pod (pod.namespace + '/' + pod.name)}
        {@const selected =
          session.selectedName === pod.name && session.selectedNamespace === pod.namespace}
        {@const key = rowKey(pod.namespace, pod.name)}
        {@const ticked = session.selection.has(key)}
        <tr
          class="group/row cursor-pointer border-t border-outline-variant/25 transition-colors duration-75
                 {selected ? 'bg-primary/8' : ticked ? 'bg-primary/5' : 'hover:bg-surface-container-low'}"
          aria-selected={ticked}
          onclick={() => session.openDetail(pod.name, pod.namespace, pod)}
        >
          <RowSelect
            selected={ticked}
            label={pod.name}
            ontoggle={(range) => session.selection.toggle(key, range)}
          />
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
          <td class="px-3 py-1.5" title={pod.name}>
            <span class="flex items-center gap-2">
              <span class="truncate font-medium text-on-surface">{pod.name}</span>
              <!--
                A mark for a pod the assessment has something to say about, so
                the findings are reachable without opening every row to check.

                INFO FINDINGS ARE EXCLUDED. A mutable tag or a Burstable QoS is
                worth reading once you are looking at a pod and is not worth a
                mark on a list — half a real cluster would carry one, and a mark
                most rows have is not a mark. What survives is the class this
                column cannot already show: a pod that looks fine and is not,
                like one whose probes will restart it or whose deletion is
                wedged.
              -->
              <!--
                WHICH POD HOLDS THE FORWARD. Not decoration: a forward survives
                its pod being deleted by moving to a replacement, so the row
                holding it afterwards is not the row it was started from — and
                with several replicas of one workload there was nothing at all
                to tell them apart.

                The port is in the mark rather than only in a tooltip, because
                the question being asked is "which of these is on 59595".
              -->
              {#each forwards.forPod(session.cluster.id, pod.namespace, pod.name) as forward (forward.id)}
                <span
                  class="inline-flex shrink-0 items-center gap-1 rounded bg-primary/12 px-1.5
                         text-body-small text-primary"
                  title={forward.reconnecting
                  ? `Waiting for a replacement pod — was ${forward.address}`
                  : `${forward.address} → container port ${forward.remotePort}`}
                >
                  {#if forward.reconnecting}
                    <Loader class="size-3 animate-spin" strokeWidth={2} />
                  {:else}
                    <Plug class="size-3" strokeWidth={2} />
                  {/if}
                  {forward.localPort}
                </span>
              {/each}

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
          <!--
            THE DASH IS AMBIGUOUS WITHOUT hasMetrics, and the flag was already
            here. `pod.cpu` formats to "—" both when nothing measured the pod and
            when the pod genuinely used no measurable CPU — so an unmeasured
            cluster and an idle one looked identical, which is what made a fresh
            cluster read as a broken application.

            The tooltip carries the distinction rather than the cell: fifteen rows
            each saying "no metrics" is noise, and the explanation of WHY belongs
            once, in the notice above the table.

            THE METER DIVIDES BY THE POD'S REQUEST, not by its limit and not by
            its node. It is the question the rest of PodSteer is built around —
            how much of what you reserved you are actually using — and it is the
            one a pod list can answer that `kubectl top` cannot. A pod declaring
            no request has no denominator, so it SAYS SO where the bar would be
            rather than being metered against something invented for it.
          -->
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
          {#if isVisible('qos')}
            <td class="truncate px-3 py-1.5 text-on-surface-variant">{pod.qosClass || '—'}</td>
          {/if}
          {#if isVisible('ip')}
            <td class="truncate px-3 py-1.5 text-on-surface-variant" data-selectable>
              {pod.podIp || '—'}
            </td>
          {/if}
          {#if isVisible('age')}
            <td class="truncate px-3 py-1.5 text-right tabular-nums text-on-surface-variant">
              {formatAge(pod.ageSeconds)}
            </td>
          {/if}
          <CustomCells specs={session.customColumns} row={pod} {isVisible} />
          <!-- Stops the click here: the row itself opens the detail drawer,
               and a click aimed at the menu — or at one of its items — must
               not also do that. -->
          <td class="px-2" onclick={(event) => event.stopPropagation()}>
            <div class="flex justify-end">
              <RowMenu actions={actionsFor(pod)} label={pod.name} />
            </div>
          </td>
        </tr>
      {/each}
    {/snippet}
  </DataTable>
</div>
