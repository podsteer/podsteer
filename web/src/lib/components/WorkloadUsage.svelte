<!--
  What a controller's pods are consuming, over time.

  A CONTROLLER HAS NO USAGE OF ITS OWN — it is a template and a replica count
  — so this is the sum over whatever pods it currently has, read from the
  cluster while the panel is open. That is also why a Deployment scaled to
  zero and a CronJob between runs both read as nothing: it is a true reading,
  not a hole in the chart.

  THE SERIES IS BUILT HERE, unlike a pod's or a node's. Those come free: every
  refresh of the pod list carries a measurement for every pod, so the history
  is a by-product of browsing. Nothing polls a controller's pods, so a sample
  is taken on each refresh of the list behind this panel and kept alongside
  the others in usageHistory — which is why the chart starts empty and fills
  in, and says so.

  The reference lines are the SUM of the pods' requests and limits rather than
  one pod's. Six replicas reserving 100m each is 600m of the cluster, and a
  chart drawn against 100m would say a healthy Deployment was six times over
  its request.
-->
<script lang="ts">
  import { workloadUsage, type WorkloadUsage } from '$lib/api/client'
  import { toApiError } from '$lib/api/errors'
  import { usageHistory, usageKey } from '$stores/usageHistory.svelte'
  import type { UsageSample } from '$stores/session.svelte'
  import DetailSection from './DetailSection.svelte'
  import UsageChart from './UsageChart.svelte'
  import MetricsBackendNote from './MetricsBackendNote.svelte'
  import type { MetricsBackend } from '$lib/api/client'
  import { preferences } from '$stores/preferences.svelte'

  interface Props {
    clusterId: string
    namespace: string
    /** The controller's Kubernetes kind, e.g. "Deployment". */
    kind: string
    name: string
    /**
     * Changes on every refresh of the list behind the panel.
     *
     * The tick this samples on. Passed in rather than timed here, so the
     * chart follows the operator's own refresh setting — including "manual
     * only", where a chart quietly sampling on a timer of its own would be
     * the one thing in the application still talking to the cluster.
     */
    tick: unknown
    /** A monitoring system found running in the cluster, if any. */
    backend?: MetricsBackend | null
  }

  let { clusterId, namespace, kind, name, tick, backend }: Props = $props()

  let reading = $state.raw<WorkloadUsage | null>(null)
  let failure = $state('')
  let samples = $state.raw<UsageSample[]>([])

  /**
   * Whether the section is open, read from the preference rather than latched
   * from DetailSection's onopen.
   *
   * onopen only ever fires one way, so a section latched open on the first
   * render kept sampling after it was collapsed — which is precisely the cost
   * the section is closed to avoid. The preference is the truth in both
   * directions.
   */
  const open = $derived(preferences.sectionOpen('workload-usage', true))

  /** Identifies this controller's series across panels and refreshes. */
  const key = $derived(usageKey('workload', namespace, `${kind}/${name}`))

  /**
   * Reads the sum and records it.
   *
   * Recorded whether or not anything was measured is WRONG here, so it is
   * not: an unmeasured cluster would otherwise accumulate a flat line at zero
   * that looks like a workload doing nothing.
   */
  async function sample(): Promise<void> {
    try {
      const result = await workloadUsage(clusterId, namespace, kind, name)
      reading = result
      failure = ''

      if (result.hasMetrics) {
        usageHistory.record(key, {
          at: Date.now(),
          cpuCores: result.cpuCores,
          memoryBytes: result.memoryBytes,
        })
      }
      samples = usageHistory.since(key)
    } catch (error) {
      failure = toApiError(error).message
    }
  }

  // Samples on open, and on every refresh after that while it stays open. A
  // closed section costs nothing, which matters on a controller list where
  // every row has one of these behind it.
  $effect(() => {
    void tick
    void key
    if (open) void sample()
  })

  /** What the panel says beside the heading. */
  const hint = $derived.by(() => {
    if (!reading) return undefined
    if (!reading.hasMetrics) return reading.pods === 0 ? 'nothing running' : 'not measured'
    return `CPU ${reading.cpu} · Memory ${reading.memory}`
  })
</script>

<DetailSection level="h3" id="workload-usage" title="Usage" {hint}>
  {#if failure}
    <p class="py-2 text-body-small text-error">{failure}</p>
  {:else if reading && reading.pods === 0}
    <!-- Not an error and not an empty state: nothing is running, which for a
         CronJob between runs or a Deployment scaled to zero is the ordinary
         condition and the answer somebody came for. -->
    <p class="py-2 text-body-small text-on-surface-variant/70">
      Nothing is running, so there is nothing to measure.
    </p>
  {:else if reading && !reading.hasMetrics}
    <p class="py-2 text-body-small text-on-surface-variant/70">
      None of the {reading.pods}
      {reading.pods === 1 ? 'pod' : 'pods'} reported usage — this cluster has no metrics source.
    </p>
  {:else if reading}
    <div class="flex flex-col gap-4">
      {#each [{ metric: 'cpu' as const, label: 'CPU', used: reading.cpu, request: reading.requestCores, limit: reading.limitCores }, { metric: 'memory' as const, label: 'Memory', used: reading.memory, request: reading.requestBytes, limit: reading.limitBytes }] as track (track.metric)}
        <div class="flex flex-col gap-1">
          <p class="flex items-baseline justify-between text-body-small text-on-surface-variant">
            <span>{track.label}</span>
            <span class="tabular-nums">{track.used}</span>
          </p>
          <UsageChart
            {samples}
            metric={track.metric}
            markers={[
              { value: track.request, label: 'Request', tone: 'warn' },
              { value: track.limit, label: 'Limit', tone: 'critical' },
            ]}
            format={track.metric === 'cpu' ? formatCores : formatBytes}
          />
        </div>
      {/each}

      <!-- Said out loud, because the total is real but short. A controller of
           twenty pods where metrics-server answered for eighteen is showing
           less than it is using, and a figure that does not say so is an
           understatement presented as a measurement. -->
      {#if reading.measured < reading.pods}
        <p class="text-body-small text-gauge-warn">
          Summed over {reading.measured} of {reading.pods} pods — the rest reported no usage,
          so this is less than the whole.
        </p>
      {/if}

      <MetricsBackendNote {backend} />
    </div>
  {/if}
</DetailSection>

<script lang="ts" module>
  /** Axis formatters, matching how Go prints the same quantities. */
  function formatCores(value: number): string {
    return value.toFixed(3)
  }

  function formatBytes(value: number): string {
    const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB']
    let size = value
    let unit = 0
    while (size >= 1024 && unit < units.length - 1) {
      size /= 1024
      unit += 1
    }
    return `${size.toFixed(1)}${units[unit]}`
  }
</script>
