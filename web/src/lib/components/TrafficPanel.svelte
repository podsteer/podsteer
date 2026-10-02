<!--
  Observed traffic for the topology page: a switch, a source, a window, and
  what the cluster's own Prometheus said.

  OFF BY DEFAULT, AND NOTHING IS ASKED UNTIL SOMEBODY ASKS. Switching on asks
  which sources the discovered monitoring backend can answer for (once per
  cluster), then queries the first one that can. Changing the source or window
  is one gesture and one query; the refresh button is another. Nothing here
  reads the session's refresh tick, and a changed scope only raises a hint —
  the same rule the metrics query holds (CLAUDE.md, ADR 7).

  The wording of the backend status, the provenance line and the "What
  PodSteer asked" disclosure follow BackendSeriesNote.svelte: PodSteer shows
  the PromQL it sent, never takes one typed, and installs nothing.
-->
<script lang="ts">
  import { onDestroy } from 'svelte'
  import { Activity, Check, Copy, RefreshCw, ShieldAlert, ShieldCheck, ShieldQuestion } from '@lucide/svelte'
  import { toApiError } from '$lib/api/errors'
  import { copyText } from '$lib/clipboard'
  import Checkbox from '$lib/components/Checkbox.svelte'
  import { settingsDialog } from '$stores/settingsDialog.svelte'
  import type {
    TrafficLayer,
    TrafficSourceName,
    TrafficSources,
    TrafficWindow,
  } from '$lib/topology/contract'
  import { isTrafficNotAvailable, traffic, trafficSources } from '$lib/topology/trafficApi'
  import {
    NOTHING_INSTALLED,
    provenanceNote,
    sourceLabel,
    trafficState,
    type TrafficFilters,
  } from '$lib/trafficLayer'

  interface Props {
    clusterId: string
    /** The scope the map is drawn for. */
    namespaces: string[]
    all: boolean
    /** The loaded layer, or null when off, loading, or failed. */
    onlayer: (layer: TrafficLayer | null) => void
    /** Overlay filters; bind to hand them to buildOverlay. */
    filters?: TrafficFilters
  }

  let {
    clusterId,
    namespaces,
    all,
    onlayer,
    filters = $bindable({ hideSystem: true, hideExternal: false }),
  }: Props = $props()

  const WINDOWS: TrafficWindow[] = ['5m', '15m', '1h']

  let on = $state(false)
  let sources = $state<TrafficSources | null>(null)
  let layer = $state<TrafficLayer | null>(null)
  let source = $state<TrafficSourceName | null>(null)
  let window = $state<TrafficWindow>('5m')
  let busy = $state(false)
  let error = $state<string | null>(null)
  let loadedScope = $state('')
  let copied = $state(false)
  /** Latest request wins; an older answer is dropped. */
  let token = 0
  let sourcesFor = ''

  const scopeKey = $derived(all ? '*' : [...namespaces].sort().join(','))
  const view = $derived(trafficState(sources, layer))
  const note = $derived(layer ? provenanceNote(layer.provenance) : null)
  const scopeChanged = $derived(layer !== null && loadedScope !== scopeKey)
  const VerifyIcon = $derived(
    note && layer && typeof layer.provenance === 'object'
      ? ((layer.provenance as { verification?: string }).verification === 'verified'
          ? ShieldCheck
          : (layer.provenance as { verification?: string }).verification === 'unverifiable'
            ? ShieldQuestion
            : ShieldAlert)
      : ShieldQuestion,
  )

  function publish(next: TrafficLayer | null) {
    layer = next
    onlayer(next)
  }

  async function loadSources(): Promise<boolean> {
    if (sources && sourcesFor === clusterId) return true
    const mine = ++token
    busy = true
    error = null
    try {
      const s = await trafficSources(clusterId)
      if (mine !== token) return false
      sources = s
      sourcesFor = clusterId
      source = s.sources.find((x) => x.available)?.source ?? null
      return true
    } catch (e) {
      if (mine !== token) return false
      sources = null
      error = isTrafficNotAvailable(e) ? e.message : toApiError(e).message
      return false
    } finally {
      if (mine === token) busy = false
    }
  }

  async function load() {
    if (!source) return
    const mine = ++token
    busy = true
    error = null
    publish(null)
    try {
      const l = await traffic(clusterId, namespaces, all, source, window)
      if (mine !== token) return
      loadedScope = scopeKey
      publish(l)
    } catch (e) {
      if (mine !== token) return
      error = toApiError(e).message
    } finally {
      if (mine === token) busy = false
    }
  }

  async function toggle(next: boolean) {
    on = next
    if (!next) {
      token++
      busy = false
      error = null
      publish(null)
      return
    }
    if (await loadSources()) await load()
  }

  function pickSource(s: TrafficSourceName) {
    if (s === source) return
    source = s
    void load()
  }

  function pickWindow(w: TrafficWindow) {
    if (w === window) return
    window = w
    void load()
  }

  async function copy() {
    if (!layer) return
    copied = await copyText(layer.expressions.join('\n'))
    if (copied) setTimeout(() => (copied = false), 1500)
  }

  // A different cluster invalidates everything, and switches the layer off:
  // asking a new cluster is a new gesture.
  $effect(() => {
    void clusterId
    if (sourcesFor && sourcesFor !== clusterId) {
      token++
      sources = null
      sourcesFor = ''
      source = null
      on = false
      busy = false
      error = null
      publish(null)
    }
  })

  onDestroy(() => {
    token++
    onlayer(null)
  })
