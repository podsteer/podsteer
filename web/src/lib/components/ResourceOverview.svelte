<!--
  Overview tab showing structured resource information.

  Parses the YAML manifest and displays key information in a readable format.
  Different resource types show different sections (e.g., pods show containers,
  deployments show replica status, etc.).
-->
<script lang="ts">
  import { parse } from 'yaml'
  import type { Node, Pod, Workload } from '$lib/api/client'
  import DetailSection from './DetailSection.svelte'
  import DetailList, { type DetailRow } from './DetailList.svelte'
  import ContainerDetail from './ContainerDetail.svelte'
  import UsageChart from './UsageChart.svelte'
  import MetricsBackendNote from './MetricsBackendNote.svelte'
  import NodePods from './NodePods.svelte'
  import type { MetricsBackend } from '$lib/api/client'
  import { parseQuantity } from '$lib/sort'
  import type { UsageSample } from '$stores/session.svelte'

  interface Props {
    manifest: string | null
    selectedPod?: Pod | null
    selectedWorkload?: Workload | null
    kind?: string
    /** The open object's recent usage, accumulated while the drawer is open. */
    usage?: UsageSample[]
    /** The node the drawer is open on, when it is a node. */
    selectedNode?: Node | null
    /**
     * The cluster the open object belongs to.
     *
     * For the sections that fetch something of their own rather than rendering
     * what the drawer already has — a node's pods are not in any list the
     * drawer holds, because that list is scoped to the namespace being browsed
     * and this question is about the machine.
     */
    clusterId?: string
    /**
     * A monitoring system found running in this cluster, if any. Shown under
     * the charts, because the shortness of our own window is exactly where
     * knowing a longer record exists is worth something.
     */
    backend?: MetricsBackend | null
    /** Whether this cluster serves a kind, so a link is only offered when
        there is somewhere for it to go. */
    canOpen?: (kindName: string) => string | null
    /** Follows a reference to the object it names. */
    onopen?: (kindName: string, name: string, namespace: string) => void
  }

  let {
    manifest,
    selectedPod,
    selectedNode,
    selectedWorkload,
    kind,
    usage = [],
    backend,
    clusterId,
    canOpen,
    onopen,
  }: Props = $props()

  /**
   * Builds the click handler for a reference, or leaves it undefined.
   *
   * Undefined is the important half: a Node row on a cluster whose nodes this
   * account cannot list must render as plain text, not as a link that fails
   * when followed. Offering a dead link is worse than offering none.
   */
  function follow(kindName: string, name: string, namespace = ''): (() => void) | undefined {
    if (!name || !onopen || !canOpen?.(kindName)) return undefined
    return () => onopen(kindName, name, namespace)
  }

  let parsedManifest = $derived.by(() => {
    if (!manifest) return null
    try {
      return parse(manifest)
    } catch {
      return null
    }
  })

  // Extract key information from the manifest
  const metadata = $derived(parsedManifest?.metadata ?? {})
  const spec = $derived(parsedManifest?.spec ?? {})
  const status = $derived(parsedManifest?.status ?? {})

  // Format labels and annotations for display
  const labels = $derived(Object.entries(metadata.labels ?? {}))
  const annotations = $derived(Object.entries(metadata.annotations ?? {}))

  // Pod-specific information
  const containers = $derived(spec.containers ?? [])
  const ephemeralContainers = $derived(spec.ephemeralContainers ?? [])
  const initContainers = $derived(spec.initContainers ?? [])
  const volumes = $derived(spec.volumes ?? [])
  const conditions = $derived(status.conditions ?? [])

  // Deployment-specific information
  const replicas = $derived(spec.replicas ?? 0)
  const strategy = $derived(spec.strategy?.type ?? 'RollingUpdate')

  // --- Rows -----------------------------------------------------------------
  //
  // Built here rather than spelt out in the markup, so each section is one
  // list rather than four hand-written label/value pairs whose classes have to
  // agree with each other and with every other pane. The em dash for a missing
  // value is applied at this boundary: what "absent" means is per-field
  // knowledge, and DetailList has none of it.

  const basicRows = $derived<DetailRow[]>([
    { label: 'Name', value: metadata.name ?? '—' },
    { label: 'Namespace', value: metadata.namespace ?? '—' },
    { label: 'Created', value: formatAge(metadata.creationTimestamp) },
    // Truncated: a UID's length is noise, and nobody reads one — they copy it.
    { label: 'UID', value: metadata.uid ?? '—', truncate: true },
  ])

  /**
   * The pod's controller, as kubectl prints it.
   *
   * Only the CONTROLLING owner. A pod can carry several ownerReferences and
   * exactly one of them is the controller — the object that will recreate it —
   * and the others are bookkeeping nobody navigates to.
   */
  const controller = $derived.by(() => {
    const owners = (metadata.ownerReferences ?? []) as { kind: string; name: string; controller?: boolean }[]
    return owners.find((owner) => owner.controller) ?? null
  })

  const statusRows = $derived.by<DetailRow[]>(() => {
    const rows: DetailRow[] = [
      { label: 'Phase', value: status.phase ?? '—' },
      { label: 'Pod IP', value: status.podIP ?? '—' },
    ]

    // Every pod IP, not only the first. A dual-stack pod has two and the
    // singular field carries whichever the cluster's primary family is,
    // which is exactly the one somebody debugging the OTHER family does not
    // want.
    const ips = ((status.podIPs ?? []) as { ip: string }[]).map((entry) => entry.ip)
    if (ips.length > 1) rows.push({ label: 'Pod IPs', value: ips.join(', ') })

    rows.push({
      label: 'Node',
      value: spec.nodeName ?? '—',
      onclick: follow('Node', spec.nodeName),
    })

    if (controller) {
      rows.push({
        label: 'Controlled by',
        value: `${controller.kind}/${controller.name}`,
        onclick: follow(controller.kind, controller.name, metadata.namespace ?? ''),
      })
    } else {
      // Said out loud, because it is a finding rather than a blank: nothing
      // will recreate this pod. The assessment says so too; this is the
      // field somebody looks at to check.
      rows.push({ label: 'Controlled by', value: 'nothing — this is a bare pod' })
    }

    rows.push({ label: 'Service account', value: spec.serviceAccountName ?? 'default' })
    rows.push({ label: 'QoS Class', value: status.qosClass ?? '—' })

    return rows
  })

  const replicaRows = $derived<DetailRow[]>([
    { label: 'Desired', value: String(status.replicas ?? replicas) },
    { label: 'Ready', value: String(status.readyReplicas ?? 0) },
    { label: 'Available', value: String(status.availableReplicas ?? 0) },
    { label: 'Updated', value: String(status.updatedReplicas ?? 0) },
  ])

  // The rolling-update numbers only exist for a rolling update; on a Recreate
  // strategy they are not zero, they are inapplicable, so the rows are absent
  // rather than showing defaults the cluster is not using.
  const strategyRows = $derived<DetailRow[]>(
    strategy === 'RollingUpdate' && spec.strategy?.rollingUpdate
      ? [
          { label: 'Type', value: strategy },
          { label: 'Max Surge', value: String(spec.strategy.rollingUpdate.maxSurge ?? '25%') },
          {
            label: 'Max Unavailable',
            value: String(spec.strategy.rollingUpdate.maxUnavailable ?? '25%'),
          },
        ]
      : [{ label: 'Type', value: strategy }],
  )

  /**
   * Containers that have died at least once and left a record of how.
   *
   * From the LIVE pod, not the manifest: the manifest tab shows the same
   * `lastState` block, but as raw YAML somebody has to know to look for and
   * an exit code they have to decode themselves. This is the pod's own
   * account of what happened, which is the thing an operator opened the pane
   * to find.
   */
  const deaths = $derived(
    (selectedPod?.containers ?? []).filter((container) => container.lastTermination),
  )

  /**
   * The live status for a container named in the spec.
   *
   * Joined by name because the two halves come from different places: the
   * spec is parsed from the manifest, the status arrives on the pod DTO
   * already derived in Go. A container present in the spec with no status yet
   * is normal — it is a pod that has not started — and returns undefined
   * rather than an empty shape that would render as "not ready".
   */
  function statusFor(name: string) {
    return selectedPod?.containers?.find((container) => container.name === name)
  }

  /**
   * The pod's declared request or limit, in the units the samples use.
   *
   * Parsed back out of the strings Go formatted, so the chart's reference
   * lines and its series are measured the same way. Zero when undeclared,
   * which draws no line — a line at zero would read as "the limit is
   * nothing", the opposite of what an absent limit means.
   */
  function declared(metric: 'cpu' | 'memory', kind: 'request' | 'limit'): number {
    if (!selectedPod) return 0

    const value =
      metric === 'cpu'
        ? kind === 'request'
          ? selectedPod.hasCpuRequest && selectedPod.cpuRequest
          : selectedPod.hasCpuLimit && selectedPod.cpuLimit
        : kind === 'request'
          ? selectedPod.hasMemoryRequest && selectedPod.memoryRequest
          : selectedPod.hasMemoryLimit && selectedPod.memoryLimit

    return typeof value === 'string' ? (parseQuantity(value) ?? 0) : 0
  }

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

  /** How long a container survived before it died, in words. */
  function survived(seconds: number): string {
    if (seconds <= 0) return ''
    if (seconds < 60) return `ran for ${Math.round(seconds)}s`
    if (seconds < 3600) return `ran for ${Math.round(seconds / 60)}m`
    if (seconds < 86400) return `ran for ${Math.round(seconds / 3600)}h`
    return `ran for ${Math.round(seconds / 86400)}d`
  }

  /**
   * Why this pod can go where it goes.
   *
   * Every one of these is a constraint on scheduling, and together they are
   * the answer to "why is this Pending" and "why did it land there" — which
   * is why they belong in one section rather than scattered through Status.
   *
   * Topology spread constraints are here because NO GUI CLIENT SURVEYED SHOWS
   * THEM AT ALL — not Lens, not Headlamp, not Octant, not the Dashboard.
   * `kubectl describe` prints them, so a pane without them is one an operator
   * has to leave.
   */
  const schedulingRows = $derived.by<DetailRow[]>(() => {
    const rows: DetailRow[] = []

    const selectors = Object.entries(spec.nodeSelector ?? {})
    if (selectors.length > 0) {
      rows.push({
        label: 'Node selector',
        value: selectors.map(([key, value]) => `${key}=${value}`).join(', '),
      })
    }

    // Rendered the way kubectl does, including the toleration seconds that
    // several clients drop — "for 300s" is the difference between a pod that
    // rides out a brief node problem and one that is evicted immediately.
    for (const toleration of (spec.tolerations ?? []) as Record<string, unknown>[]) {
      const key = (toleration.key as string) ?? ''
      const effect = toleration.effect ? `:${toleration.effect}` : ''
      const op = toleration.operator === 'Exists' ? 'op=Exists' : `=${toleration.value ?? ''}`
      const seconds =
        toleration.tolerationSeconds !== undefined && toleration.tolerationSeconds !== null
          ? ` for ${toleration.tolerationSeconds}s`
          : ''
      rows.push({ label: 'Toleration', value: `${key}${effect} ${op}${seconds}`.trim() })
    }

    for (const constraint of (spec.topologySpreadConstraints ?? []) as Record<string, unknown>[]) {
      rows.push({
        label: 'Spread',
        value: `max skew ${constraint.maxSkew} across ${constraint.topologyKey}, ` +
          `${constraint.whenUnsatisfiable === 'DoNotSchedule' ? 'or do not schedule' : 'best effort'}`,
      })
    }

    if (spec.priorityClassName) {
      rows.push({ label: 'Priority class', value: String(spec.priorityClassName) })
    }
    // Only when set: hostNetwork is unusual enough that its presence is the
    // information, and a "false" row on every pod would bury that.
    if (spec.hostNetwork) rows.push({ label: 'Host network', value: 'yes — binds the node’s interfaces' })

    return rows
  })

  /**
   * A volume as one line: what it is, then what it points at.
   *
   * The kind first because that is what decides whether it matters — an
   * emptyDir is scratch that dies with the pod, a PVC is data that outlives
   * it, and a projected service-account token is present on every pod and
   * interesting on none.
   */
  const volumeRows = $derived<DetailRow[]>(
    (volumes as Record<string, any>[]).map((volume) => {
      let detail = 'Unknown'
      if (volume.emptyDir) {
        detail = volume.emptyDir.sizeLimit ? `EmptyDir · limit ${volume.emptyDir.sizeLimit}` : 'EmptyDir'
      } else if (volume.configMap) {
        detail = `ConfigMap · ${volume.configMap.name}`
      } else if (volume.secret) {
        detail = `Secret · ${volume.secret.secretName}`
      } else if (volume.persistentVolumeClaim) {
        const claim = volume.persistentVolumeClaim
        detail = `PVC · ${claim.claimName}${claim.readOnly ? ' (ro)' : ''}`
      } else if (volume.hostPath) {
        // Called out: a hostPath mount reaches out of the container onto the
        // node's own filesystem, which is worth noticing among a list of
        // volumes that cannot.
        detail = `HostPath · ${volume.hostPath.path}`
      } else if (volume.projected) {
        const sources = (volume.projected.sources ?? []).length
        detail = `Projected · ${sources} ${sources === 1 ? 'source' : 'sources'}`
      } else {
        detail = Object.keys(volume).filter((key) => key !== 'name')[0] ?? 'Unknown'
      }
      return { label: volume.name, value: detail }
    }),
  )

  /**
   * A condition as one line, with the reason kubectl throws away.
   *
   * `kubectl describe` prints conditions as Type and Status only, which
   * discards the half that explains anything: PodScheduled=False is a fact,
   * and its reason and message are the scheduler's own account of why.
   *
   * Coloured only when False, because for a pod's conditions True is the
   * satisfied state throughout — Ready, Initialized, PodScheduled,
   * ContainersReady, PodReadyToStartContainers. Colouring both would leave a
   * list where every row is coloured and none of them stands out.
   */
  const conditionRows = $derived<DetailRow[]>(
    (conditions as Record<string, string>[]).map((condition) => {
      const explanation = [condition.reason, condition.message].filter(Boolean).join(' — ')
      return {
        label: condition.type,
        value: explanation ? `${condition.status} · ${explanation}` : condition.status,
        tone: condition.status === 'False' ? ('warn' as const) : undefined,
      }
    }),
  )

  /** Turns a metadata map into rows, for labels and annotations. */
  function pairRows(pairs: [string, unknown][], truncate = false): DetailRow[] {
    return pairs.map(([key, value]) => ({ label: key, value: String(value), truncate }))
  }

  function formatAge(timestamp: string): string {
    if (!timestamp) return '—'
    const date = new Date(timestamp)
    const now = new Date()
    const diff = now.getTime() - date.getTime()
    const seconds = Math.floor(diff / 1000)
    const minutes = Math.floor(seconds / 60)
    const hours = Math.floor(minutes / 60)
    const days = Math.floor(hours / 24)

    if (days > 0) return `${days}d`
    if (hours > 0) return `${hours}h`
    if (minutes > 0) return `${minutes}m`
    return `${seconds}s`
  }
