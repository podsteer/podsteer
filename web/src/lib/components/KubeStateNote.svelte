<!--
  Points at a kube-state-metrics installation already running in the cluster.

  WHY THIS EXISTS, and it is a different question from the one
  MetricsBackendNote answers. kube-state-metrics turns the API server's
  objects into Prometheus gauges — replicas wanted against replicas ready, why
  a pod is not ready, when a Job last succeeded — and it is where a great many
  of the panels in somebody's Grafana actually come from. An operator looking
  at two screens with two sets of numbers deserves to be told which of them is
  fed by what, rather than left to guess whether the two are meant to agree.

  SO THE NOTE SAYS BOTH HALVES. It names what was found, and it says plainly
  that nothing on PodSteer's screens comes from it: the figures here are read
  from the metrics API and from the samples PodSteer takes while it is open.

  IT IS ADVICE AND NOT A SOURCE. Discovery listed Services by label and name;
  that establishes that an object called kube-state-metrics exists here, not
  that it is running, not that anything scrapes it, and not that a single
  series it produces has been kept. PodSteer never connects to it — there is
  not even a proxy target on the value, deliberately — and this note claims
  nothing a service listing cannot support.
-->
<script lang="ts">
  import { Boxes } from '@lucide/svelte'
  import type { KubeStateMetrics } from '$lib/api/client'

  interface Props {
    kubeState: KubeStateMetrics | null | undefined
  }

  let { kubeState }: Props = $props()
</script>

{#if kubeState?.found}
  <p
    class="flex items-start gap-2 rounded-md bg-surface-container-low px-3 py-2
           text-body-small text-on-surface-variant"
  >
    <Boxes class="mt-0.5 size-3.5 shrink-0 opacity-60" strokeWidth={2} />
    <span>
      <strong class="font-medium text-on-surface">{kubeState.label}</strong>
      is running in this cluster, which is where object gauges like replica counts and Job
      results in a Grafana dashboard usually come from. PodSteer does not read it: every figure
      on this screen comes from the metrics API and from the samples PodSteer takes while it is
      open.
    </span>
  </p>
{/if}
