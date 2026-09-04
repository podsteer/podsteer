<!--
  What happened while this tab was open — the session timeline.

  One component behind both surfaces: the drawer's Timeline tab, scoped to
  the open object, and the cluster-wide view, scoped to the tab. They differ
  by which entries they are handed and by whether a row can be opened; the
  rendering, the grouping and the honest line about what this covers are the
  same thing in both places and are therefore written once.

  THE ONE LINE AT THE TOP IS NOT DECORATION. This is held in memory for the
  life of the tab and is written nowhere, so it covers the window the tab has
  been open and nothing before it — which has to be SAID rather than implied,
  the way SeriesResult.spanSeconds lets the sampled charts say "the last 40
  minutes" instead of suggesting a monitoring stack. See $stores/timeline for
  why it is in memory, and CLAUDE.md for what the durable version would be.
-->
<script lang="ts">
  import { Activity, AlertTriangle, CircleCheck, PencilLine, Clock } from '@lucide/svelte'

  import { formatAge, formatClockTime } from '$lib/format'
  import {
    groupTimeline,
    type TimelineEntry,
    type TimelineEntryKind,
    type TimelineTarget,
  } from '$lib/timeline'

  interface Props {
    /** The entries to render, newest first. */
    entries: TimelineEntry[]
    /** When this cluster's timeline started, or null before anything landed. */
    startedAt: number | null
    /** Whether a row names an object worth opening — false in the drawer,
        where every row is already about the object on screen. */
    showTarget?: boolean
    /** Opens the object a row is about. Absent means rows are not clickable. */
    onopen?: (target: TimelineTarget) => void
  }

  let { entries, startedAt, showTarget = false, onopen }: Props = $props()

  const FILTERS: { id: TimelineEntryKind; label: string }[] = [
    { id: 'event', label: 'Events' },
    { id: 'finding', label: 'Findings' },
    { id: 'write', label: 'Writes' },
  ]

  /**
   * Which kinds of entry are showing. Empty means all of them.
   *
   * Empty-is-everything rather than starting with all three selected, which
   * is the same rule the pod status chips follow: pressing one chip then
   * reads as "only this", and pressing it again returns to the whole list.
   */
  let active = $state<TimelineEntryKind[]>([])

  function toggle(id: TimelineEntryKind): void {
    active = active.includes(id) ? active.filter((held) => held !== id) : [...active, id]
  }

  const shown = $derived(
    active.length === 0 ? entries : entries.filter((entry) => active.includes(entry.kind)),
  )

  /** How many entries each filter would show, counted before filtering so an
      unselected chip reports what selecting it would give rather than what is
      left after its own selection. */
  const counts = $derived.by(() => {
    const totals: Record<string, number> = { event: 0, finding: 0, write: 0 }
    for (const entry of entries) totals[entry.kind]++
    return totals
  })

  const groups = $derived(groupTimeline(shown))

  /** How long this timeline covers, in words. */
  const span = $derived(
    startedAt === null ? '' : formatAge(Math.max(0, (Date.now() - startedAt) / 1000)),
  )

  function icon(entry: TimelineEntry) {
    if (entry.kind === 'write') return PencilLine
    if (entry.kind === 'finding') return entry.state === 'cleared' ? CircleCheck : AlertTriangle
    return entry.severity === 'info' ? Activity : AlertTriangle
  }

  /** The tone a row reads in. A cleared finding is good news whatever the
      severity of the thing that cleared, so it is never drawn as a warning. */
  function tone(entry: TimelineEntry): string {
    if (entry.kind === 'finding' && entry.state === 'cleared') return 'text-success'
    if (entry.severity === 'critical') return 'text-error'
    if (entry.severity === 'warning') return 'text-warning'
    return 'text-on-surface-variant/60'
  }

  /** The verb a row leads with, so the three kinds read differently at a
      glance without needing their icon decoded. */
  function lead(entry: TimelineEntry): string {
    if (entry.kind === 'finding') return entry.state === 'cleared' ? 'Cleared' : 'Raised'
    if (entry.kind === 'write' && entry.outcome === 'failed') return 'Refused'
    return ''
  }
</script>

