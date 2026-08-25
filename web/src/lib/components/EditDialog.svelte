<!--
  Dialog for editing a resource's YAML manifest.

  The same pane the drawer reads a manifest in — editor, search, wrap, managed
  fields — so that reading a manifest and changing one look like the same act.
  It was a bare textarea, which is why applying a change felt like it happened
  somewhere other than the application.
-->
<script lang="ts">
  import { Check, Copy } from '@lucide/svelte'
  import Button from './Button.svelte'
  import YamlPane from './YamlPane.svelte'
  import ToolbarButton from './ToolbarButton.svelte'
  import { withoutManagedFields } from '$lib/manifest'
  import { preferences } from '$stores/preferences.svelte'

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
  let copied = $state(false)

  /**
   * What the dialog was opened with, to tell an edited buffer from a fresh one.
   *
   * The managed-fields control re-seeds the editor, which would throw away
   * whatever had been typed. Rather than silently discarding an edit — or
   * trying to splice a block back into a document that has since changed
   * underneath it — the control is simply locked once there is something to
   * lose, and says so.
   */
  let seeded = $state('')
  const dirty = $derived(editedManifest !== seeded)

  /**
   * Re-seeds on opening, and when the managed-fields preference changes.
   *
   * Reading `preferences.showManagedFields` here is what makes the toolbar
   * control work inside the dialog. It is safe only because the control is
   * disabled while `dirty`, so this can never overwrite an edit.
   */
  $effect(() => {
    if (!open) return
    const next = preferences.showManagedFields ? manifest : withoutManagedFields(manifest)
    editedManifest = next
    seeded = next
  })

  $effect(() => {
    if (!open) copied = false
  })

  async function copyManifest(): Promise<void> {
    try {
      await navigator.clipboard.writeText(editedManifest)
      copied = true
      setTimeout(() => (copied = false), 1500)
    } catch {
      copied = false
    }
  }

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

    <!-- The frame is the dialog's, not the pane's: YamlPane paints no
         background of its own, so it takes this one and cannot disagree with
         the panel around it. -->
    <div class="field mt-4 flex min-h-0 flex-1 flex-col overflow-hidden">
      <YamlPane
        content={editedManifest}
        onchange={(value) => (editedManifest = value)}
        managedFieldsDisabled={dirty}
        managedFieldsDisabledReason="Can’t change while there are unsaved edits"
      >
        {#snippet actions()}
          <ToolbarButton
            icon={copied ? Check : Copy}
            label="Copy manifest"
            title={copied ? 'Copied' : 'Copy manifest'}
            active={copied}
            onclick={copyManifest}
          />
        {/snippet}
      </YamlPane>
    </div>

    <div class="mt-4 flex justify-end gap-3">
      <Button variant="outlined" onclick={onclose}>Cancel</Button>
      <Button variant="filled" onclick={handleSubmit}>Apply</Button>
    </div>
  </div>
{/if}
