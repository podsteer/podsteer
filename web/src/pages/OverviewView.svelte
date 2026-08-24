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
  import type { Figure } from '$lib/components/CapacityFigures.svelte'
  import SlotsBar from '$lib/components/SlotsBar.svelte'
  import FindingCard from '$lib/components/FindingCard.svelte'
  import NodeLoadChart from '$lib/components/NodeLoadChart.svelte'
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
    HardDrive,
    Flame,
    Gauge,
    ChevronRight,
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

  /**
   * Findings the operator should act on, and the rest.
   *
   * The two lists of issues come from the session rather than being filtered
   * here, because that is where snoozing is applied — the navigator badge, the
   * verdict and this list all have to agree about what is outstanding, and
   * three separate filters eventually would not.
   */
  const issues = $derived(session.activeIssues)
  const snoozed = $derived(session.snoozedIssues)
  const notes = $derived((overview?.findings ?? []).filter((finding) => finding.severity === 'info'))

  /**
   * Node scratch disk — the container writable layer, emptyDir volumes and
   * logs. Shown beside CPU and memory because it is a scheduling dimension
   * like them, and because it is the one nobody reserves: a node whose disk
   * fills has no allocation protecting it, and the kubelet's answer is to
   * evict.
   */
  const hasEphemeral = $derived(overview?.capacity.ephemeral.reported ?? false)

  const ephemeralNote = $derived(
    overview && !overview.capacity.ephemeral.declared
      ? 'Nothing requests it, so nothing is reserved — the scheduler will fill these disks.'
      : '',
  )

  /**
   * Pressure conditions in the words an operator uses for them.
   *
   * The Kubernetes names say which resource but not what is happening: a node
   * "reporting DiskPressure" is a node the kubelet is already evicting from,
   * because the condition only trips once it is nearly full.
   */
  /**
   * Phases that are not simply "working", coloured.
   *
   * Bound and Available are the healthy states and stay neutral: colouring
   * everything is the same as colouring nothing.
   */
  const PHASE_TONE: Record<string, string> = {
    Pending: 'text-warning',
    Lost: 'text-error',
    Failed: 'text-error',
    Released: 'text-warning',
  }

  const PRESSURE_LABELS: Record<string, string> = {
    DiskPressure: 'Out of disk',
    MemoryPressure: 'Out of memory',
    PIDPressure: 'Out of process IDs',
  }

  /**
   * What stands in for Efficiency on the ephemeral track.
   *
   * That figure compares what pods use with what they reserved, and nothing
   * reports per-pod disk use — so the slot held a dash. The fullest node is
   * the number worth having there instead, and for the same reason the load
   * chart exists: 13% used across eighteen nodes is equally consistent with
   * every disk at 13% and with one at 90% about to start evicting.
   */
  const fullestDisk = $derived.by((): Figure => {
    const disks = overview?.nodes.disks
    if (!disks || disks.measured === 0) {
      return { label: 'Fullest node', value: '—', muted: true }
    }
    const percent = Math.round(disks.fullestPercent)
    return {
      label: 'Fullest node',
      percent: `${percent}%`,
      tone: percent >= 90 ? 'text-error' : percent >= 80 ? 'text-warning' : undefined,
      title: `${disks.fullestNode} — the fullest of ${disks.measured} node filesystems`,
    }
  })

  const criticalCount = $derived(issues.filter((finding) => finding.severity === 'critical').length)
  const warningCount = $derived(issues.length - criticalCount)

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

  const health = $derived(HEALTH[session.health])
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

    // Reading lastRefreshedAt is what makes that true. It subscribes this
    // effect to the session's refresh cycle, so the chart reloads in the same
    // turn the numbers above it do — one timer, one moment.
    //
    // A second setInterval here was the previous approach and could not keep
    // that promise: started at a different instant, it drifted out of phase
    // with the session's timer, and clamping it to the backend's sampling
    // cadence put the chart on a different period entirely whenever sampling
    // was slower than Settings → Refresh. Following the session also means
    // "Manual only" and ⌘R are honoured for free.
    session.lastRefreshedAt
    void current.load()
  })

  /**
   * When one object's snooze lapses within a finding, for this cluster.
   *
   * Scoped to the finding as well as the object, so deferring a pod's restart
   * loop says nothing about the same pod failing to mount a volume tomorrow.
   */
  function snoozedUntil(findingId: string, namespace: string, name: string): number {
    return preferences.snoozedUntil(session.cluster.id, findingId, namespace, name)
  }

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
      <!-- Verdict, and the findings behind it.
           The findings used to be a section of their own below this card, and
           two of them filled the screen before the operator had read the
           verdict that summarises them. The card IS the alarm; the detail is a
           click away, and stays where it was left. -->
      <section class="rounded-sm border {health.classes}">
      <div class="flex flex-wrap items-center gap-4 p-4">
        <HealthIcon class="size-8 shrink-0" strokeWidth={1.6} />

        <div class="min-w-0 flex-1">
          <h2 class="text-title-large font-semibold">{health.title}</h2>
          <p class="text-body-medium opacity-80">
            {#if issues.length === 0}
              {overview.nodes.ready} of {overview.nodes.total} nodes ready · {overview.pods.total} pods
              {#if notes.length > 0}· {notes.length} {notes.length === 1 ? 'note' : 'notes'}{/if}
            {:else}
              {criticalCount > 0 ? `${criticalCount} critical` : ''}
              {criticalCount > 0 && warningCount > 0 ? ' · ' : ''}
              {warningCount > 0 ? `${warningCount} warning` : ''}
              {issues.length === 1 ? ' finding' : ' findings'}
            {/if}
            <!-- Always said, and said here. A cluster that reads as healthy
                 only because somebody deferred its two warnings is not the
                 same cluster as one with nothing wrong, and the difference
                 belongs next to the verdict rather than three clicks away. -->
            {#if snoozed.length > 0}· {snoozed.length} snoozed{/if}
          </p>
        </div>

        <dl class="flex shrink-0 flex-wrap gap-x-6 gap-y-1 text-body-small">
          <div class="flex flex-col">
            <dt class="opacity-70">Version</dt>
            <dd class="flex items-center gap-1.5 tabular-nums">
              {overview.version || '—'}
              <!-- Said where the version already is. A control plane past end
                   of life receives no fix for a vulnerability disclosed
                   tomorrow, and nothing in Kubernetes reports that — the
                   number simply sits there looking like any other. Silent
                   when the table does not cover the release: claiming a fresh
                   version is unsupported would be worse than saying nothing. -->
              {#if overview.support.state === 'unknown' && overview.support.minor}
                <!-- Newer than the table this build was compiled with, which
                     is a fact about PodSteer and not about the cluster. Said
                     quietly, and only on hover, because it is not a problem. -->
                <span
                  class="text-label-small text-on-surface-variant/50"
                  title="This build's support table was compiled on {overview.support.compiledAt} and does not cover {overview.support.minor}"
                >
                  ?
                </span>
              {:else if overview.support.state === 'ended' || overview.support.state === 'ending'}
                <span
                  class="rounded-full px-1.5 py-0.5 text-label-small
                         {overview.support.state === 'ended'
                           ? 'bg-warning-container text-on-warning-container'
                           : 'bg-surface-container-high text-on-surface-variant'}"
                  title={overview.support.state === 'ended'
                    ? `${overview.support.minor} stopped receiving patches on ${overview.support.endOfLife}`
                    : `${overview.support.minor} stops receiving patches on ${overview.support.endOfLife}`}
                >
                  {overview.support.state === 'ended' ? 'End of life' : `${overview.support.days}d left`}
                </span>
              {/if}
            </dd>
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
      </div>

      {#if issues.length + snoozed.length > 0}
        <!-- Full width and inside the card's tint, so the toggle reads as
             belonging to the verdict rather than floating between sections. -->
        <button
          type="button"
          onclick={preferences.toggleFindings}
          aria-expanded={preferences.findingsExpanded}
          aria-controls="overview-findings"
          class="state-layer flex w-full items-center gap-1.5 border-t border-current/15 px-4 py-2
                 text-left text-label-large opacity-80 transition-opacity duration-100 hover:opacity-100"
        >
          <ChevronRight
            class="size-4 shrink-0 transition-transform duration-150
                   {preferences.findingsExpanded ? 'rotate-90' : ''}"
            strokeWidth={2}
          />
          {preferences.findingsExpanded ? 'Hide details' : 'Show details'}
        </button>

        {#if preferences.findingsExpanded}
          <!-- One distance throughout: the gap between findings, the padding
               around them and the space below the toggle are all the same, so
               each finding reads as its own card rather than as a row in a
               stack. The toggle carries its own hover background, so its
               padding is part of that control and not part of this gap. -->
          <div id="overview-findings" class="flex flex-col gap-4 p-4">
            {#each issues as finding (finding.id)}
              <FindingCard
                {finding}
                snoozedUntil={(namespace, name) => snoozedUntil(finding.id, namespace, name)}
                onopen={openList}
                onselect={openObject}
                onsnooze={(namespace, name, durationMs) =>
                  preferences.snooze(session.cluster.id, finding.id, namespace, name, durationMs)}
                onunsnooze={(namespace, name) =>
                  preferences.unsnooze(session.cluster.id, finding.id, namespace, name)}
              />
            {/each}

            <!-- Below the live ones, in the order attention is owed. -->
            {#each snoozed as finding (finding.id)}
              <FindingCard
                {finding}
                snoozedUntil={(namespace, name) => snoozedUntil(finding.id, namespace, name)}
                onopen={openList}
                onselect={openObject}
                onsnooze={(namespace, name, durationMs) =>
                  preferences.snooze(session.cluster.id, finding.id, namespace, name, durationMs)}
                onunsnooze={(namespace, name) =>
                  preferences.unsnooze(session.cluster.id, finding.id, namespace, name)}
              />
            {/each}
          </div>
        {/if}
      {/if}
      </section>

      <!-- A source that could not be read is stated, never silently zeroed. -->
      {#if overview.unavailable.length > 0}
        <p
          class="flex items-center gap-2 rounded-sm border border-outline-variant/40 bg-surface-container-low
                 px-3 py-2 text-body-small text-on-surface-variant"
        >
          <CircleSlash class="size-4 shrink-0 text-on-surface-variant/60" strokeWidth={1.8} />
          Assessed without {overview.unavailable.join(', ')} — those figures are missing rather than zero.
        </p>
      {/if}

      <!-- Capacity -->
      <section class="flex flex-col gap-3 rounded-sm border border-outline-variant/40 bg-surface-container-low p-4">
        <div class="flex items-baseline justify-between gap-3">
          <h3 class="flex items-center gap-2 text-title-medium font-semibold text-on-surface">
            <Gauge class="size-4 text-on-surface-variant" strokeWidth={1.8} />
            Capacity
          </h3>
          <span class="text-body-small text-on-surface-variant/70">
            Requests decide what schedules, not usage
          </span>
        </div>

        <!-- Two by two. Three columns squeezed each bar's four figures into
             a third of the window, where the values wrapped and collided into
             their neighbour; a denser layout that cannot be read is worse
             than an emptier one.

             Pod slots is a peer of the resource tracks rather than a footnote
             below them. It is the limit that catches people out — a node
             refuses pods at its cap however much CPU and memory are free — so
             a cluster can be 9% committed everywhere else and still be full. -->
        <div class="grid gap-x-8 gap-y-5 md:grid-cols-2">
          <!-- The unit lives in the label now, so the figures below stay bare
               numbers and the rule between amount and share lines up across
               all four tracks. -->
          <CapacityBar label="CPU cores" usage={overview.capacity.cpu} />
          <CapacityBar label="Memory" usage={overview.capacity.memory} />

          <!-- Only when the cluster reports any. A node that never published
               ephemeral-storage capacity would otherwise get a track reading
               zero of zero, which asserts something the API never said. -->
          {#if hasEphemeral}
            <CapacityBar
              label="Ephemeral storage"
              usage={overview.capacity.ephemeral}
              note={ephemeralNote}
              fourth={fullestDisk}
            />
          {/if}

          <SlotsBar capacity={overview.capacity.pods} />
        </div>
      </section>

      <!-- Trend.
           Kubernetes reports only the present, so this is PodSteer's own
           record: sampled every 30 seconds while the application is open. It
           says so rather than implying the completeness a monitoring stack
           would have. -->
      <section class="flex flex-col gap-3 rounded-sm border border-outline-variant/40 bg-surface-container-low p-4">
        <div class="flex flex-wrap items-center justify-between gap-3">
          <h3 class="flex items-center gap-2 text-title-medium font-semibold text-on-surface">
            <TrendingUp class="size-4 text-on-surface-variant" strokeWidth={1.8} />
            Trend
          </h3>

          <div class="flex items-center gap-3">
            <!-- Metric -->
            <div class="flex rounded-sm border border-outline-variant/60 p-0.5">
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
            <div class="flex rounded-sm border border-outline-variant/60 p-0.5">
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
            Collecting. PodSteer samples every {formatAge(history.intervalSeconds)} while it is
            open, so a line appears once a second sample lands.
          </p>
        {:else}
          <TrendChart samples={history.samples} {metric} />
          <p class="text-body-small text-on-surface-variant/60">
            Covering the last {formatAge(history.spanSeconds)} that PodSteer has been open on this
            cluster — not the cluster's whole history.
          </p>
        {/if}
      </section>

      <div class="grid gap-4 lg:grid-cols-2">
        <!-- Inventory -->
        <section class="flex flex-col gap-3 rounded-sm border border-outline-variant/40 bg-surface-container-low p-4">
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
        <section class="flex flex-col gap-3 rounded-sm border border-outline-variant/40 bg-surface-container-low p-4">
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

            <!-- Named individually. Disk, memory and process IDs are three
                 different jobs fixed in three different places, and one line
                 saying "3 under pressure" only sends somebody to the node list
                 to find out which of them it is. -->
            <!-- Disk occupancy, which no other view in PodSteer can show:
                 the API server does not know how full a node's disk is, so
                 this is the kubelets' own answer. Only rendered when at least
                 one of them gave it. -->
            {#if overview.nodes.disks.measured > 0}
              <dt class="text-on-surface-variant" title="Across {overview.nodes.disks.measured} of {overview.nodes.total} nodes">
                Fullest disk
              </dt>
              <dd
                class="text-right tabular-nums {overview.nodes.disks.fullestPercent >= 90
                  ? 'text-error'
                  : overview.nodes.disks.fullestPercent >= 80
                    ? 'text-warning'
                    : 'text-on-surface'}"
                title={overview.nodes.disks.fullestNode}
              >
                {Math.round(overview.nodes.disks.fullestPercent)}%
              </dd>
            {/if}

            {#each overview.nodes.pressure as pressure (pressure.condition)}
              <dt class="text-on-surface-variant">{PRESSURE_LABELS[pressure.condition] ?? pressure.condition}</dt>
              <dd class="text-right tabular-nums text-warning">{pressure.nodes}</dd>
            {/each}

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

      <!-- Persistent storage.
           Provisioned rather than used, and said plainly: how full a volume is
           belongs to the workload that mounted it and is in no API PodSteer
           can reach. What IS knowable — what has been provisioned, what is
           waiting, what nobody uses any more — is the part nothing else
           surfaces, and the last of those is a bill somebody is still paying. -->
      {#if overview.storage.totalVolumes > 0 || overview.storage.totalClaims > 0}
        {@const storage = overview.storage}
        <section class="flex flex-col gap-3 rounded-sm border border-outline-variant/40 bg-surface-container-low p-4">
          <div class="flex items-baseline justify-between gap-3">
            <h3 class="flex items-center gap-2 text-title-medium font-semibold text-on-surface">
              <HardDrive class="size-4 text-on-surface-variant" strokeWidth={1.8} />
              Persistent storage
            </h3>
            <span class="text-body-small text-on-surface-variant/70">
              Provisioned, not consumed
            </span>
          </div>

          <div class="grid gap-4 sm:grid-cols-3">
            <div>
              <p class="text-title-large tabular-nums text-on-surface">{storage.provisioned}</p>
              <p class="text-label-small uppercase text-on-surface-variant">
                Bound across {storage.totalVolumes}
                {storage.totalVolumes === 1 ? 'volume' : 'volumes'}
              </p>
            </div>
            <div>
              <p class="text-title-large tabular-nums text-on-surface">{storage.totalClaims}</p>
              <p class="text-label-small uppercase text-on-surface-variant">Claims</p>
            </div>
            <div>
              <p
                class="text-title-large tabular-nums {storage.orphanedBytes > 0
                  ? 'text-warning'
                  : 'text-on-surface-variant/50'}"
              >
                {storage.orphanedBytes > 0 ? storage.orphaned : '—'}
              </p>
              <p
                class="text-label-small uppercase text-on-surface-variant"
                title="Released volumes whose reclaim policy keeps them, so nothing will ever remove them"
              >
                Unused, retained
              </p>
            </div>
          </div>

          <!-- Phases, only the ones that exist. A cluster with nothing Lost
               should say nothing about Lost. -->
          {#if storage.claims.length > 0 || storage.volumes.length > 0}
            <div class="flex flex-wrap gap-x-5 gap-y-1 border-t border-outline-variant/40 pt-3 text-body-small">
              {#each storage.claims as phase (phase.phase)}
                <span class="tabular-nums {PHASE_TONE[phase.phase] ?? 'text-on-surface-variant'}">
                  <span class="text-on-surface-variant/70">Claims {phase.phase.toLowerCase()}</span>
                  {phase.count}
                </span>
              {/each}
              {#each storage.volumes as phase (phase.phase)}
                <span class="tabular-nums {PHASE_TONE[phase.phase] ?? 'text-on-surface-variant'}">
                  <span class="text-on-surface-variant/70">Volumes {phase.phase.toLowerCase()}</span>
                  {phase.count}
                </span>
              {/each}
            </div>
          {/if}

          <!-- By class, because a cluster quietly paying for premium disks it
               did not mean to buy cannot see that anywhere else. -->
          {#if storage.classes.length > 0}
            <div class="flex flex-col gap-1.5 border-t border-outline-variant/40 pt-3">
              {#each storage.classes as class_ (class_.name)}
                <div class="flex items-center gap-3">
                  <span class="w-40 shrink-0 truncate text-body-small text-on-surface" title={class_.name}>
                    {class_.name}
                  </span>
                  <div class="h-2 flex-1 overflow-hidden rounded-full bg-surface-container-highest">
                    <span
                      class="block h-full rounded-full bg-primary/45 transition-all duration-300 ease-standard"
                      style="width: {Math.max(2, class_.share)}%"
                    ></span>
                  </div>
                  <span class="w-28 shrink-0 text-right text-body-small tabular-nums text-on-surface-variant">
                    {class_.size}
                    <span class="text-on-surface-variant/60">×{class_.volumes}</span>
                  </span>
                </div>
              {/each}
            </div>
          {/if}
        </section>
      {/if}

      <!-- Per-node load. The cluster totals above cannot distinguish an
           evenly loaded cluster from one where half the nodes are full, and
           only the second explains a pod that will not schedule on a cluster
           reading 46% requested. -->
      {#if overview.nodeLoads.length > 1}
        <section class="flex flex-col gap-3 rounded-sm border border-outline-variant/40 bg-surface-container-low p-4">
          <div class="flex items-baseline justify-between gap-3">
            <h3 class="flex items-center gap-2 text-title-medium font-semibold text-on-surface">
              <Server class="size-4 text-on-surface-variant" strokeWidth={1.8} />
              Load per node
            </h3>
            <span class="text-body-small text-on-surface-variant/70">Busiest first</span>
          </div>

          <NodeLoadChart
            loads={overview.nodeLoads}
            onselect={(name) => void openObject('core/v1/nodes', name, '')}
          />
        </section>
      {/if}

      <!-- What is actually using the cluster.
           Two rankings rather than one: the pod holding the most CPU and the
           pod holding the most memory are usually different pods, and one
           combined "biggest" would hide whichever dimension is under pressure.
           Each row carries the reservation beside the usage, because usage
           alone cannot tell a busy pod from one sized wrong. -->
      {#if overview.consumers.measured && overview.consumers.byCpu.length > 0}
        <section class="flex flex-col gap-3 rounded-sm border border-outline-variant/40 bg-surface-container-low p-4">
          <div class="flex items-baseline justify-between gap-3">
            <h3 class="flex items-center gap-2 text-title-medium font-semibold text-on-surface">
              <Flame class="size-4 text-on-surface-variant" strokeWidth={1.8} />
              Top consumers
            </h3>
            <span class="text-body-small text-on-surface-variant/70">Measured now, against what was reserved</span>
          </div>

          <div class="grid gap-x-6 gap-y-4 lg:grid-cols-2">
            {#each [{ id: 'cpu', label: 'CPU', rows: overview.consumers.byCpu }, { id: 'memory', label: 'Memory', rows: overview.consumers.byMemory }] as column (column.id)}
              <div class="flex flex-col gap-1.5">
                <p class="text-label-small uppercase tracking-wider text-on-surface-variant">
                  {column.label}
                </p>

                {#each column.rows as row (row.namespace + '/' + row.name)}
                  <button
                    type="button"
                    onclick={() => openObject('core/v1/pods', row.name, row.namespace)}
                    class="state-layer group flex w-full flex-col gap-1 rounded-xs px-1.5 py-1 text-left
                           transition-colors duration-100 hover:bg-surface-container"
                  >
                    <span class="flex w-full items-baseline gap-2">
                      <span class="min-w-0 flex-1 truncate text-body-small text-on-surface" title="{row.namespace}/{row.name}">
                        <span class="text-on-surface-variant/60">{row.namespace}/</span>{row.name}
                      </span>
                      <span class="shrink-0 text-body-small tabular-nums text-on-surface">{row.usage}</span>
                      <!-- Over its reservation is the finding. A pod with no
                           reservation says so rather than showing nothing. -->
                      <span
                        class="w-16 shrink-0 text-right text-label-small tabular-nums
                               {row.share < 0
                                 ? 'text-on-surface-variant/50'
                                 : row.share >= 100
                                   ? 'text-warning'
                                   : 'text-on-surface-variant/70'}"
                        title={row.share < 0
                          ? 'Nothing reserved'
                          : `${row.usage} used of ${row.request} reserved`}
                      >
                        {row.share < 0 ? 'no request' : `${Math.round(row.share)}%`}
                      </span>
                    </span>
                    <span class="h-1.5 w-full overflow-hidden rounded-full bg-surface-container-highest">
                      <span
                        class="block h-full rounded-full bg-primary/45 transition-all duration-300 ease-standard"
                        style="width: {Math.max(2, row.percent)}%"
                      ></span>
                    </span>
                  </button>
                {/each}
              </div>
            {/each}
          </div>
        </section>
      {/if}

      <div class="grid gap-4 lg:grid-cols-2">
        <!-- Namespaces, ranked by what they reserve rather than what they use:
             reservations are what fill a cluster. -->
        <section class="flex flex-col gap-3 rounded-sm border border-outline-variant/40 bg-surface-container-low p-4">
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
        <section class="flex flex-col gap-3 rounded-sm border border-outline-variant/40 bg-surface-container-low p-4">
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
