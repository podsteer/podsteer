<!--
  What changed in this cluster while you were watching.

  A PSEUDO-ENTRY, NOT A KIND, and for the same reason the overview, the
  Applications view and All clusters are: there is no object to GET called a
  timeline. It is a record this tab kept of what it saw, so a catalogue entry
  would offer it to every consumer that expects to fetch what it names.

  ALL THREE OF ITS SOURCES COST NOTHING, and it fetches nothing itself. The
  assessment each refresh fetches whatever view is open carries the findings
  AND the events — the Go side gathers events on every assessment regardless,
  because the event findings are derived from them — and a write's outcome is
  recorded as it is made.

  That was not always true of events, and the exception was a bug rather than
  a saving. They were recorded only from a view that had fetched them, which
  meant this page opened empty and, worse, that the navigator's count stayed
  at nothing until somebody opened one of those pages: a tab recording nothing
  looked identical to one with plenty to show. Giving this page its own event
  fetch fixed the page and left the count exactly as wrong. Carrying the
  events the assessment already had fixes both. See ClusterSession's timeline
  case and #adopt.
-->
<script lang="ts">
  import type { ClusterSession } from '$stores/session.svelte'
  import { timeline } from '$stores/timeline.svelte'
  import TimelinePanel from '$lib/components/TimelinePanel.svelte'
  import type { TimelineTarget } from '$lib/timeline'

  interface Props {
    session: ClusterSession
  }

  let { session }: Props = $props()

  const entries = $derived(timeline.forCluster(session.cluster.id))

  /**
   * Opens the object a row is about.
   *
   * Resolved against the navigator's catalogue by KIND, exactly as a
   * reference followed from a detail pane is — a kind this cluster does not
   * serve, or an account that may not list it, simply does not open, rather
   * than navigating to a list that cannot exist. A row naming no object (a
   * finding about the cluster's capacity, say) carries an empty name and is
   * not clickable at all.
   */
  async function open(target: TimelineTarget): Promise<void> {
    const kind = session.kinds.find((entry) => entry.kind === target.kind)
    if (!kind) return
    await session.openObject(kind.id, target.name, target.namespace, kind.namespaced)
  }
</script>

<TimelinePanel
  {entries}
  startedAt={timeline.startedAt(session.cluster.id)}
  eventsRefused={timeline.eventsRefused(session.cluster.id)}
  showTarget
  paged
  onopen={(target) => void open(target)}
/>
