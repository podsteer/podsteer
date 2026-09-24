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

  IT IS BEHIND AN ICON BESIDE THE TREND HEADING, not printed under the chart.
  It is provenance — true of the whole screen, asked for once, and then known.
  Three paragraphs standing permanently under a chart pushed the sections
  below it off the fold and read as a warning about the chart, which it is
  not. The icon keeps both halves a hover or a click away and costs the page
  nothing while nobody is asking.

  IT IS ADVICE AND NOT A SOURCE. Discovery listed Services by label and name;
  that establishes that an object called kube-state-metrics exists here, not
  that it is running, not that anything scrapes it, and not that a single
  series it produces has been kept. PodSteer never connects to it — there is
  not even a proxy target on the value, deliberately — and this note claims
  nothing a service listing cannot support.
-->
<script lang="ts">
  import type { KubeStateMetrics } from '$lib/api/client'
  import InfoHint from './InfoHint.svelte'

  interface Props {
    kubeState: KubeStateMetrics | null | undefined
  }

  let { kubeState }: Props = $props()

  /**
   * Plain text rather than markup, because the panel takes a string. The
   * service's own name led the sentence when it was a paragraph and still
   * leads it here, so the reader is told what was found before being told
   * what PodSteer does with it.
   */
  const text = $derived(
    kubeState?.found
      ? `${kubeState.label} is running in this cluster, which is where object gauges ` +
        'like replica counts and Job results in a Grafana dashboard usually come from. ' +
        'PodSteer does not read it: every figure on this screen comes from the metrics ' +
        'API and from the samples PodSteer takes while it is open.'
      : '',
  )
</script>

{#if text}
  <InfoHint {text} label="Where these figures come from" />
{/if}
