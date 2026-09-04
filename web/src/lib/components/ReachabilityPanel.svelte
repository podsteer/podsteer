<!--
  Can this actually be reached, and from where.

  TWO VANTAGES, AND THE DIFFERENCE BETWEEN THEM IS THE FEATURE. The API
  server's own proxy reaching a Service says the endpoints answer the API
  server; it says nothing about whether a workload subject to a NetworkPolicy
  can reach them. A container reaching it says the opposite thing. So the
  vantage is named on the control before a probe runs, and named again on the
  result after — there is no path through this component that shows an outcome
  without saying who is speaking.

  EVERY PROBE IS EXPLICIT AND ONE-SHOT. Nothing here is on the refresh tick,
  and nothing re-runs on its own: an answer stays on screen with the time it
  was taken, the way a sampled chart says how long its window is, rather than
  quietly implying it is live. A target that refuses a connection is an
  ordinary result rendered in place — never a dialog, because a refusal is the
  answer somebody pressed the button to get.

  THE IN-CLUSTER PROBE RUNS SOMETHING IN SOMEBODY ELSE'S CONTAINER, which is
  write-shaped whatever it reads. The backend refuses it on a cluster marked
  read-only and audits it by cluster, namespace, pod, container and target; the
  control here is disabled with the reason rather than failing when pressed,
  the same way every other write control in the drawer behaves.
-->
<script lang="ts">
  import { probeFromHere, probeFromPod, type ProbeResult } from '$lib/api/client'
  import { toApiError } from '$lib/api/errors'
  import {
    outcomeTone,
    probeSubject,
    probeTargets,
    routeLabel,
    stepLabel,
    stepTone,
    takenAgo,
    vantageOptions,
    type ProbeTarget,
    type Vantage,
  } from '$lib/reachability'
  import DetailSection from './DetailSection.svelte'
  import Button from './Button.svelte'
  import Select from './Select.svelte'
  import { CircleCheck, CircleX, CircleHelp, Radar } from '@lucide/svelte'

  interface Props {
    /** The object's Kubernetes Kind, verbatim. */
    kind: string
    /** The object's own manifest, already parsed by the pane above. */
    manifest: unknown
    clusterId: string
    /**
     * A pod and container the pane already has in hand, used to seed the
     * in-cluster probe's source.
     *
     * ALWAYS AN EXISTING POD — nothing is created for a probe, ever. On a pod
     * pane the pod itself is the obvious source; on a Service or an Ingress
     * pane there is none in hand, so the operator names one, which is the
     * same "a pod the operator chooses" either way.
     */
    probePod?: string
    probeContainer?: string
    /** Whether the cluster is marked read-only, and the sentence saying so. */
    isReadOnly?: boolean
    readOnlyReason?: string
  }

  let {
    kind,
    manifest,
    clusterId,
    probePod = '',
    probeContainer = '',
    isReadOnly = false,
    readOnlyReason = '',
  }: Props = $props()

  const targets = $derived(probeTargets(kind, manifest))
  const vantages = $derived(vantageOptions(kind))

  let selectedKey = $state('')

  /**
   * Where an in-cluster probe runs, seeded by whatever the pane had.
   *
   * Typed rather than picked from a list on purpose: listing the namespace's
   * pods to fill a dropdown would cost a LIST every time this section opened,
   * which is the polling storm the read cache exists to prevent — and the
   * operator opening this already knows which workload's point of view they
   * want.
   */
  let typedPod = $state<string | null>(null)
  let typedContainer = $state<string | null>(null)
  // Null means "nothing typed yet", so the pane's own pod stays the default
  // until the operator says otherwise — and keeps following the object when
  // the drawer moves to another one.
  const sourcePod = $derived(typedPod ?? probePod)
  const sourceContainer = $derived(typedContainer ?? probeContainer)
  const selected = $derived<ProbeTarget | undefined>(
    targets.find((target) => target.key === selectedKey) ?? targets[0],
  )

  /**
   * The last answer, and when it was taken. Component-local and nowhere else:
   * a probe result is about one object at one instant, it dies with the
   * drawer, and the caller keys this component on the object so switching
   * rows starts over rather than showing a stale answer under a new name.
   */
  let result = $state<ProbeResult | null>(null)
  let takenAt = $state(0)
  let error = $state('')
  let running = $state<Vantage | null>(null)

  /** Re-read on each render so the age is current without a timer of its own. */
  let now = $state(Date.now())

  function canProbeInCluster(): boolean {
    return !!sourcePod.trim() && !!sourceContainer.trim() && !isReadOnly
  }

  function inClusterBlockedReason(): string {
    if (isReadOnly) {
      // Refused here as well as in the backend, and for the reason every
      // other write control is: the guard is against the UI's own bugs, and a
      // control that fails when pressed is worse than one that says why.
      return readOnlyReason || 'This cluster is marked read-only, and a probe runs a command in a container.'
    }
    if (!sourcePod.trim() || !sourceContainer.trim()) {
      return 'Name a pod and container in this namespace to probe from. It has to be one that is already running: nothing is created for a probe.'
    }
    return ''
  }

  async function run(vantage: Vantage): Promise<void> {
    if (!selected) return

    running = vantage
    error = ''
    // Cleared before the call rather than after it: leaving the previous
    // answer up while a new probe runs is how a panel shows one vantage's
    // verdict under another vantage's heading.
    result = null

    const subject = probeSubject(kind, manifest, selected)

    try {
      result =
        vantage === 'local'
          ? await probeFromHere(clusterId, subject)
          : await probeFromPod(
              clusterId,
              subject.namespace,
              sourcePod.trim(),
              sourceContainer.trim(),
              subject,
            )
      takenAt = Date.now()
      now = takenAt
    } catch (cause) {
      // A refusal to PERFORM the probe — an ExternalName Service, a UDP port,
      // an account that may not use the proxy. Rendered here, in place of the
      // result, because each is a fact that stays true however many times the
      // button is pressed.
      error = toApiError(cause).message
    } finally {
      running = null
    }
  }

  const TONE_ICON = {
    good: CircleCheck,
    bad: CircleX,
    unknown: CircleHelp,
  } as const

  const TONE_CLASS = {
    good: 'text-success',
    bad: 'text-error',
    unknown: 'text-on-surface-variant/70',
  } as const
