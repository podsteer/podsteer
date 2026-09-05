<!--
  One finding: what is wrong, how widely, why it matters, and where to go next.

  The affected objects collapse behind a disclosure because a finding that
  names forty pods is not more useful than one that says "forty pods" — until
  the operator asks which. What stays visible is the sentence that decides
  whether to act.

  Each of those objects can be snoozed. Not every true finding is actionable
  today — the one pod nobody may restart before the freeze ends — and an alarm
  that cannot be acknowledged is one people learn to read past, taking the real
  ones with it. Snoozing is per object rather than per finding because that is
  the granularity the excuse has: twelve pods crash-looping for the same reason
  are rarely twelve things anybody may defer together.

  A snooze is time-boxed and reversible, and what it covers stays listed while
  it lasts, so quietening something is never the same as hiding it.
-->
<script lang="ts">
  import type { Finding } from '$lib/api/client'
  import { formatAge } from '$lib/format'
  import Select from './Select.svelte'
  import {
    AlertOctagon,
    AlertTriangle,
    Info,
    ChevronRight,
    ArrowUpRight,
    BellOff,
  } from '@lucide/svelte'

  interface Props {
    finding: Finding
    /**
     * When one object's snooze lapses, in epoch milliseconds, or 0 when it is
     * not snoozed. A function rather than a map so the card never has to know
     * how snoozes are keyed or stored.
     */
    snoozedUntil?: (namespace: string, name: string) => number
    /** Opens the list the finding came from. */
    onopen?: (kindId: string) => void
    /** Opens one affected object in the detail drawer. */
    onselect?: (kindId: string, name: string, namespace: string) => void
    /** Quietens one object for the chosen number of milliseconds. */
    onsnooze?: (namespace: string, name: string, durationMs: number) => void
    /** Brings one object back before its snooze lapses. */
    onunsnooze?: (namespace: string, name: string) => void
  }

  let {
    finding,
    snoozedUntil = () => 0,
    onopen,
    onselect,
    onsnooze,
    onunsnooze,
  }: Props = $props()

  /**
   * Opened by default once everything in it is snoozed.
   *
   * A finding that is quiet only because each of its objects was deferred is
   * one whose rows are the entire content — collapsed, it is a dimmed card
   * asserting something the operator cannot see the reason for, or undo.
   */
  let expanded = $state(false)

  const HOUR = 3_600_000
  const DAY = 24 * HOUR

  /**
   * Deliberately no "forever".
   *
   * Something permanently dismissed is something nobody will ever look at
   * again, including the person who dismissed it — and the reasons given for
   * wanting this (a freeze, a maintenance window, a quarter) all end.
   */
  const SNOOZE_OPTIONS = [
    { value: String(HOUR), label: '1 hour' },
    { value: String(DAY), label: '1 day' },
    { value: String(7 * DAY), label: '7 days' },
    { value: String(15 * DAY), label: '15 days' },
    { value: String(30 * DAY), label: '30 days' },
  ]

  /** Affected objects — absent, like an empty list, means none are named. */
  const subjects = $derived(finding.subjects ?? [])

  /** How many listed objects are quiet, which decides how the card reads. */
  const snoozedCount = $derived(
    subjects.filter((subject) => snoozedUntil(subject.namespace, subject.name) > 0).length,
  )
  const allSnoozed = $derived(
    subjects.length > 0 && !finding.truncated && snoozedCount === subjects.length,
  )

  const SEVERITY = {
    critical: {
      icon: AlertOctagon,
      accent: 'border-l-error',
      iconClass: 'text-error',
      chip: 'bg-error-container text-on-error-container',
    },
    warning: {
      icon: AlertTriangle,
      accent: 'border-l-warning',
      iconClass: 'text-warning',
      chip: 'bg-warning-container text-on-warning-container',
    },
    info: {
      icon: Info,
      accent: 'border-l-outline-variant',
      iconClass: 'text-on-surface-variant/70',
      chip: 'bg-surface-container-high text-on-surface-variant',
    },
  } as const

  const style = $derived(SEVERITY[finding.severity as keyof typeof SEVERITY] ?? SEVERITY.info)
  const Icon = $derived(style.icon)
  const hasSubjects = $derived(subjects.length > 0)

  /**
   * What the disclosure promises to show, which is rows and not events.
   *
   * `count` is the extent — three warning events, forty affected pods — and
   * saying "Show all 3" above a list of two is the sort of small lie that
   * makes an operator distrust the rest of the panel. Only a capped list
   * genuinely holds back more than it lists, and that one still counts up to
   * the total because the remainder is stated at the bottom.
   */
  const disclosure = $derived.by(() => {
    const listed = finding.truncated ? finding.count : subjects.length
    return listed === 1 ? 'the object' : `all ${listed}`
  })

  /** How much of a snooze is left, in the same words as everything else. */
  function remaining(until: number): string {
    return formatAge(Math.max(1, Math.round((until - Date.now()) / 1000)))
  }

  $effect(() => {
    if (allSnoozed) expanded = true
  })
</script>

<article
  class="overflow-hidden rounded-sm border border-outline-variant/40 border-l-[3px] bg-surface-container-low
         transition-opacity duration-150 {allSnoozed ? 'border-l-outline-variant opacity-60' : style.accent}"
