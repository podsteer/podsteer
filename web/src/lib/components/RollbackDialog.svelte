<!--
  Dialog for rolling a Deployment, StatefulSet or DaemonSet back to a
  previous revision, the way `kubectl rollout undo --to-revision` does.

  Mirrors SetImageDialog's production banner and KubectlHint, and DrainDialog's
  Preview-before-you-act shape: Preview runs the SAME call as Confirm, with
  dryRun true, so "the server accepted this" is never a guess made ahead of
  what the real rollback will do — the API server's own admission chain
  answers both.
-->
<script lang="ts">
  import { escapeLayer, type EscapeClaim } from '$lib/escape'
  import { modal } from '$lib/modal'
  import { rolloutUndo } from '$lib/kubectl'
  import { rollbackWorkload } from '$lib/api/client'
  import { toApiError } from '$lib/api/errors'
  import Button from './Button.svelte'
  import KubectlHint from './KubectlHint.svelte'
  import { TriangleAlert, Check, Loader } from '@lucide/svelte'

  interface Props {
    open: boolean
    /** The kubeconfig context this cluster connects through — see $lib/kubectl. */
    ctx: string
    /** 'Deployment', 'StatefulSet' or 'DaemonSet'. */
    kind: string
    name: string
    namespace: string
    toRevision: number
    /** The group's name, when this workload's cluster is marked production — null or undefined otherwise. */
    productionGroup?: string | null
    isReadOnly: boolean
    readOnlyReason: string
    onclose: () => void
    /** Called once the real rollback (not the preview) succeeds. */
    onrolledback: () => void
  }

  let {
    open,
    ctx,
    kind,
    name,
    namespace,
    toRevision,
    productionGroup,
    isReadOnly,
    readOnlyReason,
    onclose,
    onrolledback,
  }: Props = $props()

  let previewing = $state(false)
  let preview = $state<{ ok: true } | { ok: false; message: string } | null>(null)

  let confirming = $state(false)
  let confirmError = $state<string | null>(null)

  // Fresh on every opening, so a previous workload's preview or error does
  // not carry over onto this one — the same reset SetImageDialog and
  // DrainDialog make.
  $effect(() => {
    if (open) {
      previewing = false
      preview = null
      confirming = false
      confirmError = null
    }
  })

  async function handlePreview(): Promise<void> {
    if (previewing || confirming) return
    previewing = true
    preview = null
    try {
      await rollbackWorkload(ctx, kind, namespace, name, toRevision, true)
      preview = { ok: true }
    } catch (error) {
      preview = { ok: false, message: toApiError(error).message }
    } finally {
      previewing = false
    }
  }

  async function handleConfirm(): Promise<void> {
    if (previewing || confirming) return
    confirming = true
    confirmError = null
    try {
      await rollbackWorkload(ctx, kind, namespace, name, toRevision, false)
      onrolledback()
    } catch (error) {
      confirmError = toApiError(error).message
    } finally {
      confirming = false
    }
  }

  /** Escape closes; there is no Enter shortcut — a rollback is not a
   * one-click mistake to make easy, the same reasoning DrainDialog uses. */
  function onKeydown(event: KeyboardEvent): void {
    if (!open || event.key !== 'Escape') return
    if (!escape?.owns()) return
    onclose()
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
    class="fixed top-1/2 left-1/2 z-[70] w-[32rem] max-w-[90vw] -translate-x-1/2 -translate-y-1/2
           rounded-sm border border-outline-variant bg-surface-container-high p-6 shadow-level-3"
    role="dialog"
    aria-modal="true"
    use:modal
    aria-label="Roll back"
  >
    <h2 class="text-headline-small text-on-surface">Roll back to revision {toRevision}</h2>

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
      {name}'s pod template will be replaced with revision {toRevision}'s. This is itself a new
      rollout — every pod is recreated to match it, the same as any other template change.
    </p>

    {#if isReadOnly}
      <p
        id="rollback-readonly-hint"
        class="mt-4 flex items-start gap-2 rounded-sm border border-outline-variant/60 bg-surface px-3 py-2 text-body-small text-on-surface-variant"
      >
        <TriangleAlert class="mt-0.5 size-3.5 shrink-0" strokeWidth={2} />
        {readOnlyReason}
      </p>
    {/if}

    <div class="mt-4 flex flex-col gap-2">
      <KubectlHint command={rolloutUndo(ctx, kind, name, namespace, toRevision)} />

      <div class="min-h-[2.5rem] rounded-sm border border-outline-variant/60 bg-surface p-3 text-body-small">
        {#if previewing}
          <p class="flex items-center gap-2 text-on-surface-variant">
            <Loader class="size-3.5 animate-spin" strokeWidth={2} />
            Checking with the cluster…
          </p>
        {:else if preview?.ok}
          <p class="flex items-center gap-2 text-success">
            <Check class="size-3.5 shrink-0" strokeWidth={2} />
            The server accepted the rollback.
          </p>
        {:else if preview && !preview.ok}
          <p class="flex items-start gap-2 text-error">
            <TriangleAlert class="mt-0.5 size-3.5 shrink-0" strokeWidth={2} />
            {preview.message}
          </p>
        {:else}
          <p class="text-on-surface-variant/60">
            Preview sends this rollback to the cluster as a dry run — nothing is changed until you
            confirm.
          </p>
        {/if}
      </div>
    </div>

    {#if confirmError}
      <p class="mt-4 flex items-start gap-2 text-body-small text-error">
        <TriangleAlert class="mt-0.5 size-3.5 shrink-0" strokeWidth={2} />
        {confirmError}
      </p>
    {/if}

    <div class="mt-6 flex justify-end gap-3">
      <Button variant="outlined" onclick={onclose}>Cancel</Button>
      <Button variant="outlined" loading={previewing} disabled={confirming} onclick={handlePreview}>
        Preview
      </Button>
      <Button
        variant="filled"
        loading={confirming}
        disabled={isReadOnly || previewing}
        describedBy={isReadOnly ? 'rollback-readonly-hint' : undefined}
        onclick={handleConfirm}
      >
        Roll back
      </Button>
    </div>
  </div>
{/if}
