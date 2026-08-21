<!--
  Dialog for editing a resource's YAML manifest.
-->
<script lang="ts">
  import Button from './Button.svelte'

  interface Props {
    open: boolean
    manifest: string
    onclose: () => void
    onconfirm: (manifest: string) => void
  }

  let { open, manifest, onclose, onconfirm }: Props = $props()

  // Seeded by the effect below rather than from the prop directly: reading a
  // prop into $state() captures only its initial value, and the dialog is kept
  // mounted between openings.
  let editedManifest = $state('')

  $effect(() => {
    if (open) editedManifest = manifest
  })

  function handleSubmit(): void {
    onconfirm(editedManifest)
  }

  function onKeydown(event: KeyboardEvent): void {
    if (event.key === 'Escape' && open) onclose()
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
    class="fixed top-1/2 left-1/2 z-[70] flex h-[80vh] w-[48rem] max-w-[95vw] -translate-x-1/2 -translate-y-1/2
           flex-col rounded-sm border border-outline-variant bg-surface-container-high p-6 shadow-level-3"
    role="dialog"
    aria-modal="true"
    aria-label="Edit resource"
  >
    <h2 class="text-headline-small text-on-surface">Edit Resource</h2>

    <p class="mt-2 text-body-small text-on-surface-variant">
      Edit the YAML manifest below and click Apply to update the resource.
    </p>

    <textarea
      bind:value={editedManifest}
      class="field mt-4 min-h-0 flex-1 resize-none px-3 py-2
             font-mono text-body-small leading-relaxed text-on-surface"
      data-selectable
    ></textarea>

    <div class="mt-4 flex justify-end gap-3">
      <Button variant="outlined" onclick={onclose}>Cancel</Button>
      <Button variant="filled" onclick={handleSubmit}>Apply</Button>
    </div>
  </div>
{/if}
