<!--
  The events of a single object, shown inside the detail drawer.

  Distinct from the cluster-wide event list in $pages/EventsView.svelte: this
  one is scoped to one involved object, so it answers "why is THIS unhealthy"
  rather than "what is happening in the cluster".

  Kubernetes discards events after roughly an hour, so an empty list here is
  the normal state for a healthy long-running object and is worded as such —
  never as an error.
-->
<script lang="ts">
  import { ListEventsForResource } from '$lib/wailsjs/go/wails/BrowseAPI'
  import type { wails } from '$lib/wailsjs/go/models'
  import { formatAge } from '$lib/format'
  import { toApiError } from '$lib/api/errors'
  import { AlertTriangle, Activity } from '@lucide/svelte'

  interface Props {
    clusterId: string
    namespace: string
    kind: string
    name: string
  }

  let { clusterId, namespace, kind, name }: Props = $props()

  let events = $state<wails.Event[]>([])
  let status = $state<'loading' | 'ready' | 'error'>('loading')
  let error = $state<string | null>(null)

  // Refetches whenever the drawer is pointed at a different object; the guard
  // keeps a response for a previous selection from landing after a newer one.
  $effect(() => {
    const target = { clusterId, namespace, kind, name }
    let current = true
    status = 'loading'

    void ListEventsForResource(target.clusterId, target.namespace, target.kind, target.name)
      .then((result) => {
        if (!current) return
        events = result
        status = 'ready'
      })
      .catch((cause) => {
        if (!current) return
        error = toApiError(cause).message
        status = 'error'
      })

    return () => {
      current = false
    }
  })
</script>

<div class="h-full overflow-auto">
  {#if status === 'loading'}
    <div class="flex h-full flex-col items-center justify-center gap-2 p-4 text-on-surface-variant/60">
      <Activity class="size-8 animate-pulse" strokeWidth={1.2} />
      <p class="text-body-medium">Loading events…</p>
    </div>
  {:else if status === 'error'}
    <div class="flex h-full flex-col items-center justify-center gap-2 p-6 text-center">
      <AlertTriangle class="size-8 text-error" strokeWidth={1.2} />
      <p class="text-body-medium text-on-surface-variant">{error}</p>
    </div>
  {:else if events.length === 0}
    <div class="flex h-full flex-col items-center justify-center gap-2 p-6 text-center">
      <Activity class="size-8 text-on-surface-variant/40" strokeWidth={1.2} />
      <p class="text-body-medium text-on-surface-variant">No recent events</p>
      <p class="max-w-xs text-body-small text-on-surface-variant/70">
        Kubernetes discards events after about an hour, so a quiet object shows nothing here.
      </p>
    </div>
  {:else}
    <ul class="divide-y divide-outline-variant/40">
      {#each events as event (event.namespace + '/' + event.name)}
        <li class="flex gap-3 px-4 py-3">
          <span
            class="mt-1.5 size-2 shrink-0 rounded-full {event.isWarning
              ? 'bg-warning'
              : 'bg-outline-variant'}"
            aria-hidden="true"
          ></span>

          <div class="min-w-0 flex-1">
            <div class="flex items-baseline justify-between gap-3">
              <span class="truncate text-label-large text-on-surface">{event.reason}</span>
              <span class="shrink-0 text-body-small tabular-nums text-on-surface-variant/70">
                {formatAge(event.ageSeconds)}
              </span>
            </div>

            <p class="mt-0.5 text-body-small break-words text-on-surface-variant" data-selectable>
              {event.message}
            </p>

            <p class="mt-1 text-body-small text-on-surface-variant/60">
              {event.source || 'unknown source'}{event.count > 1 ? ` · ×${event.count}` : ''}
            </p>
          </div>
        </li>
      {/each}
    </ul>
  {/if}
</div>
