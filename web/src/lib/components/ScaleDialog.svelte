<!--
  Dialog for scaling a deployment or statefulset.

  On a cluster whose group is marked production, scaling to ZERO — the one
  target that can take a workload off the air entirely — costs the same
  type-the-name confirmation Delete does. Scaling to any other number stays a
  single click: it changes capacity, not whether the workload exists, and a
  production banner without an extra gate is enough for that. See CLAUDE.md,
  "Type-the-name confirmation on production".
-->
<script lang="ts">
  import { escapeLayer, type EscapeClaim } from '$lib/escape'
  import { modal } from '$lib/modal'
  import { nameConfirmed } from '$lib/confirm'
  import Button from './Button.svelte'
  import { TriangleAlert } from '@lucide/svelte'

  interface Props {
    open: boolean
    currentReplicas: number
    /** The workload's name, needed only to typeset the confirmation field. */
    workloadName?: string | null
    /**
     * The group's name, when this workload's cluster is marked production —
     * null or undefined otherwise. Shows the banner whenever set; only turns
     * on the type-the-name gate when the chosen target is also zero.
     */
    productionGroup?: string | null
    onclose: () => void
    onconfirm: (replicas: number) => void
  }

  let { open, currentReplicas, workloadName, productionGroup, onclose, onconfirm }: Props =
    $props()

  // Seeded by the effect below rather than from the prop directly: reading a
  // prop into $state() captures only its initial value, and the dialog is kept
  // mounted between openings.
  let replicas = $state(0)
  /** What has been typed into the confirmation field. */
  let typed = $state('')

  $effect(() => {
    if (open) {
      replicas = currentReplicas
      typed = ''
    }
  })

  const requiresTypedName = $derived(!!productionGroup && replicas === 0)
  const confirmed = $derived(
    !requiresTypedName || (!!workloadName && nameConfirmed(typed, workloadName)),
  )

  function handleSubmit(): void {
    if (replicas >= 0 && confirmed) {
      onconfirm(replicas)
    }
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
      Set the number of replicas for this workload.
    </p>

    <label class="mt-4 block">
      <span class="text-body-small text-on-surface-variant">Replicas</span>
      <input
        type="number"
        bind:value={replicas}
        min="0"
        class="field mt-1 w-full px-3 py-2 text-body-medium"
      />
    </label>

    {#if requiresTypedName}
      <label class="mt-4 block">
        <span class="text-body-small text-on-surface-variant">
          Scaling to zero takes this workload off the air. Type
          <strong class="text-on-surface" data-selectable>{workloadName}</strong> to confirm
        </span>
        <input
          type="text"
          bind:value={typed}
          autocomplete="off"
          spellcheck="false"
          aria-describedby="scale-confirm-hint"
          class="field mt-1 w-full px-3 py-2 text-body-medium"
        />
      </label>
      <p id="scale-confirm-hint" class="mt-1.5 text-body-small text-on-surface-variant/70">
        {confirmed
          ? 'Name confirmed.'
          : `Scale stays disabled until the name above matches exactly.`}
      </p>
    {/if}

    <div class="mt-6 flex justify-end gap-3">
      <Button variant="outlined" onclick={onclose}>Cancel</Button>
      <Button
        variant="filled"
        disabled={!confirmed}
        describedBy={requiresTypedName ? 'scale-confirm-hint' : undefined}
        onclick={handleSubmit}
      >
        Scale
      </Button>
    </div>
  </div>
{/if}
