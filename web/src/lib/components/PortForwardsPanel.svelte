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
  import ForwardKubectl from './ForwardKubectl.svelte'
  import { Plug, Loader, RefreshCw, Unplug, X } from '@lucide/svelte'

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

{#if forwards.active.length > 0 || forwards.paused.length > 0}
  <div class="relative" data-forwards-panel>
    <button
      type="button"
      onclick={() => (open = !open)}
      aria-expanded={open}
      aria-haspopup="dialog"
      title="Port forwards"
      class="state-layer flex cursor-pointer items-center gap-1 rounded-xs px-1 tabular-nums
             transition-opacity duration-100 hover:opacity-100
             {forwards.lost.length > 0 ? 'text-gauge-warn-ink opacity-100' : 'opacity-70'}"
    >
      <Plug class="size-3" strokeWidth={2} />
      {forwards.active.length + forwards.paused.length}
      {#if forwards.lost.length > 0}
        <span class="sr-only">, {forwards.lost.length} lost</span>
      {/if}
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
            disabled={forwards.stoppingAll || forwards.active.length === 0}
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
                {#if forward.lost}
                  <!--
                    THE FORWARD USED TO VANISH here. Nothing is bound and
                    whatever was pointed at the port is being refused, so it
                    is said in words and offered the way back; it also keeps
                    looking by itself, slowly.
                  -->
                  <span class="flex min-w-0 items-center gap-1.5 text-body-small text-gauge-warn-ink">
                    Lost — nothing is answering
                    <button
                      type="button"
                      onclick={() => forwards.reconnect(forward)}
                      class="state-layer inline-flex items-center gap-1 rounded-xs px-1 text-label-large
                             text-on-surface hover:bg-surface-container-highest"
                    >
                      <RefreshCw class="size-3" strokeWidth={2} />
                      Reconnect
                    </button>
                  </span>
                {:else if forward.reconnecting}
                  <!--
                    Distinct from a live address on purpose: the local port
                    stays bound, but nothing should be told this is fine while
                    a replacement pod is still being sought.
                  -->
                  <span class="flex min-w-0 items-center gap-1.5 text-body-small text-gauge-warn-ink">
                    <Loader class="size-3.5 shrink-0 animate-spin" strokeWidth={2} />
                    Waiting for a replacement pod
                  </span>
                {:else}
                  <ForwardAddress {forward} />
                {/if}

                <!-- Opt-in, per forward. Saves the definition only. -->
                <label
                  class="flex shrink-0 cursor-pointer items-center gap-1 text-body-small text-on-surface-variant"
                  title="Reopen this forward after PodSteer restarts, once its cluster is connected."
                >
                  <input
                    type="checkbox"
                    checked={forward.kept}
                    onchange={(event) => forwards.setKept(forward, event.currentTarget.checked)}
                    class="size-3.5 accent-primary"
                  />
                  Keep
                </label>

                <ForwardKubectl {forward} />

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

        {#if forwards.paused.length > 0}
          <!--
            KEPT FORWARDS THAT ARE NOT RUNNING. Nothing here connects a
            cluster: opening the cluster is what brings its forwards back, and
            until then they are listed as paused so they are not mistaken for
            lost ones. A failed restore says why and can be retried.
          -->
          <p
            class="border-t border-outline-variant/40 px-3 pt-2 pb-1 text-label-small font-semibold
                   tracking-wider text-on-surface-variant/60 uppercase"
          >
            Kept, not running ({forwards.paused.length})
          </p>
          <ul class="divide-y divide-outline-variant/30 pb-1">
            {#each forwards.paused as paused (paused.clusterId + ':' + paused.localPort)}
              <li class="flex flex-col gap-1 px-3 py-2">
                <div class="flex min-w-0 items-center gap-1.5 text-body-small text-on-surface-variant">
                  <span class="truncate font-medium text-on-surface">{paused.targetName}</span>
                  <span class="text-on-surface-variant/40" aria-hidden="true">·</span>
                  <span class="truncate">{paused.namespace}</span>
                  <span class="text-on-surface-variant/40" aria-hidden="true">·</span>
                  <span class="truncate">{paused.clusterId}</span>
                </div>
                <div class="flex min-w-0 items-center justify-between gap-2 text-body-small">
                  <span class="min-w-0 truncate {paused.state === 'failed' ? 'text-error' : 'text-on-surface-variant'}">
                    {#if paused.state === 'paused'}
                      Paused — reconnect {paused.clusterId} to resume · localhost:{paused.localPort}
                    {:else if paused.state === 'restoring'}
                      Restoring · localhost:{paused.localPort}
                    {:else}
                      Could not resume: {paused.reason}
                    {/if}
                  </span>
                  <span class="flex shrink-0 items-center gap-1">
                    {#if paused.state === 'failed'}
                      <button
                        type="button"
                        onclick={() => forwards.resume(paused)}
                        class="state-layer rounded-xs px-1.5 text-label-large text-on-surface
                               hover:bg-surface-container-highest"
                      >
                        Retry
                      </button>
                    {/if}
                    <button
                      type="button"
                      onclick={() => forwards.forget(paused)}
                      aria-label="Forget the kept forward on localhost:{paused.localPort}"
                      title="Forget — do not restore this forward"
                      class="state-layer grid size-6 place-items-center rounded-xs
                             text-on-surface-variant hover:bg-surface-container-highest
                             hover:text-on-surface"
                    >
                      <X class="size-3.5" strokeWidth={1.8} />
                    </button>
                  </span>
                </div>
              </li>
            {/each}
          </ul>
        {/if}
      </div>
    {/if}
  </div>
{/if}
