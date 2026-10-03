<!--
  Where $stores/notices are drawn: a small stack in the bottom-right corner.

  A polite live region rather than an alert, and it never takes focus — these
  are things that happened in the background, and interrupting typing to
  announce one would be worse than the silence it replaces. Each can be
  dismissed, and each goes away by itself.
-->
<script lang="ts">
  import { notices } from '$stores/notices.svelte'
  import { X } from '@lucide/svelte'
</script>

<div
  class="pointer-events-none fixed right-3 bottom-8 z-[60] flex w-80 flex-col gap-2"
  role="status"
  aria-live="polite"
>
  {#each notices.items as notice (notice.id)}
    <div
      class="pointer-events-auto flex items-start gap-2 rounded-sm border border-outline-variant/60
             bg-surface-container-high px-3 py-2 text-body-small text-on-surface shadow-level-2"
    >
      <p class="min-w-0 flex-1" data-selectable>{notice.message}</p>
      <button
        type="button"
        onclick={() => notices.dismiss(notice.id)}
        aria-label="Dismiss"
        class="state-layer grid size-5 shrink-0 place-items-center rounded-xs text-on-surface-variant
               hover:text-on-surface"
      >
        <X class="size-3.5" strokeWidth={2} />
      </button>
    </div>
  {/each}
</div>
