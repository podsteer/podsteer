<!--
  Overview tab showing structured resource information.

  Parses the YAML manifest and displays key information in a readable format.
  Different resource types show different sections (e.g., pods show containers,
  deployments show replica status, etc.).
-->
<script lang="ts">
  import { parse } from 'yaml'
  import type { Pod, Workload } from '$lib/api/client'
  import DetailSection from './DetailSection.svelte'
  import DetailList, { type DetailRow } from './DetailList.svelte'
  import ContainerDetail from './ContainerDetail.svelte'

  interface Props {
    manifest: string | null
    selectedPod?: Pod | null
    selectedWorkload?: Workload | null
    kind?: string
  }

  let { manifest, selectedPod, selectedWorkload, kind }: Props = $props()

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

  const statusRows = $derived<DetailRow[]>([
    { label: 'Phase', value: status.phase ?? '—' },
    { label: 'Pod IP', value: status.podIP ?? '—' },
    { label: 'Node', value: spec.nodeName ?? '—' },
    { label: 'QoS Class', value: status.qosClass ?? '—' },
  ])

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

  /** How long a container survived before it died, in words. */
  function survived(seconds: number): string {
    if (seconds <= 0) return ''
    if (seconds < 60) return `ran for ${Math.round(seconds)}s`
    if (seconds < 3600) return `ran for ${Math.round(seconds / 60)}m`
    if (seconds < 86400) return `ran for ${Math.round(seconds / 3600)}h`
    return `ran for ${Math.round(seconds / 86400)}d`
  }

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

  function formatBytes(bytes: number): string {
    if (bytes === 0) return '0 B'
    const k = 1024
    const sizes = ['B', 'Ki', 'Mi', 'Gi', 'Ti']
    const i = Math.floor(Math.log(bytes) / Math.log(k))
    return `${(bytes / Math.pow(k, i)).toFixed(1)}${sizes[i]}`
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
    <!-- Basic Information -->
    <DetailSection level="h3" title="Basic Information">
      <DetailList rows={basicRows} />
    </DetailSection>

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
      <DetailSection level="h3" title="Worth knowing">
        <div class="flex flex-col gap-2">
          {#each selectedPod.findings as finding (finding.title)}
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

    <!-- Pod-specific sections -->
    {#if kind === 'Pod' || selectedPod}
      <!-- Status -->
      <DetailSection level="h3" title="Status">
        <DetailList rows={statusRows} />
      </DetailSection>

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
        <DetailSection level="h3" title="Why it restarted, last time">
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

      <!-- Containers -->
      {#if containers.length > 0}
        <DetailSection level="h3" title="Containers ({containers.length})">
          <div class="flex flex-col gap-3">
            {#each containers as container (container.name)}
              <ContainerDetail
                spec={container}
                status={statusFor(container.name)}
                clusterId={selectedPod?.clusterId ?? ''}
                namespace={metadata.namespace ?? ''}
              />
            {/each}
          </div>
        </DetailSection>
      {/if}

      <!-- Init containers, kept separate. They have already exited on a
           running pod, so mixing them in makes a healthy pod look like it has
           four containers of which two are dead. -->
      {#if initContainers.length > 0}
        <DetailSection level="h3" title="Init containers ({initContainers.length})">
          <div class="flex flex-col gap-3">
            {#each initContainers as container (container.name)}
              <ContainerDetail
                spec={container}
                status={statusFor(container.name)}
                clusterId={selectedPod?.clusterId ?? ''}
                namespace={metadata.namespace ?? ''}
              />
            {/each}
          </div>
        </DetailSection>
      {/if}

      <!-- Volumes -->
      {#if volumes.length > 0}
        <DetailSection level="h3" title="Volumes ({volumes.length})">
          <div class="space-y-2">
            {#each volumes as volume, i (i)}
              <div class="rounded-sm border border-outline-variant bg-surface-container-low p-3">
                <p class="text-body-medium font-medium text-on-surface" data-selectable>{volume.name}</p>
                <p class="mt-1 text-body-small text-on-surface-variant">
                  {#if volume.emptyDir}
                    EmptyDir
                  {:else if volume.configMap}
                    ConfigMap: {volume.configMap.name}
                  {:else if volume.secret}
                    Secret: {volume.secret.secretName}
                  {:else if volume.persistentVolumeClaim}
                    PVC: {volume.persistentVolumeClaim.claimName}
                  {:else if volume.hostPath}
                    HostPath: {volume.hostPath.path}
                  {:else}
                    {Object.keys(volume).filter(k => k !== 'name')[0] ?? 'Unknown'}
                  {/if}
                </p>
              </div>
            {/each}
          </div>
        </DetailSection>
      {/if}
    {/if}

    <!-- Deployment/StatefulSet-specific sections -->
    {#if selectedWorkload && (kind === 'Deployment' || kind === 'StatefulSet' || kind === 'DaemonSet')}
      <!-- Replicas -->
      <DetailSection level="h3" title="Replicas">
        <DetailList rows={replicaRows} />
      </DetailSection>

      <!-- Strategy -->
      {#if kind === 'Deployment'}
        <DetailSection level="h3" title="Update Strategy">
          <DetailList rows={strategyRows} />
        </DetailSection>
      {/if}
    {/if}

    <!-- Conditions -->
    {#if conditions.length > 0}
      <DetailSection level="h3" title="Conditions">
        <div class="space-y-2">
          {#each conditions as condition, i (i)}
            <div class="rounded-sm border border-outline-variant bg-surface-container-low p-3">
              <div class="flex items-center justify-between">
                <p class="text-body-medium font-medium text-on-surface">{condition.type}</p>
                <span
                  class="rounded-full px-2 py-0.5 text-body-small
                         {condition.status === 'True' ? 'bg-success-container text-on-success-container' :
                          condition.status === 'False' ? 'bg-error-container text-on-error-container' :
                          'bg-surface-container-high text-on-surface-variant'}"
                >
                  {condition.status}
                </span>
              </div>
              {#if condition.reason}
                <p class="mt-1 text-body-small text-on-surface-variant">{condition.reason}</p>
              {/if}
              {#if condition.message}
                <p class="mt-1 text-body-small text-on-surface-variant">{condition.message}</p>
              {/if}
            </div>
          {/each}
        </div>
      </DetailSection>
    {/if}

    <!-- Labels -->
    {#if labels.length > 0}
      <DetailSection level="h3" title="Labels ({labels.length})">
        <DetailList rows={pairRows(labels)} />
      </DetailSection>
    {/if}

    <!-- Annotations -->
    {#if annotations.length > 0}
      <!-- Truncated, unlike labels: an annotation routinely holds an entire
           serialised manifest, and letting one wrap turns the pane into a
           page of JSON. It is still selectable, so copying it works. -->
      <DetailSection level="h3" title="Annotations ({annotations.length})">
        <DetailList rows={pairRows(annotations, true)} />
      </DetailSection>
    {/if}
  {/if}
</div>
