<!--
  Tells the operator a newer PodSteer exists: a download icon after the
  version in the status bar, and nothing else.

  IT IS ABSENT WHEN THERE IS NOTHING TO SAY. No icon for "you are up to date",
  none for "we could not reach GitHub", none while checking. Three of the four
  states are silent, because a control that is permanently present and almost
  always means "everything is fine" is one people stop seeing.

  IN THE STATUS BAR, BESIDE THE VERSION, rather than in the tab strip where it
  used to be a filled pill: the version is where somebody looks to learn what
  they are running, so that is where "and a newer one exists" belongs. It sits
  in the version's own grey and GLOWS to the primary colour for a moment once
  a minute — often enough to be noticed, rarely enough never to nag. On a
  fixed timer rather than the refresh interval, which can be manual (never)
  or five seconds (far too often). Reduced motion keeps it still, through the
  global rule in app.css.

  It does not update anything. PodSteer installs however the operator
  installed it, and the button opens the release page in the browser. Turning
  the notice off for good is the Settings switch.
-->
<script lang="ts">
  import { Download } from '@lucide/svelte'
  import { updates } from '$stores/updates.svelte'
  import { openURL } from '$lib/api/client'

  /** How often the icon glows. */
  const PULSE_EVERY_MS = 60_000

  const version = $derived(updates.status?.latest ?? '')

  /** Bumped once a minute; re-keying the icon restarts its animation. */
  let pulse = $state(0)

  $effect(() => {
    if (!updates.available) return
    const timer = window.setInterval(() => (pulse += 1), PULSE_EVERY_MS)
    return () => window.clearInterval(timer)
  })
</script>

{#if updates.available}
  <button
    type="button"
    onclick={() => {
      const url = updates.status?.url
      if (url) void openURL(url)
    }}
    aria-label="PodSteer {version} is available"
    title="PodSteer {version} is available — opens the release notes"
    class="state-layer grid size-5 shrink-0 place-items-center rounded-full
           transition-colors duration-100 hover:text-primary"
  >
    {#key pulse}
      <Download class="update-glow size-3.5" strokeWidth={2} />
    {/key}
  </button>
{/if}

<style>
  /* Grey, blue, grey: the version's own colour at both ends, so between
     glows the icon is as quiet as the text beside it. */
  :global(.update-glow) {
    opacity: 0.6;
    animation: update-glow 1.6s ease-in-out;
  }

  @keyframes update-glow {
    0%,
    100% {
      opacity: 0.6;
    }
    45%,
    55% {
      opacity: 1;
      color: var(--color-primary);
      filter: drop-shadow(0 0 3px color-mix(in srgb, var(--color-primary) 60%, transparent));
    }
  }
</style>
