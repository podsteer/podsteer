<!--
  The cluster dashboard.

  It is laid out in the order the questions are actually asked:

    1. Is anything wrong?      — the verdict, then the findings themselves
    2. Can I still schedule?   — capacity, measured against requests
    3. What is this cluster?   — nodes, pods, controllers, namespaces

  Everything shown here was decided in Go (app/domain/overview.go). This file
  draws the assessment; it does not make one. That split is why the rules are
  testable, and why the same analysis could drive a CLI without being rewritten
  in another language first.
-->
<script lang="ts">
  import CapacityBar from '$lib/components/CapacityBar.svelte'
  import FindingCard from '$lib/components/FindingCard.svelte'
  import TrendChart from '$lib/components/TrendChart.svelte'
  import { formatAge } from '$lib/format'
  import { ClusterHistory, TREND_WINDOWS } from '$stores/history.svelte'
  import { preferences } from '$stores/preferences.svelte'
  import type { ClusterSession } from '$stores/session.svelte'
  import {
    CheckCircle2,
    AlertOctagon,
    AlertTriangle,
    Boxes,
    Server,
    Layers,
    RefreshCw,
    Activity,
    CircleSlash,
    TrendingUp,
  } from '@lucide/svelte'

  interface Props {
    session: ClusterSession
  }

  let { session }: Props = $props()

  const overview = $derived(session.overview)

  /** Findings the operator should act on, and the rest. */
  const issues = $derived((overview?.findings ?? []).filter((finding) => finding.severity !== 'info'))
  const notes = $derived((overview?.findings ?? []).filter((finding) => finding.severity === 'info'))

  const HEALTH = {
    healthy: {
      icon: CheckCircle2,
      title: 'No problems found',
      classes: 'bg-success/10 text-success border-success/30',
    },
    degraded: {
      icon: AlertTriangle,
      title: 'Degraded',
      classes: 'bg-warning-container/40 text-on-warning-container border-warning/40',
    },
    critical: {
      icon: AlertOctagon,
      title: 'Needs attention',
      classes: 'bg-error-container/40 text-on-error-container border-error/40',
    },
  } as const

  const health = $derived(HEALTH[(overview?.health ?? 'healthy') as keyof typeof HEALTH])
  const HealthIcon = $derived(health.icon)

  /** Which quantity the trend chart plots. */
  let metric = $state<'cpu' | 'memory' | 'pods'>('cpu')

  const METRIC_TABS = [
    { id: 'cpu', label: 'CPU' },
    { id: 'memory', label: 'Memory' },
    { id: 'pods', label: 'Pods' },
  ] as const

  /**
   * This cluster's recorded history.
   *
   * Reloaded on the same cadence as the rest of the view rather than on its
   * own timer: a chart that advanced while the numbers above it did not would
   * be showing two different moments.
   */
  const history = $derived(new ClusterHistory(session.cluster.id))

  $effect(() => {
    const current = history
    void current.load()

    const interval = preferences.effectiveIntervalMs
    if (interval <= 0) return

    // Never poll faster than the backend samples: anything quicker just
    // redraws the same points.
    const timer = setInterval(
      () => void current.load(),
      Math.max(interval, current.intervalSeconds * 1000),
    )
    return () => clearInterval(timer)
  })

  /** Opens a kind's list from a finding. */
  function openList(kindId: string): void {
    void session.selectKind(kindId)
  }

  /** Opens one object from a finding, in the list it belongs to. */
  async function openObject(kindId: string, name: string, namespace: string): Promise<void> {
    await session.selectKind(kindId)
    await session.openDetail(name, namespace)
  }
</script>

