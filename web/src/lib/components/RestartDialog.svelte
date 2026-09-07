<!--
  Confirmation dialog for restarting a workload rollout.
-->
<script lang="ts">
  import { escapeLayer, type EscapeClaim } from '$lib/escape'
  import { modal } from '$lib/modal'
  import { rolloutRestart } from '$lib/kubectl'
  import Button from './Button.svelte'
  import DialogHeader from './DialogHeader.svelte'
  import DialogFooter from './DialogFooter.svelte'
  import { TriangleAlert } from '@lucide/svelte'

  interface Props {
    open: boolean
    workloadName: string | null
    workloadKind: string
    /**
     * The group's name, when this workload's cluster is marked production —
     * null or undefined otherwise. Shows a banner only; a restart does not
     * take a workload off the air the way a delete or a scale-to-zero does,
     * so it does not gain the type-the-name gate those two do.
     */
    productionGroup?: string | null
    onclose: () => void
    onconfirm: () => void
    /** The kubeconfig context this cluster connects through. See $lib/kubectl. */
    ctx: string
    namespace: string
  }

  let { open, workloadName, workloadKind, productionGroup, onclose, onconfirm, ctx, namespace }: Props = $props()

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
    aria-label="Restart rollout"
  >
    <DialogHeader title="Restart {workloadKind}" help="restart" {onclose} />

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
      Are you sure you want to restart <strong class="text-on-surface" data-selectable>{workloadName}</strong>?
      This will trigger a rolling update of all pods.
    </p>

    <DialogFooter command={workloadName ? rolloutRestart(ctx, workloadKind, workloadName, namespace) : ''}>
      <Button variant="outlined" onclick={onclose}>Cancel</Button>
      <Button variant="filled" onclick={onconfirm}>Restart</Button>
    </DialogFooter>
  </div>
{/if}