<div class="flex h-full flex-col">
  <div
    class="flex flex-wrap items-center gap-1.5 border-b border-outline-variant/40
           bg-surface-container-low/40 px-4 py-2"
  >
    {#each FILTERS as filter (filter.id)}
      {@const pressed = active.includes(filter.id)}
      <button
        type="button"
        onclick={() => toggle(filter.id)}
        aria-pressed={pressed}
        class="rounded-full border px-2.5 py-1 text-label-small transition-colors duration-100
               {pressed
                 ? 'border-primary/40 bg-primary/14 text-primary'
                 : 'border-outline-variant/50 text-on-surface-variant hover:bg-surface-container hover:text-on-surface'}"
      >
        {filter.label}
        <span class="ml-1 tabular-nums opacity-70">{counts[filter.id]}</span>
      </button>
    {/each}
  </div>

  {#if groups.length === 0}
    <div class="flex flex-1 flex-col items-center justify-center gap-2 p-6 text-center">
      <Clock class="size-8 text-on-surface-variant/40" strokeWidth={1.2} />
      <p class="text-body-medium text-on-surface-variant">Nothing recorded yet</p>
      <p class="max-w-xs text-body-small text-on-surface-variant/70">
        The timeline starts when the tab opens and fills as events arrive, findings appear and
        clear, and PodSteer writes to the cluster.
      </p>
    </div>
  {:else}
    <ul class="flex-1 divide-y divide-outline-variant/40 overflow-auto">
      {#each groups as group (group.head.id)}
        {@const entry = group.head}
        {@const Icon = icon(entry)}
        {@const openable = Boolean(onopen) && entry.target.name !== ''}
        <li>
          <!-- A real button when there is something to open and a plain div
               when there is not, rather than one element wearing a role: a
               row about a cluster-wide finding names no object, and a button
               that does nothing is worse to reach by keyboard than a
               paragraph. -->
          {#snippet row()}
            <Icon class="mt-0.5 size-4 shrink-0 {tone(entry)}" strokeWidth={1.8} />
            <div class="min-w-0 flex-1">
              <p class="flex flex-wrap items-baseline gap-x-2 text-body-medium text-on-surface">
                {#if lead(entry)}
                  <span class="text-label-small uppercase tracking-wide {tone(entry)}">
                    {lead(entry)}
                  </span>
                {/if}
                <span class="truncate font-medium">{entry.title}</span>
                {#if group.count > 1}
                  <!-- The count and the span together, which is what makes a
                       collapsed row honest: forty identical BackOff events
                       over twelve minutes is one line that still says forty. -->
                  <span
                    class="rounded-full bg-surface-container-high px-1.5 py-0.5 text-label-small
                           tabular-nums text-on-surface-variant/70"
                    title="{group.count} occurrences over {formatAge(
                      Math.max(1, (group.lastAt - group.firstAt) / 1000),
                    )}"
                  >
                    ×{group.count}
                  </span>
                {/if}
              </p>
              {#if entry.detail}
                <p class="mt-0.5 text-body-small text-on-surface-variant">{entry.detail}</p>
              {/if}
              <p class="mt-1 flex flex-wrap gap-x-2 text-label-small text-on-surface-variant/70">
                <span>{formatClockTime(new Date(entry.lastAt))}</span>
                {#if showTarget && entry.target.name}
                  <span class="truncate">
                    {entry.target.kind}
                    {entry.target.namespace ? `${entry.target.namespace}/` : ''}{entry.target.name}
                  </span>
                {/if}
              </p>
            </div>
          {/snippet}

          {#if openable}
            <button
              type="button"
              onclick={() => onopen?.(entry.target)}
              class="flex w-full gap-3 px-4 py-3 text-left hover:bg-surface-container"
            >
              {@render row()}
            </button>
          {:else}
            <div class="flex w-full gap-3 px-4 py-3">{@render row()}</div>
          {/if}
        </li>
      {/each}
    </ul>
  {/if}

  <p
    class="shrink-0 border-t border-outline-variant/40 bg-surface-container-low/40 px-4 py-2
           text-label-small text-on-surface-variant/70"
  >
    This session only{span ? `, the last ${span}` : ''}. The timeline is held in memory, is never
    written to disk, and goes when the tab closes.
  </p>
</div>
