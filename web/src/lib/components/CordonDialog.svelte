<!--
  Confirmation dialog for cordoning a node.

  Uncordoning needs no dialog — it undoes a deliberate, visible state rather
  than doing anything destructive — so this only ever confirms the cordon
  direction, mirroring SuspendDialog's Suspend/Resume asymmetry.
-->
<script lang="ts">
  import { escapeLayer, type EscapeClaim } from '$lib/escape'
  import { modal } from '$lib/modal'
  import { cordon } from '$lib/kubectl'
  import Button from './Button.svelte'
  import DialogHeader from './DialogHeader.svelte'
  import DialogFooter from './DialogFooter.svelte'

  interface Props {
    open: boolean
    /** The kubeconfig context this cluster connects through. See $lib/kubectl. */
    ctx: string
    nodeName: string | null
    onclose: () => void
    onconfirm: () => void
  }

  let { open, ctx, nodeName, onclose, onconfirm }: Props = $props()

  /**
   * Escape closes; Enter confirms, but only where Enter meant nothing else.
   * Mirrors RestartDialog, TriggerDialog and SuspendDialog.
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
    onconfirm()
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
    class="fixed inset-0 z-[70] m-auto h-fit max-h-[90vh] overflow-y-auto
           w-[28rem] max-w-[90vw]
           rounded-sm border border-outline-variant bg-surface-container-high p-6 shadow-level-3"
    role="dialog"
    aria-modal="true"
    use:modal
    aria-label="Cordon node"
  >
    <DialogHeader title="Cordon {nodeName}" help="cordon" {onclose} />

    <p class="mt-4 text-body-medium text-on-surface-variant">
      New pods will not be scheduled here; running pods stay.
    </p>

    <DialogFooter command={nodeName ? cordon(ctx, [nodeName], true) : ''}>
      <Button variant="outlined" onclick={onclose}>Cancel</Button>
      <Button variant="filled" onclick={onconfirm}>Cordon</Button>
    </DialogFooter>
  </div>
{/if}
