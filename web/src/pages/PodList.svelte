<!--
  Pod list for the connected cluster.

  This is the view that proves the whole stack: kubeconfig → client-go → domain
  → use case → Wails binding → store → table. Everything shown here was derived
  in Go, so the table renders values rather than computing them.
-->
<script lang="ts">
  import { SvelteSet } from 'svelte/reactivity'
  import Button from '$lib/components/Button.svelte'
  import EmptyState from '$lib/components/EmptyState.svelte'
  import ErrorBanner from '$lib/components/ErrorBanner.svelte'
  import Select from '$lib/components/Select.svelte'
  import StatTile from '$lib/components/StatTile.svelte'
  import StatusIndicator from '$lib/components/StatusIndicator.svelte'
  import { formatAge, formatClockTime, podStatusLabel, podTone } from '$lib/format'
  import { ALL_NAMESPACES, k8s } from '$stores/k8s.svelte'

  /** Pods whose container breakdown is expanded, keyed by namespace/name. */
  let expanded = $state(new SvelteSet<string>())

  const namespaceOptions = $derived([
    { value: ALL_NAMESPACES, label: 'All namespaces' },
    ...k8s.namespaces.map((namespace) => ({
      value: namespace.name,
      label: namespace.name,
      hint: namespace.isActive ? undefined : namespace.phase.toLowerCase(),
    })),
  ])

  const showNamespaceColumn = $derived(k8s.selectedNamespace === ALL_NAMESPACES)

  /** Stable row key. Pod UIDs can be absent, but namespace/name never is. */
  function keyOf(pod: { namespace: string; name: string }): string {
    return `${pod.namespace}/${pod.name}`
  }

  function toggle(key: string): void {
    if (expanded.has(key)) {
      expanded.delete(key)
    } else {
      expanded.add(key)
    }
  }

  // Poll while this view is mounted, and stop as soon as it is not — an
  // interval that outlives its view is the classic way a desktop app ends up
  // quietly hammering an API server in the background.
  $effect(() => {
    k8s.startAutoRefresh()
    return () => k8s.stopAutoRefresh()
  })
</script>

