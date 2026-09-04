<!--
  Application shell: the cluster tab bar, then whichever tab is in front.

  Routing is still a conditional rather than a router — the app has exactly two
  states, picker or workspace, and the tab bar carries the navigation a router
  would otherwise provide. It earns its place once views become linkable.
-->
<script lang="ts">
  import ClusterTabs from '$lib/components/ClusterTabs.svelte'
  import CommandPalette from '$lib/components/CommandPalette.svelte'
  import ShortcutSheet from '$lib/components/ShortcutSheet.svelte'
  import Splash from '$lib/components/Splash.svelte'
  import StatusBar from '$lib/components/StatusBar.svelte'
  import ClusterView from '$pages/ClusterView.svelte'
  import ClusterWorkspace from '$pages/ClusterWorkspace.svelte'
  import { workspace } from '$stores/workspace.svelte'
  import { loadAppInfo } from '$stores/system.svelte'
  import { updates } from '$stores/updates.svelte'
  import { alertPlayer } from '$stores/alerts.svelte'
  import { forwards } from '$stores/forwards.svelte'
  import { nodeShells } from '$stores/nodeShells.svelte'
  import { shortcutSheet } from '$stores/shortcutSheet.svelte'
  import { palette } from '$stores/palette.svelte'
  import { isTypingTarget, shortcut } from '$lib/shortcuts'

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
    // Ask what is already forwarded. Nothing survives a restart of the
    // application — every forward is a goroutine in this process — but a
    // window reopened over a running backend, or a hot reload in development,
    // would otherwise show a Forward button for a port that is already open.
    void forwards.refresh()
    const unwatchForwards = forwards.watch()
    // Node shells the same way, and for the same reason: a window reopened over
    // a running backend must show what is still running so it can be stopped.
    void nodeShells.refresh()
    const unwatchNodeShells = nodeShells.watch()
    // Audio output is only allowed to start from a user gesture, and a context
    // created before one exists stays suspended for the life of the process.
    // Arming here means the first click or keypress of the session wakes it,
    // so an alert never arrives to find the speaker asleep.
    alertPlayer.arm()
    // The update check, on its own delay and off the startup path entirely.
    // Nothing here waits for it, and it does nothing at all when the operator
    // has switched it off — see updates.svelte.ts.
    updates.start()
    const minimum = new Promise((resolve) => setTimeout(resolve, MIN_SPLASH_MS))
    // .catch AS WELL, because there was none: a rejection anywhere in
    // initialise left the splash screen up for ever with nothing on it. A
    // failed start must still reach the application — whatever went wrong is
    // reported there, where somebody can read it.
    void Promise.all([workspace.initialise(), minimum])
      .catch((cause) => {
        console.error('Failed to initialise the workspace:', cause)
      })
      .finally(() => {
        booted = true
      })
    return () => {
      unwatchForwards()
      unwatchNodeShells()
      updates.stop()
      workspace.dispose()
    }
  })

  /**
   * Shortcuts that belong to the WINDOW rather than to a tab.
   *
   * Here rather than in ClusterWorkspace, which is where ⌘B, ⌘R and ⌘K live:
   * those act on the cluster in front, so they are unmounted on the picker and
   * should be. Moving between tabs has to work from the picker too, or the one
   * tab you cannot leave by keyboard is the one you start on.
   *
   * Every combo below is matched against $lib/shortcuts rather than a literal
   * key check, so this handler and ShortcutSheet.svelte can never disagree
   * about what ⌘] does or how it is spelled on this platform.
   *
   * ⌘] / ⌘[ move between tabs, the picker counting as the first.
   * ⌘1…9 jumps straight to the Nth open cluster tab.
   * ⌘N goes to the picker, which is where a new cluster is opened from.
   * ⌘/ opens the shortcut sheet, and so does a bare "?" — but only when focus
   * is not inside a text field, or typing a literal question mark into the
   * search box or the YAML editor would pop it open instead.
   * ⌘⇧P / ⌘P opens the command palette — also global, for the same reason:
   * jumping to another kind, cluster or object cannot depend on which tab
   * happens to be in front when the operator reaches for it.
   */
  function onKeydown(event: KeyboardEvent): void {
    if (shortcut('command-palette').matches(event)) {
      event.preventDefault()
      palette.show()
      return
    }
    if (shortcut('next-tab').matches(event)) {
      event.preventDefault()
      workspace.cycleTab(1)
      return
    }
    if (shortcut('previous-tab').matches(event)) {
      event.preventDefault()
      workspace.cycleTab(-1)
      return
    }
    if (shortcut('switch-tab').matches(event)) {
      event.preventDefault()
      const target = workspace.sessions[Number(event.key) - 1]
      if (target) void workspace.focus(target.cluster.id)
      return
    }
    if (shortcut('new-cluster').matches(event)) {
      event.preventDefault()
      workspace.showPicker()
      return
    }
    if (
      shortcut('shortcut-sheet').matches(event) ||
      (event.key === '?' && !isTypingTarget(event.target))
    ) {
      event.preventDefault()
      shortcutSheet.show()
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

<ShortcutSheet open={shortcutSheet.open} onclose={shortcutSheet.hide} />
<CommandPalette open={palette.open} onclose={palette.hide} />