</script>

<section
  class="flex flex-col gap-2 rounded-md bg-surface-container-low px-3 py-2 text-body-small
         text-on-surface-variant"
  aria-label="Observed traffic"
>
  <div class="flex flex-wrap items-center gap-x-3 gap-y-1.5">
    <Checkbox checked={on} onchange={toggle}>
      <span class="flex items-center gap-1.5 font-medium text-on-surface">
        <Activity class="size-3.5 opacity-70" strokeWidth={2} />
        Observed traffic
      </span>
    </Checkbox>

    {#if on && sources && source}
      <div class="flex items-center gap-1" role="group" aria-label="Traffic source">
        {#each sources.sources as s (s.source)}
          <button
            type="button"
            disabled={!s.available || busy}
            aria-pressed={source === s.source}
            title={s.detail}
            onclick={() => pickSource(s.source)}
            class="rounded px-2 py-0.5 transition-colors duration-100 disabled:opacity-40
                   {source === s.source
              ? 'bg-secondary-container text-on-surface'
              : 'hover:bg-surface-container-high'}"
          >
            {sourceLabel(s.source)}
          </button>
        {/each}
      </div>

      <div class="flex items-center gap-1" role="group" aria-label="Window">
        {#each WINDOWS as w (w)}
          <button
            type="button"
            disabled={busy}
            aria-pressed={window === w}
            onclick={() => pickWindow(w)}
            class="rounded px-2 py-0.5 transition-colors duration-100 disabled:opacity-40
                   {window === w
              ? 'bg-secondary-container text-on-surface'
              : 'hover:bg-surface-container-high'}"
          >
            {w}
          </button>
        {/each}
      </div>

      <button
        type="button"
        onclick={load}
        disabled={busy}
        title="Ask the monitoring backend now"
        class="rounded p-1 transition-colors duration-100 hover:bg-surface-container-high
               hover:text-on-surface disabled:opacity-40"
      >
        <RefreshCw class="size-3.5 {busy ? 'animate-spin' : ''}" strokeWidth={2} />
        <span class="sr-only">Ask the monitoring backend now</span>
      </button>
    {/if}
  </div>

  {#if !on}
    <p>
      Off. Turning it on asks this cluster's monitoring backend how much traffic it has seen between
      workloads. Nothing is asked until you do.
    </p>
  {:else}
    {#if error}
      <p class="rounded bg-error-container/25 px-2 py-1" role="alert">{error}</p>
    {/if}

    {#if busy && !layer && !sources}
      <p>Asking the monitoring backend which traffic sources it holds…</p>
    {/if}

    {#if view.kind === 'not-enabled'}
      <p>
        <strong class="font-medium text-on-surface">Metrics query is not enabled for this cluster.</strong>
        {view.message}
        <button
          type="button"
          class="underline hover:text-on-surface"
          onclick={() => settingsDialog.show('clusters')}
        >
          Open the cluster's metrics query setting
        </button>
      </p>
    {:else if view.kind === 'no-prometheus'}
      <p>
        <strong class="font-medium text-on-surface">No Prometheus was found for this cluster.</strong>
        {view.message}
        Traffic is read from the monitoring backend the cluster already runs. {NOTHING_INSTALLED}
      </p>
    {:else if view.kind === 'backend-problem'}
      <p class="rounded bg-error-container/25 px-2 py-1">
        <strong class="font-medium text-on-surface">
          The monitoring backend did not answer the query ({view.status}).
        </strong>
        {view.message}
      </p>
    {:else if view.kind === 'no-source'}
      <div class="flex flex-col gap-1.5">
        <p>
          <strong class="font-medium text-on-surface">
            No traffic source was found in {sources?.backend || 'the monitoring backend'}.
          </strong>
          Each of these would make one appear:
        </p>
        <ul class="flex list-disc flex-col gap-0.5 pl-5">
          {#each view.needs as n (n.source)}
            <li><span class="font-medium text-on-surface">{n.label}</span> — {n.needs}</li>
          {/each}
        </ul>
        <p>{NOTHING_INSTALLED}</p>
      </div>
    {:else if view.kind === 'too-large'}
      <p class="rounded bg-error-container/25 px-2 py-1">
        <strong class="font-medium text-on-surface">There is more traffic than PodSteer will draw.</strong>
        {view.message} Narrow the scope to fewer namespaces.
      </p>
    {:else if view.kind === 'no-traffic'}
      <p>
        <strong class="font-medium text-on-surface">
          {sources?.backend || 'The monitoring backend'} answered with no traffic in the last {window}.
        </strong>
        {view.message}
      </p>
    {:else if view.kind === 'ready'}
      <p>
        <strong class="font-medium text-on-surface">
          {view.edges} traffic {view.edges === 1 ? 'edge' : 'edges'} from {sourceLabel(layer!.source)},
          last {layer!.window}.
        </strong>
        Drawn over the map as a layer; they are not relationships Kubernetes holds.
        {#if view.unmapped > 0}
          {view.unmapped} {view.unmapped === 1 ? 'endpoint' : 'endpoints'} could not be matched to a
          box on this map.
        {/if}
      </p>

      {#if scopeChanged}
        <p>The scope changed since this was asked. Refresh to ask again.</p>
      {/if}

      <div class="flex flex-wrap items-center gap-x-4 gap-y-1">
        <Checkbox
          checked={filters.hideSystem ?? false}
          onchange={(v) => (filters = { ...filters, hideSystem: v })}
        >
          Hide system namespaces
        </Checkbox>
        <Checkbox
          checked={filters.hideExternal ?? false}
          onchange={(v) => (filters = { ...filters, hideExternal: v })}
        >
          Hide external and unknown
        </Checkbox>
      </div>

      {#if note}
        <p class="flex items-start gap-2">
          <VerifyIcon class="mt-0.5 size-3.5 shrink-0 opacity-60" strokeWidth={2} />
          <span>
            Read from {note.source}. {note.text}
            This is the backend's measurement over a window, not a live count.
          </span>
        </p>
      {/if}

      {#if layer && layer.expressions.length > 0}
        <details>
          <summary class="cursor-pointer text-on-surface-variant/70 hover:text-on-surface">
            What PodSteer asked
          </summary>
          <div class="mt-1 flex items-start gap-1">
            <code
              class="block flex-1 overflow-x-auto rounded-sm bg-surface-container px-2 py-1
                     font-mono text-body-small whitespace-pre text-on-surface-variant"
            >
              {layer.expressions.join('\n')}
            </code>
            <button
              type="button"
              onclick={copy}
              title="Copy the expressions"
              class="rounded p-1 transition-colors duration-100 hover:bg-surface-container-high
                     hover:text-on-surface"
            >
              {#if copied}
                <Check class="size-3.5" strokeWidth={2} />
              {:else}
                <Copy class="size-3.5" strokeWidth={2} />
              {/if}
              <span class="sr-only">Copy the expressions</span>
            </button>
          </div>
        </details>
      {/if}
    {/if}
  {/if}
</section>