</script>

{#if targets.length > 0}
  <DetailSection
    level="h3"
    id="reachability"
    title="Reachability"
    defaultOpen={false}
    hint={String(targets.length)}
  >
    <div class="flex flex-col gap-4">
      <p class="text-body-small leading-relaxed text-on-surface-variant/70">
        One probe, when you ask for one. Nothing here runs on the refresh tick, and
        each attempt is bounded by a few seconds.
      </p>

      {#if targets.length > 1}
        <Select
          label="Port"
          value={selected?.key ?? ''}
          onchange={(key) => (selectedKey = key)}
          options={targets.map((target) => ({ value: target.key, label: target.label }))}
        />
      {:else}
        <p class="text-body-small text-on-surface-variant">{targets[0].label}</p>
      {/if}

      <!-- One block per vantage, each stating what an answer from it would
           actually establish. That sentence is not decoration: it is the
           difference between the two, and the reason both are offered. -->
      {#each vantages as vantage (vantage.vantage)}
        {@const blocked =
          vantage.vantage === 'in_cluster' && !canProbeInCluster() ? inClusterBlockedReason() : ''}
        <div class="flex flex-col gap-2 border-t border-outline-variant pt-3 first:border-t-0 first:pt-0">
          <div class="flex items-start justify-between gap-3">
            <div class="min-w-0">
              <p class="text-body-medium font-medium text-on-surface">{vantage.label}</p>
              <p class="mt-1 text-body-small leading-relaxed text-on-surface-variant/80">
                {vantage.available ? vantage.meaning : vantage.unavailableReason}
              </p>
              {#if blocked}
                <p class="mt-1 text-body-small leading-relaxed text-on-surface-variant/80">
                  {blocked}
                </p>
              {/if}

              {#if vantage.vantage === 'in_cluster' && vantage.available && !isReadOnly}
                <div class="mt-2 flex gap-2">
                  <label class="min-w-0 flex-1">
                    <span class="text-body-small text-on-surface-variant">Pod</span>
                    <input
                      type="text"
                      value={sourcePod}
                      oninput={(event) => (typedPod = event.currentTarget.value)}
                      autocomplete="off"
                      spellcheck="false"
                      placeholder="pod name"
                      class="field mt-1 w-full px-3 py-1.5 font-mono text-body-small"
                    />
                  </label>
                  <label class="min-w-0 flex-1">
                    <span class="text-body-small text-on-surface-variant">Container</span>
                    <input
                      type="text"
                      value={sourceContainer}
                      oninput={(event) => (typedContainer = event.currentTarget.value)}
                      autocomplete="off"
                      spellcheck="false"
                      placeholder="container name"
                      class="field mt-1 w-full px-3 py-1.5 font-mono text-body-small"
                    />
                  </label>
                </div>
              {/if}
            </div>

            {#if vantage.available}
              <Button
                variant="outlined"
                class="shrink-0"
                loading={running === vantage.vantage}
                disabled={!!blocked || running !== null}
                onclick={() => run(vantage.vantage)}
              >
                <Radar class="size-3.5" strokeWidth={2} />
                Probe
              </Button>
            {/if}
          </div>
        </div>
      {/each}

      {#if error}
        <p class="text-body-small leading-relaxed text-on-surface-variant" role="status">
          {error}
        </p>
      {/if}

      {#if result}
        {@const tone = outcomeTone(result.outcome)}
        {@const Icon = TONE_ICON[tone]}
        <div class="flex flex-col gap-3 rounded-sm border border-outline-variant bg-surface-container-low p-3">
          <div class="flex items-start gap-2">
            <Icon class="mt-0.5 size-4 shrink-0 {TONE_CLASS[tone]}" strokeWidth={2} />
            <div class="min-w-0 flex-1">
              <!-- The summary already names the vantage; the line under it
                   names the route, because "from this machine" means two
                   different things depending on whether the API server made
                   the connection or merely carried it. -->
              <p class="text-body-medium leading-relaxed text-on-surface" data-selectable>
                {result.summary}
              </p>
              <p class="mt-1 text-body-small text-on-surface-variant/70">
                {result.target}
                {#if routeLabel(result)}· {routeLabel(result)}{/if}
                · {result.elapsedMs} ms · taken {takenAgo(takenAt, now)}
              </p>
            </div>
          </div>

          <!-- Every step, in the order they happened. Resolution and
               connection stay apart because they need opposite next steps:
               a name that does not resolve is a Service that is not there or
               a resolver that is not serving it; an address that refuses is a
               policy, a listener that is not up, or a port nothing binds. -->
          <dl class="flex flex-col gap-1.5">
            {#each result.steps as step (step.name)}
              {@const StepIcon = TONE_ICON[stepTone(step)]}
              <div class="flex items-start gap-2">
                <StepIcon
                  class="mt-0.5 size-3.5 shrink-0 {TONE_CLASS[stepTone(step)]}"
                  strokeWidth={2}
                />
                <dt class="text-body-small font-medium text-on-surface">{stepLabel(step)}</dt>
                <dd class="min-w-0 flex-1 text-body-small text-on-surface-variant" data-selectable>
                  {step.detail || step.status}
                </dd>
              </div>
            {/each}
          </dl>
        </div>
      {/if}
    </div>
  </DetailSection>
{/if}
