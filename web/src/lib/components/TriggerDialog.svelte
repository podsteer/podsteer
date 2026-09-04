<!--
  Confirmation dialog for triggering a CronJob outside its schedule.
-->
<script lang="ts">
  import { escapeLayer, type EscapeClaim } from '$lib/escape'
  import { modal } from '$lib/modal'
  import Button from './Button.svelte'

  interface Props {
    open: boolean
    workloadName: string | null
    onclose: () => void
    onconfirm: () => void
  }

  let { open, workloadName, onclose, onconfirm }: Props = $props()

  /**
   * Escape closes; Enter confirms, but only where Enter meant nothing else.
   *
   * Mirrors RestartDialog: the browser already activates a focused button on
   * Enter, so a global handler on top of it would mean tabbing to Cancel and
   * pressing Enter did the thing anyway.
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
    aria-label="Run now"
  >
    <h2 class="text-headline-small text-on-surface">Run now</h2>

    <p class="mt-4 text-body-medium text-on-surface-variant">
      Creates a Job from <strong class="text-on-surface" data-selectable>{workloadName}</strong>'s template now,
      outside its schedule. It appears under the CronJob and counts towards its history limits.
    </p>

    <div class="mt-6 flex justify-end gap-3">
      <Button variant="outlined" onclick={onclose}>Cancel</Button>
      <Button variant="filled" onclick={onconfirm}>Run now</Button>
    </div>
  </div>
{/if}
