<!--
  Confirmation dialog for deleting a resource.
-->
<script lang="ts">
  import { escapeLayer, type EscapeClaim } from '$lib/escape'
  import { modal } from '$lib/modal'
  import { del } from '$lib/kubectl'
  import Button from './Button.svelte'
  import KubectlHint from './KubectlHint.svelte'

  interface Props {
    open: boolean
    resourceName: string | null
    resourceKind: string
    onclose: () => void
    onconfirm: () => void
    /** The kubeconfig context this cluster connects through. See $lib/kubectl. */
    ctx: string
    /** The kubectl API resource argument, e.g. "pods" or "deployments.apps". */
    resource: string
    /** Empty for a cluster-scoped object. */
    namespace: string
  }

  let { open, resourceName, resourceKind, onclose, onconfirm, ctx, resource, namespace }: Props =
    $props()

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
    class="fixed top-1/2 left-1/2 z-[70] w-[28rem] max-w-[90vw] -translate-x-1/2 -translate-y-1/2
           rounded-sm border border-outline-variant bg-surface-container-high p-6 shadow-level-3"
    role="dialog"
    aria-modal="true"
    use:modal
    aria-label="Delete resource"
  >
    <h2 class="text-headline-small text-on-surface">Delete {resourceKind}</h2>

    <p class="mt-4 text-body-medium text-on-surface-variant">
      Are you sure you want to delete <strong class="text-on-surface" data-selectable>{resourceName}</strong>?
      This action cannot be undone.
    </p>

    {#if resourceName}
      <div class="mt-4">
        <KubectlHint command={del(ctx, resource, resourceName, namespace || undefined)} />
      </div>
    {/if}

    <div class="mt-6 flex justify-end gap-3">
      <Button variant="outlined" onclick={onclose}>Cancel</Button>
      <Button variant="filled" onclick={onconfirm}>Delete</Button>
    </div>
  </div>
{/if}
