<!--
  Dialog for editing a resource's YAML manifest.

  The same editor the drawer reads a manifest in, not a textarea. Editing is
  the more demanding of the two jobs — indentation decides meaning in YAML,
  and a mistyped key is invisible in unhighlighted monospace — so the view
  with line numbers and syntax colour belonged here at least as much as in the
  read-only tab. Having them differ also made applying a change feel like it
  happened somewhere other than the application.
-->
<script lang="ts">
  import Button from './Button.svelte'
  import YamlEditor from './YamlEditor.svelte'
  import PaneToolbar from './PaneToolbar.svelte'
  import WrapLinesToggle from './WrapLinesToggle.svelte'

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
    class="fixed top-1/2 left-1/2 z-[70] flex h-[85vh] w-[68rem] max-w-[95vw] -translate-x-1/2 -translate-y-1/2
           flex-col rounded-sm border border-outline-variant bg-surface-container-high p-6 shadow-level-3"
    role="dialog"
    aria-modal="true"
    aria-label="Edit resource"
  >
    <h2 class="text-headline-small text-on-surface">Edit Resource</h2>

    <p class="mt-2 text-body-small text-on-surface-variant">
      Edit the YAML manifest below and click Apply to update the resource.
    </p>

    <!-- The frame is the dialog's, not the editor's: YamlEditor paints no
         background of its own, so it takes this one and cannot disagree with
         the panel around it. -->
    <div class="field mt-4 flex min-h-0 flex-1 flex-col overflow-hidden">
      <PaneToolbar>
        <WrapLinesToggle />
      </PaneToolbar>
      <div class="min-h-0 flex-1">
        <YamlEditor content={editedManifest} onchange={(value) => (editedManifest = value)} />
      </div>
    </div>

    <div class="mt-4 flex justify-end gap-3">
      <Button variant="outlined" onclick={onclose}>Cancel</Button>
      <Button variant="filled" onclick={handleSubmit}>Apply</Button>
    </div>
  </div>
{/if}