</script>

<!--
  Sections are spaced by the container, not by a margin each one carries.
  `{#if}` adds no element, so every section below is a direct child of this
  flex column however deeply the conditionals nest — which is what lets one
  gap govern the lot instead of ten margins that can disagree.
-->
<div class="flex flex-col gap-6 overflow-auto p-4">
  {#if !parsedManifest}
    <p class="text-body-medium text-on-surface-variant">No manifest available</p>
  {:else}
    <!--
      WHAT IS WRONG, OR ABOUT TO BE — first, because a pane that opens on
      "Basic Information" makes somebody read a property list to find out what
      they came for.

      Every other client in this category shows the fields and leaves the
      conclusions to the reader: Headlamp's diagnostics section classifies a
      non-zero exit code as the colour red and reports "Pod has node selector
      constraints" without ever evaluating that constraint against the node.
      These are computed in the Go domain — see AssessPod — so each rule is
      argued with in a test rather than only observed against a real cluster.
    -->
    {#if selectedPod?.findings?.length}
      <DetailSection level="h3" id="findings" title="Worth knowing" hint={String(selectedPod?.findings?.length ?? 0)}>
        <div class="flex flex-col gap-2">
          <!-- By position, for the same reason as DetailList: titles are
               written by rules that can legitimately produce two alike, and a
               duplicate key throws rather than degrading. -->
          {#each selectedPod.findings as finding, index (index)}
            <div
              class="rounded-sm border p-3 {finding.severity === 'critical'
                ? 'border-error/40 bg-error-container/20'
                : finding.severity === 'warning'
                  ? 'border-gauge-warn/40 bg-gauge-warn/10'
                  : 'border-outline-variant bg-surface-container-low'}"
            >
              <p class="text-body-medium font-medium text-on-surface">{finding.title}</p>
              <p class="mt-1 text-body-medium leading-relaxed text-on-surface-variant" data-selectable>
                {finding.detail}
              </p>
              <!-- The advice is the half that makes it a finding rather than
                   an observation, and is set apart so it reads as the answer
                   rather than as more description. -->
              <p class="mt-1.5 text-body-medium leading-relaxed text-on-surface">{finding.advice}</p>
            </div>
          {/each}
        </div>
      </DetailSection>
    {/if}

    <!--
      WHY IT RESTARTED — placed above the container list because when it
      applies it is the reason the pane was opened.

      Every other client shows a restart COUNT. A count says a container
      died seventeen times; it does not say whether the kernel took it for
      memory, whether a rollout stopped it cleanly, or whether the process
      exited on its own — three problems with nothing in common but the
      number reporting them. The sentence comes from the domain, because
      deciding that a 137 WITHOUT an OOMKilled reason is a grace-period
      expiry rather than a memory limit is a judgement, not a lookup.

      Kubernetes keeps ONE prior termination per container, so a container
      with seventeen restarts has sixteen deaths that no longer exist
      anywhere. The heading says "last time" rather than implying a history
      this cannot show.
    -->
    {#if deaths.length > 0}
      <DetailSection level="h3" id="restarts" title="Why it restarted, last time">
        <div class="flex flex-col gap-2">
          {#each deaths as container (container.name)}
            {@const death = container.lastTermination!}
            <div
              class="rounded-sm border p-3 {death.alarming
                ? 'border-error/40 bg-error-container/20'
                : 'border-outline-variant bg-surface-container-low'}"
            >
              <p class="flex flex-wrap items-baseline gap-x-2 text-body-medium">
                <span class="font-medium text-on-surface" data-selectable>{container.name}</span>
                <span class="text-on-surface-variant">
                  {death.reason || 'Terminated'} · exit {death.exitCode}{death.signal
                    ? ` · signal ${death.signal}`
                    : ''}
                </span>
                {#if death.lifetimeSeconds > 0}
                  <span class="text-on-surface-variant/70">{survived(death.lifetimeSeconds)}</span>
                {/if}
                {#if container.restartCount > 1}
                  <!-- Named so the single record is not mistaken for the
                       whole story. -->
                  <span class="text-on-surface-variant/70">
                    · {container.restartCount} restarts in total
                  </span>
                {/if}
              </p>
              <p class="mt-1 text-body-medium leading-relaxed text-on-surface-variant" data-selectable>
                {death.diagnosis}
              </p>
            </div>
          {/each}
        </div>
      </DetailSection>
    {/if}

    <!--
      A NODE'S USAGE, against its allocatable rather than a request and a
      limit. A node has one ceiling and no notion of either — which is why
      the chart takes a list of markers rather than the pod's two by name.

      CPU and memory only. Disk is on this node's row in the list and in the
      overview's findings, but it moves in hours: half an hour of samples
      would draw a flat line and imply the disk is stable when nobody has
      watched it long enough to know.
    -->
    {#if selectedNode?.hasMetrics}
      <DetailSection
        level="h3"
        id="usage"
        title="Usage"
        hint="cpu {selectedNode.cpu} · memory {selectedNode.memory}"
      >
        <div class="flex flex-col gap-4">
          {#each [{ metric: 'cpu' as const, label: 'CPU', allocatable: selectedNode.allocatableCpu }, { metric: 'memory' as const, label: 'Memory', allocatable: selectedNode.allocatableMemory }] as track (track.metric)}
            <div class="flex flex-col gap-1">
              <p class="flex items-baseline justify-between text-body-small text-on-surface-variant">
                <span>{track.label}</span>
                <span class="tabular-nums">
                  {track.metric === 'cpu' ? selectedNode.cpu : selectedNode.memory}
                  of {track.allocatable}
                </span>
              </p>
              <UsageChart
                samples={usage}
                metric={track.metric}
                markers={[
                  { value: parseQuantity(track.allocatable) ?? 0, label: 'allocatable', tone: 'critical' },
                ]}
                format={track.metric === 'cpu' ? formatCores : formatBytes}
              />
            </div>
          {/each}
          <MetricsBackendNote {backend} />
        </div>
      </DetailSection>
    {/if}

    <!--
      What is on the node, which is the second thing a node panel is opened to
      answer. Below usage because "how full is it" comes first and this is the
      detail behind that number; above identity because a machine's labels
      matter less than its tenants.
    -->
    {#if selectedNode && clusterId}
      <NodePods {clusterId} nodeName={selectedNode.name} {onopen} />
    {/if}

    <!--
      Usage, only once something measured it. A pod on a cluster with no
      metrics source would otherwise get a section whose entire content is
      an apology, which the notice above the table already covers once.
    -->
    {#if selectedPod?.hasMetrics}
      <DetailSection
        level="h3"
        id="usage"
        title="Usage"
        hint="cpu {selectedPod.cpu} · memory {selectedPod.memory}"
      >
        <div class="flex flex-col gap-4">
          {#each [{ metric: 'cpu' as const, label: 'CPU' }, { metric: 'memory' as const, label: 'Memory' }] as track (track.metric)}
            <div class="flex flex-col gap-1">
              <p class="flex items-baseline justify-between text-body-small text-on-surface-variant">
                <span>{track.label}</span>
                <span class="tabular-nums">
                  {track.metric === 'cpu' ? selectedPod.cpu : selectedPod.memory}
                </span>
              </p>
              <UsageChart
                samples={usage}
                metric={track.metric}
                markers={[
                  { value: declared(track.metric, 'request'), label: 'request', tone: 'warn' },
                  { value: declared(track.metric, 'limit'), label: 'limit', tone: 'critical' },
                ]}
                format={track.metric === 'cpu' ? formatCores : formatBytes}
              />
            </div>
          {/each}
          <MetricsBackendNote {backend} />
        </div>
      </DetailSection>
    {/if}

    <!--
      THE CANONICAL ORDER, and it is the same on every pane so that moving
      between kinds does not mean relearning where anything is:

        what is wrong  ·  usage  ·  identity  ·  labels  ·  annotations
        ·  then whatever this kind specifically is

      Identity, labels and annotations sat at the very bottom, which is where
      a property list ends up when sections are added in front of it one at a
      time. They are what an object IS, and burying them under six sections of
      what it is doing meant scrolling past everything to answer "which one is
      this". They are still closed by default — reference material somebody
      looks up rather than reads — but they are now where somebody looks.

      Findings stay above usage when there are any, which is the one departure
      from "usage first". A chart is what a healthy object has to say; a pod
      that is crash-looping has something more urgent, and putting a graph
      above it would bury the reason the pane was opened.
    -->
    <DetailSection level="h3" id="identity" title="Identity" defaultOpen={false}>
      <DetailList rows={basicRows} />
    </DetailSection>

    <!-- Labels -->
    {#if labels.length > 0}
      <DetailSection level="h3" id="labels" title="Labels" defaultOpen={false} hint={String(labels.length)}>
        <DetailList rows={pairRows(labels)} wideLabels />
      </DetailSection>
    {/if}

    <!-- Annotations -->
    {#if annotations.length > 0}
      <!-- Truncated, unlike labels: an annotation routinely holds an entire
           serialised manifest, and letting one wrap turns the pane into a
           page of JSON. It is still selectable, so copying it works. -->
      <DetailSection level="h3" id="annotations" title="Annotations" defaultOpen={false} hint={String(annotations.length)}>
        <DetailList rows={pairRows(annotations, true)} wideLabels />
      </DetailSection>
    {/if}
    <!-- Pod-specific sections -->
    {#if kind === 'Pod' || selectedPod}

      <!-- Status -->
      <DetailSection level="h3" id="status" title="Status" hint={status.phase ?? ''}>
        <DetailList rows={statusRows} />
      </DetailSection>


      <!--
        Scheduling, when anything constrains it. A pod with no selector, no
        toleration and no spread rule has an empty section, and an empty
        section that says "no constraints" is a row of nothing.
      -->
      {#if schedulingRows.length > 0}
        <DetailSection level="h3" id="scheduling" title="Scheduling" defaultOpen={false} hint={String(schedulingRows.length)}>
          <DetailList rows={schedulingRows} />
        </DetailSection>
      {/if}

      <!-- Containers -->
      {#if containers.length > 0}
        <DetailSection level="h3" id="containers" title="Containers" hint={String(containers.length)}>
          <div class="flex flex-col gap-3">
            {#each containers as container (container.name)}
              <ContainerDetail
                spec={container}
                status={statusFor(container.name)}
                clusterId={selectedPod?.clusterId ?? ''}
                namespace={metadata.namespace ?? ''}
                podName={metadata.name ?? ''}
                podUID={metadata.uid ?? ''}
                labels={metadata.labels ?? {}}
              />
            {/each}
          </div>
        </DetailSection>
      {/if}

      <!--
        Ephemeral containers — the ones `kubectl debug` injects. Freelens
        requests them as a feature and Aptakube has an open issue for them;
        somebody who has attached a debug container is mid-investigation and
        needs to see that it is there, and to reach its logs and shell.
      -->
      {#if ephemeralContainers.length > 0}
        <DetailSection level="h3" id="debug-containers" title="Debug containers" hint={String(ephemeralContainers.length)}>
          <div class="flex flex-col gap-3">
            {#each ephemeralContainers as container (container.name)}
              <ContainerDetail
                spec={container}
                status={statusFor(container.name)}
                clusterId={selectedPod?.clusterId ?? ''}
                namespace={metadata.namespace ?? ''}
                podName={metadata.name ?? ''}
                podUID={metadata.uid ?? ''}
                labels={metadata.labels ?? {}}
              />
            {/each}
          </div>
        </DetailSection>
      {/if}

      <!-- Init containers, kept separate. They have already exited on a
           running pod, so mixing them in makes a healthy pod look like it has
           four containers of which two are dead. -->
      {#if initContainers.length > 0}
        <DetailSection level="h3" id="init-containers" title="Init containers" defaultOpen={false} hint={String(initContainers.length)}>
          <div class="flex flex-col gap-3">
            {#each initContainers as container (container.name)}
              <ContainerDetail
                spec={container}
                status={statusFor(container.name)}
                clusterId={selectedPod?.clusterId ?? ''}
                namespace={metadata.namespace ?? ''}
                podName={metadata.name ?? ''}
                podUID={metadata.uid ?? ''}
                labels={metadata.labels ?? {}}
              />
            {/each}
          </div>
        </DetailSection>
      {/if}

      <!-- Volumes -->
      {#if volumes.length > 0}
        <DetailSection level="h3" id="volumes" title="Volumes" defaultOpen={false} hint={String(volumes.length)}>
          <DetailList rows={volumeRows} />
        </DetailSection>
      {/if}
    {/if}

    <!-- Deployment/StatefulSet-specific sections -->
    {#if selectedWorkload && (kind === 'Deployment' || kind === 'StatefulSet' || kind === 'DaemonSet')}
      <!-- Replicas -->
      <DetailSection level="h3" id="replicas" title="Replicas">
        <DetailList rows={replicaRows} />
      </DetailSection>

      <!-- Strategy -->
      {#if kind === 'Deployment'}
        <DetailSection level="h3" id="strategy" title="Update strategy" defaultOpen={false}>
          <DetailList rows={strategyRows} />
        </DetailSection>
      {/if}
    {/if}

    <!-- Conditions -->
    {#if conditions.length > 0}
      <DetailSection level="h3" id="conditions" title="Conditions" defaultOpen={false} hint={String(conditions.length)}>
        <DetailList rows={conditionRows} wideLabels />
      </DetailSection>
    {/if}

  {/if}
</div>
