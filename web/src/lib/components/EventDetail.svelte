<!--
  One event, in the drawer.

  The first of the kind-specific detail panes, and the shape the rest should
  follow: a heading that says what this is, the payload in full, then the facts
  as a list of label-and-value rows in the same type scale the overview cards
  use.

  An event is unusual among Kubernetes objects in having nothing under spec or
  status — everything worth reading is at the top level, which is why the
  generic overview showed almost none of it. It is also the only kind that is
  entirely ABOUT something else, so the object it concerns is a link rather
  than a string: arriving at an event and not being able to reach the pod it
  describes is the dead end this pane exists to remove.
-->
<script lang="ts">
  import { AlertTriangle, Activity, ArrowUpRight } from '@lucide/svelte'

  interface InvolvedObject {
    kind: string
    name: string
    namespace: string
  }

  interface Props {
    /** The parsed event manifest. */
    event: Record<string, unknown> | null
    /** Opens the object the event is about, when that kind is reachable. */
    onopen?: (target: InvolvedObject) => void
    /** False when nothing in the cluster serves the involved kind. */
    canOpen?: boolean
  }

  let { event, onopen, canOpen = false }: Props = $props()

  const involved = $derived.by((): InvolvedObject | null => {
    const raw = event?.involvedObject as Record<string, string> | undefined
    if (!raw?.kind || !raw?.name) return null
    return { kind: raw.kind, name: raw.name, namespace: raw.namespace ?? '' }
  })

  const isWarning = $derived(event?.type === 'Warning')
  const message = $derived((event?.message as string) ?? '')
  const reason = $derived((event?.reason as string) ?? 'Event')

  /**
   * The event's own timestamps, in the order they are asked about.
   *
   * Both generations are read for the same reason the list does it: an event
   * from events.k8s.io carries eventTime and neither of the older fields, and
   * showing a blank there would suggest it never happened.
   */
  const rows = $derived.by(() => {
    const metadata = (event?.metadata ?? {}) as Record<string, string>
    const series = (event?.series ?? {}) as Record<string, unknown>
    const source = (event?.source ?? {}) as Record<string, string>

    const first = (event?.firstTimestamp as string) ?? (event?.eventTime as string) ?? ''
    const last =
      (series?.lastObservedTime as string) ??
      (event?.lastTimestamp as string) ??
      (event?.eventTime as string) ??
      ''
    const count = (series?.count as number) ?? (event?.count as number) ?? 1

    return [
      { label: 'Type', value: (event?.type as string) ?? '—' },
      { label: 'Reason', value: reason },
      { label: 'Count', value: String(count || 1) },
      { label: 'First seen', value: formatStamp(first) },
      { label: 'Last seen', value: formatStamp(last) },
      {
        label: 'Reported by',
        value: source.component || (event?.reportingComponent as string) || '—',
      },
      { label: 'Node', value: source.host || '—' },
      { label: 'Namespace', value: metadata.namespace || '—' },
      { label: 'Name', value: metadata.name || '—' },
    ].filter((row) => row.value !== '—' || row.label === 'Type')
  })

  /** Absolute time, since an event's whole value is when it happened. */
  function formatStamp(value: string): string {
    if (!value) return '—'
    const at = new Date(value)
    if (Number.isNaN(at.getTime())) return value
    return at.toLocaleString()
  }
</script>

{#if event}
  <div class="flex flex-col gap-5 p-4">
    <!-- What this is, marked the way the row that led here was marked. -->
    <div class="flex items-start gap-3">
      {#if isWarning}
        <AlertTriangle class="mt-0.5 size-5 shrink-0 text-gauge-warn" strokeWidth={2.75} />
      {:else}
        <Activity class="mt-0.5 size-5 shrink-0 text-on-surface-variant/50" strokeWidth={1.75} />
      {/if}
      <div class="min-w-0">
        <h3 class="text-title-medium font-semibold text-on-surface">{reason}</h3>
        {#if involved}
          <p class="text-body-small text-on-surface-variant">
            {involved.kind} · {involved.name}
          </p>
        {/if}
      </div>
    </div>

    <!-- The message in full and selectable. It is the one field somebody came
         to read, and the list could only ever show a truncated line of it. -->
    {#if message}
      <p
        class="rounded-sm border border-outline-variant/40 bg-surface-container px-3 py-2
               text-body-medium leading-relaxed text-on-surface"
        data-selectable
      >
        {message}
      </p>
    {/if}

    <!-- The object this is about, as somewhere to go. -->
    {#if involved && canOpen && onopen}
      <button
        type="button"
        onclick={() => onopen?.(involved)}
        class="resource-link flex items-center gap-2 self-start text-body-medium"
      >
        Open {involved.kind.toLowerCase()}
        <ArrowUpRight class="size-4 shrink-0" strokeWidth={2} />
      </button>
    {/if}

    <dl class="grid grid-cols-[auto_1fr] items-baseline gap-x-6 gap-y-2">
      {#each rows as row (row.label)}
        <dt class="text-body-medium text-on-surface">{row.label}</dt>
        <dd class="min-w-0 text-right text-body-medium break-words text-on-surface-variant">
          {row.value}
        </dd>
      {/each}
    </dl>
  </div>
{/if}
