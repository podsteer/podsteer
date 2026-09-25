<!--
  Confirmation dialog for triggering a CronJob outside its schedule.
-->
<script lang="ts">
  import { escapeLayer, type EscapeClaim } from '$lib/escape'
  import { modal } from '$lib/modal'
  import { createJobFromCronJob } from '$lib/kubectl'
  import Button from './Button.svelte'
  import DialogHeader from './DialogHeader.svelte'
  import DialogFooter from './DialogFooter.svelte'

  interface Props {
    open: boolean
    /** The kubeconfig context this cluster connects through. See $lib/kubectl. */
    ctx: string
    /** The CronJob's namespace, for the command. */
    namespace: string
    workloadName: string | null
    onclose: () => void
    onconfirm: () => void
  }

  let { open, ctx, namespace, workloadName, onclose, onconfirm }: Props = $props()

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
    class="fixed inset-0 z-[70] m-auto h-fit max-h-[90vh] overflow-y-auto
           w-[28rem] max-w-[90vw]
           rounded-sm border border-outline-variant bg-surface-container-high p-6 shadow-level-3"
    role="dialog"
    aria-modal="true"
    use:modal
    aria-label="Run now"
  >
    <DialogHeader title="Run now" help="trigger" {onclose} />

    <p class="mt-4 text-body-medium text-on-surface-variant">
      Creates a Job from <strong class="text-on-surface" data-selectable>{workloadName}</strong>'s template now,
      outside its schedule. It appears under the CronJob and counts towards its history limits.
    </p>

    <!-- The name kubectl would need is one PodSteer does not choose: the
         server generates it from the CronJob. `-manual-<stamp>` is what
         kubectl itself suggests, and it is shown as an example rather than as
         the name this button will produce. -->
    <DialogFooter
      command={workloadName
        ? createJobFromCronJob(ctx, workloadName, namespace, `${workloadName}-manual`)
        : ''}
    >
      <Button variant="outlined" onclick={onclose}>Cancel</Button>
      <Button variant="filled" onclick={onconfirm}>Run now</Button>
    </DialogFooter>
  </div>
{/if}
