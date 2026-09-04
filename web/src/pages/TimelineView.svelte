<!--
  What changed in this cluster while you were watching.

  A PSEUDO-ENTRY, NOT A KIND, and for the same reason the overview, the
  Applications view and All clusters are: there is no object to GET called a
  timeline. It is a record this tab kept of what it saw, so a catalogue entry
  would offer it to every consumer that expects to fetch what it names.

  It costs nothing to show. Every entry was recorded from something that had
  already crossed the bridge — the assessment each refresh fetches whatever
  view is open, the findings on each row of the pod list, the event lists, and
  the outcome of each write PodSteer made — so this view issues no request of
  its own and there is no timer here.
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
  onopen={(target) => void open(target)}
/>
