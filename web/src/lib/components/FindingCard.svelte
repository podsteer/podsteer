<!--
  One finding: what is wrong, how widely, why it matters, and where to go next.

  The affected objects collapse behind a disclosure because a finding that
  names forty pods is not more useful than one that says "forty pods" — until
  the operator asks which. What stays visible is the sentence that decides
  whether to act.
-->
<script lang="ts">
  import type { Finding } from '$lib/api/client'
  import { formatAge } from '$lib/format'
  import { AlertOctagon, AlertTriangle, Info, ChevronRight, ArrowUpRight } from '@lucide/svelte'

  interface Props {
    finding: Finding
    /** Opens the list the finding came from. */
    onopen?: (kindId: string) => void
    /** Opens one affected object in the detail drawer. */
    onselect?: (kindId: string, name: string, namespace: string) => void
  }

  let { finding, onopen, onselect }: Props = $props()

  let expanded = $state(false)

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
  const hasSubjects = $derived(finding.subjects.length > 0)
</script>

<article
  class="overflow-hidden rounded-lg border border-outline-variant/40 border-l-[3px] bg-surface-container-low {style.accent}"
>
  <div class="flex items-start gap-3 p-3">
    <Icon class="mt-0.5 size-[18px] shrink-0 {style.iconClass}" strokeWidth={2} />

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
            class="state-layer flex items-center gap-1 rounded-md px-1.5 py-1 text-label-medium
                   text-on-surface-variant transition-colors duration-100
                   hover:bg-surface-container hover:text-on-surface"
          >
            <ChevronRight
              class="size-3.5 transition-transform duration-150 ease-standard {expanded ? 'rotate-90' : ''}"
              strokeWidth={2.5}
            />
            {expanded ? 'Hide' : 'Show'}
            {finding.count === 1 ? 'the object' : `all ${finding.count}`}
          </button>
        {/if}

        {#if finding.kindId && onopen}
          <button
            type="button"
            onclick={() => onopen?.(finding.kindId)}
            class="state-layer flex items-center gap-1 rounded-md px-1.5 py-1 text-label-medium
                   text-primary transition-colors duration-100 hover:bg-primary/10"
          >
            Open list
            <ArrowUpRight class="size-3.5" strokeWidth={2} />
          </button>
        {/if}
      </div>
    </div>
  </div>

  {#if expanded && hasSubjects}
    <ul class="border-t border-outline-variant/40 bg-surface-container/40">
      {#each finding.subjects as subject (subject.namespace + '/' + subject.name)}
        <li>
          <button
            type="button"
            onclick={() => onselect?.(finding.kindId, subject.name, subject.namespace)}
            disabled={!onselect}
            class="flex w-full items-start gap-3 px-4 py-1.5 text-left transition-colors duration-100
                   enabled:hover:bg-surface-container-high disabled:cursor-default"
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
        </li>
      {/each}

      <!-- Never let a capped list read as a complete one. -->
      {#if finding.truncated}
        <li class="px-4 py-1.5 text-body-small text-on-surface-variant/60">
          and {finding.count - finding.subjects.length} more
        </li>
      {/if}
    </ul>
  {/if}
</article>
