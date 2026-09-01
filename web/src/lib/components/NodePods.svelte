<!--
  What is actually running on this node.

  THE QUESTION A NODE PANEL IS OPENED TO ANSWER, second only to how full it is:
  before draining one, before believing a noisy-neighbour theory, or when a
  machine is unhealthy and the thing that matters is who is on it.

  Fetched here rather than filtered from a list already in memory, because the
  pod list is scoped to the namespace being browsed and this is a question
  about the machine — the answer routinely includes kube-system pods somebody
  never navigated to. The API server indexes `spec.nodeName`, so this is one
  narrow read rather than the whole cluster sifted client-side.

  ON DEMAND, when the section is opened. A node list of fifty rows would
  otherwise fire fifty cross-namespace reads for panels nobody expanded.
-->
<script lang="ts">
  import { listPodsOnNode, type Pod } from '$lib/api/client'
  import { toApiError } from '$lib/api/errors'
  import DetailSection from './DetailSection.svelte'
  import StatusIndicator from './StatusIndicator.svelte'
  import { podTone } from '$lib/format'

  interface Props {
    clusterId: string
    nodeName: string
    /** Follows a pod into its own panel. */
    onopen?: (kindName: string, name: string, namespace: string) => void
  }

  let { clusterId, nodeName, onopen }: Props = $props()

  let pods = $state.raw<Pod[]>([])
  let loading = $state(false)
  let failure = $state('')
  /** The node the current `pods` describe, so a change of node refetches. */
  let loadedFor = $state('')

  /**
   * Groups by namespace, because that is how somebody reads a node.
   *
   * A flat list of forty pods on a busy node is a wall; the same list under
   * `kube-system`, `monitoring`, `default` is answerable at a glance — and the
   * first question about an unfamiliar node is usually how much of it is
   * platform and how much is workload.
   */
  const grouped = $derived.by(() => {
    const byNamespace = new Map<string, Pod[]>()
    for (const pod of pods) {
      const list = byNamespace.get(pod.namespace)
      if (list) list.push(pod)
      else byNamespace.set(pod.namespace, [pod])
    }
    return [...byNamespace.entries()].sort(([a], [b]) => a.localeCompare(b))
  })

  /** Pods that are not simply running, which is what the count should lead with. */
  const unhealthy = $derived(pods.filter((pod) => !pod.isHealthy).length)

  async function load(): Promise<void> {
    if (loading || loadedFor === nodeName) return

    loading = true
    failure = ''
    try {
      pods = await listPodsOnNode(clusterId, nodeName)
      loadedFor = nodeName
    } catch (error) {
      // Named rather than swallowed: an account that may not list pods across
      // namespaces gets a partial or refused answer, and "no pods" would be a
      // lie about the node.
      failure = toApiError(error).message
      pods = []
    } finally {
      loading = false
    }
  }

  // A different node invalidates what is held. Not a refetch — the section may
  // be closed, and opening it is what asks.
  $effect(() => {
    if (nodeName !== loadedFor) {
      pods = []
      failure = ''
    }
  })
</script>

<DetailSection
  level="h3"
  id="node-pods"
  title="Pods"
  hint={loadedFor === nodeName
    ? `${pods.length}${unhealthy > 0 ? ` · ${unhealthy} not ready` : ''}`
    : undefined}
  onopen={load}
>
  {#if loading}
    <p class="py-2 text-body-small text-on-surface-variant/70">Reading the node's pods…</p>
  {:else if failure}
    <p class="py-2 text-body-small text-error">{failure}</p>
  {:else if pods.length === 0}
    <p class="py-2 text-body-small text-on-surface-variant/70">
      Nothing is scheduled on this node.
    </p>
  {:else}
    <div class="flex flex-col gap-3">
      {#each grouped as [namespace, group] (namespace)}
        <div class="flex flex-col gap-1">
          <p class="text-label-small text-on-surface-variant/70">
            {namespace}
            <span class="text-on-surface-variant/50">· {group.length}</span>
          </p>

          <ul class="flex flex-col divide-y divide-outline-variant/30">
            {#each group as pod (pod.name)}
              <li>
                <button
                  type="button"
                  onclick={() => onopen?.('Pod', pod.name, pod.namespace)}
                  disabled={!onopen}
                  class="state-layer flex w-full items-center gap-2 py-1.5 text-left
                         transition-colors duration-100 disabled:pointer-events-none"
                >
                  <span class="min-w-0 flex-1 truncate text-body-small text-on-surface">
                    {pod.name}
                  </span>
                  <!-- Restarts, but only when there are any: a column of
                       zeroes down a healthy node is noise that makes the one
                       row that matters harder to find. -->
                  {#if pod.restarts > 0}
                    <span class="shrink-0 text-label-small text-on-surface-variant tabular-nums">
                      {pod.restarts}×
                    </span>
                  {/if}
                  <span class="shrink-0 text-label-small text-on-surface-variant">
                    {pod.phase}
                  </span>
                  <StatusIndicator tone={podTone(pod)} label={pod.phase} />
                </button>
              </li>
            {/each}
          </ul>
        </div>
      {/each}
    </div>
  {/if}
</DetailSection>
