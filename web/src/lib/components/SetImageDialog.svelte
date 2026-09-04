<!--
  Dialog for setting one or more container images on a Deployment,
  StatefulSet or DaemonSet.

  Lists every container the pod template carries — ordinary and init — each
  with its current image beside a field pre-filled with it. Only a field an
  operator actually changed produces a write; see $lib/setImage.changedImages
  for exactly what "changed" means (trimmed, and never equal to what is
  already there).

  UNLIKE Scale or Restart, this makes its own calls rather than handing a
  single primitive back through onconfirm — one call per changed container,
  in template order, STOPPING AT THE FIRST FAILURE. A rollout is not atomic
  either way, so there is no correctness lost by stopping early, and reporting
  exactly which containers changed before a failure is more useful than a
  single "failed" for what might have been three separate writes. Mirrors
  DrainDialog, which is self-contained for the same reason: several requests
  behind one confirm, worth narrating individually while they run.
-->
<script lang="ts">
  import { escapeLayer, type EscapeClaim } from '$lib/escape'
  import { modal } from '$lib/modal'
  import { setImage as kubectlSetImage } from '$lib/kubectl'
  import { setImage as callSetImage } from '$lib/api/client'
  import { toApiError } from '$lib/api/errors'
  import { changedImages, type ImageChange } from '$lib/setImage'
  import type { PodTemplate } from '$lib/podTemplate'
  import Button from './Button.svelte'
  import KubectlHint from './KubectlHint.svelte'
  import { TriangleAlert, Check } from '@lucide/svelte'

  interface Props {
    open: boolean
    /** The kubeconfig context this cluster connects through — also what the actual calls below are made against. See $lib/kubectl. */
    ctx: string
    /** 'Deployment', 'StatefulSet' or 'DaemonSet' — the three kinds whose pod template sits at spec.template. */
    kind: string
    name: string
    namespace: string
    /**
     * The pod template to list containers from, FROM THE OBJECT'S OWN
     * MANIFEST — never the watch store, which strips a template to its
     * images and would show every field but this one blank. See
     * $lib/podTemplate and CLAUDE.md.
     */
    template: PodTemplate | null
    /** The group's name, when this workload's cluster is marked production — null or undefined otherwise. */
    productionGroup?: string | null
    onclose: () => void
    /** Called once every changed container has been written successfully. */
    onapplied: () => void
  }

  let { open, ctx, kind, name, namespace, template, productionGroup, onclose, onapplied }: Props = $props()

  /** One row per container in the template: name, current image, and whether it is an init container. */
  interface Row {
    name: string
    image: string
    initContainer: boolean
  }

  function rowsOf(entries: Record<string, unknown>[] | undefined, initContainer: boolean): Row[] {
    const rows: Row[] = []
    for (const entry of entries ?? []) {
      const entryName = entry.name
      const entryImage = entry.image
      if (typeof entryName !== 'string') continue
      rows.push({ name: entryName, image: typeof entryImage === 'string' ? entryImage : '', initContainer })
    }
    return rows
  }

  const rows = $derived([
    ...rowsOf(template?.spec?.containers, false),
    ...rowsOf(template?.spec?.initContainers, true),
  ])

  /** What has been typed into each row's field, keyed by container name. Unedited rows are simply absent — see $lib/setImage.changedImages. */
  let edits = $state<Record<string, string>>({})

  // Fresh on every opening, so a previous workload's edits and result do not
  // carry over onto this one — the same reset ScaleDialog and DrainDialog do.
  $effect(() => {
    if (open) {
      edits = {}
      applying = false
      succeeded = []
      failure = null
    }
  })

  const changes = $derived(changedImages(template, edits))
  const hasChanges = $derived(changes.length > 0)

  let applying = $state(false)
  /** Container names successfully written so far, in the order they were applied. */
  let succeeded = $state<string[]>([])
  let failure = $state<{ container: string; message: string } | null>(null)

  async function handleApply(): Promise<void> {
    if (applying || !hasChanges) return
    applying = true
    succeeded = []
    failure = null

    for (const change of changes) {
      try {
        await callSetImage(ctx, kind, namespace, name, change.container, change.image, change.initContainer)
        succeeded = [...succeeded, change.container]
      } catch (error) {
        failure = { container: change.container, message: toApiError(error).message }
        applying = false
        return
      }
    }

    applying = false
    onapplied()
  }

  /**
   * Escape closes; Enter applies, but only where Enter meant nothing else.
   *
   * Mirrors ScaleDialog: the fields here are text inputs an operator is
   * expected to press Enter from, not only the confirm button.
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
    void handleApply()
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
    aria-label="Set image"
  >
    <h2 class="text-headline-small text-on-surface">Set image</h2>

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
      Change the image below for any container you want updated. Only edited fields are applied.
    </p>

    {#if rows.length === 0}
      <p class="mt-4 text-body-small text-on-surface-variant">No containers found in the pod template.</p>
    {:else}
      <div class="mt-4 flex max-h-[18rem] flex-col gap-3 overflow-y-auto">
        {#each rows as row (row.initContainer ? `init:${row.name}` : row.name)}
          {@const applied = succeeded.includes(row.name)}
          {@const failed = failure?.container === row.name}
          <label class="block">
            <span class="flex items-center gap-1.5 text-body-small text-on-surface-variant">
              {row.name}
              {#if row.initContainer}
                <span class="rounded-sm bg-surface-container px-1 py-px text-label-small text-on-surface-variant/70">
                  init
                </span>
              {/if}
              {#if applied}
                <Check class="size-3.5 text-success" strokeWidth={2.5} />
              {/if}
            </span>
            <input
              type="text"
              value={edits[row.name] ?? row.image}
              oninput={(event) => (edits = { ...edits, [row.name]: event.currentTarget.value })}
              disabled={applying}
              autocomplete="off"
              spellcheck="false"
              class="field mt-1 w-full px-3 py-2 font-mono text-body-small {failed ? 'border-error' : ''}"
            />
            {#if failed && failure}
              <p class="mt-1 flex items-start gap-1.5 text-body-small text-error">
                <TriangleAlert class="mt-0.5 size-3.5 shrink-0" strokeWidth={2} />
                {failure.message}
              </p>
            {/if}
          </label>
        {/each}
      </div>
    {/if}

    <!-- Live: one hint per row currently changed, so the preview always
         matches what Apply is about to send — the same convention
         ScaleDialog's single hint follows. -->
    {#if changes.length > 0}
      <div class="mt-4 flex flex-col gap-2">
        {#each changes as change (change.container)}
          <KubectlHint command={kubectlSetImage(ctx, kind, name, namespace, change.container, change.image)} />
        {/each}
      </div>
    {/if}

    {#if failure}
      <p class="mt-4 text-body-small text-on-surface-variant">
        {succeeded.length > 0
          ? `Updated ${succeeded.length} of ${changes.length} before this failed. Fix the image above and try again.`
          : 'Nothing was changed on the cluster.'}
      </p>
    {/if}

    <div class="mt-6 flex justify-end gap-3">
      <Button variant="outlined" onclick={onclose}>{failure ? 'Close' : 'Cancel'}</Button>
      <Button variant="filled" disabled={!hasChanges} loading={applying} onclick={handleApply}>
        {applying ? 'Applying…' : 'Apply'}
      </Button>
    </div>
  </div>
{/if}
