<!--
  Confirmation dialog for restarting a workload rollout.
-->
<script lang="ts">
  import { escapeLayer, type EscapeClaim } from '$lib/escape'
  import { modal } from '$lib/modal'
  import Button from './Button.svelte'

  interface Props {
    open: boolean
    workloadName: string | null
    workloadKind: string
    onclose: () => void
    onconfirm: () => void
  }

  let { open, workloadName, workloadKind, onclose, onconfirm }: Props = $props()

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
    class="fixed top-1/2 left-1/2 z-[70] w-[28rem] max-w-[90vw] -translate-x-1/2 -translate-y-1/2
           rounded-sm border border-outline-variant bg-surface-container-high p-6 shadow-level-3"
    role="dialog"
    aria-modal="true"
    use:modal
    aria-label="Restart rollout"
  >
    <h2 class="text-headline-small text-on-surface">Restart {workloadKind}</h2>

    <p class="mt-4 text-body-medium text-on-surface-variant">
      Are you sure you want to restart <strong class="text-on-surface" data-selectable>{workloadName}</strong>?
      This will trigger a rolling update of all pods.
    </p>

    <div class="mt-6 flex justify-end gap-3">
      <Button variant="outlined" onclick={onclose}>Cancel</Button>
      <Button variant="filled" onclick={onconfirm}>Restart</Button>
    </div>
  </div>
{/if}
