<!--
  Confirmation for promoting or aborting an Argo Rollouts Rollout.

  Both are writes that change what is serving traffic, so both go through the
  same three things every other write dialog here does: the production banner
  and type-the-name gate on a cluster whose group is marked production, the
  kubectl equivalent of what is about to happen, and a sentence saying what
  the operator is actually agreeing to.

  THE TWO SENTENCES ARE DIFFERENT ON PURPOSE. Promoting moves a rollout
  forward one step; aborting sends traffic back to the stable ReplicaSet and
  leaves the Rollout Degraded against the revision that was being deployed,
  because it changes no spec. One sentence covering both would undersell the
  second — an operator who reads "abort" as "undo" will go looking for a
  rollback that never happened.
-->
<script lang="ts">
  import { escapeLayer, type EscapeClaim } from '$lib/escape'
  import { modal } from '$lib/modal'
  import { argoRollouts } from '$lib/kubectl'
  import { nameConfirmed } from '$lib/confirm'
  import Button from './Button.svelte'
  import KubectlHint from './KubectlHint.svelte'
  import { TriangleAlert } from '@lucide/svelte'

  interface Props {
    open: boolean
    /** 'promote' or 'abort' — the two the panel offers. */
    action: 'promote' | 'abort'
    name: string
    namespace: string
    /** The kubeconfig context this cluster connects through. See $lib/kubectl. */
    ctx: string
    /**
     * The group's name, when the cluster this Rollout lives on is marked
     * production — null otherwise. Non-null both shows the banner and turns
     * on the type-the-name requirement, exactly as in DeleteDialog.
     */
    productionGroup?: string | null
    onclose: () => void
    onconfirm: () => void
  }

  let { open, action, name, namespace, ctx, productionGroup, onclose, onconfirm }: Props = $props()

  const isAbort = $derived(action === 'abort')
  const title = $derived(isAbort ? 'Abort rollout' : 'Promote rollout')

  const requiresTypedName = $derived(!!productionGroup)

  /** What has been typed into the confirmation field. */
  let typed = $state('')
  $effect(() => {
    if (!open) typed = ''
  })

  const confirmed = $derived(!requiresTypedName || nameConfirmed(typed, name))

  function onKeydown(event: KeyboardEvent): void {
    if (event.key !== 'Escape' || !open) return
    if (!escape?.owns()) return
    onclose()
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
    class="fixed top-1/2 left-1/2 z-[70] w-[30rem] max-w-[90vw] -translate-x-1/2 -translate-y-1/2
           rounded-sm border border-outline-variant bg-surface-container-high p-6 shadow-level-3"
    role="dialog"
    aria-modal="true"
    use:modal
    aria-label={title}
  >
    <h2 class="text-headline-small text-on-surface">{title}</h2>

    {#if productionGroup}
      <p
        class="mt-4 flex items-start gap-2 rounded-sm border border-error/30 bg-error-container/40
               px-3 py-2 text-body-small text-on-error-container"
      >
        <TriangleAlert class="mt-0.5 size-4 shrink-0" strokeWidth={1.8} />
        This cluster is in {productionGroup}, marked production.
      </p>
    {/if}

    <p class="mt-4 text-body-medium text-on-surface-variant">
      {#if isAbort}
        Aborting <strong class="text-on-surface" data-selectable>{name}</strong> returns traffic to the
        stable ReplicaSet and scales the new one down. It does not change the spec, so the Rollout stays
        Degraded against the revision it was deploying until something updates its template.
      {:else}
        Promoting <strong class="text-on-surface" data-selectable>{name}</strong> clears whatever is holding
        it and lets the controller carry on to the next step. On a canary that means more traffic on the
        new version.
      {/if}
    </p>

    <div class="mt-4">
      <KubectlHint command={argoRollouts(action, ctx, name, namespace)} />
    </div>

    {#if requiresTypedName}
      <label class="mt-4 block">
        <span class="text-body-small text-on-surface-variant">
          Type <strong class="text-on-surface" data-selectable>{name}</strong> to confirm
        </span>
        <input
          type="text"
          bind:value={typed}
          autocomplete="off"
          spellcheck="false"
          aria-describedby="rollout-confirm-hint"
          class="field mt-1 w-full px-3 py-2 text-body-medium"
        />
      </label>
      <p id="rollout-confirm-hint" class="mt-1.5 text-body-small text-on-surface-variant/70">
        {confirmed ? 'Name confirmed.' : `${title} stays disabled until the name above matches exactly.`}
      </p>
    {/if}

    <div class="mt-6 flex justify-end gap-3">
      <Button variant="outlined" onclick={onclose}>Cancel</Button>
      <Button
        variant="filled"
        disabled={!confirmed}
        describedBy={requiresTypedName ? 'rollout-confirm-hint' : undefined}
        onclick={onconfirm}
      >
        {isAbort ? 'Abort' : 'Promote'}
      </Button>
    </div>
  </div>
{/if}
