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
  import GaugeTrack from '$lib/components/GaugeTrack.svelte'
  import InfoHint from '$lib/components/InfoHint.svelte'
  import type { Figure } from '$lib/components/CapacityFigures.svelte'
  import SlotsBar from '$lib/components/SlotsBar.svelte'
  import FindingCard from '$lib/components/FindingCard.svelte'
  import NodeLoadGrid from '$lib/components/NodeLoadGrid.svelte'
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

  /**
   * The six facts the Nodes card states, in the order they are asked for.
   *
   * Built here rather than inline so the card and the card beside it can be
   * kept the same length: Workloads lists six kinds, so this lists six rows,
   * and a row with nothing to say shows a dash instead of vanishing and
   * shortening the card.
   */
  const nodeRows = $derived.by(() => {
    const nodes = overview?.nodes
    if (!nodes) return []

    const versions = nodes.kubeletVersions
    const skewed = versions.length > 1

    return [
      {
        label: 'Schedulable',
        value: `${nodes.schedulable}/${nodes.total}`,
        title: 'Ready, uncordoned and carrying no blocking taint',
        tone: nodes.schedulable === 0 ? 'text-warning' : undefined,
      },
      {
        label: 'Tainted',
        value: `${nodes.tainted}/${nodes.total}`,
        title: 'Refuse pods that do not tolerate their taint',
      },
      {
        label: 'Control plane',
        value: `${nodes.controlPlane}/${nodes.total}`,
      },
      // A dash rather than 0% when no kubelet answered: nothing in the core
      // API knows how full a disk is, and reporting zero would say the
      // opposite of "nobody was asked".
      nodes.disks.measured > 0
        ? {
            label: 'Fullest disk',
            value: `${Math.round(nodes.disks.fullestPercent)}%`,
            title: `${nodes.disks.fullestNode} — across ${nodes.disks.measured} of ${nodes.total} nodes`,
            tone:
              nodes.disks.fullestPercent >= 90
                ? 'text-error'
                : nodes.disks.fullestPercent >= 80
                  ? 'text-warning'
                  : undefined,
          }
        : {
            label: 'Fullest disk',
            value: '—',
            tone: 'text-on-surface-variant/50',
            title: 'No kubelet could be read, so disk occupancy is unknown',
          },
      // One version is a fact; more than one is usually an upgrade that
      // stopped part-way, so the breakdown moves behind an icon and the
      // figure itself carries the warning.
      skewed
        ? {
            label: 'Kubelet',
            value: `${versions.length} versions`,
            tone: 'text-warning',
            hint: versions
              .map((entry) => `${entry.version} on ${entry.nodes} ${entry.nodes === 1 ? 'node' : 'nodes'}`)
              .join(', '),
          }
        : {
            label: 'Kubelet',
            value: versions[0]?.version ?? '—',
          },
      {
        label: 'Oldest',
        value: formatAge(nodes.oldestSeconds),
        title: "Age of the longest-lived node, a fair proxy for the cluster's own",
      },
    ]
  })

  /** How many claims are in one phase, zero when none are. */
  function claimPhase(phase: string): number {
    return overview?.storage.claims.find((entry) => entry.phase === phase)?.count ?? 0
  }

  /**
   * What the storage card states, in the order it is asked for.
   *
   * Volumes and claims are counted separately rather than assumed equal: they
   * are one-to-one on a healthy cluster and the moment they are not is
   * exactly what this card exists to show.
   */
  const storageRows = $derived.by(() => {
    const storage = overview?.storage
    if (!storage) return []

    const volumesBound = storage.volumes.find((entry) => entry.phase === 'Bound')?.count ?? 0

    return [
      {
        label: 'Provisioned',
        value: storage.provisioned,
        title: 'Total size of every bound volume',
      },
      {
        label: 'Volumes',
        value: `${volumesBound}/${storage.totalVolumes}`,
        title: 'Bound of every volume the cluster has',
      },
      {
        label: 'Claims',
        value: `${claimPhase('Bound')}/${storage.totalClaims}`,
        title: 'Bound of every claim workloads have made',
      },
      {
        label: 'Largest volume',
        value: storage.largestBytes > 0 ? storage.largest : '—',
        tone: storage.largestBytes > 0 ? undefined : 'text-on-surface-variant/50',
        title: storage.largestName || 'No bound volumes',
      },
      // Both of these are usually nothing, and both are worth a permanent row
      // rather than one that appears only once there is bad news: a zero
      // states that somebody checked.
      {
        label: 'Waiting to bind',
        value: storage.unboundBytes > 0 ? storage.unbound : '—',
        tone: storage.unboundBytes > 0 ? 'text-warning' : 'text-on-surface-variant/50',
        title: 'Storage claims have asked for and not received',
      },
      {
        label: 'Unused, retained',
        value: storage.orphanedBytes > 0 ? storage.orphaned : '—',
        tone: storage.orphanedBytes > 0 ? 'text-warning' : 'text-on-surface-variant/50',
        title:
          'Released volumes whose reclaim policy keeps them, so nothing will ever remove them',
      },
    ]
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
                    class="resource-link flex w-full items-center gap-3 py-1.5 text-left"
                  >
                    <!-- No colour of its own, or it would beat the one the
                         link gives it and never change on hover. -->
                    <span class="flex-1 truncate text-body-medium">{kind.title}</span>
                    {#if kind.rolling > 0}
                      <span class="text-body-small text-primary" title="Rollout in progress">
                        {kind.rolling} rolling
                      </span>
                    {/if}
                    {#if kind.degraded > 0}
                      <span class="text-body-small text-warning">{kind.degraded} degraded</span>
                    {/if}
                    <span class="w-16 shrink-0 text-right text-body-medium tabular-nums text-on-surface-variant">
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

        <!-- Nodes.
             The same shape as Workloads beside it, down to the row count: six
             facts, then the three states worth a glance. Nothing sits below
             the footer, which is why the kubelet versions became a row rather
             than a block — a card with a tail its neighbour does not have
             reads as a different kind of card.

             Every row is always present, an unavailable one showing a dash
             rather than disappearing. A card whose height changes with what
             could be measured is one that moves the page under the reader. -->
        <section class="flex flex-col gap-3 rounded-sm border border-outline-variant/40 bg-surface-container-low p-4">
          <h3 class="flex items-center gap-2 text-title-medium font-semibold text-on-surface">
            <Server class="size-4 text-on-surface-variant" strokeWidth={1.8} />
            Nodes
          </h3>

          <ul class="flex flex-col divide-y divide-outline-variant/30">
            {#each nodeRows as row (row.label)}
              <li class="flex items-center gap-3 py-1.5">
                <!-- The truncation belongs to the text alone. Wrapping the
                     hint in it too clipped the tooltip: `truncate` is
                     `overflow: hidden`, which an absolutely positioned child
                     cannot escape, so the panel opened and was never drawn. -->
                <span class="flex min-w-0 flex-1 items-center gap-1.5">
                  <span class="truncate text-body-medium text-on-surface" title={row.title}>
                    {row.label}
                  </span>
                  {#if row.hint}
                    <InfoHint text={row.hint} label="About {row.label.toLowerCase()}" />
                  {/if}
                </span>
                <span
                  class="shrink-0 text-right text-body-medium tabular-nums {row.tone ??
                    'text-on-surface-variant'}"
                >
                  {row.value}
                </span>
              </li>
            {/each}
          </ul>

          <div class="mt-1 grid grid-cols-3 gap-3 border-t border-outline-variant/40 pt-3 text-center">
            <div>
              <p class="text-title-large tabular-nums text-on-surface">{overview.nodes.ready}</p>
              <p class="text-label-small uppercase text-on-surface-variant">Ready</p>
            </div>
            <div>
              <p
                class="text-title-large tabular-nums {overview.nodes.notReady > 0
                  ? 'text-error'
                  : 'text-on-surface'}"
              >
                {overview.nodes.notReady}
              </p>
              <p class="text-label-small uppercase text-on-surface-variant">Not ready</p>
            </div>
            <div>
              <p
                class="text-title-large tabular-nums {overview.nodes.cordoned > 0
                  ? 'text-warning'
                  : 'text-on-surface'}"
              >
                {overview.nodes.cordoned}
              </p>
              <p class="text-label-small uppercase text-on-surface-variant">Cordoned</p>
            </div>
          </div>
        </section>
      </div>

      <!-- Persistent storage.
           The same language as the two cards above it: divided rows on the
           left, the totals along the bottom, and regular type throughout. The
           width it has spare goes to the class breakdown rather than to
           bigger numbers — a headline set in 30pt is not more informative
           than the same figure in a row, it is just louder.

           Provisioned rather than consumed, said out loud. How full a volume
           is belongs to the workload that mounted it and is in no API
           PodSteer can reach without a per-CSI exporter, so claiming
           otherwise would be inventing a number. -->
      {#if overview.storage.totalVolumes > 0 || overview.storage.totalClaims > 0}
        {@const storage = overview.storage}
        <section class="flex flex-col gap-3 rounded-sm border border-outline-variant/40 bg-surface-container-low p-4">
          <div class="flex items-baseline justify-between gap-3">
            <h3 class="flex items-center gap-2 text-title-medium font-semibold text-on-surface">
              <HardDrive class="size-4 text-on-surface-variant" strokeWidth={1.8} />
              Persistent storage
            </h3>
            <span class="text-body-small text-on-surface-variant/70">Provisioned, not consumed</span>
          </div>

          <div class="grid gap-x-8 gap-y-3 md:grid-cols-2">
            <ul class="flex flex-col divide-y divide-outline-variant/30">
              {#each storageRows as row (row.label)}
                <li class="flex items-center gap-3 py-1.5">
                  <span class="flex min-w-0 flex-1 items-center gap-1.5">
                    <span class="truncate text-body-medium text-on-surface" title={row.title}>
                      {row.label}
                    </span>
                  </span>
                  <span
                    class="shrink-0 text-right text-body-medium tabular-nums {row.tone ??
                      'text-on-surface-variant'}"
                  >
                    {row.value}
                  </span>
                </li>
              {/each}
            </ul>

            <!-- By class, because a cluster quietly paying for premium disks
                 it did not mean to buy cannot see that anywhere else. -->
            {#if storage.classes.length > 0}
              <div class="flex flex-col gap-1.5">
                <p class="text-label-small uppercase tracking-wider text-on-surface-variant">
                  By storage class
                </p>
                {#each storage.classes as class_ (class_.name)}
                  <div class="flex items-center gap-3">
                    <span
                      class="w-32 shrink-0 truncate text-body-medium text-on-surface"
                      title={class_.name}
                    >
                      {class_.name}
                    </span>
                    <!-- A share of the total, not a utilisation, so it takes
                         no thresholds: a class holding every volume in the
                         cluster is not "critical", it is the only class. One
                         neutral colour from the same palette. -->
                    <div class="h-2 flex-1 overflow-hidden rounded-full bg-surface-container-highest">
                      <span
                        class="block h-full rounded-full bg-gauge-normal/70 transition-all duration-300 ease-standard"
                        style="width: {Math.max(2, class_.share)}%"
                      ></span>
                    </div>
                    <span
                      class="w-28 shrink-0 text-right text-body-medium tabular-nums text-on-surface-variant"
                    >
                      {class_.size}
                      <span class="text-body-small text-on-surface-variant/60">×{class_.volumes}</span>
                    </span>
                  </div>
                {/each}
              </div>
            {/if}
          </div>

          <!-- Claims by phase, where the other two cards put their pod and
               node states. Bound is the working state; the other two are the
               ones somebody has to do something about. -->
          <div class="mt-1 grid grid-cols-3 gap-3 border-t border-outline-variant/40 pt-3 text-center">
            <div>
              <p class="text-title-large tabular-nums text-on-surface">{claimPhase('Bound')}</p>
              <p class="text-label-small uppercase text-on-surface-variant">Bound</p>
            </div>
            <div>
              <p
                class="text-title-large tabular-nums {claimPhase('Pending') > 0
                  ? 'text-warning'
                  : 'text-on-surface'}"
              >
                {claimPhase('Pending')}
              </p>
              <p class="text-label-small uppercase text-on-surface-variant">Pending</p>
            </div>
            <div>
              <p
                class="text-title-large tabular-nums {claimPhase('Lost') > 0
                  ? 'text-error'
                  : 'text-on-surface'}"
              >
                {claimPhase('Lost')}
              </p>
              <p class="text-label-small uppercase text-on-surface-variant">Lost</p>
            </div>
          </div>
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

          <NodeLoadGrid
            loads={overview.nodeLoads}
            onselect={(name) => void openObject('core/v1/nodes', name, '')}
          />
        </section>
      {/if}

      <!-- What is actually using the cluster.
           Two rankings rather than one: the pod holding the most CPU and the
           pod holding the most memory are usually different pods, and one
           combined "biggest" would hide whichever dimension is under
           pressure.

           Laid out like the node grid beside it — the dimension in the weight
           a node name carries, the pod on its own line beneath, then the bar
           and its figures — because they answer the same question from
           opposite ends: which node is full, and what filled it. -->
      {#if overview.consumers.measured && overview.consumers.byCpu.length > 0}
        <section class="flex flex-col gap-3 rounded-sm border border-outline-variant/40 bg-surface-container-low p-4">
          <div class="flex items-baseline justify-between gap-3">
            <h3 class="flex items-center gap-2 text-title-medium font-semibold text-on-surface">
              <Flame class="size-4 text-on-surface-variant" strokeWidth={1.8} />
              Top consumers
            </h3>
            <span class="text-body-small text-on-surface-variant/70">
              Measured now, against what was reserved
            </span>
          </div>

          <div class="grid gap-x-10 gap-y-5 lg:grid-cols-2">
            {#each [{ id: 'cpu', label: 'CPU', rows: overview.consumers.byCpu }, { id: 'memory', label: 'Memory', rows: overview.consumers.byMemory }] as column (column.id)}
              <div class="flex min-w-0 flex-col gap-2">
                <p class="text-label-large text-on-surface">{column.label}</p>

                {#each column.rows as row (row.namespace + '/' + row.name)}
                  <button
                    type="button"
                    onclick={() => openObject('core/v1/pods', row.name, row.namespace)}
                    class="resource-link flex w-full min-w-0 flex-col gap-1 text-left"
                  >
                    <!-- The pod alone on its line. Namespaced names are long
                         enough that sharing a row with the figures left them
                         truncated to the point of being unidentifiable. -->
                    <span
                      class="min-w-0 truncate text-body-medium"
                      title="{row.namespace}/{row.name} on {row.node}"
                    >
                      <span class="text-on-surface-variant/60">{row.namespace}/</span>{row.name}
                    </span>

                    <span class="flex w-full items-center gap-3">
                      <!-- Usage against this pod's OWN reservation, not its
                           rank in the list. The ranking is already carried by
                           the order and by the amount printed beside it,
                           whereas nothing else here says whether a pod has
                           outgrown what it asked for — which is the question
                           the card's own subtitle poses. Being a utilisation,
                           it takes the same bands and marks as every other
                           bar; a pod that reserved nothing gets an empty
                           track, because there is no reservation to measure
                           it against. -->
                      <GaugeTrack
                        value={row.share}
                        height="h-1.5"
                        width="min-w-0 flex-1"
                        label="{row.name} against its reservation"
                      />

                      <span
                        class="flex shrink-0 items-baseline gap-2 text-body-medium tabular-nums
                               text-on-surface-variant"
                      >
                        <span class="w-16 text-right">{row.usage}</span>
                        <span
                          aria-hidden="true"
                          class="text-outline-variant {row.shareLabel ? '' : 'invisible'}">|</span
                        >
                        <!-- Plain, like every other figure beside a bar. The
                             bar carries the overrun now that it measures a
                             pod against its own reservation; it did not when
                             it was a ranking, which is why this used to be
                             the one tinted figure on the page. -->
                        <span
                          class="w-[4.5ch] text-right"
                          title={row.shareLabel
                            ? `${row.usage} used of ${row.request} reserved`
                            : 'Nothing reserved'}
                        >
                          {row.shareLabel || '—'}
                        </span>
                      </span>
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
            <!-- Laid out like the consumers and the node grid: the name on
                 its own line, then the bar with its figures. What a namespace
                 has RESERVED is the reading, because reservations are what
                 fill a cluster — the usage beside it is how much of that
                 reservation is real. -->
            <ul class="flex flex-col gap-2">
              {#each overview.namespaces as load (load.name)}
                <li class="flex min-w-0 flex-col gap-1">
                  <div class="flex items-baseline justify-between gap-3">
                    <button
                      type="button"
                      onclick={() => session.selectNamespace(load.name)}
                      class="resource-link min-w-0 truncate text-body-medium"
                      title="Filter everything to {load.name}"
                    >
                      {load.name}
                    </button>
                    <span class="shrink-0 text-body-small tabular-nums text-on-surface-variant/70">
                      {load.pods} pod{load.pods === 1 ? '' : 's'}{load.notReady > 0
                        ? `, ${load.notReady} down`
                        : ''}
                    </span>
                  </div>

                  <div class="flex items-center gap-3">
                    <GaugeTrack
                      value={load.cpuShare}
                      height="h-1.5"
                      width="min-w-0 flex-1"
                      label="{load.name} share of requested CPU"
                    />
                    <!-- Both halves are quantities here, not a quantity and a
                         percentage, so both need room for a unit: "22.21
                         cores" does not fit the slot a "92%" lives in, and
                         wrapped onto two lines it took the row with it. -->
                    <span
                      class="flex shrink-0 items-baseline gap-2 text-body-medium tabular-nums
                             whitespace-nowrap text-on-surface-variant"
                    >
                      <span class="w-[4.75rem] text-right" title="Requested">
                        {load.cpuRequests}
                      </span>
                      <span
                        aria-hidden="true"
                        class="text-outline-variant {load.measured ? '' : 'invisible'}">|</span
                      >
                      <span
                        class="w-[4.75rem] text-right"
                        title={load.measured
                          ? `${load.cpuUsage} actually used`
                          : 'No metrics, so usage is unknown'}
                      >
                        {load.measured ? load.cpuUsage : ''}
                      </span>
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
                    class="resource-link flex w-full items-center gap-3 py-1.5 text-left"
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
