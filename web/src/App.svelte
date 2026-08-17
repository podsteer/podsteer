<!--
  Application shell.

  Routing is a single conditional rather than a router: K8Sense has exactly two
  states at this stage — no cluster connected, or one connected — and a router
  would be scaffolding around a boolean. It earns its place once there are
  sibling resource views to navigate between.
-->
<script lang="ts">
  import Button from '$lib/components/Button.svelte'
  import LinearProgress from '$lib/components/LinearProgress.svelte'
  import TopAppBar from '$lib/components/TopAppBar.svelte'
  import StatusIndicator from '$lib/components/StatusIndicator.svelte'
  import ClusterView from '$pages/ClusterView.svelte'
  import PodList from '$pages/PodList.svelte'
  import { k8s } from '$stores/k8s.svelte'

  // Kick off discovery once, when the shell mounts, and release the store's
  // timer and event subscriptions when it goes away.
  $effect(() => {
    void k8s.initialise()
    return () => k8s.dispose()
  })

  const subtitle = $derived.by(() => {
    const cluster = k8s.activeCluster
    if (!cluster) return 'Not connected'

    const version = cluster.version ? ` · ${cluster.version}` : ''
    return `${cluster.host}${version}`
  })

  const busy = $derived(k8s.podsStatus === 'loading' || k8s.clustersStatus === 'loading')
</script>

<div class="flex h-screen flex-col overflow-hidden bg-surface">
  <TopAppBar title={k8s.activeCluster?.id ?? 'K8Sense'} {subtitle}>
    {#snippet actions()}
      {#if k8s.isConnected}
        <StatusIndicator tone="success" label="Connected" compact />
        <Button variant="text" onclick={k8s.disconnect}>Switch cluster</Button>
      {/if}
    {/snippet}
  </TopAppBar>

  <!-- Progress sits directly under the bar so a background refresh is visible
       without the content it is refreshing being replaced by a spinner. -->
  <LinearProgress active={busy} />

  <main class="flex min-h-0 flex-1 flex-col overflow-hidden">
    {#if k8s.isConnected}
      <PodList />
    {:else}
      <div class="min-h-0 flex-1 overflow-auto">
        <ClusterView />
      </div>
    {/if}
  </main>
</div>
