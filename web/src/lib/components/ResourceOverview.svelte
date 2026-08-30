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
      <div class="grid grid-cols-2 gap-4">
        <div>
          <p class="text-body-small text-on-surface-variant">Name</p>
          <p class="text-body-medium text-on-surface" data-selectable>{metadata.name ?? '—'}</p>
        </div>
        <div>
          <p class="text-body-small text-on-surface-variant">Namespace</p>
          <p class="text-body-medium text-on-surface" data-selectable>{metadata.namespace ?? '—'}</p>
        </div>
        <div>
          <p class="text-body-small text-on-surface-variant">Created</p>
          <p class="text-body-medium text-on-surface">{formatAge(metadata.creationTimestamp)}</p>
        </div>
        <div>
          <p class="text-body-small text-on-surface-variant">UID</p>
          <p class="truncate text-body-medium text-on-surface" data-selectable>{metadata.uid ?? '—'}</p>
        </div>
      </div>
    </DetailSection>

    <!-- Pod-specific sections -->
    {#if kind === 'Pod' || selectedPod}
      <!-- Status -->
      <DetailSection level="h3" title="Status">
        <div class="grid grid-cols-2 gap-4">
          <div>
            <p class="text-body-small text-on-surface-variant">Phase</p>
            <p class="text-body-medium text-on-surface">{status.phase ?? '—'}</p>
          </div>
          <div>
            <p class="text-body-small text-on-surface-variant">Pod IP</p>
            <p class="text-body-medium text-on-surface" data-selectable>{status.podIP ?? '—'}</p>
          </div>
          <div>
            <p class="text-body-small text-on-surface-variant">Node</p>
            <p class="text-body-medium text-on-surface" data-selectable>{spec.nodeName ?? '—'}</p>
          </div>
          <div>
            <p class="text-body-small text-on-surface-variant">QoS Class</p>
            <p class="text-body-medium text-on-surface">{status.qosClass ?? '—'}</p>
          </div>
        </div>
      </DetailSection>

      <!-- Containers -->
      {#if containers.length > 0}
        <DetailSection level="h3" title="Containers ({containers.length})">
          <div class="space-y-3">
            {#each containers as container, i (i)}
              <div class="rounded-sm border border-outline-variant bg-surface-container-low p-3">
                <p class="mb-2 text-body-medium font-medium text-on-surface" data-selectable>{container.name}</p>
                <div class="grid grid-cols-2 gap-2 text-body-small">
                  <div>
                    <span class="text-on-surface-variant">Image:</span>
                    <span class="ml-2 text-on-surface" data-selectable>{container.image ?? '—'}</span>
                  </div>
                  {#if container.ports}
                    <div>
                      <span class="text-on-surface-variant">Ports:</span>
                      <span class="ml-2 text-on-surface">
                        {container.ports
                          .map((p: { containerPort: number; protocol?: string }) => `${p.containerPort}/${p.protocol ?? 'TCP'}`)
                          .join(', ')}
                      </span>
                    </div>
                  {/if}
                  {#if container.resources?.requests}
                    <div>
                      <span class="text-on-surface-variant">Requests:</span>
                      <span class="ml-2 text-on-surface">
                        CPU: {container.resources.requests.cpu ?? '—'},
                        Memory: {container.resources.requests.memory ?? '—'}
                      </span>
                    </div>
                  {/if}
                  {#if container.resources?.limits}
                    <div>
                      <span class="text-on-surface-variant">Limits:</span>
                      <span class="ml-2 text-on-surface">
                        CPU: {container.resources.limits.cpu ?? '—'},
                        Memory: {container.resources.limits.memory ?? '—'}
                      </span>
                    </div>
                  {/if}
                </div>
              </div>
            {/each}
          </div>
        </DetailSection>
      {/if}

      <!-- Init Containers -->
      {#if initContainers.length > 0}
        <DetailSection level="h3" title="Init Containers ({initContainers.length})">
          <div class="space-y-3">
            {#each initContainers as container, i (i)}
              <div class="rounded-sm border border-outline-variant bg-surface-container-low p-3">
                <p class="mb-2 text-body-medium font-medium text-on-surface" data-selectable>{container.name}</p>
                <div class="grid grid-cols-2 gap-2 text-body-small">
                  <div>
                    <span class="text-on-surface-variant">Image:</span>
                    <span class="ml-2 text-on-surface" data-selectable>{container.image ?? '—'}</span>
                  </div>
                </div>
              </div>
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
        <div class="grid grid-cols-2 gap-4">
          <div>
            <p class="text-body-small text-on-surface-variant">Desired</p>
            <p class="text-body-medium text-on-surface">{status.replicas ?? replicas}</p>
          </div>
          <div>
            <p class="text-body-small text-on-surface-variant">Ready</p>
            <p class="text-body-medium text-on-surface">{status.readyReplicas ?? 0}</p>
          </div>
          <div>
            <p class="text-body-small text-on-surface-variant">Available</p>
            <p class="text-body-medium text-on-surface">{status.availableReplicas ?? 0}</p>
          </div>
          <div>
            <p class="text-body-small text-on-surface-variant">Updated</p>
            <p class="text-body-medium text-on-surface">{status.updatedReplicas ?? 0}</p>
          </div>
        </div>
      </DetailSection>

      <!-- Strategy -->
      {#if kind === 'Deployment'}
        <DetailSection level="h3" title="Update Strategy">
          <div class="grid grid-cols-2 gap-4">
            <div>
              <p class="text-body-small text-on-surface-variant">Type</p>
              <p class="text-body-medium text-on-surface">{strategy}</p>
            </div>
            {#if strategy === 'RollingUpdate' && spec.strategy?.rollingUpdate}
              <div>
                <p class="text-body-small text-on-surface-variant">Max Surge</p>
                <p class="text-body-medium text-on-surface">{spec.strategy.rollingUpdate.maxSurge ?? '25%'}</p>
              </div>
              <div>
                <p class="text-body-small text-on-surface-variant">Max Unavailable</p>
                <p class="text-body-medium text-on-surface">{spec.strategy.rollingUpdate.maxUnavailable ?? '25%'}</p>
              </div>
            {/if}
          </div>
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
        <div class="space-y-1">
          {#each labels as [key, value], i (i)}
            <div class="flex gap-2 text-body-small">
              <span class="font-medium text-on-surface" data-selectable>{key}:</span>
              <span class="text-on-surface-variant" data-selectable>{value}</span>
            </div>
          {/each}
        </div>
      </DetailSection>
    {/if}

    <!-- Annotations -->
    {#if annotations.length > 0}
      <DetailSection level="h3" title="Annotations ({annotations.length})">
        <div class="space-y-1">
          {#each annotations as [key, value], i (i)}
            <div class="flex gap-2 text-body-small">
              <span class="font-medium text-on-surface" data-selectable>{key}:</span>
              <span class="truncate text-on-surface-variant" data-selectable>{value}</span>
            </div>
          {/each}
        </div>
      </DetailSection>
    {/if}
  {/if}
</div>
