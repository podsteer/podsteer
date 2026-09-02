<!--
  Tells the operator a newer PodSteer exists, and nothing else.

  IT IS ABSENT WHEN THERE IS NOTHING TO SAY. No icon for "you are up to date",
  none for "we could not reach GitHub", none while checking. Three of the four
  states are silent, because a control that is permanently present and almost
  always means "everything is fine" is one people stop seeing — and the one
  time it matters it will be invisible for the same reason.

  It does not update anything. PodSteer installs however the operator installed
  it — Homebrew, a zip, a package — and a client that replaced its own binary
  would be a far larger promise than this feature is making. The button opens
  the release page in the system browser and stops there.
-->
<script lang="ts">
  import { Download, X } from '@lucide/svelte'
  import { updates } from '$stores/updates.svelte'
  import { preferences } from '$stores/preferences.svelte'
  import { openURL } from '$lib/api/client'

  const version = $derived(updates.status?.latest ?? '')
</script>

{#if updates.available}
  <div class="flex shrink-0 items-center self-center">
    <button
      type="button"
      onclick={() => {
        const url = updates.status?.url
        if (url) void openURL(url)
      }}
      aria-label="PodSteer {version} is available"
      title="PodSteer {version} is available — opens the release notes"
      class="state-layer no-drag flex h-7 shrink-0 items-center gap-1.5 rounded-full
             bg-primary-container px-2.5 text-label-small text-on-primary-container
             transition-colors duration-100 hover:brightness-105"
    >
      <Download class="size-3.5" strokeWidth={2} />
      <span class="font-medium">{version}</span>
    </button>

    <!--
      Dismissal is per VERSION, not a blanket silence: somebody who is not
      upgrading today still wants to hear about the release after this one.
      Turning it off for good is what the switch in Settings is for, and the
      title says so rather than leaving them to guess.
    -->
    <button
      type="button"
      onclick={() => preferences.dismissUpdate(version)}
      aria-label="Dismiss the notice about {version}"
      title="Dismiss until the next release — turn these off in Settings → Notifications"
      class="state-layer no-drag ml-0.5 grid size-6 shrink-0 place-items-center rounded-full
             text-on-surface-variant/70 transition-colors duration-100
             hover:bg-surface-container-high hover:text-on-surface"
    >
      <X class="size-3" strokeWidth={2.5} />
    </button>

    <div class="mx-1 h-5 w-px shrink-0 bg-outline-variant/60" aria-hidden="true"></div>
  </div>
{/if}
