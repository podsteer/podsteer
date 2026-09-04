<!--
  Confirmation dialog for suspending a CronJob or a Job.

  Resuming needs no dialog — it undoes a deliberate, visible state rather than
  doing anything destructive — so this only ever confirms the suspend
  direction. The copy differs by kind: pausing a CronJob's schedule and
  stopping a Job's running pods are different enough consequences that one
  sentence for both would undersell one of them.
-->
<script lang="ts">
  import { escapeLayer, type EscapeClaim } from '$lib/escape'
  import { modal } from '$lib/modal'
  import Button from './Button.svelte'

  interface Props {
    open: boolean
    workloadName: string | null
    /** 'CronJob' or 'Job'. Anything else falls back to the CronJob copy. */
    workloadKind: string
    onclose: () => void
    onconfirm: () => void
  }

  let { open, workloadName, workloadKind, onclose, onconfirm }: Props = $props()

  const isJob = $derived(workloadKind === 'Job')

  /**
   * Escape closes; Enter confirms, but only where Enter meant nothing else.
   *
   * Mirrors RestartDialog and TriggerDialog.
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
    aria-label="Suspend {workloadKind}"
  >
    <h2 class="text-headline-small text-on-surface">Suspend {workloadKind}</h2>

    <p class="mt-4 text-body-medium text-on-surface-variant">
      {#if isJob}
        Suspending <strong class="text-on-surface" data-selectable>{workloadName}</strong> deletes its active
        pods; resuming starts them again from scratch.
      {:else}
        Scheduled runs of <strong class="text-on-surface" data-selectable>{workloadName}</strong> stop until it
        is resumed; a run already in progress finishes.
      {/if}
    </p>

    <div class="mt-6 flex justify-end gap-3">
      <Button variant="outlined" onclick={onclose}>Cancel</Button>
      <Button variant="filled" onclick={onconfirm}>Suspend</Button>
    </div>
  </div>
{/if}
