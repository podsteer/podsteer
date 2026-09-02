<!--
  Overview tab showing structured resource information.

  Parses the YAML manifest and displays key information in a readable format.
  Different resource types show different sections (e.g., pods show containers,
  deployments show replica status, etc.).
-->
<script lang="ts">
  import { parse } from 'yaml'
  import type { NamespaceSummary, Node, Pod, Workload } from '$lib/api/client'
  import DetailSection from './DetailSection.svelte'
  import DetailList, { type DetailRow } from './DetailList.svelte'
  import ContainerDetail from './ContainerDetail.svelte'
  import UsageChart from './UsageChart.svelte'
  import MetricsBackendNote from './MetricsBackendNote.svelte'
  import NodePods from './NodePods.svelte'
  import NamespaceContents from './NamespaceContents.svelte'
  import WorkloadUsage from './WorkloadUsage.svelte'
  import type { MetricsBackend } from '$lib/api/client'
  import { parseQuantity } from '$lib/sort'
  import { follower, type OpenObject, type ServesKind } from '$lib/reference'
  import { podTemplateOf } from '$lib/podTemplate'
  import { classifyConditions, type ConditionRef } from '$lib/api/client'
  import { ingressAddresses, ingressCertificates, ingressRoutes, isOpenable } from '$lib/ingress'
  import { isCordoned, nodeTaints } from '$lib/taints'
  import { BrowserOpenURL } from '$lib/wailsjs/runtime/runtime'
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
     * The namespace row the drawer is open on, when it is a namespace.
     *
     * The figures live on the row rather than in the manifest, exactly as a
     * node's and a pod's do — a namespace object says nothing about what is
     * inside it.
     */
    selectedNamespaceRow?: NamespaceSummary | null
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
    canOpen?: ServesKind
    /** Follows a reference to the object it names. */
    onopen?: OpenObject
    /**
     * Filters the application to a namespace.
     *
     * A namespace is not opened the way an object is — following one narrows
     * every list to it, which is the same thing clicking it in the drawer's
     * header does.
     */
    onnamespace?: (namespace: string) => void
    /** Opens a kind's list, filtered to a namespace. */
    onbrowse?: (kindId: string, namespace: string) => void
    /**
     * Changes on every refresh of the list behind the panel.
     *
     * The tick a controller's usage samples on. A controller has no usage in
     * any list, so its series has to be built while its panel is open — and
     * it follows the operator's own refresh setting rather than a timer of
     * its own. See WorkloadUsage.
     */
    tick?: unknown
  }

  let {
    manifest,
    selectedPod,
    selectedNode,
    selectedNamespaceRow,
    selectedWorkload,
    kind,
    usage = [],
    backend,
    clusterId,
    canOpen,
    onopen,
    onnamespace,
    onbrowse,
    tick,
  }: Props = $props()

  /** Turns a reference into a click handler, or into nothing. See $lib/reference. */
  const follow = $derived(follower(canOpen, onopen))

  /** Following a namespace narrows the application to it. */
  function filterTo(namespace: string): (() => void) | undefined {
    if (!namespace || !onnamespace) return undefined
    return () => onnamespace(namespace)
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

  /** The six controllers, which are the kinds whose usage is their pods'. */
  const isWorkload = $derived(
    kind === 'Deployment' ||
      kind === 'StatefulSet' ||
      kind === 'DaemonSet' ||
      kind === 'ReplicaSet' ||
      kind === 'Job' ||
      kind === 'CronJob',
  )

  /**
   * A CronJob's schedule, and whether it is running.
   *
   * THE SCHEDULE IS A CRONJOB'S IDENTITY. It appeared nowhere in this panel —
   * not the expression, not whether it is suspended, not when it last ran —
   * which left a CronJob between runs showing usage of nothing, a template,
   * and its metadata.
   */
  const cronRows = $derived.by<DetailRow[]>(() => {
    if (kind !== 'CronJob') return []

    const rows: DetailRow[] = [{ label: 'Schedule', value: spec.schedule ?? '—' }]
    if (spec.timeZone) rows.push({ label: 'Time zone', value: spec.timeZone })

    // Said out loud and coloured, because a suspended CronJob looks identical
    // to one that simply has not fired — and "why has this not run" is the
    // question it is usually asked.
    rows.push({
      label: 'Suspended',
      value: spec.suspend ? 'yes — it will not run until resumed' : 'no',
      tone: spec.suspend ? ('warn' as const) : undefined,
    })

    if (spec.concurrencyPolicy) {
      rows.push({ label: 'Concurrency', value: spec.concurrencyPolicy })
    }
    rows.push({
      label: 'Last scheduled',
      value: status.lastScheduleTime ? formatAge(status.lastScheduleTime) + ' ago' : 'never',
    })
    // Kubernetes records these separately, and the gap between them is the
    // whole story: a CronJob that fires hourly and last SUCCEEDED yesterday
    // is failing every hour.
    if (status.lastSuccessfulTime) {
      rows.push({ label: 'Last succeeded', value: formatAge(status.lastSuccessfulTime) + ' ago' })
    }
    const active = (status.active ?? []).length
    if (active > 0) rows.push({ label: 'Running now', value: String(active) })

    return rows
  })

  /** A Job's progress, which is what a Job is. */
  const jobRows = $derived.by<DetailRow[]>(() => {
    if (kind !== 'Job') return []

    const rows: DetailRow[] = [
      { label: 'Completions', value: `${status.succeeded ?? 0} of ${spec.completions ?? 1}` },
    ]
    if (spec.parallelism !== undefined) {
      rows.push({ label: 'Parallelism', value: String(spec.parallelism) })
    }
    rows.push({ label: 'Active', value: String(status.active ?? 0) })
    rows.push({
      label: 'Failed',
      value: String(status.failed ?? 0),
      tone: (status.failed ?? 0) > 0 ? ('warn' as const) : undefined,
    })
    if (spec.backoffLimit !== undefined) {
      rows.push({ label: 'Backoff limit', value: String(spec.backoffLimit) })
    }
    if (status.startTime) rows.push({ label: 'Started', value: formatAge(status.startTime) + ' ago' })
    if (status.completionTime) {
      rows.push({ label: 'Finished', value: formatAge(status.completionTime) + ' ago' })
      rows.push({ label: 'Took', value: elapsed(status.startTime, status.completionTime) })
    }
    return rows
  })

  /** How long a Job ran for, from the two times it records. */
  function elapsed(from: string, to: string): string {
    const seconds = Math.round((new Date(to).getTime() - new Date(from).getTime()) / 1000)
    if (!Number.isFinite(seconds) || seconds < 0) return '—'
    if (seconds < 60) return `${seconds}s`
    if (seconds < 3600) return `${Math.floor(seconds / 60)}m ${seconds % 60}s`
    return `${Math.floor(seconds / 3600)}h ${Math.floor((seconds % 3600) / 60)}m`
  }

  /**
   * The machine facts a node's panel never showed.
   *
   * Taints above all: the pod panel opposite renders tolerations in full, and
   * the thing they exist to tolerate appeared nowhere. Every scheduling
   * question needs both sides of that.
   */
  const machineRows = $derived.by<DetailRow[]>(() => {
    if (!selectedNode) return []

    const rows: DetailRow[] = []
    const info = (status.nodeInfo ?? {}) as Record<string, string>

    if (isCordoned(parsedManifest)) {
      rows.push({
        label: 'Scheduling',
        value: 'cordoned — nothing new will be placed here',
        tone: 'warn' as const,
      })
    }

    for (const taint of nodeTaints(parsedManifest)) {
      rows.push({ label: 'Taint', value: taint })
    }

    const capacity = (status.capacity ?? {}) as Record<string, string>
    const allocatable = (status.allocatable ?? {}) as Record<string, string>
    for (const [label, key] of [
      ['CPU', 'cpu'],
      ['Memory', 'memory'],
      ['Pods', 'pods'],
    ] as const) {
      if (!capacity[key] && !allocatable[key]) continue
      // Allocatable against capacity, because the difference is what the
      // kubelet and the system reserved and it is the number the scheduler
      // actually has to give out.
      rows.push({
        label: `${label} allocatable`,
        value: `${allocatable[key] ?? '—'} of ${capacity[key] ?? '—'}`,
      })
    }

    if (info.kubeletVersion) rows.push({ label: 'Kubelet', value: info.kubeletVersion })
    if (info.containerRuntimeVersion) {
      rows.push({ label: 'Runtime', value: info.containerRuntimeVersion })
    }
    if (info.osImage) rows.push({ label: 'OS', value: info.osImage })
    if (info.kernelVersion) rows.push({ label: 'Kernel', value: info.kernelVersion })
    if (info.architecture) rows.push({ label: 'Architecture', value: info.architecture })

    return rows
  })

  /**
   * The addresses an Ingress answers on, and the certificates behind them.
   *
   * An Ingress is a routing table written as a nested list, and every client
   * renders it as one — hosts here, paths there, backends in a third column —
   * leaving the reader to assemble the URL in their head. The one thing they
   * came for is the address, so that is what this shows, and it opens.
   */
  const routeRows = $derived.by<DetailRow[]>(() =>
    ingressRoutes(parsedManifest).map((route) => ({
      label: route.host || 'Any host',
      value: route.url,
      info: `${route.pathType} path to ${route.backend}${route.secure ? ', TLS terminated here' : ', not encrypted'}`,
      // Opened in the real browser, not the webview: this is somebody else's
      // site, and loading it inside the application would replace PodSteer.
      onclick: isOpenable(route) ? () => BrowserOpenURL(route.url) : undefined,
      external: isOpenable(route),
      tone: route.secure ? undefined : ('warn' as const),
    })),
  )

  const certificateRows = $derived.by<DetailRow[]>(() =>
    ingressCertificates(parsedManifest).map((certificate) => ({
      label: certificate.secretName,
      // An empty host list means EVERY host, which is the API's meaning and
      // the opposite of how an empty cell reads.
      value: certificate.hosts.length > 0 ? certificate.hosts.join(', ') : 'every host in this Ingress',
      onclick: follow('Secret', certificate.secretName, metadata.namespace ?? ''),
    })),
  )

  const addressRows = $derived.by<DetailRow[]>(() =>
    ingressAddresses(parsedManifest).map((address) => ({ label: 'Address', value: address })),
  )

  const isIngress = $derived(kind === 'Ingress')

  /** Whether the panel is showing a pod. */
  const isPodPanel = $derived(kind === 'Pod' || !!selectedPod)

  /**
   * A kind with no purpose-built sections at all — a CRD, or anything served
   * by the generic table.
   *
   * Its panel is otherwise Identity, Labels and Annotations, all collapsed,
   * which opens on three closed headers and nothing else and reads as a panel
   * that failed to load.
   */
  const isGenericKind = $derived(
    !isPodPanel &&
      !isWorkload &&
      !isIngress &&
      !selectedNode &&
      !selectedNamespaceRow &&
      kind !== 'Namespace',
  )

  /**
   * Whether this kind's conditions are its verdict or its receipts.
   *
   * A controller's are the rollout's state, a node's are the kubelet's own
   * alarms, and a custom resource's are usually the whole of its status. A
   * pod's are a transcript that Status and the findings already summarise,
   * and a namespace's exist only when a deletion is stuck — where their mere
   * presence is the signal.
   */
  const conditionsLead = $derived(
    !isPodPanel && (kind !== 'Namespace' || conditions.length > 0),
  )

  /** Whether this kind has anything above Identity to look at. */
  const identityLeads = $derived(isGenericKind)

  /**
   * The kind, for the section ids that now differ by it.
   *
   * DetailSection remembers what an operator opened or closed against its id,
   * so one shared "conditions" id would carry a pod's collapsed choice onto a
   * Deployment where the section is open by design.
   */
  const conditionsKey = $derived((kind ?? 'object').toLowerCase())

  /**
   * The pod template a controller carries, if it carries one.
   *
   * A CronJob nests it one level deeper, under the Job it creates — which is
   * the whole difference between the two, and the reason this is a lookup
   * rather than a field.
   *
   * FROM THE MANIFEST, NEVER FROM A LIST. A ReplicaSet read out of the watch
   * store has had its template stripped to the container images; a panel
   * sourcing one from there would show a container with no environment, no
   * volumes and no probes, and be right about the images. See CLAUDE.md.
   */
  const podTemplate = $derived(isWorkload ? podTemplateOf(parsedManifest, kind) : null)

  const templateContainers = $derived(podTemplate?.spec?.containers ?? [])
  const templateInitContainers = $derived(podTemplate?.spec?.initContainers ?? [])
  const templateVolumes = $derived(podTemplate?.spec?.volumes ?? [])


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

  const basicRows = $derived.by<DetailRow[]>(() => {
    const rows: DetailRow[] = [{ label: 'Name', value: metadata.name ?? '—' }]

    // Absent for a cluster-scoped kind rather than an em dash. A Node has no
    // namespace, and a row saying so is a row asserting that something is
    // missing from an object that never had it.
    if (metadata.namespace) {
      rows.push({
        label: 'Namespace',
        value: metadata.namespace,
        onclick: filterTo(metadata.namespace),
      })
    }

    rows.push({ label: 'Created', value: formatAge(metadata.creationTimestamp) })
    rows.push({ label: 'UID', value: metadata.uid ?? '—' })
    return rows
  })

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

    // 'default' is not a placeholder here — it is the name of a
    // ServiceAccount that exists in every namespace, so the link is as real
    // as any other.
    const serviceAccount = spec.serviceAccountName ?? 'default'
    rows.push({
      label: 'Service account',
      value: serviceAccount,
      onclick: follow('ServiceAccount', serviceAccount, metadata.namespace ?? ''),
    })
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
      // Cluster-scoped, so no namespace: a PriorityClass is one object the
      // whole cluster shares.
      rows.push({
        label: 'Priority class',
        value: String(spec.priorityClassName),
        onclick: follow('PriorityClass', String(spec.priorityClassName)),
      })
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
  const volumeRows = $derived(volumeRowsFor(volumes))
  const templateVolumeRows = $derived(volumeRowsFor(templateVolumes))

  function volumeRowsFor(source: unknown[]): DetailRow[] {
    const namespace = metadata.namespace ?? ''

    return (source as Record<string, any>[]).map((volume) => {
      let detail = 'Unknown'
      // The three that name another object are followable. A projected volume
      // is not: it names several, and a row links to one thing or to nothing.
      let go: (() => void) | undefined

      if (volume.emptyDir) {
        detail = volume.emptyDir.sizeLimit ? `EmptyDir · limit ${volume.emptyDir.sizeLimit}` : 'EmptyDir'
      } else if (volume.configMap) {
        detail = `ConfigMap · ${volume.configMap.name}`
        go = follow('ConfigMap', volume.configMap.name, namespace)
      } else if (volume.secret) {
        detail = `Secret · ${volume.secret.secretName}`
        go = follow('Secret', volume.secret.secretName, namespace)
      } else if (volume.persistentVolumeClaim) {
        const claim = volume.persistentVolumeClaim
        detail = `PVC · ${claim.claimName}${claim.readOnly ? ' (ro)' : ''}`
        go = follow('PersistentVolumeClaim', claim.claimName, namespace)
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
      return { label: volume.name, value: detail, onclick: go }
    })
  }

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
  /**
   * Which conditions are reporting a problem, from the domain.
   *
   * ASKED RATHER THAN ASSUMED. This used to colour any condition whose status
   * was False, which is right for a pod and backwards for a node: every
   * healthy node showed its three pressure conditions as warnings, and a node
   * genuinely under MemoryPressure rendered as though nothing were wrong. The
   * polarity is a verdict, so it lives in Go with a test — see
   * domain.ClassifyCondition.
   */
  let conditionTones = $state.raw<string[]>([])

  $effect(() => {
    const asked = (conditions as Record<string, string>[]).map((condition) => ({
      type: condition.type,
      status: condition.status,
    }))
    // CLEARED BEFORE THE NEW ONES ARRIVE, and the reason is that the tones
    // are paired with the conditions BY INDEX. Opening pod A's drawer and
    // then pod B left B's conditions wearing A's tones for one IPC
    // round-trip — a healthy pod briefly showing another pod's warnings,
    // which is worse than showing none.
    conditionTones = []
    if (asked.length === 0) return

    let current = true
    void classifyConditions(asked as ConditionRef[])
      .then((tones) => {
        if (current) conditionTones = tones
      })
      // Uncoloured is the safe failure: a row with no tone still says what
      // the condition is, while a wrong tone says something untrue about it.
      .catch(() => {
        if (current) conditionTones = []
      })

    return () => {
      current = false
    }
  })

  /**
   * A condition as one line, with the reason kubectl throws away.
   *
   * `kubectl describe` prints conditions as Type and Status only, which
   * discards the half that explains anything: PodScheduled=False is a fact,
   * and its reason and message are the scheduler's own account of why.
   */
  const conditionRows = $derived(
    (conditions as Record<string, string>[]).map((condition, index) => {
      const explanation = [condition.reason, condition.message].filter(Boolean).join(' — ')
      return {
        label: condition.type,
        value: explanation ? `${condition.status} · ${explanation}` : condition.status,
        tone: (conditionTones[index] || undefined) as 'warn' | 'critical' | undefined,
      }
    }),
  )

  /**
   * What the container boxes belong to, so two pods cannot share one.
   *
   * See the keys on the each-blocks below: a container name is unique within
   * a pod and repeated across every replica of a workload.
   */
  const containerScope = $derived(
    `${selectedPod?.clusterId ?? ''}/${metadata.namespace ?? ''}/${metadata.name ?? ''}`,
  )

  /** Turns a metadata map into rows, for labels and annotations. */
  function pairRows(pairs: [string, unknown][]): DetailRow[] {
    return pairs.map(([key, value]) => ({ label: key, value: String(value) }))
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
        hint="CPU {selectedNode.cpu} · Memory {selectedNode.memory}"
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
                  { value: parseQuantity(track.allocatable) ?? 0, label: 'Allocatable', tone: 'critical' },
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
      What is in a namespace, for the same reason and in the same place: the
      panel's own labels matter less than its contents, and "is this namespace
      empty" is the question that decides whether anything else here is worth
      reading.
    -->
    <!--
      A namespace's usage, on the same terms as a node's: the row carries the
      figures, the series comes from what the list has been recording since
      the tab opened, and the reference lines are the SUM of its pods'
      requests and limits. A namespace has no capacity of its own to measure
      against — only what the things inside it asked for.
    -->
    {#if selectedNamespaceRow?.hasMetrics}
      <DetailSection
        level="h3"
        id="namespace-usage"
        title="Usage"
        hint="CPU {selectedNamespaceRow.cpu} · Memory {selectedNamespaceRow.memory}"
      >
        <div class="flex flex-col gap-4">
          {#each [{ metric: 'cpu' as const, label: 'CPU', used: selectedNamespaceRow.cpu, request: selectedNamespaceRow.requestCores, limit: selectedNamespaceRow.limitCores }, { metric: 'memory' as const, label: 'Memory', used: selectedNamespaceRow.memory, request: selectedNamespaceRow.requestBytes, limit: selectedNamespaceRow.limitBytes }] as track (track.metric)}
            <div class="flex flex-col gap-1">
              <p class="flex items-baseline justify-between text-body-small text-on-surface-variant">
                <span>{track.label}</span>
                <span class="tabular-nums">{track.used}</span>
              </p>
              <UsageChart
                samples={usage}
                metric={track.metric}
                markers={[
                  { value: track.request, label: 'Request', tone: 'warn' },
                  { value: track.limit, label: 'Limit', tone: 'critical' },
                ]}
                format={track.metric === 'cpu' ? formatCores : formatBytes}
              />
            </div>
          {/each}

          <!-- Against the pods that could be measured. See WorkloadUsage. -->
          {#if selectedNamespaceRow.measuredPods < selectedNamespaceRow.measurablePods}
            <p class="text-body-small text-gauge-warn">
              Summed over {selectedNamespaceRow.measuredPods} of {selectedNamespaceRow.measurablePods}
              running pods — the rest reported no usage, so this is less than the whole.
            </p>
          {/if}

          <MetricsBackendNote {backend} />
        </div>
      </DetailSection>
    {/if}

    {#if kind === 'Namespace' && clusterId && metadata.name}
      <NamespaceContents {clusterId} namespace={metadata.name} {onbrowse} />
    {/if}

    <!--
      A controller's usage, in the same place a pod's and a node's sit: what
      is wrong, then what it is doing, then what it is. Unlike those two it is
      read rather than remembered — nothing polls a controller's pods — which
      is why it is a component of its own rather than another UsageChart here.
    -->
    {#if isWorkload && clusterId && metadata.name}
      <WorkloadUsage
        {clusterId}
        namespace={metadata.namespace ?? ''}
        kind={kind ?? ''}
        name={metadata.name}
        {tick}
        {backend}
      />
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
        hint="CPU {selectedPod.cpu} · Memory {selectedPod.memory}"
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
                  { value: declared(track.metric, 'request'), label: 'Request', tone: 'warn' },
                  { value: declared(track.metric, 'limit'), label: 'Limit', tone: 'critical' },
                ]}
                format={track.metric === 'cpu' ? formatCores : formatBytes}
              />
            </div>
          {/each}
          <MetricsBackendNote {backend} />
        </div>
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
          <div class="flex flex-col">
            <!--
              KEYED WITH THE POD, NOT ONLY THE CONTAINER NAME. Every replica
              of a workload runs containers with the same names, so keying on
              the name alone let Svelte reuse one pod's ContainerDetail for
              another's — carrying its resolved ConfigMap values and its open
              rows across as though they described the new pod.
            -->
            {#each containers as container (`${containerScope}/${container.name}`)}
              <ContainerDetail
                spec={container}
                status={statusFor(container.name)}
                clusterId={selectedPod?.clusterId ?? ''}
                namespace={metadata.namespace ?? ''}
                podName={metadata.name ?? ''}
                podUID={metadata.uid ?? ''}
                labels={metadata.labels ?? {}}
                pod={parsedManifest}
                {canOpen}
                {onopen}
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
          <div class="flex flex-col">
            {#each ephemeralContainers as container (`${containerScope}/${container.name}`)}
              <ContainerDetail
                spec={container}
                status={statusFor(container.name)}
                clusterId={selectedPod?.clusterId ?? ''}
                namespace={metadata.namespace ?? ''}
                podName={metadata.name ?? ''}
                podUID={metadata.uid ?? ''}
                labels={metadata.labels ?? {}}
                pod={parsedManifest}
                {canOpen}
                {onopen}
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
          <div class="flex flex-col">
            {#each initContainers as container (`${containerScope}/${container.name}`)}
              <ContainerDetail
                spec={container}
                status={statusFor(container.name)}
                clusterId={selectedPod?.clusterId ?? ''}
                namespace={metadata.namespace ?? ''}
                podName={metadata.name ?? ''}
                podUID={metadata.uid ?? ''}
                labels={metadata.labels ?? {}}
                pod={parsedManifest}
                {canOpen}
                {onopen}
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
    <!--
      Replicas, and it is the most-read section a controller has: desired
      against ready is the question the panel was opened with. It sat below
      Identity and Labels, which is a lookup people do occasionally.

      A ReplicaSet is included now. It has the same desired and ready numbers
      as the Deployment above it and was excluded for no reason anybody
      recorded.
    -->
    {#if selectedWorkload && kind !== 'Job' && kind !== 'CronJob'}
      <DetailSection level="h3" id="replicas" title="Replicas">
        <DetailList rows={replicaRows} />
      </DetailSection>

      {#if kind === 'Deployment'}
        <DetailSection level="h3" id="strategy" title="Update strategy" defaultOpen={false}>
          <DetailList rows={strategyRows} />
        </DetailSection>
      {/if}
    {/if}

    <!--
      THE POD TEMPLATE, and it is called that on purpose.
      
      A controller has no containers. It has a description of the ones its
      next pod will get — which is the same thing as a running pod's only
      until a rollout is in progress, or a ConfigMap has changed since those
      pods started, or an admission webhook adds a sidecar on the way in.
      Calling this "Containers", the way the pod panel does, would quietly
      claim otherwise; calling it what Kubernetes calls it carries the tense
      without a caveat anybody has to read.

      It earns its place most where there is nothing else to look at: a
      CronJob between runs has no pods to open, and its template is the only
      description of what it does.
    -->
    {#if isWorkload && templateContainers.length > 0}
      <DetailSection
        level="h3"
        id="pod-template"
        title="Pod template"
        hint={String(templateContainers.length)}
        defaultOpen={kind !== 'ReplicaSet'}
      >
        <div class="flex flex-col">
          {#each templateContainers as container, index (container.name ?? index)}
            <ContainerDetail
              spec={container}
              context="template"
              clusterId={clusterId ?? ''}
              namespace={metadata.namespace ?? ''}
              {canOpen}
              {onopen}
            />
          {/each}
        </div>

        <!--
          Said once, under the containers rather than beside every value: what
          a pod ends up with is not always what its controller asked for, and
          the difference is invisible from here.
        -->
        <p class="mt-4 text-body-small text-on-surface-variant/60">
          What the next pod will be given. A running pod may differ — from an
          older revision, or from a webhook that adds to the spec on the way in.
        </p>
      </DetailSection>

      {#if templateInitContainers.length > 0}
        <DetailSection
          level="h3"
          id="template-init-containers"
          title="Template init containers"
          defaultOpen={false}
          hint={String(templateInitContainers.length)}
        >
          <div class="flex flex-col">
            {#each templateInitContainers as container, index (container.name ?? index)}
              <ContainerDetail
                spec={container}
                context="template"
                clusterId={clusterId ?? ''}
                namespace={metadata.namespace ?? ''}
                {canOpen}
                {onopen}
              />
            {/each}
          </div>
        </DetailSection>
      {/if}

      <!-- The other half of "what secrets does this reach": a Secret mounted
           as a volume never appears in the environment. -->
      {#if templateVolumes.length > 0}
        <DetailSection
          level="h3"
          id="template-volumes"
          title="Template volumes"
          defaultOpen={false}
          hint={String(templateVolumes.length)}
        >
          <DetailList rows={templateVolumeRows} />
        </DetailSection>
      {/if}
    {/if}

    <!--
      A CRONJOB'S SCHEDULE IS ITS IDENTITY, and it appeared nowhere: not the
      expression, not whether it is suspended, not when it last ran. Between
      runs that left the panel showing usage of nothing, a template, and some
      metadata.
    -->
    {#if cronRows.length > 0}
      <DetailSection level="h3" id="schedule" title="Schedule" hint={spec.schedule ?? ''}>
        <DetailList rows={cronRows} />
      </DetailSection>
    {/if}

    <!-- A Job's progress, which is the whole of what a Job is. -->
    {#if jobRows.length > 0}
      <DetailSection
        level="h3"
        id="job-progress"
        title="Progress"
        hint="{status.succeeded ?? 0}/{spec.completions ?? 1}"
      >
        <DetailList rows={jobRows} />
      </DetailSection>
    {/if}

    <!--
      Where an Ingress actually sends people, as somewhere they can go.
    -->
    {#if isIngress && routeRows.length > 0}
      <DetailSection level="h3" id="routes" title="Addresses" hint={String(routeRows.length)}>
        <DetailList rows={routeRows} />
        {#if addressRows.length > 0}
          <div class="mt-3">
            <DetailList rows={addressRows} />
          </div>
        {:else}
          <!-- Ordinary rather than an error, and often the answer to "why
               does this not work": nothing has claimed it yet. -->
          <p class="mt-3 text-body-small text-on-surface-variant/60">
            No controller has published an address for this Ingress yet.
          </p>
        {/if}
      </DetailSection>
    {/if}

    {#if isIngress && certificateRows.length > 0}
      <DetailSection
        level="h3"
        id="certificates"
        title="TLS"
        hint={String(certificateRows.length)}
      >
        <DetailList rows={certificateRows} />
        <p class="mt-3 text-body-small text-on-surface-variant/60">
          Which Secret terminates which hosts. What the certificate itself says — who
          issued it, when it expires — is inside that Secret and is not read here.
        </p>
      </DetailSection>
    {/if}

    <!--
      The machine, and above all its taints. The pod panel opposite renders
      tolerations in full, including the toleration seconds several clients
      drop, and the thing they exist to tolerate was shown nowhere.
    -->
    {#if machineRows.length > 0}
      <DetailSection level="h3" id="machine" title="Machine" defaultOpen={false} hint={String(machineRows.length)}>
        <DetailList rows={machineRows} />
      </DetailSection>
    {/if}

    <!--
      CONDITIONS ARE NOT ONE THING, which is why they are not in one place.

      On a pod they are a transcript that Status and the findings above
      already summarise — receipts, worth keeping and worth keeping out of the
      way. On a controller they are the ROLLOUT VERDICT: ReplicaFailure
      carries the quota message that explains a stuck rollout and nothing else
      in this panel does. On a node they are the kubelet's own alarms. And on
      a custom resource they are usually the whole of its status — a
      cert-manager Certificate or a Flux Kustomization says Ready=False with a
      message, and that is the entire reason somebody opened the panel.
    -->
    {#if conditions.length > 0 && conditionsLead}
      <DetailSection level="h3" id="{conditionsKey}-conditions" title="Conditions" hint={String(conditions.length)}>
        <DetailList rows={conditionRows} />
      </DetailSection>
    {/if}

    <!--
      REFERENCE, IN A FIXED FOOTER, ON EVERY KIND.
      
      These sat at the very bottom once, were moved up here because "which one
      is this" meant scrolling past everything, and are back at the bottom
      because that diagnosis solved a real problem on the wrong axis. What
      makes a section findable is being in the SAME PLACE on every kind, not
      being early; and the drawer's header already answers which object this
      is, in the two lines above the tabs.
      
      What the middle position cost was the reading order: on every pod open,
      three closed headers sat between the usage chart and the status and
      containers people came for, interrupting the flow from "what is it
      doing" to "what was it told to be" in order to optimise a lookup that
      happens occasionally.
    -->
    <DetailSection level="h3" id="identity" title="Identity" defaultOpen={identityLeads}>
      <DetailList rows={basicRows} />
    </DetailSection>

    {#if labels.length > 0}
      <DetailSection level="h3" id="labels" title="Labels" defaultOpen={false} hint={String(labels.length)}>
        <DetailList rows={pairRows(labels)} />
      </DetailSection>
    {/if}

    {#if annotations.length > 0}
      <!-- An annotation routinely holds an entire serialised manifest, which
           is the case the list's clipping exists for: one line each, and the
           one somebody wants opens. -->
      <DetailSection level="h3" id="annotations" title="Annotations" defaultOpen={false} hint={String(annotations.length)}>
        <DetailList rows={pairRows(annotations)} />
      </DetailSection>
    {/if}

    {#if conditions.length > 0 && !conditionsLead}
      <DetailSection
        level="h3"
        id="{conditionsKey}-conditions"
        title="Conditions"
        defaultOpen={false}
        hint={String(conditions.length)}
      >
        <DetailList rows={conditionRows} />
      </DetailSection>
    {/if}

  {/if}
</div>
