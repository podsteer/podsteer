<!--
  Settings.

  Refresh lives here rather than as a button in the toolbar. A refresh control
  on the main surface implies refreshing is something you do — it is not, the
  views poll on their own. What an operator occasionally wants to change is how
  *often*, and that is a setting, not an action.

  The manual refresh remains available as a keyboard shortcut, because the one
  time you genuinely want it is right after changing something with kubectl.
-->
<script lang="ts">
  import { preferences, REFRESH_INTERVALS, PAGE_SIZES, type PageSize } from '$stores/preferences.svelte'
  import Button from './Button.svelte'

  interface Props {
    open: boolean
    onclose: () => void
    /** Runs a one-off refresh of the active view. */
    onrefresh: () => void
  }

  let { open, onclose, onrefresh }: Props = $props()

  function onKeydown(event: KeyboardEvent): void {
    if (event.key === 'Escape' && open) onclose()
  }
</script>

<svelte:window onkeydown={onKeydown} />

{#if open}
  <button
    type="button"
    aria-label="Close settings"
    tabindex="-1"
    class="fixed inset-0 z-40 cursor-default bg-scrim/40"
    onclick={onclose}
  ></button>

  <div
    class="fixed top-1/2 left-1/2 z-50 w-[28rem] max-w-[90vw] -translate-x-1/2 -translate-y-1/2
           rounded-xl border border-outline-variant bg-surface-container-high p-6 shadow-level-3"
    role="dialog"
    aria-modal="true"
    aria-label="Settings"
  >
    <h2 class="text-headline-small text-on-surface">Settings</h2>

    <div class="mt-6 flex flex-col gap-6">
      <section>
        <h3 class="text-title-small text-on-surface">Refresh</h3>
        <p class="mt-0.5 text-body-small text-on-surface-variant">
          How often the open view re-reads the cluster.
        </p>

        <div class="mt-3 flex flex-col gap-1.5">
          {#each REFRESH_INTERVALS as interval (interval.value)}
            <label class="flex cursor-pointer items-center gap-3 text-body-medium text-on-surface">
              <input
                type="radio"
                name="refresh-interval"
                value={interval.value}
                checked={preferences.effectiveIntervalMs === interval.value}
                onchange={() => preferences.setRefreshInterval(interval.value)}
                class="accent-primary"
              />
              {interval.label}
            </label>
          {/each}
        </div>

        <Button variant="tonal" class="mt-4" onclick={onrefresh}>Refresh now</Button>
      </section>

      <section class="border-t border-outline-variant pt-5">
        <h3 class="text-title-small text-on-surface">Rows per page</h3>
        <p class="mt-0.5 text-body-small text-on-surface-variant">
          Applies to every resource table.
        </p>

        <div class="mt-3 flex gap-2">
          {#each PAGE_SIZES as size (size)}
            <button
              type="button"
              onclick={() => preferences.setPageSize(size as PageSize)}
              class="state-layer h-8 min-w-12 rounded-xs border px-3 text-label-large
                     transition-colors duration-150 ease-standard
                     {preferences.pageSize === size
                       ? 'border-transparent bg-secondary-container text-on-secondary-container'
                       : 'border-outline text-on-surface-variant'}"
            >
              {size}
            </button>
          {/each}
        </div>
      </section>
    </div>

    <div class="mt-8 flex justify-end">
      <Button onclick={onclose}>Done</Button>
    </div>
  </div>
{/if}
