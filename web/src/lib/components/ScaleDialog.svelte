<!--
  Dialog for scaling a deployment or statefulset.
-->
<script lang="ts">
  import { escapeLayer, type EscapeClaim } from '$lib/escape'
  import { modal } from '$lib/modal'
  import { scale } from '$lib/kubectl'
  import Button from './Button.svelte'
  import KubectlHint from './KubectlHint.svelte'
  import { follower, type OpenObject, type ServesKind } from '$lib/reference'
  import type { AutoscalerCheck, AutoscalerRef } from '$lib/autoscalers'
  import { TriangleAlert } from '@lucide/svelte'

  interface Props {
    open: boolean
    currentReplicas: number
    onclose: () => void
    onconfirm: (replicas: number) => void
    /** The kubeconfig context this cluster connects through. See $lib/kubectl. */
    ctx: string
    /** "Deployment" or "StatefulSet" — the only two kinds this dialog scales; also matched against an autoscaler's scale target. */
    kind: string
    name: string
    namespace: string
    /**
     * Asks whether an autoscaler targets this workload.
     *
     * A PROP RATHER THAN AN IMPORT, so the dialog stays a plain view over
     * what it is handed. The caching that keeps this to one request per kind
     * — not one per workload the operator opens the dialog on — lives with
     * the session that already holds the cluster and its catalog.
     */
    checkAutoscalers: (kind: string, namespace: string, name: string) => Promise<AutoscalerCheck>
    /** Resolves a Kind to this cluster's navigator id, for the autoscaler link. */
    canOpen?: ServesKind
    onopen?: OpenObject
  }

  let {
    open,
    currentReplicas,
    onclose,
    onconfirm,
    ctx,
    kind,
    name,
    namespace,
    checkAutoscalers,
    canOpen,
    onopen,
  }: Props = $props()

  // Seeded by the effect below rather than from the prop directly: reading a
  // prop into $state() captures only its initial value, and the dialog is kept
  // mounted between openings.
  let replicas = $state(0)

  $effect(() => {
    if (open) replicas = currentReplicas
  })

  function handleSubmit(): void {
    if (replicas >= 0) {
      onconfirm(replicas)
    }
  }

  /**
   * Whether an autoscaler owns this workload's replica count, asked once per
   * opening rather than once per keystroke in the field below.
   *
   * UNDEFINED WHILE PENDING, AND RENDERED AS NOTHING. Showing a placeholder
   * that then flips to "no autoscaler" a moment later is worse than showing
   * nothing at all: on a slow cluster the operator may already have clicked
   * Scale by the time the honest answer arrives.
   */
  let autoscalers = $state<AutoscalerCheck | undefined>(undefined)
  let checkRequest = 0

  $effect(() => {
    if (!open) return
    const request = ++checkRequest
    autoscalers = undefined
    void checkAutoscalers(kind, namespace, name).then((result) => {
      // The dialog can close and reopen on a different workload before a slow
      // read returns; a stale answer landing on the new one would warn about
      // — or silently clear a warning about — the wrong autoscaler.
      if (request === checkRequest) autoscalers = result
    })
  })

  /** Builds the click handler for an autoscaler's name, or nothing when it cannot be followed. See $lib/reference. */
  const follow = $derived(follower(canOpen, onopen))

  /** "HorizontalPodAutoscaler, min 2, max 10" — only the bounds the server printed. */
  function describe(ref: AutoscalerRef): string {
    const bounds = [
      ref.minReplicas ? `min ${ref.minReplicas}` : null,
      ref.maxReplicas ? `max ${ref.maxReplicas}` : null,
    ].filter((part): part is string => part !== null)
    return bounds.length ? `${ref.kind}, ${bounds.join(', ')}` : ref.kind
  }

  /**
   * Escape closes; Enter confirms, but only where Enter meant nothing else.
   *
   * ENTER USED TO CONFIRM FROM ANYWHERE. The browser already activates a
   * focused button on Enter, so a global handler on top of it meant that
   * tabbing to Cancel and pressing Enter did the dangerous thing and then
   * closed the dialog — leaving Escape as the only way to back out of it, and
   * nothing on screen saying so. DeleteDialog deliberately binds no Enter at
   * all: an irreversible action should cost a deliberate click.
   */
  function onKeydown(event: KeyboardEvent): void {
    if (!open) return
    if (event.key === 'Escape') {
      if (!escape?.owns()) return
      onclose()
      return
    }
    if (event.key !== 'Enter') return
    if ((event.target as HTMLElement | null)?.closest('button, a, [role="button"]')) return
    handleSubmit()
  }

  /** Escape belongs to the innermost open layer. See $lib/escape. */
  let escape = $state<EscapeClaim | null>(null)
  $effect(() => {
    if (!open) return
    const held = escapeLayer()
    escape = held
    return () => {
      held.release()
      escape = null
    }
  })
