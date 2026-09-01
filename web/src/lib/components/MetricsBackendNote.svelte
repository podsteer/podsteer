<!--
  Points at a monitoring system already running in the cluster.

  WHY THIS EXISTS. PodSteer's charts cover the window the application has been
  open — minutes, usually — because Kubernetes reports only the present and the
  samples are ours. A cluster running Prometheus already holds months of the
  same figures. Saying so is more useful than quietly implying our window is
  the whole picture, and far more honest than persisting samples to disk to
  fake a history that would be full of gaps whenever the app was closed.

  It is ADVICE, not a feature toggle: PodSteer does not query the backend, and
  this note claims nothing about what it contains. Discovery found a service
  whose name and labels say Prometheus; whether it scrapes this cluster's
  kubelets is not something a service listing can establish.
-->
<script lang="ts">
  import { LineChart } from '@lucide/svelte'
  import type { MetricsBackend } from '$lib/api/client'

  interface Props {
    backend: MetricsBackend | null | undefined
    /** How long our own samples actually cover, for the contrast. */
    windowLabel?: string
  }

  let { backend, windowLabel }: Props = $props()
</script>

{#if backend?.kind}
  <p
    class="flex items-start gap-2 rounded-md bg-surface-container-low px-3 py-2
           text-body-small text-on-surface-variant"
  >
    <LineChart class="mt-0.5 size-3.5 shrink-0 opacity-60" strokeWidth={2} />
    <span>
      {#if windowLabel}
        This chart covers {windowLabel} — the samples PodSteer has taken since it opened.
      {:else}
        This chart covers only the time PodSteer has been open.
      {/if}
      <strong class="font-medium text-on-surface">{backend.label}</strong>
      is running in this cluster and will have kept far more.
    </span>
  </p>
{/if}
