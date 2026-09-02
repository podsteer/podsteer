<!--
  One event, in the drawer.

  The first of the kind-specific detail panes, and the shape the rest should
  follow: a heading that says what this is, the payload in full, then the facts
  as a list of label-and-value rows in the same type scale the overview cards
  use.

  An event is unusual among Kubernetes objects in having nothing under spec or
  status — everything worth reading is at the top level, which is why the
  generic overview showed almost none of it. It is also the only kind that is
  entirely ABOUT something else, so the object it concerns is written as a path
  whose last part is a link: arriving at an event and not being able to reach
  the pod it describes is the dead end this pane exists to remove.

  THE SAME APPLIES INSIDE THE DETAILS LIST. An event names a node and a
  namespace, and both are somewhere to go — the node the kubelet that reported
  this is running on, and the namespace the whole application can be filtered
  to. Leaving them as text made them facts to copy and paste into a search box.
  The event's own name is not one of those: it names the record you are already
  reading, so it is labelled as such and left as text.
-->
<script lang="ts">
  import { Activity } from '@lucide/svelte'
  import { iconForKind } from '$lib/kindIcons'
  import DetailSection from './DetailSection.svelte'
  import DetailList, { type DetailRow } from './DetailList.svelte'
  import { follower, type OpenObject, type ServesKind } from '$lib/reference'

  interface InvolvedObject {
    kind: string
    name: string
    namespace: string
  }

  interface Props {
    /** The parsed event manifest. */
    event: Record<string, unknown> | null
    /**
     * Whether this cluster serves a kind, so a link is only offered when there
     * is somewhere for it to go.
     *
     * An event can name a kind PodSteer has no list for — a CRD removed since
     * the event fired, most obviously — and a node reported by a kubelet is
     * unreachable to an account that cannot list nodes.
     */
    canOpen?: ServesKind
    /** Follows a reference to the object it names. */
    onopen?: OpenObject
    /** Filters the application to a namespace, as the drawer header does. */
    onnamespace?: (namespace: string) => void
  }

  let { event, canOpen, onopen, onnamespace }: Props = $props()

  /** Turns a reference into a click handler, or into nothing. See $lib/reference. */
  const follow = $derived(follower(canOpen, onopen))

  const involved = $derived.by((): InvolvedObject | null => {
    const raw = event?.involvedObject as Record<string, string> | undefined
    if (!raw?.kind || !raw?.name) return null
    return { kind: raw.kind, name: raw.name, namespace: raw.namespace ?? '' }
  })

  const openInvolved = $derived(
    involved ? follow(involved.kind, involved.name, involved.namespace) : undefined,
  )

  const isWarning = $derived(event?.type === 'Warning')

  /**
   * The icon of the thing the event is about, not a generic alarm.
   *
   * The severity is already in the colour, so the shape is free to say what
   * kind of object this concerns — which is the first thing somebody wants
   * from an event, and the same icon the row they clicked was marked with.
   */
  const Icon = $derived(involved ? iconForKind({ kind: involved.kind }) : Activity)
  const message = $derived((event?.message as string) ?? '')
  const reason = $derived((event?.reason as string) ?? 'Event')

  /**
   * The event's own timestamps, in the order they are asked about.
   *
   * Both generations are read for the same reason the list does it: an event
   * from events.k8s.io carries eventTime and neither of the older fields, and
   * showing a blank there would suggest it never happened.
   */
  const rows = $derived.by((): DetailRow[] => {
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
    const namespace = metadata.namespace ?? ''

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
      // The machine the reporting component was running on. A kubelet event
      // carries it and a scheduler event does not, which is why the row can
      // drop out below rather than showing a blank.
      {
        label: 'Node',
        value: source.host || '—',
        onclick: follow('Node', source.host ?? ''),
      },
      {
        // Following a namespace filters the whole application to it, which is
        // the same thing clicking it in the drawer's header does — not an
        // object to open, but the most common next move from an event.
        label: 'Namespace',
        value: namespace || '—',
        onclick: namespace && onnamespace ? () => onnamespace(namespace) : undefined,
      },
      // The EVENT's name, not the object's — `nginx-7d8f.17a2b3c4d5e6f7`.
      // Labelled in full because "Name" beside an object's kind and name in
      // the header above reads as though it were followable, and it is not:
      // it identifies the record already open.
      { label: 'Event name', value: metadata.name || '—' },
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
  <!-- The sections are far enough apart to read as separate things. At the
       tighter spacing the rule under a heading sat almost on the text above
       it, so the pane read as one block with lines through it. -->
  <div class="flex flex-col gap-7 p-4">
    <!-- Marked the way the row that led here was marked: the object's own
         icon, in the severity's colour and weight. -->
    <div class="flex items-start gap-3">
      <Icon
        class="mt-0.5 size-5 shrink-0 {isWarning ? 'text-gauge-warn' : 'text-on-surface-variant/50'}"
        strokeWidth={isWarning ? 2.75 : 1.75}
      />
      <div class="min-w-0">
        <h3 class="text-title-medium font-semibold text-on-surface">{reason}</h3>

        <!-- A path rather than a sentence, and its last part is where it
             goes. "Pod · name" read as a caption; "Pod / name" reads as the
             address of something, which is what it is. -->
        {#if involved}
          <p class="flex min-w-0 items-baseline gap-1.5 text-body-medium text-on-surface-variant">
            <span class="shrink-0">{involved.kind}</span>
            <span class="shrink-0 text-on-surface-variant/40" aria-hidden="true">/</span>
            {#if openInvolved}
              <button
                type="button"
                onclick={openInvolved}
                class="resource-link min-w-0 truncate text-left"
                title="Open {involved.kind.toLowerCase()} {involved.name}"
              >
                {involved.name}
              </button>
            {:else}
              <span class="min-w-0 truncate">{involved.name}</span>
            {/if}
          </p>
        {/if}
      </div>
    </div>

    <!-- Named sections, because the pane holds two different things: the one
         sentence the cluster wrote, and the facts around it. Without headings
         the message read as an unlabelled quotation above an unlabelled
         list. -->
    {#if message}
      <DetailSection id="event-message" title="Message">
        <!-- In full, selectable, and unboxed. It is the one field somebody
             came to read, and the list could only ever show a truncated line
             of it — but a border around it made a sentence look like an
             exhibit, when the section's own rule already sets it apart. -->
        <p class="text-body-medium leading-relaxed text-on-surface" data-selectable>
          {message}
        </p>
      </DetailSection>
    {/if}

    <!-- The layout this pane established, now shared: see DetailList. -->
    <DetailSection id="event-details" title="Details">
      <DetailList {rows} />
    </DetailSection>
  </div>
{/if}