<div class="min-h-0 flex-1 overflow-y-auto">
  {#if !overview}
    <!-- Only shown before the first assessment lands. Afterwards the previous
         one stays on screen while the next is fetched, so a 10-second refresh
         does not flash the whole dashboard away. -->
    <div class="flex h-full flex-col items-center justify-center gap-3 text-on-surface-variant/60">
      <Activity class="size-9 animate-pulse" strokeWidth={1.2} />
      <p class="text-body-medium">Assessing the cluster…</p>
    </div>
  {:else}
    <div class="mx-auto flex max-w-[1400px] flex-col gap-5 p-5">
      <!-- Verdict -->
      <section class="flex flex-wrap items-center gap-4 rounded-xl border p-4 {health.classes}">
        <HealthIcon class="size-8 shrink-0" strokeWidth={1.6} />

        <div class="min-w-0 flex-1">
          <h2 class="text-title-large font-semibold">{health.title}</h2>
          <p class="text-body-medium opacity-80">
            {#if issues.length === 0}
              {overview.nodes.ready} of {overview.nodes.total} nodes ready · {overview.pods.total} pods
              {#if notes.length > 0}· {notes.length} {notes.length === 1 ? 'note' : 'notes'}{/if}
            {:else}
              {overview.criticalCount > 0 ? `${overview.criticalCount} critical` : ''}
              {overview.criticalCount > 0 && overview.warningCount > 0 ? ' · ' : ''}
              {overview.warningCount > 0 ? `${overview.warningCount} warning` : ''}
              {issues.length === 1 ? ' finding' : ' findings'}
            {/if}
          </p>
        </div>

        <dl class="flex shrink-0 flex-wrap gap-x-6 gap-y-1 text-body-small">
          <div class="flex flex-col">
            <dt class="opacity-70">Version</dt>
            <dd class="tabular-nums">{overview.version || '—'}</dd>
          </div>
          <div class="flex flex-col">
            <dt class="opacity-70">Nodes</dt>
            <dd class="tabular-nums">{overview.nodes.total}</dd>
          </div>
          <div class="flex flex-col">
            <dt class="opacity-70">Pods</dt>
            <dd class="tabular-nums">{overview.pods.total}</dd>
          </div>
          <div class="flex flex-col">
            <dt class="opacity-70">Age</dt>
            <dd class="tabular-nums">{formatAge(overview.nodes.oldestSeconds)}</dd>
          </div>
        </dl>
      </section>

      <!-- A source that could not be read is stated, never silently zeroed. -->
      {#if overview.unavailable.length > 0}
        <p
          class="flex items-center gap-2 rounded-lg border border-outline-variant/40 bg-surface-container-low
                 px-3 py-2 text-body-small text-on-surface-variant"
        >
          <CircleSlash class="size-4 shrink-0 text-on-surface-variant/60" strokeWidth={1.8} />
          Assessed without {overview.unavailable.join(', ')} — those figures are missing rather than zero.
        </p>
      {/if}

      <!-- Findings -->
      {#if issues.length > 0}
        <section class="flex flex-col gap-2">
          <h3 class="text-label-large uppercase tracking-wider text-on-surface-variant">
            Needs attention
          </h3>
          {#each issues as finding (finding.id)}
            <FindingCard {finding} onopen={openList} onselect={openObject} />
          {/each}
        </section>
      {/if}

      <!-- Capacity -->
      <section class="flex flex-col gap-3 rounded-xl border border-outline-variant/40 bg-surface-container-low p-4">
        <div class="flex items-baseline justify-between gap-3">
          <h3 class="text-title-medium font-semibold text-on-surface">Capacity</h3>
          <span class="text-body-small text-on-surface-variant/70">
            Requests decide what schedules, not usage
          </span>
        </div>

        <div class="grid gap-5 md:grid-cols-2">
          <CapacityBar label="CPU" usage={overview.capacity.cpu} unit="cores" />
          <CapacityBar label="Memory" usage={overview.capacity.memory} />
        </div>

        <!-- Pod slots: the limit that catches people out, because a node
             refuses pods at its cap however much CPU and memory are free. -->
        <div class="flex flex-col gap-2 border-t border-outline-variant/40 pt-3">
          <div class="flex items-baseline justify-between gap-3">
            <span class="text-label-large text-on-surface">Pod slots</span>
            <span class="text-body-small tabular-nums text-on-surface-variant">
              {overview.capacity.pods.scheduled} / {overview.capacity.pods.capacity}
            </span>
          </div>
          <div class="h-3 w-full overflow-hidden rounded-full bg-surface-container-highest">
            <span
              class="block h-full rounded-full transition-all duration-300 ease-standard
                     {overview.capacity.pods.usedPercent >= 85 ? 'bg-error/70' : 'bg-primary/45'}"
              style="width: {Math.min(100, overview.capacity.pods.usedPercent)}%"
            ></span>
          </div>
          {#if overview.capacity.pods.unschedulable > 0}
            <p class="text-body-small text-warning">
              {overview.capacity.pods.unschedulable} pod{overview.capacity.pods.unschedulable === 1
                ? ''
                : 's'} waiting for a node
            </p>
          {/if}
        </div>
      </section>

      <!-- Trend.
           Kubernetes reports only the present, so this is K8Sense's own
           record: sampled every 30 seconds while the application is open. It
           says so rather than implying the completeness a monitoring stack
           would have. -->
      <section class="flex flex-col gap-3 rounded-xl border border-outline-variant/40 bg-surface-container-low p-4">
        <div class="flex flex-wrap items-center justify-between gap-3">
          <h3 class="flex items-center gap-2 text-title-medium font-semibold text-on-surface">
            <TrendingUp class="size-4 text-on-surface-variant" strokeWidth={1.8} />
            Trend
          </h3>

          <div class="flex items-center gap-3">
            <!-- Metric -->
            <div class="flex rounded-md border border-outline-variant/60 p-0.5">
              {#each METRIC_TABS as tab (tab.id)}
                <button
                  type="button"
                  onclick={() => (metric = tab.id)}
                  aria-pressed={metric === tab.id}
                  class="rounded px-2.5 py-1 text-label-medium transition-colors duration-100
                         {metric === tab.id
                           ? 'bg-secondary-container text-on-secondary-container'
                           : 'text-on-surface-variant hover:text-on-surface'}"
                >
                  {tab.label}
                </button>
              {/each}
            </div>

            <!-- Window -->
            <div class="flex rounded-md border border-outline-variant/60 p-0.5">
              {#each TREND_WINDOWS as option (option.minutes)}
                <button
                  type="button"
                  onclick={() => void history.setWindow(option.minutes)}
                  aria-pressed={history.windowMinutes === option.minutes}
                  class="rounded px-2 py-1 text-label-medium tabular-nums transition-colors duration-100
                         {history.windowMinutes === option.minutes
                           ? 'bg-secondary-container text-on-secondary-container'
                           : 'text-on-surface-variant hover:text-on-surface'}"
                >
                  {option.label}
                </button>
              {/each}
            </div>
          </div>
        </div>

        {#if !history.recording}
          <p class="py-8 text-center text-body-small text-on-surface-variant/70">
            History is not being recorded. Turn it on in Settings → Data.
          </p>
        {:else if !history.hasTrend}
          <p class="py-8 text-center text-body-small text-on-surface-variant/70">
            Collecting. K8Sense samples every {formatAge(history.intervalSeconds)} while it is
            open, so a line appears once a second sample lands.
          </p>
        {:else}
          <TrendChart samples={history.samples} {metric} />
          <p class="text-body-small text-on-surface-variant/60">
            Covering the last {formatAge(history.spanSeconds)} that K8Sense has been open on this
            cluster — not the cluster's whole history.
          </p>
        {/if}
      </section>

      <div class="grid gap-4 lg:grid-cols-2">
        <!-- Inventory -->
        <section class="flex flex-col gap-3 rounded-xl border border-outline-variant/40 bg-surface-container-low p-4">
          <h3 class="flex items-center gap-2 text-title-medium font-semibold text-on-surface">
            <Boxes class="size-4 text-on-surface-variant" strokeWidth={1.8} />
            Workloads
          </h3>

          {#if overview.workloads.length === 0}
            <p class="text-body-small text-on-surface-variant/60">Nothing deployed.</p>
          {:else}
            <ul class="flex flex-col divide-y divide-outline-variant/30">
              {#each overview.workloads as kind (kind.kindId)}
                <li>
                  <button
                    type="button"
                    onclick={() => openList(kind.kindId)}
                    class="flex w-full items-center gap-3 py-1.5 text-left transition-colors duration-100
                           hover:text-primary"
                  >
                    <span class="flex-1 truncate text-body-medium text-on-surface">{kind.title}</span>
                    {#if kind.rolling > 0}
                      <span class="text-body-small text-primary" title="Rollout in progress">
                        {kind.rolling} rolling
                      </span>
                    {/if}
                    {#if kind.degraded > 0}
                      <span class="text-body-small text-warning">{kind.degraded} degraded</span>
                    {/if}
                    <span class="w-16 shrink-0 text-right text-body-small tabular-nums text-on-surface-variant">
                      {kind.healthy}/{kind.total}
                    </span>
                  </button>
                </li>
              {/each}
            </ul>
          {/if}

          <div class="mt-1 grid grid-cols-3 gap-3 border-t border-outline-variant/40 pt-3 text-center">
            <div>
              <p class="text-title-large tabular-nums text-on-surface">{overview.pods.running}</p>
              <p class="text-label-small uppercase text-on-surface-variant">Running</p>
            </div>
            <div>
              <p
                class="text-title-large tabular-nums {overview.pods.pending > 0
                  ? 'text-warning'
                  : 'text-on-surface'}"
              >
                {overview.pods.pending}
              </p>
              <p class="text-label-small uppercase text-on-surface-variant">Pending</p>
            </div>
            <div>
              <p
                class="text-title-large tabular-nums {overview.pods.failed > 0
                  ? 'text-error'
                  : 'text-on-surface'}"
              >
                {overview.pods.failed}
              </p>
              <p class="text-label-small uppercase text-on-surface-variant">Failed</p>
            </div>
          </div>
        </section>

        <!-- Nodes -->
        <section class="flex flex-col gap-3 rounded-xl border border-outline-variant/40 bg-surface-container-low p-4">
          <h3 class="flex items-center gap-2 text-title-medium font-semibold text-on-surface">
            <Server class="size-4 text-on-surface-variant" strokeWidth={1.8} />
            Nodes
          </h3>

          <dl class="grid grid-cols-2 gap-x-4 gap-y-1.5 text-body-small">
            <dt class="text-on-surface-variant">Ready</dt>
            <dd class="text-right tabular-nums text-on-surface">
              {overview.nodes.ready} / {overview.nodes.total}
            </dd>

            {#if overview.nodes.notReady > 0}
              <dt class="text-error">Not ready</dt>
              <dd class="text-right tabular-nums text-error">{overview.nodes.notReady}</dd>
            {/if}

            {#if overview.nodes.cordoned > 0}
              <dt class="text-on-surface-variant">Cordoned</dt>
              <dd class="text-right tabular-nums text-warning">{overview.nodes.cordoned}</dd>
            {/if}

            {#if overview.nodes.underPressure > 0}
              <dt class="text-on-surface-variant">Under pressure</dt>
              <dd class="text-right tabular-nums text-warning">{overview.nodes.underPressure}</dd>
            {/if}

            <dt class="text-on-surface-variant">Control plane</dt>
            <dd class="text-right tabular-nums text-on-surface">{overview.nodes.controlPlane}</dd>
          </dl>

          {#if overview.nodes.kubeletVersions.length > 0}
            <div class="border-t border-outline-variant/40 pt-2">
              <p class="mb-1 text-label-small uppercase text-on-surface-variant">Kubelet</p>
              <ul class="flex flex-wrap gap-x-3 gap-y-1 text-body-small">
                {#each overview.nodes.kubeletVersions as version (version.version)}
                  <li class="tabular-nums text-on-surface-variant">
                    {version.version}
                    <span class="text-on-surface-variant/60">×{version.nodes}</span>
                  </li>
                {/each}
              </ul>
            </div>
          {/if}
        </section>
      </div>

      <div class="grid gap-4 lg:grid-cols-2">
        <!-- Namespaces, ranked by what they reserve rather than what they use:
             reservations are what fill a cluster. -->
        <section class="flex flex-col gap-3 rounded-xl border border-outline-variant/40 bg-surface-container-low p-4">
          <div class="flex items-baseline justify-between gap-3">
            <h3 class="flex items-center gap-2 text-title-medium font-semibold text-on-surface">
              <Layers class="size-4 text-on-surface-variant" strokeWidth={1.8} />
              Busiest namespaces
            </h3>
            <span class="text-body-small text-on-surface-variant/70">by CPU requested</span>
          </div>

          {#if overview.namespaces.length === 0}
            <p class="text-body-small text-on-surface-variant/60">Nothing scheduled.</p>
          {:else}
            <ul class="flex flex-col gap-2">
              {#each overview.namespaces as load (load.name)}
                <li class="flex flex-col gap-1">
                  <div class="flex items-baseline justify-between gap-3">
                    <button
                      type="button"
                      onclick={() => session.selectNamespace(load.name)}
                      class="min-w-0 truncate text-body-medium text-on-surface transition-colors
                             duration-100 hover:text-primary"
                      title="Filter everything to {load.name}"
                    >
                      {load.name}
                    </button>
                    <span class="shrink-0 text-body-small tabular-nums text-on-surface-variant">
                      {load.cpuRequests}
                      {#if load.measured}
                        <span class="text-on-surface-variant/60">· using {load.cpuUsage}</span>
                      {/if}
                    </span>
                  </div>
                  <div class="flex items-center gap-2">
                    <span class="h-1.5 flex-1 overflow-hidden rounded-full bg-surface-container-highest">
                      <span
                        class="block h-full rounded-full bg-primary/50"
                        style="width: {Math.min(100, load.cpuShare)}%"
                      ></span>
                    </span>
                    <span class="w-24 shrink-0 text-right text-body-small text-on-surface-variant/60">
                      {load.pods} pod{load.pods === 1 ? '' : 's'}{load.notReady > 0
                        ? `, ${load.notReady} down`
                        : ''}
                    </span>
                  </div>
                </li>
              {/each}
            </ul>
          {/if}
        </section>

        <!-- Restart hotspots: pods every list shows as healthy. -->
        <section class="flex flex-col gap-3 rounded-xl border border-outline-variant/40 bg-surface-container-low p-4">
          <div class="flex items-baseline justify-between gap-3">
            <h3 class="flex items-center gap-2 text-title-medium font-semibold text-on-surface">
              <RefreshCw class="size-4 text-on-surface-variant" strokeWidth={1.8} />
              Most restarted
            </h3>
            <span class="text-body-small tabular-nums text-on-surface-variant/70">
              {overview.pods.restarts} total
            </span>
          </div>

          {#if overview.restarts.length === 0}
            <p class="text-body-small text-on-surface-variant/60">Nothing has restarted.</p>
          {:else}
            <ul class="flex flex-col divide-y divide-outline-variant/30">
              {#each overview.restarts as hotspot (hotspot.namespace + '/' + hotspot.name)}
                <li>
                  <button
                    type="button"
                    onclick={() => openObject('core/v1/pods', hotspot.name, hotspot.namespace)}
                    class="flex w-full items-center gap-3 py-1.5 text-left transition-colors duration-100
                           hover:text-primary"
                  >
                    <span class="min-w-0 flex-1 truncate text-body-small" title={hotspot.name}>
                      <span class="text-on-surface-variant/60">{hotspot.namespace}/</span
                      >{hotspot.name}
                    </span>
                    {#if hotspot.reason}
                      <span class="shrink-0 text-body-small text-warning">{hotspot.reason}</span>
                    {/if}
                    <span
                      class="w-14 shrink-0 text-right text-body-small tabular-nums text-on-surface-variant"
                      title="over {formatAge(hotspot.ageSeconds)}"
                    >
                      {hotspot.restarts}×
                    </span>
                  </button>
                </li>
              {/each}
            </ul>
          {/if}
        </section>
      </div>

      <!-- Notes: true, worth knowing, not a fault. Kept last so they never
           compete with the findings above. -->
      {#if notes.length > 0}
        <section class="flex flex-col gap-2">
          <h3 class="text-label-large uppercase tracking-wider text-on-surface-variant">
            Worth knowing
          </h3>
          {#each notes as finding (finding.id)}
            <FindingCard {finding} onopen={openList} onselect={openObject} />
          {/each}
        </section>
      {/if}
    </div>
  {/if}
</div>
