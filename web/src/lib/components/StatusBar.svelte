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
  import { preferences } from '$stores/preferences.svelte'
  import { formatClockTime } from '$lib/format'
  import { iconForKind } from '$lib/kindIcons'
  import { openURL } from '$lib/api/client'
  import { ExternalLink, Clock, Server, RefreshCw } from '@lucide/svelte'
  import ShareMenu from './ShareMenu.svelte'
  import GithubIcon from './icons/GithubIcon.svelte'
  import LinkedinIcon from './icons/LinkedinIcon.svelte'
  import BlueskyIcon from './icons/BlueskyIcon.svelte'

  const GITHUB_URL = 'https://github.com/podsteer'
  const LINKEDIN_URL = 'https://linkedin.com/company/podsteer'
  const BLUESKY_URL = 'https://bsky.app/profile/podsteer.com'

  const session = $derived(workspace.active)

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
        class="size-1.5 rounded-full {session.cluster.isReachable ? 'bg-success' : 'bg-error'}"
        aria-hidden="true"
      ></span>
      <span class="truncate font-medium">{session.cluster.id}</span>
      <span class="sr-only">
        {session.cluster.isReachable ? 'reachable' : 'not reachable'}
      </span>
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

  <div class="ml-auto flex items-center gap-3">
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
