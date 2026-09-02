<!--
  Add a cluster, by giving PodSteer a kubeconfig to merge into the local one.

  Paste or choose a file, rather than a form of fields. A form would look
  friendlier and would fail for most people: EKS, GKE and AKS all authenticate
  through an exec credential plugin, and there is no set of text inputs that
  expresses `aws eks get-token` with its arguments and environment. A
  kubeconfig already does, and it is what every provider hands you.

  The preview is not decoration. This is the one place PodSteer writes to a
  file full of credentials, so what is about to happen is shown — which
  contexts, into which file — before anything is written, and it is computed by
  the same code that performs the write rather than by a second guess at it.
-->
<script lang="ts">
  import Button from './Button.svelte'
  import {
    addKubeconfig,
    previewKubeconfig,
    readKubeconfigFile,
    type KubeconfigMerge,
  } from '$lib/api/client'
  import { toApiError, type ApiError } from '$lib/api/errors'
  import { workspace } from '$stores/workspace.svelte'
  import { AlertTriangle, CheckCircle2, FileUp } from '@lucide/svelte'

  interface Props {
    open: boolean
    onclose: () => void
  }

  let { open, onclose }: Props = $props()

  let raw = $state('')
  let preview = $state<KubeconfigMerge | null>(null)
  let previewError = $state<string | null>(null)
  let busy = $state(false)
  let error = $state<ApiError | null>(null)
  let added = $state<KubeconfigMerge | null>(null)

  /** Discards everything, so reopening never shows the previous attempt. */
  function reset(): void {
    raw = ''
    preview = null
    previewError = null
    error = null
    added = null
    busy = false
  }

  function close(): void {
    reset()
    onclose()
  }

  /**
   * Previews as the text changes, debounced.
   *
   * Parsing half-typed YAML fails constantly, and a message that flickers on
   * every keystroke is noise — so the error only appears once typing pauses.
   */
  let debounce: ReturnType<typeof setTimeout> | null = null
  /** Previews asked for so far, so a superseded one cannot land. */
  let previewRequest = 0
  $effect(() => {
    const text = raw
    if (debounce) clearTimeout(debounce)

    if (text.trim() === '') {
      preview = null
      previewError = null
      return
    }

    // THE DEBOUNCE IS NOT THE GUARD. It stops a request per keystroke; it
    // does not stop two in flight from landing out of order, and this dialog
    // exists to show what is about to happen before a file full of
    // credentials is written. Paste one config, edit to another, and the
    // first could land last — leaving the preview describing a paste that is
    // no longer in the box, while `add()` posts the one that is.
    const asked = ++previewRequest

    debounce = setTimeout(async () => {
      try {
        const result = await previewKubeconfig(text)
        if (asked !== previewRequest) return
        preview = result
        previewError = null
      } catch (cause) {
        if (asked !== previewRequest) return
        preview = null
        previewError = toApiError(cause).message
      }
    }, 350)

    return () => {
      if (debounce) clearTimeout(debounce)
    }
  })

  async function chooseFile(): Promise<void> {
    error = null
    try {
      const contents = await readKubeconfigFile()
      // An empty string is a cancelled picker, not a failure.
      if (contents !== '') raw = contents
    } catch (cause) {
      error = toApiError(cause)
    }
  }

  async function add(): Promise<void> {
    busy = true
    error = null
    try {
      added = await addKubeconfig(raw)
      // The picker is the confirmation: the contexts appear in it, in the
      // Default project, ready to be organised or opened.
      await workspace.loadClusters()
    } catch (cause) {
      error = toApiError(cause)
    } finally {
      busy = false
    }
  }

  function onKeydown(event: KeyboardEvent): void {
    if (event.key === 'Escape' && open) close()
  }

  const canAdd = $derived(
    !busy && preview !== null && preview.added.length > 0 && preview.conflicts.length === 0,
  )
</script>

<svelte:window onkeydown={onKeydown} />

