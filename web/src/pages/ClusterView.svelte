<!--
  Cluster picker — the first view, shown until a cluster is connected.

  It lists every context in the local kubeconfig. Connecting is an explicit
  action rather than something that happens on launch: probing every configured
  cluster at startup would stall behind whichever ones are unreachable, which
  on a laptop with a dozen contexts is most of them.
-->
<script lang="ts">
  import Button from '$lib/components/Button.svelte'
  import Card from '$lib/components/Card.svelte'
  import EmptyState from '$lib/components/EmptyState.svelte'
  import ErrorBanner from '$lib/components/ErrorBanner.svelte'
  import StatusIndicator from '$lib/components/StatusIndicator.svelte'
  import { k8s } from '$stores/k8s.svelte'
</script>

<div class="mx-auto w-full max-w-3xl px-6 py-10">
  <div class="mb-8">
    <h2 class="text-headline-small text-on-surface">Choose a cluster</h2>
    <p class="mt-1 text-body-medium text-on-surface-variant">
      Contexts found in your kubeconfig. K8Sense connects only when you ask it to.
    </p>
  </div>

  <ErrorBanner
    error={k8s.error}
    onretry={k8s.loadClusters}
    ondismiss={() => (k8s.error = null)}
    class="mb-6"
  />

  {#if k8s.clustersStatus === 'loading' && k8s.clusters.length === 0}
    <p class="text-body-medium text-on-surface-variant">Reading kubeconfig…</p>
  {:else if k8s.clusters.length === 0}
    <EmptyState
      title="No clusters configured"
      description="K8Sense found no usable contexts in your kubeconfig. Add one with `kubectl config set-context`, then reload."
    >
      {#snippet action()}
        <Button variant="tonal" onclick={k8s.loadClusters}>Reload kubeconfig</Button>
      {/snippet}
    </EmptyState>
  {:else}
    <ul class="flex flex-col gap-3">
      {#each k8s.clusters as cluster (cluster.id)}
        <li>
          <Card variant="outlined" class="flex items-center gap-4 p-4">
            <div class="min-w-0 flex-1">
              <div class="flex items-center gap-2">
                <h3 class="truncate text-title-medium text-on-surface" data-selectable>
                  {cluster.id}
                </h3>
                {#if cluster.isCurrent}
                  <!-- Marking the kubeconfig's current context saves the
                       operator cross-checking against kubectl. -->
                  <span
                    class="rounded-xs bg-secondary-container px-2 py-0.5 text-label-small text-on-secondary-container"
                  >
                    current
                  </span>
                {/if}
              </div>

              <p class="mt-0.5 truncate text-body-small text-on-surface-variant" data-selectable>
                {cluster.host} · {cluster.authInfo || 'no user'}
                {#if cluster.defaultNamespace}
                  · ns {cluster.defaultNamespace}
                {/if}
              </p>

              {#if cluster.isReachable}
                <StatusIndicator
                  tone="success"
                  label="{cluster.version} · {cluster.platform}"
                  class="mt-2"
                />
              {/if}
            </div>

            <Button
              variant={cluster.isCurrent ? 'filled' : 'tonal'}
              loading={k8s.connectingTo === cluster.id}
              disabled={k8s.connectingTo !== null && k8s.connectingTo !== cluster.id}
              onclick={() => k8s.connect(cluster.id)}
            >
              {k8s.connectingTo === cluster.id ? 'Connecting' : 'Connect'}
            </Button>
          </Card>
        </li>
      {/each}
    </ul>
  {/if}
</div>
