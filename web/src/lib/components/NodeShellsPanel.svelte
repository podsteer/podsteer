<!--
  Every node shell running right now — the activity-list counterpart to
  PortForwardsPanel, and built the same way and for the same reason: a node
  shell is a privileged pod PodSteer created, so it must be visible somewhere
  it can always be stopped, whatever surface opened it. See CLAUDE.md's
  node-shell lifecycle note.

  A VIEW OVER nodeShells.active AND NOTHING ELSE — the store never invents an
  entry, so a shell shows here only while its pod exists.
-->
<script lang="ts">
  import { nodeShells } from '$stores/nodeShells.svelte'
  import { escapeLayer, type EscapeClaim } from '$lib/escape'
  import { SquareTerminal, Loader, X } from '@lucide/svelte'

  let open = $state(false)

  function onWindowPointerDown(event: PointerEvent): void {
    if (!open) return
    const target = event.target as HTMLElement | null
    if (!target?.closest('[data-node-shells-panel]')) open = false
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

{#if nodeShells.active.length > 0}
  <div class="relative" data-node-shells-panel>
    <button
      type="button"
      onclick={() => (open = !open)}
      aria-expanded={open}
      aria-haspopup="dialog"
      title="Node shells"
      class="state-layer flex cursor-pointer items-center gap-1 rounded-xs px-1 tabular-nums
             opacity-70 transition-opacity duration-100 hover:opacity-100"
    >
      <SquareTerminal class="size-3" strokeWidth={2} />
      {nodeShells.active.length}
    </button>

    {#if open}
      <div
        role="dialog"
        aria-label="Node shells"
        class="absolute bottom-full left-0 z-50 mb-1.5 max-h-96 w-96 overflow-y-auto rounded-sm
               border border-outline-variant/60 bg-surface-container-high shadow-level-2"
      >
        <div class="flex items-center justify-between border-b border-outline-variant/40 px-3 py-2">
          <p class="text-label-small font-semibold tracking-wider text-on-surface-variant/60 uppercase">
            Node shells ({nodeShells.active.length})
          </p>

          <button
            type="button"
            disabled={nodeShells.stoppingAll}
            onclick={() => void nodeShells.stopAll()}
            class="state-layer flex items-center gap-1 rounded-xs px-1.5 py-0.5 text-body-small
                   text-on-surface-variant transition-colors duration-100
                   hover:bg-surface-container-highest hover:text-on-surface disabled:opacity-50"
          >
            {#if nodeShells.stoppingAll}
              <Loader class="size-3 animate-spin" strokeWidth={2} />
            {:else}
              <X class="size-3" strokeWidth={2} />
            {/if}
            Stop all
          </button>
        </div>

        <ul class="divide-y divide-outline-variant/30 py-1">
          {#each nodeShells.active as shell (shell.id)}
            {@const rowBusy = nodeShells.isBusy(shell.id)}
            <li class="flex flex-col gap-1 px-3 py-2">
              <!-- Which node, which namespace, which cluster — a node shell in
                   this list is as likely to be on another tab entirely. -->
              <div class="flex min-w-0 items-center gap-1.5 text-body-small text-on-surface-variant">
                <span class="truncate font-medium text-on-surface">{shell.node}</span>
                <span class="text-on-surface-variant/40" aria-hidden="true">·</span>
                <span class="truncate">{shell.namespace}</span>
                <span class="text-on-surface-variant/40" aria-hidden="true">·</span>
                <span class="truncate">{shell.clusterId}</span>
              </div>

              <div class="flex min-w-0 items-center justify-between gap-2">
                <span class="min-w-0 truncate font-mono text-body-small text-on-surface-variant">
                  {shell.image}
                </span>

                <button
                  type="button"
                  disabled={rowBusy}
                  onclick={() => void nodeShells.stop(shell)}
                  aria-label="Stop node shell on {shell.node}"
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