<div class="flex min-h-0 flex-1 flex-col">
  <!-- Summary and filters -->
  <div class="flex flex-wrap items-end gap-6 border-b border-outline-variant px-6 py-4">
    <Select
      label="Namespace"
      value={k8s.selectedNamespace}
      options={namespaceOptions}
      onchange={(value) => k8s.selectNamespace(value)}
      class="w-64"
    />

    <div class="flex items-end gap-8">
      <StatTile label="Pods" value={k8s.podSummary.total} />
      <StatTile label="Healthy" value={k8s.podSummary.healthy} tone="success" />
      <StatTile
        label="Unhealthy"
        value={k8s.podSummary.unhealthy}
        tone={k8s.podSummary.unhealthy > 0 ? 'error' : 'neutral'}
      />
      <StatTile
        label="Restarts"
        value={k8s.podSummary.restarts}
        tone={k8s.podSummary.restarts > 0 ? 'warning' : 'neutral'}
      />
    </div>

    <div class="ml-auto flex items-center gap-3">
      <span class="text-body-small text-on-surface-variant">
        Updated {formatClockTime(k8s.lastRefreshedAt)}
      </span>
      <Button variant="tonal" onclick={k8s.refresh} loading={k8s.podsStatus === 'loading'}>
        Refresh
      </Button>
    </div>
  </div>

  {#if k8s.error}
    <div class="px-6 pt-4">
      <ErrorBanner error={k8s.error} onretry={k8s.loadPods} ondismiss={() => (k8s.error = null)} />
    </div>
  {/if}

  <!-- Table -->
  <div class="min-h-0 flex-1 overflow-auto">
    {#if k8s.isInitialPodLoad}
      <p class="px-6 py-10 text-body-medium text-on-surface-variant">Loading pods…</p>
    {:else if k8s.pods.length === 0 && k8s.podsStatus === 'ready'}
      <EmptyState
        title="No pods here"
        description={k8s.selectedNamespace === ALL_NAMESPACES
          ? 'This cluster is not running any pods you can see.'
          : `Namespace "${k8s.selectedNamespace}" has no pods.`}
      >
        {#snippet action()}
          <Button variant="text" onclick={() => k8s.selectNamespace(ALL_NAMESPACES)}>
            Show all namespaces
          </Button>
        {/snippet}
      </EmptyState>
    {:else}
      <table class="w-full border-collapse text-body-medium">
        <thead class="sticky top-0 z-10 bg-surface-container">
          <tr class="text-left text-label-medium text-on-surface-variant">
            <th scope="col" class="px-6 py-3 font-medium">Status</th>
            <th scope="col" class="px-4 py-3 font-medium">Name</th>
            {#if showNamespaceColumn}
              <th scope="col" class="px-4 py-3 font-medium">Namespace</th>
            {/if}
            <th scope="col" class="px-4 py-3 font-medium">Ready</th>
            <th scope="col" class="px-4 py-3 text-right font-medium">Restarts</th>
            <th scope="col" class="px-4 py-3 font-medium">Node</th>
            <th scope="col" class="px-4 py-3 font-medium">IP</th>
            <th scope="col" class="px-6 py-3 text-right font-medium">Age</th>
          </tr>
        </thead>

        <tbody>
          {#each k8s.pods as pod (keyOf(pod))}
            {@const key = keyOf(pod)}
            {@const open = expanded.has(key)}

            <tr
              class="cursor-pointer border-t border-outline-variant/40 hover:bg-surface-container-low"
              onclick={() => toggle(key)}
            >
              <td class="px-6 py-2.5">
                <StatusIndicator
                  tone={podTone(pod)}
                  label={podStatusLabel(pod)}
                  pulse={pod.phase === 'Terminating'}
                />
              </td>
              <td class="max-w-xs truncate px-4 py-2.5 font-mono text-on-surface" data-selectable>
                {pod.name}
              </td>
              {#if showNamespaceColumn}
                <td class="px-4 py-2.5 text-on-surface-variant">{pod.namespace}</td>
              {/if}
              <td
                class="px-4 py-2.5 tabular-nums {pod.readyContainers === pod.totalContainers
                  ? 'text-on-surface-variant'
                  : 'text-warning'}"
              >
                {pod.ready}
              </td>
              <td
                class="px-4 py-2.5 text-right tabular-nums {pod.restarts > 0
                  ? 'text-warning'
                  : 'text-on-surface-variant'}"
              >
                {pod.restarts}
              </td>
              <td class="max-w-40 truncate px-4 py-2.5 text-on-surface-variant" title={pod.nodeName}>
                {pod.nodeName || '—'}
              </td>
              <td class="px-4 py-2.5 font-mono text-on-surface-variant" data-selectable>
                {pod.podIp || '—'}
              </td>
              <td class="px-6 py-2.5 text-right tabular-nums text-on-surface-variant">
                {formatAge(pod.ageSeconds)}
              </td>
            </tr>

            {#if open}
              <tr class="border-t border-outline-variant/40 bg-surface-container-lowest">
                <td colspan={showNamespaceColumn ? 8 : 7} class="px-6 py-3">
                  <ul class="flex flex-col gap-2">
                    {#each pod.containers as container (container.name)}
                      <li class="flex flex-wrap items-center gap-3">
                        <StatusIndicator
                          tone={container.ready ? 'success' : 'warning'}
                          label={container.state}
                          class="w-32"
                        />
                        <span class="font-mono text-on-surface" data-selectable>
                          {container.name}
                        </span>
                        <span
                          class="truncate font-mono text-body-small text-on-surface-variant"
                          data-selectable
                        >
                          {container.image}
                        </span>
                        {#if container.reason}
                          <span class="text-body-small text-warning">{container.reason}</span>
                        {/if}
                        {#if container.restartCount > 0}
                          <span class="text-body-small text-on-surface-variant">
                            {container.restartCount} restarts
                          </span>
                        {/if}
                      </li>
                    {:else}
                      <li class="text-body-small text-on-surface-variant">
                        This pod declares no containers.
                      </li>
                    {/each}
                  </ul>
                </td>
              </tr>
            {/if}
          {/each}
        </tbody>
      </table>
    {/if}
  </div>
</div>
