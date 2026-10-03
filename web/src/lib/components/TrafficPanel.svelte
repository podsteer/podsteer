<!--
  Observed traffic's controls, for the topology page's traffic popover: a
  source, a window, a refresh, two filters and what PodSteer asked.

  SWITCHED ON AND OFF BY THE PAGE (`on`, bound to the toolbar's traffic
  toggle), and NOTHING IS ASKED UNTIL IT IS ON. Switching on asks which
  sources the discovered monitoring backend can answer for — every time, since
  the answer depends on a setting that may have changed — then queries the
  first one that can. Changing the source or window is one gesture and one
  query; the refresh button is another. Nothing here reads the session's
  refresh tick, and a changed scope only raises a hint (CLAUDE.md, ADR 7).

  THE EXPLANATIONS ARE NOT HERE. Why nothing is drawn, what each source needs,
  where the numbers came from: `onhelp` hands them up as Help sections, and
  the page shows them in the Help panel. This keeps one line of status.
-->
<script lang="ts">
  import { onDestroy, untrack } from 'svelte'
  import { clusterSettings } from '$stores/clusterSettings.svelte'
  import { Check, Copy, RefreshCw } from '@lucide/svelte'
  import { toApiError } from '$lib/api/errors'
  import { copyText } from '$lib/clipboard'
  import Checkbox from '$lib/components/Checkbox.svelte'
  import type { HelpSection } from '$lib/help'
  import { settingsDialog } from '$stores/settingsDialog.svelte'
  import type {
    TrafficLayer,
    TrafficSourceName,
    TrafficSources,
    TrafficWindow,
  } from '$lib/topology/contract'
  import { isTrafficNotAvailable, traffic, trafficSources } from '$lib/topology/trafficApi'
  import {
    sourceLabel,
    trafficHelp,
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
    /** Whether the layer is on — the toolbar's toggle. */
    on?: boolean
    /** What there is to explain about the layer now, for the Help panel. */
    onhelp?: (help: { problem: boolean; sections: HelpSection[] }) => void
  }

  let {
    clusterId,
    namespaces,
    all,
    onlayer,
    filters = $bindable({ hideSystem: true, hideExternal: false }),
    on = $bindable(false),
    onhelp,
  }: Props = $props()

  const WINDOWS: TrafficWindow[] = ['5m', '15m', '1h']

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
  const scopeChanged = $derived(layer !== null && loadedScope !== scopeKey)
  const said = $derived(
    trafficHelp(view, { on, busy, error, backend: sources?.backend ?? '', window, layer }),
  )
  $effect(() => {
    onhelp?.({ problem: said.problem, sections: said.sections })
  })

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

  /**
   * Forgets the sources answer, so the next ask is a real one. Done on every
   * switch-on: the answer depends on the cluster's metrics-query setting,
   * which can change in between — "not enabled", then enabled through the
   * very link this panel offers, must not be answered from memory.
   */
  function forgetSources() {
    sources = null
    sourcesFor = ''
    source = null
  }

  // The toolbar switches the layer; this follows it.
  let wasOn = untrack(() => on)
  $effect(() => {
    const next = on
    if (next === wasOn) return
    wasOn = next
    void untrack(() => toggle(next))
  })
  if (untrack(() => on)) void toggle(true)

  async function toggle(next: boolean) {
    if (!next) {
      token++
      busy = false
      error = null
      publish(null)
      return
    }
    forgetSources()
    if (await loadSources()) await load()
  }

  // The cluster's metrics-query setting changed while the layer is on (the
  // Settings pane saved it): ask again rather than show a stale verdict.
  const settingsKey = $derived.by(() => {
    const s = clusterSettings.for(clusterId)
    return `${s.metricsQueryMode}|${s.preferredNamespace}/${s.preferredService}|${s.fleetPolicy}`
  })
  let seenSettings = untrack(() => settingsKey)
  $effect(() => {
    const key = settingsKey
    if (key === seenSettings) return
    seenSettings = key
    if (!untrack(() => on)) return
    forgetSources()
    void loadSources().then((ok) => (ok ? load() : undefined))
  })

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
  class="flex flex-col gap-2 text-body-small text-on-surface-variant"
  aria-label="Observed traffic"
>
  <p role="status" class="text-on-surface">
    {said.headline}
    {#if view.kind === 'not-enabled'}
      <button
        type="button"
        class="underline hover:text-on-surface"
        onclick={() => settingsDialog.show('clusters')}
      >
        Turn it on in Settings → Clusters
      </button>
    {/if}
  </p>

  {#if on && sources && source}
    <div class="flex flex-wrap items-center gap-x-3 gap-y-1.5">
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
    </div>
  {/if}

  {#if on && view.kind === 'ready'}
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

    {#if layer && layer.expressions.length > 0}
      <details>
        <summary class="cursor-pointer text-on-surface-variant/70 hover:text-on-surface">
          What PodSteer asked
        </summary>
        <div class="mt-1 flex items-start gap-1">
          <code
            class="block max-h-40 flex-1 overflow-auto rounded-sm bg-surface-container px-2 py-1
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
</section>
