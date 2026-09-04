<!--
  The bar that appears over a list once rows are ticked: how many, and what
  can be done to them at once.

  Floating rather than in the toolbar, because it exists only while a
  selection does and it belongs to the rows, not to the view's controls — a
  count that sat beside the pager would be read as one more filter. The
  actions offered are those the LIST's kind can take (see $lib/bulk); which
  of the ticked rows each one will actually touch is the review dialog's
  answer, per object, from the same plan the run then executes.

  On a read-only cluster the actions are disabled with the reason, the same
  way every write control in the drawer is. The backend refuses regardless
  (see ManagementService.runBulk); this is the first line, not the last.
-->
<script lang="ts">
  import Button from './Button.svelte'
  import { BULK_ACTIONS, bulkActionsFor, type BulkActionId } from '$lib/bulk'
  import type { ClusterSession } from '$stores/session.svelte'
  import { X } from '@lucide/svelte'

  interface Props {
    session: ClusterSession
    isReadOnly: boolean
    readOnlyReason: string
    onaction: (action: BulkActionId) => void
  }

  let { session, isReadOnly, readOnlyReason, onaction }: Props = $props()

  const count = $derived(session.bulkItems.length)
  const kind = $derived(session.selectedKind)
  const actions = $derived(kind ? bulkActionsFor(kind.kind) : [])

  /** "3 pods", "1 deployment" — the kind as the navigator names it. */
  const noun = $derived(
    kind ? (count === 1 ? kind.singular : kind.title).toLowerCase() : count === 1 ? 'row' : 'rows',
  )
</script>

{#if count > 0 && kind}
  <div
    role="toolbar"
    aria-label="Bulk actions"
    class="fixed bottom-6 left-1/2 z-40 flex -translate-x-1/2 items-center gap-2 rounded-sm border
           border-outline-variant bg-surface-container-high py-2 pr-2 pl-4 shadow-level-3"
  >
    <span class="whitespace-nowrap text-label-large text-on-surface">
      <span class="tabular-nums">{count}</span>
      {noun} selected
    </span>

    <div class="mx-1 h-5 w-px shrink-0 bg-outline-variant/60" aria-hidden="true"></div>

    {#each actions as action (action)}
      {@const copy = BULK_ACTIONS[action]}
      <!-- A span carries the title: the button is inert while disabled and
           a disabled control shows no tooltip of its own. -->
      <span title={isReadOnly ? readOnlyReason : undefined}>
        <Button
          variant={copy.destructive ? 'outlined' : 'tonal'}
          class={copy.destructive ? 'border-error text-error' : ''}
          disabled={isReadOnly}
          onclick={() => onaction(action)}
        >
          {copy.label}
        </Button>
      </span>
    {/each}

    <button
      type="button"
      onclick={() => session.selection.clear()}
      aria-label="Clear selection"
      title="Clear selection (Escape)"
      class="state-layer grid size-8 shrink-0 place-items-center rounded-full text-on-surface-variant
             transition-colors duration-100 hover:bg-surface-container hover:text-on-surface"
    >
      <X class="size-4" strokeWidth={1.8} />
    </button>
  </div>
{/if}
