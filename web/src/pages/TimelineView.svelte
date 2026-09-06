<!--
  What changed in this cluster while you were watching.

  A PSEUDO-ENTRY, NOT A KIND, and for the same reason the overview, the
  Applications view and All clusters are: there is no object to GET called a
  timeline. It is a record this tab kept of what it saw, so a catalogue entry
  would offer it to every consumer that expects to fetch what it names.

  Two of its three sources cost nothing: the assessment each refresh fetches
  whatever view is open carries the findings, and a write's outcome is
  recorded as it is made. The third does not, and pretending otherwise is what
  this page used to do — EVENTS were recorded only while the Events page was
  open, so opening the Timeline first showed nothing, opening Events and
  coming back filled it, and the record a cluster produced depended on which
  pages somebody had visited rather than on what happened in it.

  So this view fetches events on the tab's tick while it is on screen, which
  is the same call, the same namespace and the same cost as the Events page
  while THAT is open, and stops when either is left. See ClusterSession's
  timeline case for the whole argument.
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
  showTarget
  paged
  onopen={(target) => void open(target)}
/>
