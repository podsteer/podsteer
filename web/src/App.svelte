<!--
  Application shell: the cluster tab bar, then whichever tab is in front.

  Routing is still a conditional rather than a router — the app has exactly two
  states, picker or workspace, and the tab bar carries the navigation a router
  would otherwise provide. It earns its place once views become linkable.
-->
<script lang="ts">
  import ClusterTabs from '$lib/components/ClusterTabs.svelte'
  import Splash from '$lib/components/Splash.svelte'
  import StatusBar from '$lib/components/StatusBar.svelte'
  import ClusterView from '$pages/ClusterView.svelte'
  import ClusterWorkspace from '$pages/ClusterWorkspace.svelte'
  import { workspace } from '$stores/workspace.svelte'
  import { loadAppInfo } from '$stores/system.svelte'
  import { alertPlayer } from '$stores/alerts.svelte'

  /**
   * The shortest time the splash stays up. Initialisation is faster than this
   * on most machines, and appearing for a single frame reads as a fault, not
   * as branding.
   */
  const MIN_SPLASH_MS = 900

  /** False until the workspace has initialised and the splash has been seen. */
  let booted = $state(false)

  // Discover clusters once when the shell mounts, and release every tab's
  // timer and the event subscription when it goes away.
  $effect(() => {
    void loadAppInfo()
    // Audio output is only allowed to start from a user gesture, and a context
    // created before one exists stays suspended for the life of the process.
    // Arming here means the first click or keypress of the session wakes it,
    // so an alert never arrives to find the speaker asleep.
    alertPlayer.arm()
    const minimum = new Promise((resolve) => setTimeout(resolve, MIN_SPLASH_MS))
    void Promise.all([workspace.initialise(), minimum]).then(() => {
      booted = true
    })
    return () => workspace.dispose()
  })

  /**
   * Shortcuts that belong to the WINDOW rather than to a tab.
   *
   * Here rather than in ClusterWorkspace, which is where ⌘B, ⌘R and ⌘K live:
   * those act on the cluster in front, so they are unmounted on the picker and
   * should be. Moving between tabs has to work from the picker too, or the one
   * tab you cannot leave by keyboard is the one you start on.
   *
   * ⌘] / ⌘[ move between tabs, the picker counting as the first.
   * ⌘N goes to the picker, which is where a new cluster is opened from.
   */
  function onKeydown(event: KeyboardEvent): void {
    if (!(event.metaKey || event.ctrlKey)) return

    switch (event.key) {
      case ']':
        event.preventDefault()
        workspace.cycleTab(1)
        break
      case '[':
        event.preventDefault()
        workspace.cycleTab(-1)
        break
      case 'n':
      case 'N':
        event.preventDefault()
        workspace.showPicker()
        break
    }
  }
</script>

<svelte:window onkeydown={onKeydown} />

<div class="flex h-screen flex-col overflow-hidden bg-surface">
  <ClusterTabs />

  <main class="flex min-h-0 flex-1 flex-col overflow-hidden">
    {#if workspace.active}
      <!--
        Keyed on the cluster id so switching tabs remounts the workspace. That
        is what moves the auto-refresh timer with the tab and guarantees no
        state — a search term, a scroll position, an open drawer — leaks from
        one cluster's view into another's.
      -->
      {#key workspace.active.cluster.id}
        <ClusterWorkspace session={workspace.active} />
      {/key}
    {:else}
      <div class="min-h-0 flex-1 overflow-auto">
        <ClusterView />
      </div>
    {/if}
  </main>

  <StatusBar />

  <!--
    Rendered last so it covers the chrome while booting; it fades itself out
    (transition:fade) once booted flips and Svelte unmounts it.
  -->
  {#if !booted}
    <Splash />
  {/if}
</div>
