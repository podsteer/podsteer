<!--
  Confirmation dialog for deleting a resource.

  On a cluster whose group is marked production, deleting costs one more
  step: typing the object's exact name before Delete enables. The ordinary
  "are you sure" click is cheap enough to make without reading it on a
  cluster nobody has flagged as special; on one that has, the extra keystrokes
  are the point — see CLAUDE.md, "Type-the-name confirmation on production".
-->
<script lang="ts">
  import { escapeLayer, type EscapeClaim } from '$lib/escape'
  import { modal } from '$lib/modal'
  import { nameConfirmed } from '$lib/confirm'
  import Button from './Button.svelte'
  import { TriangleAlert } from '@lucide/svelte'

  interface Props {
    open: boolean
    resourceName: string | null
    resourceKind: string
    /**
     * The group's name, when the cluster this object lives on is marked
     * production — null or undefined otherwise. Non-null both shows the
     * banner and turns on the type-the-name requirement below.
     */
    productionGroup?: string | null
    onclose: () => void
    onconfirm: () => void
  }

  let { open, resourceName, resourceKind, productionGroup, onclose, onconfirm }: Props = $props()

  const requiresTypedName = $derived(!!productionGroup)

  /** What has been typed into the confirmation field. */
  let typed = $state('')
  $effect(() => {
    if (!open) typed = ''
  })

  const confirmed = $derived(
    !requiresTypedName || (resourceName !== null && nameConfirmed(typed, resourceName)),
  )

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
    aria-label="Close dialog"
    tabindex="-1"
    class="fixed inset-0 z-[60] cursor-default bg-scrim/40"
    onclick={onclose}
  ></button>

  <div
    class="fixed top-1/2 left-1/2 z-[70] w-[28rem] max-w-[90vw] -translate-x-1/2 -translate-y-1/2
           rounded-sm border border-outline-variant bg-surface-container-high p-6 shadow-level-3"
    role="dialog"
    aria-modal="true"
    use:modal
    aria-label="Delete resource"
  >
    <h2 class="text-headline-small text-on-surface">Delete {resourceKind}</h2>

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
      Are you sure you want to delete <strong class="text-on-surface" data-selectable>{resourceName}</strong>?
      This action cannot be undone.
    </p>

    {#if requiresTypedName}
      <label class="mt-4 block">
        <span class="text-body-small text-on-surface-variant">
          Type <strong class="text-on-surface" data-selectable>{resourceName}</strong> to confirm
        </span>
        <input
          type="text"
          bind:value={typed}
          autocomplete="off"
          spellcheck="false"
          aria-describedby="delete-confirm-hint"
          class="field mt-1 w-full px-3 py-2 text-body-medium"
        />
      </label>
      <p id="delete-confirm-hint" class="mt-1.5 text-body-small text-on-surface-variant/70">
        {confirmed
          ? 'Name confirmed.'
          : `Delete stays disabled until the name above matches exactly.`}
      </p>
    {/if}

    <div class="mt-6 flex justify-end gap-3">
      <Button variant="outlined" onclick={onclose}>Cancel</Button>
      <Button
        variant="filled"
        disabled={!confirmed}
        describedBy={requiresTypedName ? 'delete-confirm-hint' : undefined}
        onclick={onconfirm}
      >
        Delete
      </Button>
    </div>
  </div>
{/if}
