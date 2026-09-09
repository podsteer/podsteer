<!--
  The status bar.

  A desktop app's bottom edge is where ambient facts belong — the ones an
  operator wants available but never wants to go looking for. On the left
  that is the connected cluster, what the current view is showing, and how
  fresh it is; on the right, where to find the project outside the app.
-->
<script lang="ts">
  import { appInfo, openWebsite } from '$stores/system.svelte'
  import { workspace } from '$stores/workspace.svelte'
  import { organisation } from '$stores/organisation.svelte'
  import { preferences } from '$stores/preferences.svelte'
  import { shortcutSheet } from '$stores/shortcutSheet.svelte'
  import { forwards } from '$stores/forwards.svelte'
  import { nodeShells } from '$stores/nodeShells.svelte'
  import { clusterShells } from '$stores/clusterShells.svelte'
  import { formatAge, formatClockTime } from '$lib/format'
  import { iconForKind } from '$lib/kindIcons'
  import { shortcut } from '$stores/shortcuts.svelte'
  import { openURL } from '$lib/api/client'
  import { ExternalLink, Clock, Server, RefreshCw, Keyboard, Lock } from '@lucide/svelte'
  import ShareMenu from './ShareMenu.svelte'
  import PortForwardsPanel from './PortForwardsPanel.svelte'
  import NodeShellsPanel from './NodeShellsPanel.svelte'
  import ClusterShellsPanel from './ClusterShellsPanel.svelte'
  import GithubIcon from './icons/GithubIcon.svelte'
  import LinkedinIcon from './icons/LinkedinIcon.svelte'
  import BlueskyIcon from './icons/BlueskyIcon.svelte'

  const GITHUB_URL = 'https://github.com/podsteer'
  const LINKEDIN_URL = 'https://linkedin.com/company/podsteer'
  const BLUESKY_URL = 'https://bsky.app/profile/podsteer.com'

  const session = $derived(workspace.active)

  /** The active cluster's guardrail settings, or the unmarked default. */
  const groupSettings = $derived.by(() => {
    if (!session) return null
    const placement = organisation.placementOf(session.cluster.id)
    return organisation.settingsFor(placement.project, placement.group)
  })

  const refreshLabel = $derived(
    preferences.effectiveIntervalMs === 0
      ? 'manual'
      : `${Math.round(preferences.effectiveIntervalMs / 1000)}s`,
  )

  /** Icon for the selected kind itself — the same one the sidebar shows next
      to that kind's row (Pod, Deployment, …) — so the count reads as "this
      many of what's actually open", not just a bare number. */
  const KindIcon = $derived(iconForKind(session?.selectedKind ?? { kind: '' }))

  function openSocial(url: string): void {
    void openURL(url)
  }

  /**
   * A clock that runs only while a cluster has stopped answering.
   *
   * The silence needs a duration to be useful — "not answering" alone leaves
   * an operator wondering whether it happened a second ago or ten minutes
   * ago, which is the difference between waiting and going to look at the
   * VPN. Nothing ticks while the cluster is fine, so the common case pays
   * nothing for it.
   */
  let now = $state(Date.now())
  $effect(() => {
    if (session?.unreachableSince == null) return
    const clock = setInterval(() => (now = Date.now()), 1000)
    return () => clearInterval(clock)
  })

  /**
   * What the last read actually did, not what the first one once did.
   *
   * `cluster.isReachable` means "a round trip completed once and told us the
   * server version"; nothing unsets it. A laptop that changes VPN kept the
   * green dot and the word "reachable" here for as long as the tab was open,
   * because a watched kind is served from the in-memory store and never
   * touches the network. The session records every tick's outcome — see
   * ClusterSession.unreachableSince — so this can report the present tense.
   */
  const health = $derived.by(() => {
    if (!session) return null
    if (!session.cluster.isReachable) return { ok: false, word: 'not reachable', silence: '' }
    if (session.answering) return { ok: true, word: 'reachable', silence: '' }
    const since = session.unreachableSince ?? now
    return {
      ok: false,
      word: 'not answering',
      silence: formatAge(Math.max(0, now - since) / 1000),
    }
  })
</script>

