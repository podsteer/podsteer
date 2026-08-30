<!--
  One container, in full.

  A card per container rather than a flat list, because a pod's containers are
  peers and the fields repeat: without the grouping, "Image" appears four
  times in one pane with nothing saying which is which.

  Everything here is a QUOTATION of the spec — ports, probes, mounts,
  environment — composed into the strings kubectl prints. Nothing on this card
  reaches a conclusion; anything that does belongs in the Go domain, where it
  can be argued with in a test. See web/src/lib/container.ts.
-->
<script lang="ts">
  import DetailList, { type DetailRow } from './DetailList.svelte'
  import { formatEnvValue, formatMount, formatPorts, formatProbe, isFromSecret, looksSensitive } from '$lib/container'
  import type { Container } from '$lib/api/client'
  import { EyeOff } from '@lucide/svelte'

  interface Props {
    /** The container's spec, from the parsed manifest. */
    spec: Record<string, any>
    /** Its live status from the pod DTO, when the names match. */
    status?: Container
  }

  let { spec, status }: Props = $props()

  /**
   * The identity rows: what is running, and whether it is up.
   *
   * `status.image` rather than `spec.image` where we have it — the status
   * carries the digest-resolved reference actually running, which differs
   * from the spec whenever a mutable tag has been re-pushed underneath.
   */
  const rows = $derived.by(() => {
    const out: DetailRow[] = [{ label: 'Image', value: status?.image || spec.image || '—', truncate: true }]

    if (spec.imagePullPolicy) out.push({ label: 'Pull policy', value: spec.imagePullPolicy })

    const ports = formatPorts(spec.ports)
    if (ports) out.push({ label: 'Ports', value: ports })

    // Requests and limits come from the DTO, already formatted in Go, so the
    // quantity strings are parsed in exactly one place in the codebase.
    if (status?.requests) out.push({ label: 'Requests', value: status.requests })
    if (status?.limits) out.push({ label: 'Limits', value: status.limits })

    // ALL THREE PROBES. A probe missing from a pane reads as a container
    // without one, which is a different and much calmer fact than the truth.
    const probes: [string, unknown][] = [
      ['Liveness', spec.livenessProbe],
      ['Readiness', spec.readinessProbe],
      ['Startup', spec.startupProbe],
    ]
    for (const [label, probe] of probes) {
      const formatted = formatProbe(probe as never)
      if (formatted) out.push({ label, value: formatted })
    }

    if (spec.command?.length) out.push({ label: 'Command', value: spec.command.join(' ') })
    if (spec.args?.length) out.push({ label: 'Args', value: spec.args.join(' ') })

    for (const mount of spec.volumeMounts ?? []) {
      out.push({ label: 'Mount', value: formatMount(mount) })
    }

    return out
  })

  const env = $derived((spec.env ?? []) as { name: string; value?: string; valueFrom?: unknown }[])
</script>

<div class="rounded-sm border border-outline-variant bg-surface-container-low p-3">
  <p class="mb-2 flex items-baseline gap-2 text-body-medium">
    <span class="font-medium text-on-surface" data-selectable>{spec.name}</span>
    {#if status}
      <!--
        `started` and `ready` are separate facts and are reported separately.
        Started-but-not-ready is a readiness problem; not-started is a startup
        problem. Every other client collapses them into one word and sends
        people to look in the wrong place.
      -->
      <span class="text-body-small text-on-surface-variant">
        {status.state.toLowerCase()}{status.ready ? ', ready' : status.started ? ', not ready' : ', starting'}
        {#if status.reason}· {status.reason}{/if}
      </span>
    {/if}
  </p>

  <DetailList {rows} />

  {#if env.length > 0}
    <!--
      Environment last, because it is the longest section and the one people
      scroll past. See container.ts for why no value from a Secret is ever
      resolved here.
    -->
    <p class="mt-3 mb-1 text-body-medium text-on-surface">Environment ({env.length})</p>
    <DetailList
      rows={env.map((variable) => ({
        label: variable.name,
        value: looksSensitive(variable as never)
          ? '••••••••  (looks like a credential — check the manifest if you meant this)'
          : formatEnvValue(variable as never),
        truncate: true,
      }))}
    />
    {#if env.some((variable) => isFromSecret(variable as never)) || env.some((variable) => looksSensitive(variable as never))}
      <p class="mt-1.5 flex items-start gap-1.5 text-body-small text-on-surface-variant/70">
        <EyeOff class="mt-0.5 size-3.5 shrink-0" strokeWidth={1.8} />
        <span>
          Secret values are not read. What a Secret holds now is not necessarily what this
          container was started with — environment is injected once, at start.
        </span>
      </p>
    {/if}
  {/if}
</div>
