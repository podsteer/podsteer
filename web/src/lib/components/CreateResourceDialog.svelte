<!--
  Creating an object from a skeleton, and duplicating one that already
  exists — the two write the same way, so they share one dialog rather than
  becoming two that could drift apart.

  This is the maximised YAML pane's own shape (see DetailDrawer's `PaneDialog`
  over `yamlSurface`): the same YamlPane/YamlEditor, the same Cancel/Apply
  footer with a KubectlHint and the production banner. It is not literally
  that dialog, because there is no drawer for it to restore into and no
  object yet for a header to name — but it is built from the same pieces so
  editing a fresh manifest looks like editing any other one.
-->
<script lang="ts">
  import { escapeLayer, type EscapeClaim } from '$lib/escape'
  import { modal } from '$lib/modal'
  import { parse } from 'yaml'
  import type { Component } from 'svelte'
  import Button from './Button.svelte'
  import KubectlHint from './KubectlHint.svelte'
  import YamlPane from './YamlPane.svelte'
  import type { EditorApi } from './YamlEditor.svelte'
  import { apply as kubectlApply } from '$lib/kubectl'
  import { updateResource } from '$lib/api/client'
  import { toApiError } from '$lib/api/errors'
  import { TriangleAlert, X } from '@lucide/svelte'

  interface Props {
    open: boolean
    /** The kind's own icon, matching the drawer's header. */
    icon?: Component
    /** The kind's display name — "Deployment", "Disruption Budget" — used for
        the dialog's title and the kubectl hint's resource argument. */
    kindLabel: string
    /** "New" or "Duplicate" — the title reads `${verb} ${kindLabel}`. */
    verb: 'New' | 'Duplicate'
    /** The manifest to seed the editor with: a skeleton or a stripped copy.
        Read once, when the dialog opens — from then on the draft is this
        dialog's own state, same as the drawer's own edit mode. */
    seed: string
    /** The cluster this object will be created in. */
    clusterId: string
    /** The namespace to show in the kubectl hint's `-n` flag — cosmetic only,
        the manifest's own `metadata.namespace` is what is actually sent. */
    namespace?: string
    productionGroup?: string | null
    isReadOnly: boolean
    readOnlyReason: string
    onclose: () => void
    /** Fired after a successful Apply, with the name and namespace read back
        out of the manifest that was sent — the caller flashes, refreshes and
        opens the drawer on it. Nothing here assumes what happens next: a
        toolbar button and a drawer action want different things to happen
        around the same write. */
    oncreated: (name: string, namespace: string) => void
  }

  let {
    open,
    icon: Icon,
    kindLabel,
    verb,
    seed,
    clusterId,
    namespace,
    productionGroup,
    isReadOnly,
    readOnlyReason,
    onclose,
    oncreated,
  }: Props = $props()

  /** Seeded by the effect below, the same convention ScaleDialog and
      DeleteDialog follow: a prop read into `$state` only captures its
      INITIAL value, and this dialog stays mounted between openings. */
  let draft = $state('')
  let error = $state<string | null>(null)
  let submitting = $state(false)

  $effect(() => {
    if (open) {
      draft = seed
      error = null
      submitting = false
    }
  })

  /** The kubectl equivalent of Apply — same reasoning as DetailDrawer's own
      `applyCommand`: what PodSteer sends is the manifest itself, so the only
      thing worth showing is the invocation that would read it from stdin. */
  const applyCommand = $derived(kubectlApply(clusterId, namespace))

  /**
   * Where the empty `name: ""` sits in a freshly seeded document, as a
   * character range CodeMirror can select.
   *
   * Both `skeletonFor` and `stripForDuplicate` write `metadata.name` as
   * exactly `name: ""` — the first one in the document, since metadata is
   * always the first block — so a plain search for it is enough without
   * either module having to hand back a position of its own. Undefined when
   * the seed does not match, which just means the caret starts wherever
   * CodeMirror puts it.
   */
  function nameCaret(manifest: string): [number, number] | undefined {
    const needle = 'name: "'
    const at = manifest.indexOf(needle)
    if (at === -1 || manifest[at + needle.length] !== '"') return undefined
    const caret = at + needle.length
    return [caret, caret]
  }

  function onEditorReady(api: EditorApi): void {
    const caret = nameCaret(draft)
    if (caret) api.select(caret[0], caret[1])
  }

  async function handleApply(): Promise<void> {
    if (isReadOnly) return
    submitting = true
    error = null
    try {
      await updateResource(clusterId, draft)

      // Best-effort: the write already succeeded, so a manifest this
      // dialog's own re-parse trips on (unlikely — it is the exact text
      // that was just accepted) should not be reported as a failure. It
      // just means nothing is auto-opened.
      let name = ''
      let objectNamespace = ''
      try {
        const parsed = parse(draft) as { metadata?: { name?: string; namespace?: string } } | null
        name = parsed?.metadata?.name ?? ''
        objectNamespace = parsed?.metadata?.namespace ?? ''
      } catch {
        // See above.
      }

      onclose()
      oncreated(name, objectNamespace)
    } catch (cause) {
      error = `Failed to create: ${toApiError(cause).message}`
    } finally {
      submitting = false
    }
  }

  function onKeydown(event: KeyboardEvent): void {
    if (event.key !== 'Escape' || !open) return
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
    aria-label="Close"
    tabindex="-1"
    class="fixed inset-0 z-[60] cursor-default bg-scrim/40"
    onclick={onclose}
  ></button>

  <div
    class="fixed inset-6 z-[70] flex flex-col overflow-hidden rounded-sm border
           border-outline-variant bg-surface-container-high shadow-level-3"
    role="dialog"
    aria-modal="true"
    use:modal
    aria-label="{verb} {kindLabel}"
  >
    <header class="flex shrink-0 items-center gap-3 border-b border-outline-variant/60 px-4 py-3">
      {#if Icon}
        <Icon class="size-5 shrink-0 text-on-surface-variant" strokeWidth={1.8} />
      {/if}
      <h2 class="min-w-0 truncate text-title-medium font-semibold text-on-surface">
        {verb} {kindLabel}
      </h2>

      <button
        type="button"
        onclick={onclose}
        aria-label="Close"
        title="Close"
        class="state-layer ml-auto grid size-8 shrink-0 place-items-center rounded-full
               text-on-surface-variant transition-colors duration-100
               hover:bg-surface-container hover:text-on-surface"
      >
        <X class="size-4" strokeWidth={1.8} />
      </button>
    </header>

    <div class="min-h-0 flex-1 bg-surface-container-lowest">
      <YamlPane content={draft} onchange={(value) => (draft = value)} managedFields={false} onready={onEditorReady} />
    </div>

    <div class="flex shrink-0 flex-col gap-3 border-t border-outline-variant/60 px-4 py-3">
      {#if isReadOnly}
        <p
          class="flex items-start gap-2 rounded-sm border border-error/30 bg-error-container/40
                 px-3 py-2 text-body-small text-on-error-container"
        >
          <TriangleAlert class="mt-0.5 size-4 shrink-0" strokeWidth={1.8} />
          {readOnlyReason}
        </p>
      {:else if productionGroup}
        <p
          class="flex items-start gap-2 rounded-sm border border-error/30 bg-error-container/40
                 px-3 py-2 text-body-small text-on-error-container"
        >
          <TriangleAlert class="mt-0.5 size-4 shrink-0" strokeWidth={1.8} />
          This cluster is in {productionGroup}, marked production.
        </p>
      {/if}

      {#if error}
        <p class="text-body-small text-error" role="alert">{error}</p>
      {/if}

      <div class="flex items-center gap-3">
        <div class="min-w-0 flex-1">
          <KubectlHint command={applyCommand} />
        </div>
        <Button variant="outlined" onclick={onclose}>Cancel</Button>
        <Button variant="filled" disabled={isReadOnly || submitting} loading={submitting} onclick={handleApply}>
          Apply
        </Button>
      </div>
    </div>
  </div>
{/if}