{#if open}
  <button
    type="button"
    aria-label="Close add cluster"
    tabindex="-1"
    class="fixed inset-0 z-40 cursor-default bg-scrim/40"
    onclick={close}
  ></button>

  <div class="pointer-events-none fixed inset-0 z-50 grid place-items-center p-4">
    <div
      class="pointer-events-auto flex max-h-[85vh] w-[42rem] max-w-[94vw] flex-col rounded-sm
             border border-outline-variant bg-surface-container-high p-6 shadow-level-3"
      role="dialog"
      aria-modal="true"
      aria-label="Add cluster"
    >
      {#if added}
        <!-- Done. Shown instead of the form rather than beside it, so there is
             no half-submitted state to misread. -->
        <div class="flex items-start gap-3">
          <CheckCircle2 class="mt-0.5 size-6 shrink-0 text-success" strokeWidth={1.8} />
          <div class="min-w-0">
            <h2 class="text-headline-small text-on-surface">
              Added {added.added.length}
              {added.added.length === 1 ? 'context' : 'contexts'}
            </h2>
            <p class="mt-1 text-body-medium text-on-surface-variant">
              {added.added.join(', ')} — now in your picker, under the default project.
            </p>
            <p class="mt-3 text-body-small text-on-surface-variant/70">
              Written to <span class="font-mono" data-selectable>{added.path}</span>. The previous
              version is beside it as
              <span class="font-mono">{added.path}.podsteer.bak</span>.
            </p>
          </div>
        </div>

        <div class="mt-6 flex shrink-0 justify-end gap-2">
          <Button variant="text" onclick={reset}>Add another</Button>
          <Button onclick={close}>Done</Button>
        </div>
      {:else}
        <h2 class="text-headline-small text-on-surface">Add cluster</h2>
        <p class="mt-1 text-body-small text-on-surface-variant">
          Paste a kubeconfig — the one your provider gave you, or a single cluster's worth — and
          PodSteer will merge it into yours. Existing contexts are never replaced.
        </p>

        <textarea
          bind:value={raw}
          spellcheck="false"
          placeholder={'apiVersion: v1\nkind: Config\nclusters:\n  - cluster:\n      server: https://…'}
          aria-label="Kubeconfig"
          class="field mt-4 min-h-56 flex-1 resize-none px-3 py-2 font-mono text-body-small"
        ></textarea>

        <!-- One row, so the panel does not jump as the message changes. -->
        <div class="mt-3 min-h-10 text-body-small">
          {#if preview && preview.conflicts.length > 0}
            <p class="flex items-start gap-2 text-warning">
              <AlertTriangle class="mt-0.5 size-4 shrink-0" strokeWidth={1.8} />
              <span>
                Your kubeconfig already has {preview.conflicts.length === 1 ? 'a context' : 'contexts'}
                named <strong>{preview.conflicts.join(', ')}</strong>. Rename
                {preview.conflicts.length === 1 ? 'it' : 'them'} in the text above and try again —
                PodSteer will not overwrite credentials that already work.
              </span>
            </p>
          {:else if preview && preview.added.length > 0}
            <p class="text-on-surface-variant">
              Adds <strong class="text-on-surface">{preview.added.join(', ')}</strong>
              to <span class="font-mono">{preview.path}</span>
            </p>
          {:else if previewError}
            <p class="text-error">{previewError}</p>
          {:else if raw.trim() !== ''}
            <p class="text-on-surface-variant/60">Checking…</p>
          {/if}
        </div>

        {#if error}
          <p class="mt-1 text-body-small text-error">{error.message}</p>
        {/if}

        <div class="mt-4 flex shrink-0 items-center justify-between gap-2">
          <Button variant="tonal" onclick={chooseFile}>
            <FileUp class="size-4" strokeWidth={1.8} />
            Choose a file
          </Button>

          <div class="flex items-center gap-2">
            <Button variant="text" onclick={close}>Cancel</Button>
            <Button disabled={!canAdd} loading={busy} onclick={add}>
              {busy ? 'Adding' : 'Add cluster'}
            </Button>
          </div>
        </div>
      {/if}
    </div>
  </div>
{/if}