{#snippet sep()}
  <span class="text-on-surface-variant/30" aria-hidden="true">|</span>
{/snippet}

<footer
  class="flex h-8 shrink-0 items-center gap-3 border-t border-outline-variant/40
         bg-surface-container-lowest px-3 text-body-medium text-on-surface-variant/80"
>
  {#if session}
    <!-- Cluster connection -->
    <!-- The dot is the glance; the word is the fact. Colour alone told a
         red/green colour-blind or screen-reader operator nothing about
         whether this cluster is answering. -->
    <span class="flex items-center gap-1.5">
      <span
        class="size-1.5 rounded-full {health?.ok ? 'bg-success' : 'bg-error'}"
        aria-hidden="true"
      ></span>
      <span class="truncate font-medium">{session.cluster.id}</span>
      {#if health && !health.ok}
        <!-- Said out loud, not only in colour, and only when it is news: a
             cluster nobody can reach is the one fact on this bar worth
             interrupting somebody with. -->
        <span class="shrink-0 font-medium text-error">
          {health.word}{health.silence ? ` for ${health.silence}` : ''}
        </span>
      {:else}
        <span class="sr-only">{health?.word ?? ''}</span>
      {/if}

      <!-- The environment word, said plainly rather than only in colour —
           the status bar is read at a glance while acting on a cluster, and
           that is exactly the moment "which one is this" matters most. -->
      {#if groupSettings?.environment}
        <span
          class="shrink-0 font-medium {groupSettings.environment === 'production'
            ? 'text-error'
            : 'opacity-70'}"
        >
          {groupSettings.environment}
        </span>
      {/if}

      {#if groupSettings?.readOnly}
        <span
          class="flex items-center"
          title="This cluster is marked read-only in PodSteer. Change that under Organise."
        >
          <Lock class="size-3" strokeWidth={2} aria-hidden="true" />
          <span class="sr-only">read-only</span>
        </span>
      {/if}
    </span>

    {#if session.cluster.version}
      {@render sep()}
      <span class="flex items-center gap-1 tabular-nums opacity-70">
        <Server class="size-3" strokeWidth={2} />
        {session.cluster.version}
      </span>
    {/if}

    <!-- Only on a list. The dashboard has no rows, and "0 items" beside a
         screen full of findings reads as a fault rather than as a count of
         something that was never being counted. -->
    {#if session.isList}
      {@render sep()}
      <span class="flex items-center gap-1.5 tabular-nums opacity-70">
        <KindIcon class="size-3" strokeWidth={2} />
        {session.visibleCount}
        {session.selectedKind?.title.toLowerCase() ?? 'items'}
      </span>
    {/if}

    {@render sep()}
    <span class="flex items-center gap-1 tabular-nums opacity-70">
      <Clock class="size-3" strokeWidth={2} />
      {formatClockTime(session.lastRefreshedAt)}
    </span>

    {@render sep()}
    <span class="flex items-center gap-1 opacity-60">
      <RefreshCw class="size-3" strokeWidth={2} />
      {refreshLabel}
    </span>
  {:else}
    <span class="opacity-60">No cluster open</span>
  {/if}

  <!-- Global: a forward is not scoped to the active tab, so this shows
       whether or not a session is even selected right now. -->
  {#if forwards.active.length > 0}
    {@render sep()}
    <PortForwardsPanel />
  {/if}

  <!-- Node shells, beside forwards and for the same reason: a running node
       shell is a privileged pod, and it must be visible and stoppable from
       here no matter which tab opened it. -->
  {#if nodeShells.active.length > 0}
    {@render sep()}
    <NodeShellsPanel />
  {/if}

  <!-- In-cluster shells, beside the node shells. Nothing here is privileged,
       and the reason to show it is the same one: it is a pod PodSteer created
       in somebody's namespace, and it must be visible and stoppable from here
       whichever tab opened it. -->
  {#if clusterShells.active.length > 0}
    {@render sep()}
    <ClusterShellsPanel />
  {/if}

  <div class="ml-auto flex items-center gap-3">
    <!-- The lightest existing place for this: one icon, no dialog to open
         first to find it. The keyboard shortcuts it lists are read from the
         same table ⌘B, ⌘R and the rest are matched against — see
         $lib/shortcuts — so this list and what the keys actually do cannot
         drift apart. -->
    <button
      type="button"
      onclick={shortcutSheet.show}
      aria-label="Keyboard shortcuts"
      title="Keyboard shortcuts  {shortcut('shortcut-sheet').keys}"
      class="state-layer flex cursor-pointer items-center rounded-xs opacity-70 transition-opacity duration-100 hover:opacity-100"
    >
      <Keyboard class="size-3.5" strokeWidth={2} />
    </button>

    {@render sep()}

    <!-- Share PodSteer: distinct from the follow-us icons after it — this
         shares the app itself, not PodSteer's own accounts. -->
    <ShareMenu />

    {@render sep()}

    <!-- Social links -->
    <div class="flex items-center gap-2.5">
      <button
        type="button"
        onclick={() => openSocial(GITHUB_URL)}
        aria-label="PodSteer on GitHub"
        title="GitHub"
        class="state-layer flex cursor-pointer items-center rounded-xs opacity-70 transition-opacity duration-100 hover:opacity-100"
      >
        <GithubIcon class="size-3.5" />
      </button>
      <button
        type="button"
        onclick={() => openSocial(LINKEDIN_URL)}
        aria-label="PodSteer on LinkedIn"
        title="LinkedIn"
        class="state-layer flex cursor-pointer items-center rounded-xs opacity-70 transition-opacity duration-100 hover:opacity-100"
      >
        <LinkedinIcon class="size-3.5" />
      </button>
      <button
        type="button"
        onclick={() => openSocial(BLUESKY_URL)}
        aria-label="PodSteer on Bluesky"
        title="Bluesky"
        class="state-layer flex cursor-pointer items-center rounded-xs opacity-70 transition-opacity duration-100 hover:opacity-100"
      >
        <BlueskyIcon class="size-3.5" />
      </button>
    </div>

    {@render sep()}

    <!-- Website link -->
    <button
      type="button"
      onclick={openWebsite}
      class="state-layer flex cursor-pointer items-center gap-1 rounded-xs px-1 text-primary/80
             transition-colors duration-100 hover:text-primary"
    >
      podsteer.com
      <ExternalLink class="size-3" strokeWidth={2} />
    </button>

    {@render sep()}

    <!-- App version. The NAME is not repeated: it is already the window title
         and the first thing in this bar, and a status bar reading
         "podsteer v0.1.1" spends a word saying where you are to somebody who
         is looking at it. -->
    <span class="tabular-nums opacity-60">{appInfo.version}</span>
  </div>
</footer>
