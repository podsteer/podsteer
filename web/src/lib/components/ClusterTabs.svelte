<!--
  The cluster tab bar.

  Each open cluster gets a tab, and the bar doubles as the window's drag region
  on macOS — hence `drag-region` here and `no-drag` on every control inside it.

  Tabs keep the order they were opened in and never reshuffle. That is a
  correctness property, not a cosmetic one: a tab that moves under the cursor
  is how somebody restarts a deployment on the wrong cluster.

  A Home tab always sits first, pinned ahead of the scrollable cluster tabs,
  so returning to the picker never depends on how many tabs are open or how
  far the strip has scrolled. The "+" tab stays at the tail end of the actual
  clusters, because adding one is an action on that list, not a peer of it.

  Refresh, the theme toggle and Settings live at the right of this same bar
  rather than in each workspace's own toolbar: they are the controls an
  operator reaches for no matter which tab — or the picker — is in front, so
  they belong somewhere that does not remount when the tab does.

  Tabs are square on top, not rounded — a rounded tab reads as a chip or a
  button floating on the surface; square keeps it looking like part of the
  same sheet as the content below it, which is what a tab actually is.

  On macOS the leading padding reserves room for the traffic lights, plus a
  second, equal-sized gap after them so the Home tab does not sit flush
  against the window controls. In native fullscreen the traffic lights are
  gone and so is the reason for that padding, so it collapses to the same
  `px-3` inset the navigator uses for its own content, and "PodSteer" fills
  the space instead of leaving it blank — sitting in the exact column the
  sidebar's text sits in below it, rather than an arbitrary one.

  The traffic lights' vertical position is native AppKit chrome fixed by
  `mac.TitleBarHiddenInset()` — nothing in this file's CSS can move them.
  Wails v2 has no supported API for it (see wailsapp/wails#4227, open as of
  this writing); the only real fix is repositioning the NSWindow's standard
  window buttons from native Go/Cgo code, which this app does not currently
  have any of.
-->
<script lang="ts">
  import { isMac } from '$lib/platform'
  import { shortcut } from '$lib/shortcuts'
  import { workspace } from '$stores/workspace.svelte'
  import { organisation } from '$stores/organisation.svelte'
  import { groupBgClass } from '$lib/groupColour'
  import { preferences, THEME_LABELS } from '$stores/preferences.svelte'
  import { windowState } from '$stores/windowState.svelte'
  import SettingsDialog from './SettingsDialog.svelte'
  import UpdateBadge from './UpdateBadge.svelte'
  import { Home, Server, Plus, X, RefreshCw, Moon, Sun, Monitor, Settings, Lock } from '@lucide/svelte'

  let settingsOpen = $state(false)

  /**
   * Cmd+, opens Settings, which is the macOS convention every application
   * follows; Ctrl+, is the equivalent on Windows and Linux, where VS Code and
   * most Electron apps have made it the expectation too.
   *
   * Handled here rather than in the workspace because Settings is
   * application-wide: it must open from the cluster picker as well, where no
   * workspace is mounted. Matched against $lib/shortcuts so this handler and
   * ShortcutSheet.svelte cannot disagree about what the combo is.
   */
  function onKeydown(event: KeyboardEvent): void {
    if (!shortcut('settings').matches(event)) return

    event.preventDefault()
    settingsOpen = !settingsOpen
  }

  /** Dot colour by connection health, so a dead tab is visible at a glance. */
  function toneFor(reachable: boolean): string {
    return reachable ? 'bg-success' : 'bg-error'
  }

  /**
   * Leading padding for the drag region.
   *
   * `pl-[100px]` clears the traffic-light cluster (~80px) plus a second,
   * matching ~20px gap so the Home tab is not flush against it — the same
   * balance as the gap between the window's own edge and the lights. That
   * reasoning stops applying the moment fullscreen hides the lights, at
   * which point `pl-3` takes over — the same inset the navigator's own
   * content uses, so "PodSteer" lines up with the sidebar text under it.
   */
  const leadingPadding = $derived(isMac && !windowState.isFullscreen ? 'pl-[100px]' : 'pl-3')
</script>

<svelte:window onkeydown={onKeydown} />

<div
  class="drag-region flex h-10 shrink-0 items-stretch border-b border-outline-variant/60
         bg-surface-container-lowest {leadingPadding} pr-2"
