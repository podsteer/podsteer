<!--
  Who owns which field of this object, as a sentence rather than as storage.

  THE RECORD WAS ALREADY ON SCREEN AND NOBODY COULD READ IT. Turning on
  managed fields showed `f:spec: {f:template: {f:spec: {f:containers:
  {k:{"name":"web"}: {.: {}, f:image: {}}}}}}` — the API server's serialization
  of a field set, which is a correct answer to a question nobody asked. The
  toggle's own tooltip says it shows "which controller owns which field", and
  until this panel that was a claim the view did not keep.

  DECODED IN GO, NOT HERE. The paths come back from the same library the API
  server builds a conflict message with, so `.spec.template.spec.containers[name="web"].image`
  is spelled identically in this panel and in the dialog that refuses an apply
  over it — by construction rather than by two implementations agreeing today.
  The manager classification comes from the same table the apply path uses,
  for the same reason.

  The raw record stays below in the YAML. This does not replace it: an
  operator debugging an apply sometimes needs the bytes, and a summary that
  hides its source is a summary you have to trust.
-->
<script lang="ts">
  import { ChevronRight } from '@lucide/svelte'
  import { FieldOwnership } from '$lib/bindings/github.com/podsteer/podsteer/app/adapters/wails/managementapi'
  import type { FieldOwnerDTO } from '$lib/bindings/github.com/podsteer/podsteer/app/adapters/wails/models'
  import { toApiError } from '$lib/api/errors'

  interface Props {
    /** The manifest as fetched — with managedFields still on it. */
    manifest: string | null
  }

  let { manifest }: Props = $props()

  /**
   * The ledger, with `fields` guaranteed to be an array.
   *
   * The Go side already sends `[]` rather than `null` — see toFieldOwnership
   * — but the generated binding types it nullable, and a template that has to
   * ask is a template that will forget. Normalised once, on the way in.
   */
  type Owner = Omit<FieldOwnerDTO, 'fields'> & { fields: string[] }

  let owners = $state<Owner[]>([])
  let error = $state('')
  let expanded = $state<string | null>(null)

  /**
   * One decode per manifest, guarded by a generation.
   *
   * The drawer re-seeds this when the operator moves to another object, and
   * a decode of the previous one can still be in the air — the same guard
   * every other read in this application carries. It reaches no cluster, so
   * there is no spinner: a decode that takes long enough to need one would
   * be a bug.
   */
  let decodeGeneration = 0
  $effect(() => {
    const source = manifest
    const generation = ++decodeGeneration
    if (!source) {
      owners = []
      error = ''
      return
    }

    void (async () => {
      try {
        const decoded = await FieldOwnership(source)
        if (generation !== decodeGeneration) return
        owners = (decoded ?? []).map((owner) => ({ ...owner, fields: owner.fields ?? [] }))
        error = ''
      } catch (cause) {
        if (generation !== decodeGeneration) return
        owners = []
        error = toApiError(cause).message
      }
    })()
  })

  /** A key that separates the two operations of one manager, as the server does. */
  function keyOf(owner: Owner): string {
    return `${owner.manager}/${owner.operation}/${owner.subresource}`
  }

  /**
   * What this owner is, in the words the conflict dialog would use.
   *
   * Empty for a manager the table does not recognise — an operator's own
   * controller is more likely here than anything we could enumerate, and
   * inventing a category for it would put words in its mouth.
   */
  function describe(owner: Owner): string {
    switch (owner.kind) {
      case 'gitops':
        return 'a reconciler'
      case 'control-plane':
        return 'Kubernetes itself'
      case 'kubectl':
        return 'a person at a terminal'
      case 'podsteer':
        return 'PodSteer'
      default:
        return ''
    }
  }

  /** The day, not the second: the exact time is in the YAML below. */
  function when(updatedAt: string): string {
    if (!updatedAt) return ''
    const at = new Date(updatedAt)
    return Number.isNaN(at.getTime()) ? '' : at.toLocaleString()
  }
</script>

<section
  class="flex max-h-52 shrink-0 flex-col gap-1 overflow-y-auto border-b border-outline-variant/40 bg-surface-container-low px-3 py-2"
  aria-label="Field ownership"
>
  {#if error}
    <p class="text-body-small text-error" role="alert">
      The ownership record could not be read — {error}
    </p>
  {:else if owners.length === 0}
    <!--
      NOT AN EMPTY STATE TO APOLOGISE FOR. An object written before
      server-side apply has no ledger, and saying so is the whole answer.
    -->
    <p class="text-body-small text-on-surface-variant">
      The API server holds no ownership record for this object.
    </p>
  {:else}
    {#each owners as owner (keyOf(owner))}
      {@const key = keyOf(owner)}
      {@const open = expanded === key}
      <div class="flex flex-col">
        <button
          type="button"
          class="flex items-center gap-2 rounded-sm px-1 py-0.5 text-left hover:bg-on-surface/5"
          aria-expanded={open}
          onclick={() => (expanded = open ? null : key)}
        >
          <ChevronRight
            size={14}
            class="shrink-0 text-on-surface-variant transition-transform {open ? 'rotate-90' : ''}"
          />
          <span class="text-body-medium font-medium text-on-surface" data-selectable>
            {owner.manager}
          </span>
          {#if describe(owner)}
            <span class="text-body-small text-on-surface-variant">({describe(owner)})</span>
          {/if}
          <!--
            THE OPERATION IS NOT DECORATION. `podsteer`/Update and
            `podsteer`/Apply are two managers as far as the server is
            concerned, and an operator looking at two rows with one name needs
            the thing that tells them apart.
          -->
          <span class="text-label-small text-on-surface-variant/80">{owner.operation}</span>
          {#if owner.subresource}
            <span
              class="rounded-sm bg-on-surface/8 px-1 text-label-small text-on-surface-variant"
              title="Written through the {owner.subresource} subresource — not reachable by a write to the object itself"
            >
              {owner.subresource}
            </span>
          {/if}
          <span class="ml-auto shrink-0 text-label-small text-on-surface-variant/80 tabular-nums">
            {owner.fields.length}
            {owner.fields.length === 1 ? 'field' : 'fields'}{when(owner.updatedAt)
              ? ` · ${when(owner.updatedAt)}`
              : ''}
          </span>
        </button>

        {#if open}
          <ul class="flex flex-col gap-0.5 py-1 pl-7">
            {#each owner.fields as field (field)}
              <li class="font-mono text-body-small text-on-surface-variant" data-selectable>
                {field}
              </li>
            {:else}
              <li class="text-body-small text-on-surface-variant/80">
                This manager's entry no longer owns any field.
              </li>
            {/each}
          </ul>
        {/if}
      </div>
    {/each}
  {/if}
</section>