>
  <div class="flex items-start gap-3 p-3">
    {#if allSnoozed}
      <BellOff class="mt-0.5 size-[18px] shrink-0 text-on-surface-variant/70" strokeWidth={2} />
    {:else}
      <Icon class="mt-0.5 size-[18px] shrink-0 {style.iconClass}" strokeWidth={2} />
    {/if}

    <div class="min-w-0 flex-1">
      <div class="flex flex-wrap items-baseline gap-x-2 gap-y-1">
        <h3 class="text-title-small font-semibold text-on-surface">{finding.title}</h3>
        <span class="rounded-full px-1.5 py-0.5 text-label-small {style.chip}">
          {finding.category}
        </span>
        {#if finding.oldestSeconds > 0}
          <span
            class="text-body-small tabular-nums text-on-surface-variant/60"
            title="Longest-standing affected object"
          >
            {formatAge(finding.oldestSeconds)}
          </span>
        {/if}
      </div>

      <p class="mt-0.5 text-body-medium text-on-surface-variant">{finding.summary}</p>

      {#if finding.advice}
        <p class="mt-1.5 text-body-small leading-relaxed text-on-surface-variant/75">
          {finding.advice}
        </p>
      {/if}

      <div class="mt-2 flex flex-wrap items-center gap-2">
        {#if hasSubjects}
          <button
            type="button"
            onclick={() => (expanded = !expanded)}
            aria-expanded={expanded}
            class="state-layer flex items-center gap-1 rounded-sm px-1.5 py-1 text-label-medium
                   text-on-surface-variant transition-colors duration-100
                   hover:bg-surface-container hover:text-on-surface"
          >
            <ChevronRight
              class="size-3.5 transition-transform duration-150 ease-standard {expanded ? 'rotate-90' : ''}"
              strokeWidth={2.5}
            />
            {expanded ? 'Hide' : 'Show'}
            {disclosure}
          </button>
        {/if}

        {#if finding.kindId && onopen}
          <button
            type="button"
            onclick={() => onopen?.(finding.kindId)}
            class="state-layer flex items-center gap-1 rounded-sm px-1.5 py-1 text-label-medium
                   text-primary transition-colors duration-100 hover:bg-primary/10"
          >
            Open list
            <ArrowUpRight class="size-3.5" strokeWidth={2} />
          </button>
        {/if}

        <!-- Said on the card, because the disclosure below it is where the
             snoozing happens and this is the only trace of it while that is
             closed. -->
        {#if snoozedCount > 0}
          <span
            class="ml-auto flex items-center gap-1.5 text-label-medium text-on-surface-variant/70"
          >
            <BellOff class="size-3.5" strokeWidth={2} />
            {allSnoozed ? 'All snoozed' : `${snoozedCount} of ${subjects.length} snoozed`}
          </span>
        {/if}
      </div>
    </div>
  </div>

  {#if expanded && hasSubjects}
    <ul
      class="divide-y divide-outline-variant/20 border-t border-outline-variant/40
             bg-surface-container/40"
    >
      <!-- Keyed by position as well as identity. Two rows CAN name the same
           object — a pod that logged two different warnings is two lines —
           and a duplicate key aborts the whole block, which is why this list
           used to render as nothing at all. -->
      {#each subjects as subject, index (index + ':' + subject.namespace + '/' + subject.name)}
        {@const until = snoozedUntil(subject.namespace, subject.name)}
        <li class="flex items-center gap-2 pr-3">
          <!-- The object and its snooze are siblings rather than nested: a
               control inside the row's own button could not be clicked
               without also opening the object.

               Only this half dims while the row is snoozed. Fading the whole
               row took the way out down with it — a control that looks
               disabled is one nobody presses. -->
          <button
            type="button"
            onclick={() => onselect?.(finding.kindId, subject.name, subject.namespace)}
            disabled={!onselect}
            class="flex min-w-0 flex-1 items-start gap-3 px-4 py-2.5 text-left transition-colors
                   duration-100 enabled:cursor-pointer enabled:hover:bg-surface-container-high
                   disabled:cursor-default {until > 0 ? 'opacity-55' : ''}"
          >
            <span class="min-w-0 flex-1 truncate text-body-small text-on-surface" title={subject.name}>
              {#if subject.namespace}<span class="text-on-surface-variant/60">{subject.namespace}/</span
                >{/if}{subject.name}
            </span>
            {#if subject.detail}
              <span
                class="max-w-[55%] shrink-0 truncate text-body-small text-on-surface-variant/70"
                title={subject.detail}
              >
                {subject.detail}
              </span>
            {/if}
          </button>

          {#if until > 0}
            <span class="shrink-0 text-label-medium tabular-nums text-on-surface-variant">
              {remaining(until)} left
            </span>
            <button
              type="button"
              aria-label="Un-snooze {subject.name}"
              onclick={() => onunsnooze?.(subject.namespace, subject.name)}
              class="state-layer shrink-0 rounded-sm px-1.5 py-1 text-label-medium text-primary
                     transition-colors duration-100 hover:bg-primary/10"
            >
              Un-snooze
            </button>
          {:else if onsnooze}
            <!-- The heading names the action, not the object: a pod's
                 generated name is sixty characters the panel would have to
                 stretch to, over a list of five short durations. The full
                 name stays in the tooltip and the accessible name. -->
            <Select
              label="Snooze for"
              accessibleName="Snooze {subject.name} for"
              placeholder="Snooze"
              value=""
              options={SNOOZE_OPTIONS}
              compact
              onchange={(value) => onsnooze?.(subject.namespace, subject.name, Number(value))}
            />
          {/if}
        </li>
      {/each}

      <!-- Never let a capped list read as a complete one. -->
      {#if finding.truncated}
        <li class="px-4 py-2.5 text-body-small text-on-surface-variant/60">
          and {finding.count - subjects.length} more
        </li>
      {/if}
    </ul>
  {/if}
</article>
