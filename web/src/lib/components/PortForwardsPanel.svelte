<!--
  Every running forward, across every cluster — the one place they are all
  listed together. ContainerDetail shows a pod's own ports; this is where
  "what have I got open right now, anywhere" gets answered, and where
  everything can be closed at once.

  A VIEW OVER forwards.active AND NOTHING ELSE. See the comment at the top of
  forwards.svelte.ts: the store never invents an entry because every leak and
  lie in the competing clients comes from a UI that shows something the
  backend is no longer holding. This panel renders straight off that store
  rather than keeping any record of its own, so the invariant holds here too.

  Hidden entirely when nothing is forwarded, like every other ambient fact in
  the status bar — a badge reading "0" earns its place no more than "0 items"
  did on an empty dashboard.
-->
<script lang="ts">
  import { forwards } from '$stores/forwards.svelte'
  import { escapeLayer, type EscapeClaim } from '$lib/escape'
  import ForwardAddress from './ForwardAddress.svelte'
  import { Plug, Loader, Unplug, X } from '@lucide/svelte'

  let open = $state(false)

  function onWindowPointerDown(event: PointerEvent): void {
    if (!open) return
    const target = event.target as HTMLElement | null
    if (!target?.closest('[data-forwards-panel]')) open = false
  }

  function onKeydown(event: KeyboardEvent): void {
    if (event.key !== 'Escape') return
    // One Escape, one layer. See $lib/escape.
    if (!escape?.owns()) return
    open = false
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

  // Closing the panel itself does not stop anything — only Stop and Stop all
  // do that, and both go through the store exactly as every other control in
  // the application does.
</script>

<svelte:window onpointerdown={onWindowPointerDown} onkeydown={onKeydown} />

{#if forwards.active.length > 0}
  <div class="relative" data-forwards-panel>
    <button
      type="button"
      onclick={() => (open = !open)}
      aria-expanded={open}
      aria-haspopup="dialog"
      title="Port forwards"
      class="state-layer flex cursor-pointer items-center gap-1 rounded-xs px-1 tabular-nums
             opacity-70 transition-opacity duration-100 hover:opacity-100"
    >
      <Plug class="size-3" strokeWidth={2} />
      {forwards.active.length}
    </button>

    {#if open}
      <div
        role="dialog"
        aria-label="Port forwards"
        class="absolute bottom-full left-0 z-50 mb-1.5 max-h-96 w-96 overflow-y-auto rounded-sm
               border border-outline-variant/60 bg-surface-container-high shadow-level-2"
      >
        <div class="flex items-center justify-between border-b border-outline-variant/40 px-3 py-2">
          <p class="text-label-small font-semibold tracking-wider text-on-surface-variant/60 uppercase">
            Port forwards ({forwards.active.length})
          </p>

          <button
            type="button"
            disabled={forwards.stoppingAll}
            onclick={() => void forwards.stopAll()}
            class="state-layer flex items-center gap-1 rounded-xs px-1.5 py-0.5 text-body-small
                   text-on-surface-variant transition-colors duration-100
                   hover:bg-surface-container-highest hover:text-on-surface disabled:opacity-50"
          >
            {#if forwards.stoppingAll}
              <Loader class="size-3 animate-spin" strokeWidth={2} />
            {:else}
              <X class="size-3" strokeWidth={2} />
            {/if}
            Stop all
          </button>
        </div>

        <ul class="divide-y divide-outline-variant/30 py-1">
          {#each forwards.active as forward (forward.id)}
            {@const rowBusy = forwards.isBusy(
              forward.clusterId,
              forward.namespace,
              forward.pod,
              forward.remotePort,
            )}
            <li class="flex flex-col gap-1 px-3 py-2">
              <!-- WHICH POD, WHICH NAMESPACE, WHICH CLUSTER — the three facts
                   ContainerDetail's per-port row can leave to context because
                   it is already open on one pod. Nothing here can, since a
                   forward here is as likely to be on a different tab
                   entirely. -->
              <div class="flex min-w-0 items-center gap-1.5 text-body-small text-on-surface-variant">
                <span class="truncate font-medium text-on-surface">{forward.pod}</span>
                <span class="text-on-surface-variant/40" aria-hidden="true">·</span>
                <span class="truncate">{forward.namespace}</span>
                <span class="text-on-surface-variant/40" aria-hidden="true">·</span>
                <span class="truncate">{forward.clusterId}</span>
              </div>

              <div class="flex min-w-0 items-center justify-between gap-2">
                {#if forward.reconnecting}
                  <!--
                    Distinct from a live address on purpose: the local port
                    stays bound, but nothing should be told this is fine while
                    a replacement pod is still being sought.

                    TODO(kubectl-transparency): the kubectl-equivalent command
                    belongs on this line once it exists.
                  -->
                  <span class="flex min-w-0 items-center gap-1.5 text-body-small text-gauge-warn">
                    <Loader class="size-3.5 shrink-0 animate-spin" strokeWidth={2} />
                    Waiting for a replacement pod
                  </span>
                {:else}
                  <ForwardAddress {forward} />
                {/if}

                <button
                  type="button"
                  disabled={rowBusy}
                  onclick={() => forwards.stop(forward)}
                  aria-label="Stop forwarding {forward.address}"
                  title="Stop"
                  class="state-layer grid size-6 shrink-0 place-items-center rounded-xs
                         text-on-surface-variant transition-colors duration-100
                         hover:bg-surface-container-highest hover:text-on-surface disabled:opacity-50"
                >
                  {#if rowBusy}
                    <Loader class="size-3.5 animate-spin" strokeWidth={2} />
                  {:else}
                    <Unplug class="size-3.5" strokeWidth={1.8} />
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
