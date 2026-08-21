<!--
  Dialog for scaling a deployment or statefulset.
-->
<script lang="ts">
  import Button from './Button.svelte'

  interface Props {
    open: boolean
    currentReplicas: number
    onclose: () => void
    onconfirm: (replicas: number) => void
  }

  let { open, currentReplicas, onclose, onconfirm }: Props = $props()

  // Seeded by the effect below rather than from the prop directly: reading a
  // prop into $state() captures only its initial value, and the dialog is kept
  // mounted between openings.
  let replicas = $state(0)

  $effect(() => {
    if (open) replicas = currentReplicas
  })

  function handleSubmit(): void {
    if (replicas >= 0) {
      onconfirm(replicas)
    }
  }

  function onKeydown(event: KeyboardEvent): void {
    if (event.key === 'Escape' && open) onclose()
    if (event.key === 'Enter' && open) handleSubmit()
  }
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
    aria-label="Scale replicas"
  >
    <h2 class="text-headline-small text-on-surface">Scale Replicas</h2>

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

    <div class="mt-6 flex justify-end gap-3">
      <Button variant="outlined" onclick={onclose}>Cancel</Button>
      <Button variant="filled" onclick={handleSubmit}>Scale</Button>
    </div>
  </div>
{/if}
