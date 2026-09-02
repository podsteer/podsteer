<!--
  One container, in full.

  NOT A CARD. It was one — a bordered, tinted box per container — and that
  made the Containers section the only part of the panel that looked like
  something else: everywhere around it, a section is a heading, a rule and
  rows on the drawer's grid, and this was a heading, a rule and a stack of
  boxes.

  The inset cost something real as well as visual. A card's own padding
  narrows the grid inside it, so the label column in a container's rows was a
  few pixels off the one in every section above and below — the shared column
  is a share of its container, and the container was different.

  The grouping the card provided is still needed, because a pod's containers
  are peers and their fields repeat: without it, "Image" appears four times in
  one pane with nothing saying which is which. A name, and a rule between one
  container and the next, does that — which is exactly how the panel
  separates everything else.

  Everything here is a QUOTATION of the spec — ports, probes, mounts,
  environment — composed into the strings kubectl prints. Nothing on this card
  reaches a conclusion; anything that does belongs in the Go domain, where it
  can be argued with in a test. See web/src/lib/container.ts.
-->
<script lang="ts">
  import DetailList, { type DetailRow } from './DetailList.svelte'
  import { formatEnvValue, formatMount, formatProbe, isFromSecret, looksSensitive } from '$lib/container'
  import { follower, type OpenObject, type ServesKind } from '$lib/reference'
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
    /** Whether this cluster serves a kind. See $lib/reference. */
    canOpen?: ServesKind
    /** Follows a reference to the object it names. */
    onopen?: OpenObject
  }

  let {
    spec,
    status,
    clusterId,
    namespace,
    podName = '',
    podUID = '',
    labels = {},
    canOpen,
    onopen,
  }: Props = $props()

  /** Turns a reference into a click handler, or into nothing. */
  const follow = $derived(follower(canOpen, onopen))

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
    const out: DetailRow[] = [{ label: 'Image', value: status?.image || spec.image || '—' }]

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

  /** The secret a variable is read from, when it is read from one. */
  function secretRef(variable: { valueFrom?: unknown }) {
    return (variable.valueFrom as { secretKeyRef?: { name?: string; key?: string } })?.secretKeyRef
  }

  /**
   * Environment as rows, so it sits on the same grid as everything else.
   *
   * Two kinds of cell are marked `control` rather than left as text: a value
   * read from a Secret, whose cell is a reveal button, and a literal that
   * looks like a credential, whose cell is a mask and a warning. Everything
   * else is a string, and a string that names a ConfigMap is followable.
   */
  const envRows = $derived.by<DetailRow[]>(() =>
    env.map((variable) => {
      const secret = secretRef(variable)
      if (secret?.name && secret?.key) {
        return {
          label: variable.name,
          value: `<set to the key '${secret.key}' in secret '${secret.name}'>`,
          control: true,
        }
      }

      if (looksSensitive(variable as never)) {
        return { label: variable.name, value: 'literal credential in the pod spec', control: true }
      }

      const configMap = (
        variable.valueFrom as { configMapKeyRef?: { name?: string; key?: string } }
      )?.configMapKeyRef

      return {
        label: variable.name,
        value: formatEnvValue(variable as never),
        onclick: configMap?.name ? follow('ConfigMap', configMap.name, namespace) : undefined,
      }
    }),
  )
</script>

<!--
  The rule and the space above it belong to every container but the first, so
  the section's own heading rule is not immediately followed by another.
-->
<div
  class="flex flex-col [&:not(:first-child)]:mt-4 [&:not(:first-child)]:border-t
         [&:not(:first-child)]:border-outline-variant/40 [&:not(:first-child)]:pt-4"
>
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
        <!--
          A GRID, not a flex row with a margin pushing the button right. With
          `ml-auto` the button's position depended on how long the address
          beside it was, so a forwarded port and an unforwarded one put their
          controls in different places and the column read as ragged. Three
          columns line them up regardless of what is in the middle.
        -->
        <div
          class="grid grid-cols-[var(--detail-label-width)_1fr_auto] items-center gap-x-4
                 gap-y-2 text-body-medium"
        >
          <span class="min-w-0 truncate text-on-surface-variant">
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
            <span class="flex min-w-0 items-center gap-1.5 text-body-medium text-gauge-warn">
              <Loader class="size-3.5 shrink-0 animate-spin" strokeWidth={2} />
              <span class="truncate">holding {open.address} — finding a replacement pod</span>
            </span>
          {:else if open}
            <!-- The address is opened in the real browser, not the webview:
                 this is a link to something on the operator's machine, and
                 loading it inside the application would replace PodSteer. -->
            <button
              type="button"
              onclick={() => BrowserOpenURL(open.address)}
              class="resource-link flex min-w-0 items-center gap-1.5 text-left"
              title="Open {open.address}"
            >
              <span class="truncate">{open.address}</span>
              <ExternalLink class="size-3.5 shrink-0" strokeWidth={1.8} />
            </button>
          {:else}
            <!-- An empty cell holds the column open, so the buttons below and
                 above this row stay in line. -->
            <span></span>
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
            class="state-layer inline-flex h-7 shrink-0 items-center gap-1.5 rounded-sm
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

    <!--
      The same list as every other section, on the same grid. It used to be a
      hand-written <dl> at its own proportions, which is exactly how a panel
      ends up looking like four components stacked rather than one.
    -->
    <DetailList rows={envRows} value={envValue} />

    {#snippet envValue(_row: DetailRow, index: number)}
      {@const variable = env[index]}
      {@const secret = secretRef(variable)}
      {#if secret?.name && secret?.key}
        <!-- Read on request only, never on render. -->
        <SecretReveal
          {clusterId}
          {namespace}
          secret={secret.name}
          secretKey={secret.key}
          onopen={follow('Secret', secret.name, namespace)}
        />
      {:else}
        <!--
          A literal that looks like a credential. There is nothing to reveal —
          it is right there in the manifest tab — so this is not a lock, it is
          a note that the pod spec is carrying a secret in the clear where
          anyone with `get pod` can read it.
        -->
        <span class="inline-flex items-baseline gap-2">
          <span class="font-mono">••••••••</span>
          <span class="text-body-small text-gauge-warn">literal credential in the pod spec</span>
        </span>
      {/if}
    {/snippet}

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
