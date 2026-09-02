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
  import {
    formatEnvValue,
    formatMount,
    formatProbe,
    isFromSecret,
    looksSensitive,
    type PodManifest,
  } from '$lib/container'
  import { follower, type OpenObject, type ServesKind } from '$lib/reference'
  import type { Container } from '$lib/api/client'
  import { forwards } from '$stores/forwards.svelte'
  import { configMapData } from '$stores/configMaps.svelte'
  import { secretReveals } from '$stores/secretReveals.svelte'
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
    /**
     * The pod this container belongs to, parsed.
     *
     * For the downward API: a `fieldRef` names a field of THIS pod, and the
     * pod is on screen, so the value is shown rather than the path to it.
     */
    pod?: PodManifest | null
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
    pod,
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

  /** The config map a variable is read from, when it is read from one. */
  function configRef(variable: { valueFrom?: unknown }) {
    return (variable.valueFrom as { configMapKeyRef?: { name?: string; key?: string } })
      ?.configMapKeyRef
  }

  /**
   * The contents of every ConfigMap this container reads from.
   *
   * Fetched on sight, unlike a Secret: a ConfigMap is not secret, and reading
   * one is an ordinary read rather than something an audit rule watches for.
   * Keyed by name, one read per distinct map however many variables cite it.
   *
   * Empty until they arrive and empty for any that cannot be read, and the
   * rows fall back to the reference in both cases — which is what they
   * printed before, so nothing is lost by a refusal or by a slow answer.
   */
  let configMaps = $state<Record<string, Record<string, string>>>({})

  /** Identifies one Secret key across panes, for what is revealed of it. */
  function revealKey(secret: string, key: string): string {
    return `${clusterId}/${namespace}/${secret}/${key}`
  }

  $effect(() => {
    const names = new Set<string>()
    for (const variable of env) {
      const ref = configRef(variable)
      if (ref?.name && ref?.key) names.add(ref.name)
    }
    if (names.size === 0 || !clusterId) return

    let current = true
    void Promise.all(
      [...names].map(async (name) => [name, await configMapData(clusterId, namespace, name)] as const),
    ).then((loaded) => {
      if (current) configMaps = Object.fromEntries(loaded)
    })

    return () => {
      current = false
    }
  })

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
        // Narrowed once, so the closures below carry strings rather than
        // maybes — the guard above already proved both are present.
        const { name: secretName, key: secretKey } = secret as { name: string; key: string }
        // The mask, and the plaintext once somebody asks for it. The words
        // are behind the info button now: thirty variables each spelling out
        // "<set to the key 'x' in secret 'y'>" is thirty lines of one
        // sentence where thirty values should be.
        const key = revealKey(secretName, secretKey)
        const shown = secretReveals.at(key)
        return {
          label: variable.name,
          value: shown.error || shown.value || '••••••••',
          info: `Set to the key '${secretKey}' in secret '${secretName}'`,
          tone: shown.error ? ('critical' as const) : undefined,
          reference: follow('Secret', secretName, namespace),
          // The wording follows what is on screen, and reading is the
          // deliberate act: see $stores/secretReveals.
          action: shown.value
            ? { label: 'Hide value', kind: 'hide' as const, onclick: () => secretReveals.hide(key) }
            : {
                label: 'Reveal value',
                kind: 'reveal' as const,
                onclick: () =>
                  void secretReveals.reveal(key, clusterId, namespace, secretName, secretKey),
              },
        }
      }

      if (looksSensitive(variable as never)) {
        return {
          label: variable.name,
          value: '••••••••',
          info: 'A literal credential, written into the pod spec in the clear — anyone who can read the pod can read it',
          tone: 'warn' as const,
        }
      }

      const configMap = configRef(variable)
      // The value when it has arrived, the reference until then. A key that
      // is absent from the map is NOT shown as empty: an absent key means the
      // container failed to start or the map has changed since it did, and
      // printing nothing would report that as an empty string.
      const resolved =
        configMap?.name && configMap?.key ? configMaps[configMap.name]?.[configMap.key] : undefined

      const field = (variable.valueFrom as { fieldRef?: { fieldPath?: string } })?.fieldRef

      return {
        label: variable.name,
        value: resolved ?? formatEnvValue(variable as never, pod ?? undefined),
        // Said behind the info button once a value replaces the reference to
        // it, because a resolved value no longer names where it came from —
        // and following it still goes there.
        info: configMap?.name
          ? `From the '${configMap.name}' config map, key '${configMap.key}'`
          : field?.fieldPath
            ? `From this pod's own ${field.fieldPath}`
            : undefined,
        // A REFERENCE, NOT A LINK ON THE VALUE. Once the value is the config
        // map's contents it no longer names the config map, so making the
        // contents blue and clickable would be pointing at something the text
        // does not mention.
        reference: configMap?.name ? follow('ConfigMap', configMap.name, namespace) : undefined,
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
          THE PORT'S NAME IS THE LABEL AND THE PORT IS THE VALUE, so a port
          row reads like every other row in the panel: what it is on the left,
          what it says on the right. It used to put the whole thing —
          "http 8080/TCP" — in the label column and leave the value column
          empty with the button floating at the far right, which is the one
          place in the panel where the middle of a row was blank.

          An unnamed port has no name to use, and says so rather than
          borrowing the number: the number is the value.
        -->
        <div class="detail-grid items-center text-body-medium">
          <span class="min-w-0 truncate text-on-surface">
            {port.name || 'Port'}
          </span>

          <span class="flex min-w-0 items-center gap-3">
            <span class="shrink-0 tabular-nums text-on-surface-variant">
              {port.containerPort}/{port.protocol ?? 'TCP'}
            </span>

            {#if open?.reconnecting}
              <!--
                The pod died and a replacement is being sought. Said out loud
                rather than shown as still-connected, because the address is
                still bound and still correct — whatever is pointed at it is
                stalling, not broken, and that is a different thing to tell
                somebody than "the forward is fine".
              -->
              <span class="flex min-w-0 items-center gap-1.5 text-gauge-warn">
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
            {/if}

            <!-- Pushed to the end of the value column rather than given a
                 column of its own: what precedes it is one field's worth of
                 text, so the controls line up anyway. -->
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
          </span>
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
    <DetailList rows={envRows} />

    {#if env.some((variable) => isFromSecret(variable as never)) || env.some((variable) => looksSensitive(variable as never))}
      <!-- Set well clear of the last row. Tucked against it, a note about how
           the pane behaves read as another variable's value. -->
      <p class="mt-5 flex items-start gap-1.5 text-body-small text-on-surface-variant/70">
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