</script>

<svelte:window onkeydown={onKeydown} />

{#if open}
  <button
    type="button"
    aria-label="Close dialog"
    tabindex="-1"
    class="fixed inset-0 z-[60] cursor-default bg-scrim/40"
    onclick={onclose}
  ></button>

  <div
    class="fixed top-1/2 left-1/2 z-[70] w-[24rem] max-w-[90vw] -translate-x-1/2 -translate-y-1/2
           rounded-sm border border-outline-variant bg-surface-container-high p-6 shadow-level-3"
    role="dialog"
    aria-modal="true"
    use:modal
    aria-label="Scale replicas"
  >
    <h2 class="text-headline-small text-on-surface">Scale Replicas</h2>

    <p class="mt-4 text-body-medium text-on-surface-variant">
      Set the number of replicas for this workload.
    </p>

    <!--
      Scaling by hand while an autoscaler targets this workload is undone
      within its next sync period — silently, unless something here says so.
      Aptakube warns; this is that warning. IT DOES NOT BLOCK THE ACTION: the
      operator may be doing this on purpose, e.g. to force an immediate
      change ahead of the autoscaler's next reconcile.
    -->
    {#if autoscalers?.status === 'known' && autoscalers.autoscalers.length > 0}
      <div class="mt-4 flex flex-col gap-2">
        {#each autoscalers.autoscalers as ref (ref.kind + '/' + ref.name)}
          {@const opener = follow(ref.kind, ref.name, namespace)}
          <div class="flex items-start gap-2 rounded-sm border border-gauge-warn/40 bg-gauge-warn/10 p-3">
            <TriangleAlert class="mt-0.5 size-4 shrink-0 text-gauge-warn" strokeWidth={2} />
            <p class="text-body-small text-on-surface">
              An autoscaler manages this replica count —
              {#if opener}
                <button
                  type="button"
                  class="resource-link font-medium"
                  onclick={() => {
                    // Closed first: what the click opens is a different
                    // object, not this workload, and leaving the dialog open
                    // over it would scale whatever was here when it was
                    // clicked rather than what is now on screen.
                    onclose()
                    opener()
                  }}
                >{ref.name}</button>
              {:else}
                <span class="font-medium" data-selectable>{ref.name}</span>
              {/if}
              ({describe(ref)}). It will override whatever you set here within its sync period.
            </p>
          </div>
        {/each}
      </div>
    {:else if autoscalers?.status === 'unknown'}
      <p class="mt-4 text-body-small text-on-surface-variant">
        Could not check for an autoscaler: {autoscalers.reason}
      </p>
    {/if}

    <label class="mt-4 block">
      <span class="text-body-small text-on-surface-variant">Replicas</span>
      <input
        type="number"
        bind:value={replicas}
        min="0"
        class="field mt-1 w-full px-3 py-2 text-body-medium"
      />
    </label>

    <!-- Live: reflects whatever is currently typed above, not the value the
         dialog opened with — the hint is a preview of what Scale is about to
         do, not a record of what it used to say. -->
    <div class="mt-4">
      <KubectlHint command={scale(ctx, kind, name, namespace, replicas)} />
    </div>

    <div class="mt-6 flex justify-end gap-3">
      <Button variant="outlined" onclick={onclose}>Cancel</Button>
      <Button variant="filled" onclick={handleSubmit}>Scale</Button>
    </div>
  </div>
{/if}
