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
  import SecretReveal from './SecretReveal.svelte'
  import { forwards } from '$stores/forwards.svelte'
  import { BrowserOpenURL } from '$lib/wailsjs/runtime/runtime'
  import { EyeOff, ExternalLink, Loader, Plug, Unplug } from '@lucide/svelte'

  interface Props {
    /** The pod this container belongs to, for forwarding its ports. */
    podName?: string
    podUID?: string
    /** The container's spec, from the parsed manifest. */
    spec: Record<string, any>
    /** Its live status from the pod DTO, when the names match. */
    status?: Container
    /** Identifies the pod, so a Secret key can be read on request. */
    clusterId: string
    namespace: string
    /** The pod's labels, so a forward can find a replacement pod if it dies. */
    labels?: Record<string, string>
  }

  let {
    spec,
    status,
    clusterId,
    namespace,
    podName = '',
    podUID = '',
    labels = {},
  }: Props = $props()

  /**
   * The ports that can actually be forwarded.
   *
   * TCP only. Kubernetes port-forward does not carry UDP, and a button that
   * offers it produces a forward that appears to establish and drops every
   * packet. The backend refuses it too — this is so the button is not there
   * to be pressed.
   */
  const forwardable = $derived(
    ((spec.ports ?? []) as { containerPort: number; name?: string; protocol?: string }[]).filter(
      (port) => !port.protocol || port.protocol.toUpperCase() === 'TCP',
    ),
  )

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

    // Ports are rendered below rather than as a row, because each one carries
    // a control.

    // Requests and limits come from the DTO, already formatted in Go, so the
    // quantity strings are parsed in exactly one place in the codebase.
    if (status?.requests) out.push({ label: 'Requests', value: status.requests })
    if (status?.limits) out.push({ label: 'Limits', value: status.limits })

    // What THIS container is using. The pod's total was always on screen and
    // never said which container it came from — on a pod with a sidecar, half
    // the time the answer is the sidecar, and nothing showed that.
    if (status?.hasMetrics) {
      out.push({ label: 'Using', value: `cpu: ${status.cpu}, memory: ${status.memory}` })
    }

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

  {#if forwardable.length > 0 && podName}
    <!--
      One control per port, next to the port it opens.

      The state comes from the backend's live registry rather than from what
      this component asked for — so a forward that died is not shown as
      running, which is the failure every competing client has an open issue
      about, with a stop button that does nothing because there is nothing
      left to stop.
    -->
    <p class="mt-3 mb-1 text-body-medium text-on-surface">Ports</p>
    <div class="flex flex-col gap-1.5">
      {#each forwardable as port, index (index)}
        {@const open = forwards.forPort(namespace, podName, port.containerPort)}
        {@const busy = forwards.isBusy(namespace, podName, port.containerPort)}
        <div class="flex items-center gap-2 text-body-medium">
          <span class="w-32 shrink-0 text-on-surface-variant">
            {port.name ? `${port.name} ` : ''}{port.containerPort}/{port.protocol ?? 'TCP'}
          </span>

          {#if open?.reconnecting}
            <!--
              The pod died and a replacement is being sought. Said out loud
              rather than shown as still-connected, because the address is
              still bound and still correct — whatever is pointed at it is
              stalling, not broken, and that is a different thing to tell
              somebody than "the forward is fine".
            -->
            <span class="inline-flex items-center gap-1.5 text-body-medium text-gauge-warn">
              <Loader class="size-3.5 animate-spin" strokeWidth={2} />
              pod went away — holding {open.address} while a replacement is found
            </span>
          {:else if open}
            <!-- The address is opened in the real browser, not the webview:
                 this is a link to something on the operator's machine, and
                 loading it inside the application would replace PodSteer. -->
            <button
              type="button"
              onclick={() => BrowserOpenURL(open.address)}
              class="resource-link inline-flex items-center gap-1.5 truncate"
              title="Open {open.address}"
            >
              {open.address}
              <ExternalLink class="size-3.5 shrink-0" strokeWidth={1.8} />
            </button>
          {/if}

          <button
            type="button"
            disabled={busy}
            onclick={() =>
              open
                ? forwards.stop(open)
                : forwards.start(
                    clusterId,
                    namespace,
                    podName,
                    podUID,
                    port.containerPort,
                    port.name ?? '',
                    port.protocol ?? 'TCP',
                    labels,
                  )}
            class="state-layer ml-auto inline-flex h-7 shrink-0 items-center gap-1.5 rounded-sm
                   border border-outline-variant px-2 text-label-large
                   text-on-surface-variant transition-colors duration-100
                   hover:bg-surface-container hover:text-on-surface disabled:opacity-50"
          >
            {#if busy}
              <Loader class="size-3.5 animate-spin" strokeWidth={2} />
            {:else if open}
              <Unplug class="size-3.5" strokeWidth={1.8} />
            {:else}
              <Plug class="size-3.5" strokeWidth={1.8} />
            {/if}
            {open ? 'Stop' : 'Forward'}
          </button>
        </div>
      {/each}
    </div>
  {/if}

  {#if env.length > 0}
    <!--
      Environment last, because it is the longest section and the one people
      scroll past. See container.ts for why no value from a Secret is ever
      resolved here.
    -->
    <p class="mt-3 mb-1 text-body-medium text-on-surface">Environment ({env.length})</p>
    <dl class="grid grid-cols-[25%_1fr] gap-x-4 gap-y-2">
      <!-- By position too. Kubernetes does NOT enforce unique environment
           variable names — a duplicate is legal and the last one wins — so
           keying by name is the same latent crash DetailList had. -->
      {#each env as variable, index (index)}
        {@const ref = (variable.valueFrom as { secretKeyRef?: { name?: string; key?: string } })
          ?.secretKeyRef}
        <dt class="min-w-0 break-words text-body-medium text-on-surface" data-selectable>
          {variable.name}
        </dt>
        <dd class="min-w-0 text-body-medium text-on-surface-variant">
          {#if ref?.name && ref?.key}
            <!-- Read on request only, never on render. -->
            <SecretReveal {clusterId} {namespace} secret={ref.name} secretKey={ref.key} />
          {:else if looksSensitive(variable as never)}
            <!--
              A literal that looks like a credential. There is nothing to
              reveal — it is right there in the manifest tab — so this is not
              a lock, it is a note that the pod spec is carrying a secret in
              the clear where anyone with `get pod` can read it.
            -->
            <span class="inline-flex items-baseline gap-2">
              <span class="font-mono">••••••••</span>
              <span class="text-body-small text-gauge-warn">
                literal credential in the pod spec
              </span>
            </span>
          {:else}
            <span class="break-words" data-selectable>{formatEnvValue(variable as never)}</span>
          {/if}
        </dd>
      {/each}
    </dl>
    {#if env.some((variable) => isFromSecret(variable as never)) || env.some((variable) => looksSensitive(variable as never))}
      <p class="mt-1.5 flex items-start gap-1.5 text-body-small text-on-surface-variant/70">
        <EyeOff class="mt-0.5 size-3.5 shrink-0" strokeWidth={1.8} />
        <span>
          Secret values are read only when you ask, and hide again shortly after. What a
          Secret holds now is not necessarily what this container was started with —
          environment is injected once, at start, and never updated.
        </span>
      </p>
    {/if}
  {/if}
</div>
