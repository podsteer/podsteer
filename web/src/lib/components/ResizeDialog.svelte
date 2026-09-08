<!--
  Change one running container's CPU and memory, in place.

  THE ONE WRITE WHOSE EFFECT DEPENDS ON A FIELD NOBODY HAS READ. A container
  declares a `resizePolicy` per resource: the default, NotRequired, means the
  kubelet changes the cgroup under the running process — which is what makes
  in-place resize worth having — and RestartContainer means it kills and
  restarts the container to apply the change. So this dialog reads the
  container's own policy and says which of the two is about to happen, before
  the button rather than after it.

  FOUR FIELDS, ANY OF THEM LEFT ALONE. An empty box means "do not touch this
  figure", which is not the same as clearing it; the backend sends only what
  was typed, so nothing rewrites three fields to change one. Every refusal —
  a quantity that is not one, a limit below its request, a change that changes
  nothing — is the domain's, made against the container as it is NOW rather
  than as it was when this opened.

  WHAT IT DOES NOT CLAIM. The kubelet decides what happens next: apply it,
  defer it until the node has room, or call it infeasible. That answer arrives
  as a condition on the pod and appears in the pod's own findings, which is
  why this dialog reports what was ASKED FOR and then gets out of the way.
-->
<script lang="ts">
  import { escapeLayer, type EscapeClaim } from '$lib/escape'
  import { modal } from '$lib/modal'
  import { resizePod as kubectlResize } from '$lib/kubectl'
  import { resizeContainer } from '$lib/api/client'
  import { toApiError } from '$lib/api/errors'
  import Button from './Button.svelte'
  import DialogHeader from './DialogHeader.svelte'
  import DialogFooter from './DialogFooter.svelte'
  import { RotateCw, TriangleAlert } from '@lucide/svelte'

  interface Props {
    open: boolean
    /** The kubeconfig context, for the command shown and the call made. */
    ctx: string
    namespace: string
    /** The pod, not the workload: this changes THIS pod's container. */
    podName: string
    container: string
    /** The figures as they stand, for the placeholders. Blank where the
        container declares none — which is a BestEffort container, and a real
        thing to see rather than a zero to invent. */
    current: {
      cpuRequest: string
      cpuLimit: string
      memoryRequest: string
      memoryLimit: string
    }
    /** The group's name when this cluster is marked production. */
    productionGroup?: string | null
    onclose: () => void
    onapplied: () => void
  }

  let {
    open,
    ctx,
    namespace,
    podName,
    container,
    current,
    productionGroup,
    onclose,
    onapplied,
  }: Props = $props()

  let cpuRequest = $state('')
  let cpuLimit = $state('')
  let memoryRequest = $state('')
  let memoryLimit = $state('')
  let applying = $state(false)
  let error = $state('')
  /** What the write said it did, once it has. */
  let applied = $state<{ restarts: boolean; restartReason: string } | null>(null)

  // Cleared every time it opens, possibly on a different container than last
  // time — the same convention CompareDialog's own reset follows.
  $effect(() => {
    if (!open) return
    cpuRequest = ''
    cpuLimit = ''
    memoryRequest = ''
    memoryLimit = ''
    error = ''
    applied = null
    applying = false
  })

  const typedAnything = $derived(
    [cpuRequest, cpuLimit, memoryRequest, memoryLimit].some((value) => value.trim() !== ''),
  )

  const command = $derived(
    kubectlResize(ctx, podName, namespace, container, {
      cpuRequest: cpuRequest.trim(),
      cpuLimit: cpuLimit.trim(),
      memoryRequest: memoryRequest.trim(),
      memoryLimit: memoryLimit.trim(),
    }),
  )

  async function apply(): Promise<void> {
    applying = true
    error = ''
    try {
      const result = await resizeContainer(
        ctx,
        namespace,
        podName,
        container,
        cpuRequest.trim(),
        cpuLimit.trim(),
        memoryRequest.trim(),
        memoryLimit.trim(),
      )
      applied = { restarts: result.restarts, restartReason: result.restartReason }
      onapplied()
    } catch (cause) {
      error = toApiError(cause).message
    } finally {
      applying = false
    }
  }

  function onKeydown(event: KeyboardEvent): void {
    if (event.key !== 'Escape' || !open) return
    if (!escape?.owns()) return
    onclose()
  }

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
    class="fixed inset-0 z-[70] m-auto h-fit max-h-[90vh] w-[32rem] max-w-[90vw] overflow-y-auto
           rounded-sm border border-outline-variant bg-surface-container-high p-6 shadow-level-3"
    role="dialog"
    aria-modal="true"
    use:modal
    aria-label="Resize container"
  >
    <DialogHeader title="Resize container" help="resize-container" {onclose} />

    {#if productionGroup}
      <p
        class="mt-4 flex items-start gap-2 rounded-sm border border-error/30 bg-error-container/40
               px-3 py-2 text-body-medium text-on-error-container"
      >
        <TriangleAlert class="mt-0.5 size-4 shrink-0" strokeWidth={1.8} />
        This cluster is in {productionGroup}, marked production.
      </p>
    {/if}

    <p class="mt-4 text-body-medium text-on-surface-variant">
      Changing <span class="text-on-surface">{container}</span> in
      <span class="text-on-surface">{podName}</span>. Leave a box empty to leave that figure
      alone.
    </p>

    <div class="mt-4 grid grid-cols-2 gap-3">
      {#each [{ label: 'CPU request', value: cpuRequest, now: current.cpuRequest, set: (v: string) => (cpuRequest = v) }, { label: 'CPU limit', value: cpuLimit, now: current.cpuLimit, set: (v: string) => (cpuLimit = v) }, { label: 'Memory request', value: memoryRequest, now: current.memoryRequest, set: (v: string) => (memoryRequest = v) }, { label: 'Memory limit', value: memoryLimit, now: current.memoryLimit, set: (v: string) => (memoryLimit = v) }] as field (field.label)}
        <label class="block">
          <span class="text-body-medium text-on-surface-variant">{field.label}</span>
          <input
            type="text"
            value={field.value}
            oninput={(event) => field.set(event.currentTarget.value)}
            placeholder={field.now || 'not set'}
            disabled={applying || applied !== null}
            autocomplete="off"
            spellcheck="false"
            class="field mt-1 w-full px-3 py-2 text-body-medium tabular-nums"
          />
        </label>
      {/each}
    </div>

    {#if applied}
      <!-- WHAT WAS ASKED FOR, not what happened: the kubelet may apply this
           now, defer it until the node has room, or call it infeasible, and
           that answer shows up in the pod's own findings. -->
      <div class="mt-4 rounded-sm border border-outline-variant/60 bg-surface-container-lowest p-3">
        <p class="text-body-medium text-on-surface">The change has been sent to the API server.</p>
        {#if applied.restarts}
          <p class="mt-1 flex items-start gap-2 text-body-medium text-gauge-warn">
            <RotateCw class="mt-0.5 size-4 shrink-0" strokeWidth={1.8} />
            This container's resizePolicy restarts it for {applied.restartReason}, so it is being
            restarted to apply the change.
          </p>
        {:else}
          <p class="mt-1 text-body-medium text-on-surface-variant">
            The kubelet decides what happens next: it may apply the change immediately, hold it
            until the node has room, or refuse it as infeasible. Whichever it does appears in this
            pod's findings.
          </p>
        {/if}
      </div>
    {/if}

    {#if error}
      <p class="mt-4 flex items-start gap-2 text-body-medium text-error" role="alert">
        <TriangleAlert class="mt-0.5 size-4 shrink-0" strokeWidth={2} />
        {error}
      </p>
    {/if}

    <DialogFooter command={typedAnything ? command : ''}>
      <Button variant="outlined" onclick={onclose}>{applied ? 'Close' : 'Cancel'}</Button>
      {#if !applied}
        <Button variant="filled" disabled={!typedAnything} loading={applying} onclick={() => void apply()}>
          {applying ? 'Applying…' : 'Apply'}
        </Button>
      {/if}
    </DialogFooter>
  </div>
{/if}
