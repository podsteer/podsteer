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
  import HelpButton from './HelpButton.svelte'
  import { applyServerSide } from '$lib/kubectl'
  import { applyResource, type FieldConflict } from '$lib/api/client'
  import { nameConfirmed } from '$lib/confirm'
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

  /**
   * The fields another manager owns, when the server refused this apply.
   *
   * NOT AN ERROR, AND RENDERED AS ONE WOULD BE WRONG. The request was well
   * formed, the server understood it, and it declined — nothing was written
   * and nothing is broken. What the operator needs is who owns what, so they
   * can decide whether to change the manifest, go and talk to whoever runs
   * that controller, or leave it alone.
   */
  let conflicts = $state<FieldConflict[]>([])

  /**
   * What the operator has typed to confirm a forced override.
   *
   * THE SAME GATE DELETE USES, on a production cluster, because taking a
   * field from another manager is at least as consequential: it changes a
   * live object in a way whoever owns that field did not ask for. Off a
   * production cluster the button alone is the confirmation — the conflict
   * list above it is already the thing being agreed to.
   */
  let overrideTyped = $state('')

  /**
   * The object's own name and namespace, read from the draft.
   *
   * BOTH HALVES TOGETHER, because they were not. handleOverride reported a
   * successful forced apply with `oncreated(objectName, '')` — the right name
   * and NO namespace — so the drawer opened on an object it then could not
   * fetch: "No manifest available", and a red "The requested resource no
   * longer exists" over a list where the object was plainly still running.
   * handleApply had always parsed both. Two call sites, one of them wrong,
   * and nothing to keep them in step.
   */
  const objectIdentity = $derived.by(() => {
    try {
      const parsed = parse(draft) as { metadata?: { name?: string; namespace?: string } } | null
      return { name: parsed?.metadata?.name ?? '', namespace: parsed?.metadata?.namespace ?? '' }
    } catch {
      return { name: '', namespace: '' }
    }
  })

  /** The object's own name, for the production confirmation gate. */
  const objectName = $derived(objectIdentity.name)

  const overrideAllowed = $derived.by(() => {
    if (conflicts.length === 0) return false
    if (!productionGroup) return true
    // AN EMPTY NAME MUST NOT OPEN THE GATE. nameConfirmed compares the typed
    // text to the expected one, and two empty strings are equal — so a draft
    // this dialog cannot parse a name out of would let an override through on
    // a production cluster with nothing typed at all.
    return objectName !== '' && nameConfirmed(overrideTyped, objectName)
  })

  /** Whether any conflicting owner will simply put its value back. */
  const revertsAnyway = $derived(conflicts.some((conflict) => conflict.kind === 'gitops'))

  /** Whether EVERY one will — which changes "some of these" to "these". */
  const revertsAll = $derived(
    conflicts.length > 0 && conflicts.every((conflict) => conflict.kind === 'gitops'),
  )
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
  // WHAT THE APPLY BUTTON SENDS, WHICH NEVER FORCES. This carried
  // --force-conflicts as soon as a conflict existed — before the operator had
  // chosen anything — so the hint beside Apply described a command Apply does
  // not send. The forced one belongs beside the button that forces, and is
  // rendered there.
  const applyCommand = $derived(applyServerSide(clusterId, namespace, false))

  /** What Override sends, shown beside Override. */
  const overrideCommand = $derived(applyServerSide(clusterId, namespace, true))

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

  /**
   * Applies again, taking the fields the operator just read and agreed to.
   *
   * The confirmed set travels to the server, which re-reads the live
   * ownership one round trip before writing — so a manager who took a field
   * while this dialog was open refuses the write rather than being
   * overridden unseen, and the new set comes back here to be read.
   */
  async function handleOverride(): Promise<void> {
    if (isReadOnly || !overrideAllowed) return
    submitting = true
    error = null
    const agreed = conflicts
    try {
      const outcome = await applyResource(clusterId, draft, false, agreed)
      if (outcome.refused) {
        // Ownership moved underneath the dialog. The new set replaces the
        // old one and the typed confirmation is cleared: it was agreement to
        // a claim that is no longer true.
        conflicts = outcome.conflicts ?? []
        overrideTyped = ''
        submitting = false
        return
      }
      conflicts = []
      onclose()
      oncreated(objectIdentity.name, objectIdentity.namespace)
    } catch (cause) {
      error = String(cause)
    } finally {
      submitting = false
    }
  }

  async function handleApply(): Promise<void> {
    if (isReadOnly) return
    submitting = true
    error = null
    conflicts = []
    try {
      // THE APPLY VERB, NOT THE EDITOR'S. This dialog's manifest is declared
      // intent — it names what it cares about and says nothing about the
      // rest — so applying it must MERGE. Sending it through updateResource
      // replaced the object whole, which is how pasting a Deployment without
      // spec.replicas over one an HPA had scaled reset the replica count.
      const outcome = await applyResource(clusterId, draft)

      // A refusal resolves rather than throws: the server understood the
      // request and declined it because somebody else owns a field. Nothing
      // was written, so the dialog stays open with the owners named.
      if (outcome.refused) {
        conflicts = outcome.conflicts ?? []
        submitting = false
        return
      }

      // From objectIdentity, which handleOverride also reads — so the two
      // cannot report the object differently. They did: one passed a
      // namespace and the other passed nothing.
      onclose()
      oncreated(objectIdentity.name, objectIdentity.namespace)
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

      <div class="ml-auto flex shrink-0 items-center gap-0.5">
        <HelpButton topic="create-resource" about="{verb} {kindLabel}" />
        <button
          type="button"
          onclick={onclose}
          aria-label="Close"
          title="Close"
          class="state-layer grid size-8 shrink-0 place-items-center rounded-full
                 text-on-surface-variant transition-colors duration-100
                 hover:bg-surface-container hover:text-on-surface"
        >
          <X class="size-4" strokeWidth={1.8} />
        </button>
      </div>
    </header>

    <div class="min-h-0 flex-1 bg-surface-container-lowest">
      <YamlPane content={draft} onchange={(value) => (draft = value)} managedFields={false} onready={onEditorReady} />
    </div>

    <div class="flex shrink-0 flex-col gap-3 border-t border-outline-variant/60 px-4 py-3">
      {#if isReadOnly}
        <p
          class="flex items-start gap-2 rounded-sm border border-error/30 bg-error-container/40
                 px-3 py-2 text-body-medium text-on-error-container"
        >
          <TriangleAlert class="mt-0.5 size-4 shrink-0" strokeWidth={1.8} />
          {readOnlyReason}
        </p>
      {:else if productionGroup}
        <p
          class="flex items-start gap-2 rounded-sm border border-error/30 bg-error-container/40
                 px-3 py-2 text-body-medium text-on-error-container"
        >
          <TriangleAlert class="mt-0.5 size-4 shrink-0" strokeWidth={1.8} />
          This cluster is in {productionGroup}, marked production.
        </p>
      {/if}

      {#if error}
        <p class="text-body-medium text-error" role="alert">{error}</p>
      {/if}

      <!--
        THE SERVER DECLINED, AND NOTHING WAS WRITTEN. Rendered in the warning
        tone rather than the error one: an operator who reads this as a
        failure goes looking for a problem that is not there. The sentence per
        owner differs because what overriding one would MEAN differs — taking
        a field from a reconciler is not durable, taking one from kubectl
        takes it from a person.
      -->
      {#if conflicts.length > 0}
        <div class="flex flex-col gap-2 rounded-sm border border-gauge-warn/30 bg-gauge-warn/10 p-3" role="status">
          <p class="text-body-medium text-on-surface">
            Not applied — {conflicts.length === 1 ? 'a field is' : 'these fields are'} owned by
            another manager, so nothing was changed.
          </p>
          <ul class="flex flex-col gap-1">
            {#each conflicts as conflict (conflict.field + conflict.manager)}
              <li class="text-body-medium text-on-surface-variant" data-selectable>
                <span class="font-medium text-on-surface">{conflict.field}</span>
                — {conflict.manager}{conflict.kind === 'gitops'
                  ? ' (a reconciler: it would put its value back on the next sync)'
                  : conflict.kind === 'control-plane'
                    ? ' (Kubernetes itself: the controller will keep rewriting it)'
                    : ''}
              </li>
            {/each}
          </ul>
          <p class="text-body-medium text-on-surface-variant/80">
            Remove {conflicts.length === 1 ? 'that field' : 'those fields'} from the manifest to apply
            the rest — or take {conflicts.length === 1 ? 'it' : 'them'} over.
          </p>

          {#if revertsAnyway}
            <!--
              THE BUTTON MUST NOT SAY "Take ownership" HERE. A reconciler
              takes the field straight back on its next sync, so ownership is
              a claim this cannot keep. Saying what will actually happen is
              the only honest label.
            -->
            <p class="text-body-medium text-gauge-warn">
              {conflicts.length === 1
                ? 'A reconciler owns this field.'
                : revertsAll
                  ? 'A reconciler owns these fields.'
                  : 'A reconciler owns some of these.'}
              Overriding changes the cluster now and is undone on its next sync — change it where it
              is declared instead.
            </p>
          {/if}

          {#if productionGroup}
            <!-- The same gate Delete uses, because taking a field from
                 another manager changes a live object in a way whoever owns
                 it did not ask for. -->
            <label class="flex flex-col gap-1 text-body-medium text-on-surface-variant">
              Type <span class="font-medium text-on-surface">{objectName}</span> to override on this
              production cluster
              <input
                type="text"
                data-testid="override-confirm"
                bind:value={overrideTyped}
                autocomplete="off"
                spellcheck="false"
                class="rounded-sm border border-outline-variant bg-surface px-2 py-1 text-body-medium text-on-surface"
              />
            </label>
          {/if}

          <!-- items-start so the button keeps its own width. A flex column
               stretches its children, which turned this into a full-width bar
               reading "Take ownership" — a destructive-ish action styled like
               a page control. -->
          <div class="flex flex-col items-start gap-2">
            <Button
              variant="outlined"
              disabled={isReadOnly || submitting || !overrideAllowed}
              onclick={handleOverride}
            >
              {revertsAnyway ? 'Override anyway' : 'Take ownership'}
            </Button>
            <KubectlHint command={overrideCommand} />
          </div>
        </div>
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
