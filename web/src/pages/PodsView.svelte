<!--
  The pod list: the view an operator opens first and stares at longest.

  Every derived value here — the status word, the ready count, CPU and memory,
  the controller — was computed in Go. The table renders; it does not decide
  what "healthy" means.
-->
<script lang="ts">
  import DataTable, { type Column } from '$lib/components/DataTable.svelte'
  import MeterBar from '$lib/components/MeterBar.svelte'
  import StatusIndicator from '$lib/components/StatusIndicator.svelte'
  import EmptyState from '$lib/components/EmptyState.svelte'
  import { formatAge, podStatusLabel, podTone } from '$lib/format'
  import { preferences } from '$stores/preferences.svelte'
  import type { ClusterSession } from '$stores/session.svelte'
  import type { Pod } from '$lib/api/client'
  import { Box, CircleDot, TriangleAlert } from '@lucide/svelte'

  interface Props {
    session: ClusterSession
  }

  let { session }: Props = $props()

  const COLUMNS: Column[] = [
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
  const byLimit = $derived(preferences.podMeasure === 'limits')

  /**
   * Everything one meter cell needs, resolved for whichever mode is on.
   *
   * Gathered in one place rather than spread across eight attributes per
   * cell: the mode changes four things at once — what fills the bar, what
   * colours it, what the empty state says, and whether ticks are drawn — and
   * they have to change together or the cell starts contradicting itself.
   */
  function meter(
    hasRequest: boolean,
    requestPercent: number,
    hasLimit: boolean,
    limitPercent: number,
  ) {
    if (byLimit) {
      return {
        // Null when nothing was declared, which draws `absent` instead of a
        // bar measured against a ceiling this pod does not have.
        percent: hasLimit ? limitPercent : null,
        // No separate colour source: the fill IS the limit ratio here, so
        // GaugeTrack colours and marks it from the same number.
        severity: null,
        thresholds: true,
        absent: 'no limit',
      }
    }

    return {
      percent: hasRequest ? requestPercent : null,
      severity: hasLimit ? limitPercent : null,
      thresholds: false,
      absent: 'no request',
    }
  }

  /**
   * Spells out what the bar is a proportion of, and what it is heading for.
   *
   * A bare percentage in a pod list is not self-explanatory — an operator
   * has no way to tell whether 85% is 85% of a request, a limit or a node,
   * and those mean three different things. The tooltip names the denominator
   * and the number, so the meter never has to be taken on trust.
   *
   * Both ratios are stated when both exist, because the bar can only draw
   * one. The limit clause is what explains a coloured memory bar, and saying
   * "no limit" out loud is worth as much: an unbounded container is not an
   * omission the reader should have to infer from an absence.
   */
  function meterTitle(
    measured: boolean,
    value: string,
    hasRequest: boolean,
    request: string,
    percent: number,
    hasLimit: boolean,
    limit: string,
    limitPercent: number,
  ): string {
    if (!measured) return 'Not measured — this cluster has no metrics source'

    const against = hasRequest
      ? `${value} of ${request} requested (${Math.round(percent)}%)`
      : `${value} used — no request declared, so there is nothing to measure it against`

    const ceiling = hasLimit
      ? `${Math.round(limitPercent)}% of its ${limit} limit`
      : 'no limit set'

    return `${against} · ${ceiling}`
  }
</script>

<DataTable
  kindId={session.selectedKindId}
  columns={COLUMNS}
  isEmpty={session.pagedPods.length === 0}
  sort={session.sort}
  onsort={session.toggleSort}
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
      <tr
        class="group cursor-pointer border-t border-outline-variant/25 transition-colors duration-75
               {selected ? 'bg-primary/8' : 'hover:bg-surface-container-low'}"
        onclick={() => session.openDetail(pod.name, pod.namespace, pod)}
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
          rather than being metered against something invented for it. See
          docs/decisions/0002-pod-meters-name-the-absent-denominator.md.
        -->
        {#if isVisible('cpu')}
          {@const cpu = meter(
            pod.hasCpuRequest,
            pod.cpuPercent,
            pod.hasCpuLimit,
            pod.cpuLimitPercent,
          )}
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
              title={meterTitle(
                pod.hasMetrics,
                pod.cpu,
                pod.hasCpuRequest,
                pod.cpuRequest,
                pod.cpuPercent,
                pod.hasCpuLimit,
                pod.cpuLimit,
                pod.cpuLimitPercent,
              )}
            />
          </td>
        {/if}
        {#if isVisible('memory')}
          {@const memory = meter(
            pod.hasMemoryRequest,
            pod.memoryPercent,
            pod.hasMemoryLimit,
            pod.memoryLimitPercent,
          )}
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
              title={meterTitle(
                pod.hasMetrics,
                pod.memory,
                pod.hasMemoryRequest,
                pod.memoryRequest,
                pod.memoryPercent,
                pod.hasMemoryLimit,
                pod.memoryLimit,
                pod.memoryLimitPercent,
              )}
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
        <td></td>
      </tr>
    {/each}
  {/snippet}
</DataTable>
