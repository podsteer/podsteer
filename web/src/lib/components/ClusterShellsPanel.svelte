<!--
  Every in-cluster shell running right now — the activity-list counterpart to
  NodeShellsPanel, and built the same way and for the same reason: it is a pod
  PodSteer created, so it must be visible somewhere it can always be stopped,
  whatever surface opened it. See CLAUDE.md's node-shell lifecycle note, which
  this follows.

  A VIEW OVER clusterShells.active AND NOTHING ELSE — the store never invents
  an entry, so a shell shows here only while its pod exists.
-->
<script lang="ts">
  import { clusterShells } from '$stores/clusterShells.svelte'
  import { escapeLayer, type EscapeClaim } from '$lib/escape'
  import { Container, Loader, X } from '@lucide/svelte'

  let open = $state(false)

  function onWindowPointerDown(event: PointerEvent): void {
    if (!open) return
    const target = event.target as HTMLElement | null
    if (!target?.closest('[data-cluster-shells-panel]')) open = false
  }

  function onKeydown(event: KeyboardEvent): void {
    if (event.key !== 'Escape') return
    if (!escape?.owns()) return
    open = false
  }

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

<svelte:window onpointerdown={onWindowPointerDown} onkeydown={onKeydown} />

{#if clusterShells.active.length > 0}
  <div class="relative" data-cluster-shells-panel>
    <button
      type="button"
      onclick={() => (open = !open)}
      aria-expanded={open}
      aria-haspopup="dialog"
      title="In-cluster shells"
      class="state-layer flex cursor-pointer items-center gap-1 rounded-xs px-1 tabular-nums
             opacity-70 transition-opacity duration-100 hover:opacity-100"
    >
      <Container class="size-3" strokeWidth={2} />
      {clusterShells.active.length}
    </button>

    {#if open}
      <div
        role="dialog"
        aria-label="In-cluster shells"
        class="absolute bottom-full left-0 z-50 mb-1.5 max-h-96 w-96 overflow-y-auto rounded-sm
               border border-outline-variant/60 bg-surface-container-high shadow-level-2"
      >
        <div class="flex items-center justify-between border-b border-outline-variant/40 px-3 py-2">
          <p class="text-label-small font-semibold tracking-wider text-on-surface-variant/60 uppercase">
            In-cluster shells ({clusterShells.active.length})
          </p>

          <button
            type="button"
            disabled={clusterShells.stoppingAll}
            onclick={() => void clusterShells.stopAll()}
            class="state-layer flex items-center gap-1 rounded-xs px-1.5 py-0.5 text-body-small
                   text-on-surface-variant transition-colors duration-100
                   hover:bg-surface-container-highest hover:text-on-surface disabled:opacity-50"
          >
            {#if clusterShells.stoppingAll}
              <Loader class="size-3 animate-spin" strokeWidth={2} />
            {:else}
              <X class="size-3" strokeWidth={2} />
            {/if}
            Stop all
          </button>
        </div>

        <ul class="divide-y divide-outline-variant/30 py-1">
          {#each clusterShells.active as shell (shell.id)}
            {@const rowBusy = clusterShells.isBusy(shell.id)}
            <li class="flex flex-col gap-1 px-3 py-2">
              <!-- Which pod, which namespace, which cluster — a shell in this
                   list is as likely to be on another tab entirely. `Reused`
                   marks a pod PodSteer took over rather than created, because
                   those are different sentences about somebody's namespace. -->
              <div class="flex min-w-0 items-center gap-1.5 text-body-small text-on-surface-variant">
                <span class="truncate font-medium text-on-surface">{shell.pod}</span>
                <span class="text-on-surface-variant/40" aria-hidden="true">·</span>
                <span class="truncate">{shell.namespace}</span>
                <span class="text-on-surface-variant/40" aria-hidden="true">·</span>
                <span class="truncate">{shell.clusterId}</span>
                {#if shell.adopted}
                  <span class="shrink-0 rounded-xs bg-surface-container-highest px-1 text-label-small">
                    Reused
                  </span>
                {/if}
              </div>

              <div class="flex min-w-0 items-center justify-between gap-2">
                <span class="min-w-0 truncate font-mono text-body-small text-on-surface-variant">
                  {shell.image}
                </span>

                <button
                  type="button"
                  disabled={rowBusy}
                  onclick={() => void clusterShells.stop(shell)}
                  aria-label="Stop in-cluster shell {shell.pod}"
                  title="Stop"
                  class="state-layer grid size-6 shrink-0 place-items-center rounded-xs
                         text-on-surface-variant transition-colors duration-100
                         hover:bg-surface-container-highest hover:text-on-surface disabled:opacity-50"
                >
                  {#if rowBusy}
                    <Loader class="size-3.5 animate-spin" strokeWidth={2} />
                  {:else}
                    <X class="size-3.5" strokeWidth={1.8} />
                  {/if}
                </button>
              </div>
            </li>
          {/each}
        </ul>
      </div>
    {/if}
  </div>
{/if}