>
  {#if isMac && windowState.isFullscreen}
    <!-- Fullscreen took the traffic lights, and the space reserved for them,
         with it — reclaim that space rather than leave it blank. The extra
         pl-1.5 on top of the bar's own pl-3 is this label's alone, so the
         Home tab and everything else that relies on pl-3 to line up with
         the sidebar is unaffected. -->
    <div
      class="flex shrink-0 items-center pr-3 pl-1.5 text-label-large font-bold text-on-surface"
      aria-hidden="true"
    >
      PodSteer
    </div>
  {/if}

  <!-- Home tab: always present, always in the same place. -->
  <button
    type="button"
    onclick={workspace.showPicker}
    title="Home"
    aria-label="Home"
    aria-current={workspace.activeClusterId === null ? 'page' : undefined}
    class="no-drag flex h-full shrink-0 items-center justify-center px-3
           transition-all duration-150 ease-standard
           {workspace.activeClusterId === null
             ? 'bg-surface-container border-b-2 border-primary text-primary shadow-sm'
             : 'text-on-surface-variant hover:bg-surface-container-low hover:text-on-surface'}"
  >
    <Home class="size-4" strokeWidth={1.8} />
  </button>

  <!-- Scrollable cluster tabs -->
  <div class="flex min-w-0 flex-1 items-stretch gap-0.5 overflow-x-auto">
    {#each workspace.sessions as session (session.cluster.id)}
      {@const active = session.cluster.id === workspace.activeClusterId}
      {@const placement = organisation.placementOf(session.cluster.id)}
      {@const settings = organisation.settingsFor(placement.project, placement.group)}

      <div class="group relative flex items-center" role="presentation">
        <button
          type="button"
          onclick={() => workspace.focus(session.cluster.id)}
          title="{session.cluster.id} — {session.cluster.host} — {session.cluster.isReachable
            ? 'reachable'
            : 'not reachable'}{settings.environment ? ` — ${settings.environment}` : ''}{settings.readOnly
            ? ' — read-only'
            : ''}"
          aria-label="{session.cluster.id}, {session.cluster.isReachable
            ? 'reachable'
            : 'not reachable'}{settings.environment
            ? `, ${settings.environment}`
            : ''}{settings.readOnly ? ', read-only' : ''}"
          aria-current={active ? 'page' : undefined}
          class="no-drag flex h-full max-w-52 items-center gap-2 pl-3 pr-7
                 text-label-medium transition-all duration-150 ease-standard
                 {active
                   ? 'bg-surface-container border-b-2 border-primary text-on-surface shadow-sm'
                   : 'text-on-surface-variant hover:bg-surface-container-low hover:text-on-surface'}"
        >
          <Server
            class="size-3.5 shrink-0 {active ? 'text-primary' : 'text-on-surface-variant/60'}"
            strokeWidth={1.8}
          />
          <!--
            THE ONLY SIGNAL WAS THE COLOUR. A dead cluster and a live one
            differed by a red or green dot, hidden from assistive technology
            and indistinguishable to a red/green colour-blind reader — on the
            control that says which cluster you are about to act on. The dot
            stays as the glanceable form; the fact is now in the tab's own
            accessible name and its tooltip.
          -->
          <span
            class="size-1.5 shrink-0 rounded-full {toneFor(session.cluster.isReachable)}"
            aria-hidden="true"
          ></span>
          <!-- The group's own colour, a second dot rather than an underline:
               the underline already means "this tab is active", and reusing
               it for something unrelated would make the active tab of an
               uncoloured group look like it lost its colour rather than
               never having had one. -->
          {#if settings.colour}
            <span
              class="size-1.5 shrink-0 rounded-full {groupBgClass(settings.colour)}"
              aria-hidden="true"
            ></span>
          {/if}
          <span class="truncate">{session.cluster.id}</span>
          <!-- Only production gets a chip here — every other environment is
               colour alone, which is what keeps a tab that has to fit eight
               of them on a laptop screen from growing a label per cluster.
               Production earns the exception: it is the one guardrail an
               operator must be able to read without opening the picker. -->
          {#if settings.environment === 'production'}
            <span
              class="shrink-0 rounded-full bg-error/15 px-1 py-px text-[9px] font-semibold
                     tracking-wide text-error uppercase"
            >
              prod
            </span>
          {/if}
          {#if settings.readOnly}
            <Lock
              class="size-3 shrink-0 {active ? 'text-on-surface-variant' : 'text-on-surface-variant/60'}"
              strokeWidth={2}
              aria-hidden="true"
            />
          {/if}
        </button>

        <!-- Close button -->
        <button
          type="button"
          onclick={() => workspace.close(session.cluster.id)}
          aria-label="Close {session.cluster.id}"
          class="state-layer no-drag absolute right-0.5 grid size-5 place-items-center rounded-full
                 text-on-surface-variant opacity-0 transition-opacity duration-100
                 group-hover:opacity-100 focus-visible:opacity-100
                 hover:bg-surface-container-high hover:text-on-surface
                 {active ? 'opacity-60' : ''}"
        >
          <X class="size-3" strokeWidth={2.5} />
        </button>
      </div>
    {/each}
  </div>

  <!-- Add cluster button: tail end of the actual clusters. -->
  {#if workspace.sessions.length > 0}
    <button
      type="button"
      onclick={workspace.showPicker}
      aria-label="Open a cluster"
      title="Open a cluster"
      class="state-layer no-drag grid size-8 shrink-0 self-center place-items-center rounded-full
             text-on-surface-variant transition-colors duration-100
             hover:bg-surface-container-high hover:text-on-surface"
    >
      <Plus class="size-4" strokeWidth={2} />
    </button>
  {/if}

  <div class="mx-1 h-5 w-px shrink-0 self-center bg-outline-variant/60" aria-hidden="true"></div>

  <!-- Between the separator and Refresh, and ABSENT unless there is genuinely
       a newer release. See UpdateBadge.svelte for why the quiet states show
       nothing at all. -->
  <UpdateBadge />

  <!-- Refresh: acts on whichever tab is in front. Nothing to refresh on the
       picker, so it is disabled rather than hidden — its position stays put. -->
  <button
    type="button"
    onclick={() => void workspace.active?.refresh()}
    disabled={!workspace.active}
    aria-label="Refresh"
    title="Refresh  {shortcut('refresh').keys}"
    class="state-layer no-drag grid size-8 shrink-0 self-center place-items-center rounded-full
           text-on-surface-variant transition-colors duration-100
           hover:bg-surface-container-high hover:text-on-surface
           disabled:pointer-events-none disabled:opacity-30
           {workspace.active?.status === 'loading' ? 'animate-spin' : ''}"
  >
    <RefreshCw class="size-4" strokeWidth={1.8} />
  </button>

  <!-- Theme cycle: light → system → dark.
       The icon shows the CHOICE, not the resolved scheme — on System it is a
       monitor whatever the OS currently resolves to, because a sun icon that
       silently became a moon at sunset would look like a bug. -->
  <button
    type="button"
    onclick={preferences.cycleTheme}
    aria-label="Theme: {THEME_LABELS[preferences.themePreference]}. Click to change."
    title="Theme: {THEME_LABELS[preferences.themePreference]}{preferences.themePreference ===
    'system'
      ? ` (following the system — currently ${preferences.resolvedTheme})`
      : ''}"
    class="state-layer no-drag grid size-8 shrink-0 self-center place-items-center rounded-full
           text-on-surface-variant transition-colors duration-100
           hover:bg-surface-container-high hover:text-on-surface"
  >
    {#if preferences.themePreference === 'light'}
      <Sun class="size-4" strokeWidth={1.8} />
    {:else if preferences.themePreference === 'dark'}
      <Moon class="size-4" strokeWidth={1.8} />
    {:else}
      <Monitor class="size-4" strokeWidth={1.8} />
    {/if}
  </button>

  <!-- Settings -->
  <button
    type="button"
    onclick={() => (settingsOpen = true)}
    aria-label="Settings"
    title="Settings  {shortcut('settings').keys}"
    class="state-layer no-drag grid size-8 shrink-0 self-center place-items-center rounded-full
           text-on-surface-variant transition-colors duration-100
           hover:bg-surface-container-high hover:text-on-surface"
  >
    <Settings class="size-4" strokeWidth={1.8} />
  </button>
</div>

<SettingsDialog
  open={settingsOpen}
  onclose={() => (settingsOpen = false)}
  onrefresh={() => void workspace.active?.refresh()}
/>
