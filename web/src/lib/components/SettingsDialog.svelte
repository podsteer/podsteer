<!--
  Settings, as a dialog with its own left-hand navigation.

  Sections rather than one long column because they answer different questions
  and are visited for different reasons: Refresh and Appearance are adjusted
  occasionally, Data is a policy decision made once, and Credits is an
  obligation nobody browses for fun but everybody must be able to find.

  Refresh lives here rather than as a button in the toolbar. A refresh control
  on the main surface implies refreshing is something you do — it is not, the
  views poll on their own. What an operator occasionally wants to change is how
  *often*, and that is a setting, not an action. The manual refresh remains a
  keyboard shortcut, because the one time you genuinely want it is right after
  changing something with kubectl.
-->
<script lang="ts">
  import { escapeLayer, type EscapeClaim } from '$lib/escape'
  import { modal } from '$lib/modal'
  import {
    preferences,
    REFRESH_INTERVALS,
    DETAIL_MAX_REM,
  DETAIL_MAX_SHARE,
  DETAIL_MIN_REM,
  DETAIL_WIDTHS,
  PAGE_SIZES,
    THEME_PREFERENCES,
    THEME_LABELS,
    THRESHOLD_SCOPES,
    USAGE_WINDOWS,
    type PageSize,
    type PodMeasure,
    type ThresholdScope,
  } from '$stores/preferences.svelte'

  /**
   * What each scope governs, in the operator's terms.
   *
   * Named after the SCREEN rather than after what is measured, because that
   * is the promise being made: the section called Nodes changes the node
   * list, and nothing else. The node load bars on the overview belong to the
   * overview, which is where somebody adjusting "Overview" would look for the
   * effect of what they just changed.
   */
  const SCOPE_META: Record<ThresholdScope, { name: string; detail: string }> = {
    overview: {
      name: 'Overview',
      detail:
        'Cluster capacity, node load and pod slots on the cluster dashboard — the bars you glance at to decide whether anything needs attention.',
    },
    nodes: {
      name: 'Nodes',
      detail: 'CPU and memory in the node list, measured against each node’s allocatable capacity.',
    },
    pods: {
      name: 'Pods',
      detail:
        'CPU and memory in the pod list. Amber and red always come from how close a pod is to its limits — to being throttled, or OOMKilled — and a pod that declares no limit has no ceiling to be near.',
    },
  }
  import {
    historySettings,
    RETENTION_OPTIONS,
    SAMPLING_INTERVALS,
  } from '$stores/history.svelte'
  import { shortcut } from '$lib/shortcuts'
  import { updates } from '$stores/updates.svelte'
  import Button from './Button.svelte'
  import CreditsPane from './CreditsPane.svelte'
  import {
    ALERT_SEVERITIES,
    ALERT_SOUNDS,
    SEVERITY_LABELS,
    SILENT,
    alertPlayer,
  } from '$stores/alerts.svelte'
  import Select from './Select.svelte'
  import GaugeTrack from './GaugeTrack.svelte'
  import SettingsTransfer from './SettingsTransfer.svelte'
  import {
    RefreshCw,
    Palette,
    Database,
    Scale,
    Bell,
    Gauge,
    Play,
    ArrowLeftRight,
    X,
  } from '@lucide/svelte'

  /**
   * The values a threshold may take, every five per cent.
   *
   * Coarse on purpose: the difference between 80 and 82 is not a decision
   * anybody makes deliberately, and a free-text box invites 800.
   */
  const THRESHOLD_OPTIONS = Array.from({ length: 10 }, (_, index) => {
    const value = 50 + index * 5
    return { value: String(value), label: `${value}%` }
  })

  /**
   * The sounds, plus silence.
   *
   * Silence is offered per severity rather than only as a master switch,
   * because the arrangement most people want is criticals audible and
   * warnings not — an operator who hears something every time a pod restarts
   * stops hearing any of it.
   */
  const SOUND_OPTIONS = [
    { value: SILENT, label: 'Silent', hint: 'no sound' },
    ...ALERT_SOUNDS.map((sound) => ({
      value: sound.id,
      label: sound.label,
      hint: sound.describe,
    })),
  ]

  interface Props {
    open: boolean
    onclose: () => void
    /** Runs a one-off refresh of the active view. */
    onrefresh: () => void
  }

  let { open, onclose, onrefresh }: Props = $props()

  const SECTIONS = [
    { id: 'refresh', label: 'Refresh', icon: RefreshCw },
    { id: 'appearance', label: 'Appearance', icon: Palette },
    { id: 'thresholds', label: 'Thresholds', icon: Gauge },
    { id: 'notifications', label: 'Notifications', icon: Bell },
    { id: 'data', label: 'Data', icon: Database },
    // Next to Data, because both are about what leaves this machine, and
    // before Credits, which is the one section nobody browses for.
    { id: 'transfer', label: 'Export & import', icon: ArrowLeftRight },
    { id: 'credits', label: 'Credits', icon: Scale },
  ] as const

  type SectionID = (typeof SECTIONS)[number]['id']

  let section = $state<SectionID>('refresh')

  /**
   * Whether the stored share is one of the presets.
   *
   * A tolerance rather than equality, because a third is 0.3333… and because
   * a width set by dragging lands wherever the pointer was — within a
   * percentage point of a preset is that preset as far as anybody looking at
   * the panel is concerned.
   */
  function matchesPreset(fraction: number): boolean {
    return Math.abs(preferences.detailWidthFraction - fraction) < 0.01
  }

  /** Loads the history settings the first time Settings is opened. */
  $effect(() => {
    if (open && !historySettings.loaded) void historySettings.load()
  })

  function onKeydown(event: KeyboardEvent): void {
    if (event.key !== 'Escape' || !open) return
    if (!escape?.owns()) return
    onclose()
  }


  /** Escape belongs to the innermost open layer. See $lib/escape. */
  let escape = $state<EscapeClaim | null>(null)
  $effect(() => {
    if (!open) return
    const held = escapeLayer()
    escape = held
    return () => {
      held.release()
      escape = null
    }
  })
</script>

<!--
  One screen's pair of lines, with a preview of what they do.

  A snippet rather than three copies of the same forty lines: the scopes
  differ only in which record they read and write, and three hand-maintained
  copies is how one of them quietly stops matching the others.
-->
{#snippet thresholdGroup(scope: ThresholdScope)}
  {@const lines = preferences.thresholdsFor(scope)}
  {@const meta = SCOPE_META[scope]}

  <div>
    <h4 class="text-title-small text-on-surface">{meta.name}</h4>
    <p class="mt-0.5 text-body-small text-on-surface-variant">{meta.detail}</p>

    <!--
      Only the pod list has two honest denominators to choose between, so only
      it gets this control. A node's bar divides by allocatable and there is
      nothing else it could divide by.
    -->
    {#if scope === 'pods'}
      <div class="mt-4 rounded-xs border border-outline-variant/60 p-3">
        <p class="text-body-medium text-on-surface">What the bars measure</p>
        <p class="mt-0.5 text-body-small text-on-surface-variant">
          Requests answer "did I reserve about the right amount", where a full bar is a success.
          Limits answer "how much headroom is left", where a full bar is the problem — and only
          that mode can mark the two lines on the track, because only then do the bar and the
          thresholds count the same thing. Either way the tooltip states both numbers.
        </p>

        <div class="mt-3 flex">
          {#each [{ id: 'requests', label: 'Requests' }, { id: 'limits', label: 'Limits' }] as mode (mode.id)}
            <button
              type="button"
              onclick={() => preferences.setPodMeasure(mode.id as PodMeasure)}
              aria-pressed={preferences.podMeasure === mode.id}
              class="state-layer h-9 flex-1 border text-label-large transition-colors
                     duration-150 ease-standard
                     {mode.id === 'requests' ? 'rounded-l-xs' : '-ml-px rounded-r-xs'}
                     {preferences.podMeasure === mode.id
                       ? 'border-transparent bg-secondary-container text-on-secondary-container'
                       : 'border-outline text-on-surface-variant'}"
            >
              {mode.label}
            </button>
          {/each}
        </div>
      </div>
    {/if}

    <!-- Each line switches off on its own. Somebody who only wants to know
         about the serious case turns the first one off and gets blue all the
         way to the second, at whatever value they chose for it. -->
    <ul class="mt-4 flex flex-col gap-3">
      {#each [{ id: 'warn', name: 'Amber', swatch: 'bg-gauge-warn', on: lines.warnEnabled, value: lines.warn }, { id: 'critical', name: 'Red', swatch: 'bg-gauge-critical', on: lines.criticalEnabled, value: lines.critical }] as line (line.id)}
        <li class="flex items-center gap-3">
          <span
            class="flex w-20 shrink-0 items-center gap-2 text-body-medium
                   {line.on ? 'text-on-surface' : 'text-on-surface-variant/50'}"
          >
            <span class="size-3 shrink-0 rounded-full {line.swatch} {line.on ? '' : 'opacity-30'}"
            ></span>
            {line.name}
          </span>

          <div class="flex shrink-0">
            {#each [true, false] as choice (choice)}
              <button
                type="button"
                onclick={() =>
                  preferences.setThresholdEnabled(scope, line.id as 'warn' | 'critical', choice)}
                aria-pressed={line.on === choice}
                aria-label="{meta.name} {line.name} threshold {choice ? 'on' : 'off'}"
                class="state-layer h-9 w-14 border text-label-large transition-colors
                       duration-150 ease-standard
                       {choice ? 'rounded-l-xs' : '-ml-px rounded-r-xs'}
                       {line.on === choice
                         ? 'border-transparent bg-secondary-container text-on-secondary-container'
                         : 'border-outline text-on-surface-variant'}"
              >
                {choice ? 'On' : 'Off'}
              </button>
            {/each}
          </div>

          <!-- Still adjustable while off, so a line can be set up before it is
               switched on rather than after. -->
          <div class="flex-1 {line.on ? '' : 'opacity-50'}">
            <Select
              label="{line.name} threshold"
              accessibleName="{meta.name} {line.name} threshold"
              value={String(line.value)}
              options={THRESHOLD_OPTIONS}
              class="w-full"
              onchange={(value) =>
                preferences.setThreshold(scope, line.id as 'warn' | 'critical', Number(value))}
            />
          </div>
        </li>
      {/each}
    </ul>

    <!-- Shown rather than described. Somebody choosing a number wants to see
         where it lands, and each scope previews its own. -->
    <div class="mt-4 flex flex-col gap-1.5">
      {#each [Math.max(5, lines.warn - 20), Math.round((lines.warn + lines.critical) / 2), Math.min(100, lines.critical + 5)] as sample (sample)}
        <div class="flex items-center gap-3">
          <GaugeTrack {scope} value={sample} label="{meta.name} example at {sample} per cent" />
          <span
            class="w-[4.5ch] shrink-0 text-right text-body-medium tabular-nums text-on-surface-variant"
          >
            {sample}%
          </span>
        </div>
      {/each}
    </div>
  </div>
{/snippet}

<svelte:window onkeydown={onKeydown} />

{#if open}
  <button
    type="button"
    aria-label="Close settings"
    tabindex="-1"
    class="fixed inset-0 z-40 cursor-default bg-scrim/40"
    onclick={onclose}
  ></button>

  <!-- Centred by `inset-0 m-auto` rather than by `-translate-1/2`. A transform
       makes the element a containing block for every `position: fixed`
       descendant, so a dropdown inside this dialog was being positioned
       against the dialog instead of against the window and landed in the
       corner. Auto margins against a full inset centre the same way and leave
       fixed positioning meaning what it says. -->
  <div
    class="fixed inset-0 z-50 m-auto flex h-[36rem] max-h-[85vh] w-[52rem] max-w-[92vw]
           overflow-hidden rounded-sm border border-outline-variant
           bg-surface-container-high shadow-level-3"
    role="dialog"
    aria-modal="true"
    use:modal
    aria-label="Settings"
  >
    <!-- Section navigation -->
    <nav
      class="flex w-52 shrink-0 flex-col gap-0.5 border-r border-outline-variant/60
             bg-surface-container p-3"
      aria-label="Settings sections"
    >
      <h2 class="px-2 pt-1 pb-3 text-title-medium text-on-surface">Settings</h2>

      {#each SECTIONS as entry (entry.id)}
        {@const Icon = entry.icon}
        <button
          type="button"
          onclick={() => (section = entry.id)}
          aria-current={section === entry.id ? 'page' : undefined}
          class="state-layer flex items-center gap-2.5 rounded-sm px-2.5 py-2 text-left
                 transition-colors duration-100
                 {section === entry.id
                   ? 'bg-primary/12 text-primary'
                   : 'text-on-surface-variant hover:bg-surface-container-high hover:text-on-surface'}"
        >
          <Icon class="size-4 shrink-0" strokeWidth={1.8} />
          <span class="text-body-medium">{entry.label}</span>
        </button>
      {/each}
    </nav>

    <!-- Section content -->
    <div class="flex min-w-0 flex-1 flex-col">
      <div class="flex items-start justify-end px-4 pt-3">
        <button
          type="button"
          onclick={onclose}
          aria-label="Close settings"
          class="state-layer grid size-8 place-items-center rounded-full text-on-surface-variant
                 transition-colors duration-100 hover:bg-surface-container hover:text-on-surface"
        >
          <X class="size-4" strokeWidth={2} />
        </button>
      </div>

      <!-- Credits scrolls its own list, so the pane must not add a second
           scrollbar around it. -->
      <div
        class="min-h-0 flex-1 px-6 pb-6 {section === 'credits' ? 'overflow-hidden' : 'overflow-y-auto'}"
      >
        {#if section === 'refresh'}
          <section>
            <h3 class="text-title-medium text-on-surface">Refresh</h3>
            <p class="mt-0.5 text-body-small text-on-surface-variant">
              How often the open view re-reads the cluster.
            </p>

            <div class="mt-4 flex flex-col gap-1.5">
              {#each REFRESH_INTERVALS as interval (interval.value)}
                <label class="flex cursor-pointer items-center gap-3 text-body-medium text-on-surface">
                  <input
                    type="radio"
                    name="refresh-interval"
                    value={interval.value}
                    checked={preferences.effectiveIntervalMs === interval.value}
                    onchange={() => preferences.setRefreshInterval(interval.value)}
                    class="accent-primary"
                  />
                  {interval.label}
                </label>
              {/each}
            </div>

            <div class="mt-5 flex items-center gap-3">
              <Button variant="tonal" onclick={onrefresh}>Refresh now</Button>
              <span class="text-body-small text-on-surface-variant/70">
                or press {shortcut('refresh').keys} at any time
              </span>
            </div>
          </section>
        {:else if section === 'appearance'}
          <section class="flex flex-col gap-6">
            <div>
              <h3 class="text-title-medium text-on-surface">Theme</h3>
              <p class="mt-0.5 text-body-small text-on-surface-variant">
                System follows your desktop's own light and dark setting, and changes with it.
              </p>

              <div class="mt-3 flex gap-2">
                {#each THEME_PREFERENCES as choice (choice)}
                  <button
                    type="button"
                    onclick={() => preferences.setTheme(choice)}
                    aria-pressed={preferences.themePreference === choice}
                    class="state-layer h-9 min-w-24 rounded-xs border px-4 text-label-large
                           transition-colors duration-150 ease-standard
                           {preferences.themePreference === choice
                             ? 'border-transparent bg-secondary-container text-on-secondary-container'
                             : 'border-outline text-on-surface-variant'}"
                  >
                    {THEME_LABELS[choice]}
                  </button>
                {/each}
              </div>

              {#if preferences.themePreference === 'system'}
                <p class="mt-2 text-body-small text-on-surface-variant/70">
                  Currently {preferences.resolvedTheme}.
                </p>
              {/if}
            </div>

            <div class="border-t border-outline-variant pt-5">
              <h3 class="text-title-medium text-on-surface">Rows per page</h3>
              <p class="mt-0.5 text-body-small text-on-surface-variant">
                Applies to every resource table.
              </p>

              <div class="mt-3 flex gap-2">
                {#each PAGE_SIZES as size (size)}
                  <button
                    type="button"
                    onclick={() => preferences.setPageSize(size as PageSize)}
                    class="state-layer h-8 min-w-12 rounded-xs border px-3 text-label-large
                           transition-colors duration-150 ease-standard
                           {preferences.pageSize === size
                             ? 'border-transparent bg-secondary-container text-on-secondary-container'
                             : 'border-outline text-on-surface-variant'}"
                  >
                    {size}
                  </button>
                {/each}
              </div>
            </div>

            <div class="border-t border-outline-variant pt-5">
              <h3 class="text-title-medium text-on-surface">Detail panel width</h3>
              <p class="mt-0.5 text-body-small text-on-surface-variant">
                How much of the window the panel covers when an object is opened. Narrower
                leaves more of the list readable behind it. A share rather than a size, so
                it means the same on any screen — and the panel's own left edge can be
                dragged to anything between, which is what these three set.
              </p>
              <p class="mt-1 text-body-small text-on-surface-variant/70">
                The divider between a section's two columns drags too, and moves every
                section at once. Double-click either edge to put it back.
              </p>

              <div class="mt-3 flex gap-2">
                {#each DETAIL_WIDTHS as choice (choice.id)}
                  <button
                    type="button"
                    onclick={() => preferences.setDetailWidth(choice.fraction)}
                    aria-pressed={matchesPreset(choice.fraction)}
                    title={choice.detail}
                    class="state-layer h-9 min-w-24 rounded-xs border px-4 text-label-large
                           transition-colors duration-150 ease-standard
                           {matchesPreset(choice.fraction)
                             ? 'border-transparent bg-secondary-container text-on-secondary-container'
                             : 'border-outline text-on-surface-variant'}"
                  >
                    {choice.label}
                  </button>
                {/each}
              </div>

              <!-- A dragged width lands between the presets, and none of them
                   then reads as pressed. Saying what it currently is beats
                   three unlit buttons, which look like the setting is unset. -->
              {#if !DETAIL_WIDTHS.some((choice) => matchesPreset(choice.fraction))}
                <p class="mt-2 text-body-small text-on-surface-variant">
                  Currently {Math.round(preferences.detailWidthFraction * 100)}% of the
                  window, set by dragging the panel's edge.
                </p>
              {/if}

              <!-- The clamp said out loud, because it is why a small window
                   may not visibly change when this does: a quarter of a narrow
                   laptop is already below the floor. -->
              <p class="mt-2 text-body-small text-on-surface-variant/70">
                Clamped either way — never under {DETAIL_MIN_REM * 16}px, which is the
                narrowest that still fits a label and its value, and never over
                {DETAIL_MAX_REM * 16}px or {DETAIL_MAX_SHARE * 100}% of the window,
                whichever comes first.
              </p>
            </div>

            <div class="border-t border-outline-variant pt-5">
              <h3 class="text-title-medium text-on-surface">Sidebar</h3>
              <label class="mt-3 flex cursor-pointer items-center gap-3 text-body-medium text-on-surface">
                <input
                  type="checkbox"
                  checked={!preferences.navigatorCollapsed}
                  onchange={preferences.toggleNavigator}
                  class="accent-primary"
                />
                Show the resource navigator
                <span class="text-body-small text-on-surface-variant/70"
                  >{shortcut('toggle-navigator').keys}</span
                >
              </label>
            </div>
          </section>
        {:else if section === 'thresholds'}
          <section class="flex flex-col gap-8">
            <div>
              <h3 class="text-title-medium text-on-surface">When a bar changes colour</h3>
              <p class="mt-0.5 text-body-small text-on-surface-variant">
                Every bar that measures how full something is uses two lines: blue below the
                first, amber between them, red past the second. Both are marked on the track, so
                the colour is never the only way to read it.
              </p>
              <p class="mt-2 text-body-small text-on-surface-variant">
                Each screen keeps its own pair. A number that is a useful early warning on a
                dashboard you glance at can be a distraction on a list you work in all day, and
                nothing here decides which of those you are doing. Set them all the same if that
                is what you want.
              </p>
            </div>

            {#each THRESHOLD_SCOPES as scope (scope)}
              {@render thresholdGroup(scope)}
            {/each}

            <!--
              Retained usage, here rather than under History because it is a
              different thing from the cluster trend on disk: this is held in
              memory, dies with the process, and exists only so a chart in a
              drawer opens with a shape instead of a blank frame.
            -->
            <div>
              <h4 class="text-title-small text-on-surface">Charts</h4>
              <p class="mt-0.5 text-body-small text-on-surface-variant">
                A drawer's chart starts with whatever usage the lists have already seen.
                metrics-server cannot be asked for history — it keeps only the latest reading —
                but every refresh of a list carries usage for every row in it, and this is how
                much of that is kept rather than discarded.
              </p>

              <div class="mt-3 flex">
                {#each USAGE_WINDOWS as minutes, index (minutes)}
                  <button
                    type="button"
                    onclick={() => preferences.setUsageWindowMinutes(minutes)}
                    aria-pressed={preferences.usageWindowMinutes === minutes}
                    class="state-layer h-9 flex-1 border text-label-large transition-colors
                           duration-150 ease-standard
                           {index === 0 ? 'rounded-l-xs' : '-ml-px'}
                           {index === USAGE_WINDOWS.length - 1 ? 'rounded-r-xs' : ''}
                           {preferences.usageWindowMinutes === minutes
                             ? 'border-transparent bg-secondary-container text-on-secondary-container'
                             : 'border-outline text-on-surface-variant'}"
                  >
                    {minutes === 0 ? 'Off' : `${minutes} min`}
                  </button>
                {/each}
              </div>

              <p class="mt-1.5 text-body-small text-on-surface-variant/70">
                Held in memory only, never written to disk — the recorded cluster history
                deliberately carries no object names, and a file of per-pod series would undo
                that. Off means every chart starts empty and fills as you watch.
              </p>
            </div>

            <p class="text-body-small text-on-surface-variant/70">
              The two lines cannot cross: moving one past the other pushes it along rather than
              leaving a range that could never be coloured. With both off, every bar on that
              screen stays blue however full it gets.
            </p>
          </section>
        {:else if section === 'notifications'}
          <section class="flex flex-col gap-6">
            <!--
              STATED PLAINLY, INCLUDING WHAT IS SENT. This is the only thing in
              PodSteer that contacts anything but a cluster, and the project
              spent its first releases promising it never would. Somebody who
              read that promise deserves to find the reversal described here
              rather than discover it in a proxy log.
            -->
            <div>
              <h3 class="text-title-medium text-on-surface">Tell me about new versions</h3>
              <p class="mt-0.5 text-body-small text-on-surface-variant">
                Asks GitHub once a day whether a newer PodSteer has been released, and shows a
                small badge beside Refresh when one has. It sends nothing about you — no version,
                no platform, no identifier — and the comparison happens here, on the answer.
                PodSteer never installs anything itself.
              </p>

              <label class="mt-3 flex items-center justify-between gap-4">
                <span class="text-body-medium text-on-surface">
                  Check for updates
                  {#if !updates.permitted}
                    <span class="ml-1 text-label-small text-on-surface-variant">
                      — turned off on this machine by PODSTEER_UPDATE_CHECK
                    </span>
                  {/if}
                </span>
                <input
                  type="checkbox"
                  checked={preferences.updateChecksEnabled}
                  disabled={!updates.permitted}
                  onchange={(event) =>
                    preferences.setUpdateChecksEnabled(event.currentTarget.checked)}
                  class="size-4 accent-primary disabled:opacity-40"
                />
              </label>

              <div class="mt-3 flex items-center gap-3">
                <button
                  type="button"
                  disabled={!preferences.updateChecksEnabled || !updates.permitted || updates.checking}
                  onclick={() => void updates.refresh(true)}
                  class="state-layer h-8 rounded-xs border border-outline px-3 text-label-large
                         text-on-surface-variant transition-colors duration-150
                         disabled:pointer-events-none disabled:opacity-40"
                >
                  {updates.checking ? 'Checking…' : 'Check now'}
                </button>

                <span class="text-body-small text-on-surface-variant/80">
                  {#if !preferences.updateChecksEnabled}
                    Nothing is sent while this is off.
                  {:else if updates.status?.state === 'available'}
                    {updates.status.latest} is available.
                  {:else if updates.status?.state === 'current'}
                    You are on the latest release.
                  {:else if updates.status?.state === 'unknown'}
                    Could not reach GitHub — that is not a problem with your cluster.
                  {/if}
                </span>
              </div>
            </div>

            <div>
              <h3 class="text-title-medium text-on-surface">Sound on a new finding</h3>
              <p class="mt-0.5 text-body-small text-on-surface-variant">
                Plays once when a warning or critical finding appears that was not there before.
                A problem that persists is announced once, not on every refresh, and anything
                snoozed stays silent.
              </p>

              <div class="mt-3 flex gap-2">
                {#each [true, false] as choice (choice)}
                  <button
                    type="button"
                    onclick={() => preferences.setAlertSoundsEnabled(choice)}
                    aria-pressed={preferences.alertSoundsEnabled === choice}
                    class="state-layer h-9 min-w-24 rounded-xs border px-4 text-label-large
                           transition-colors duration-150 ease-standard
                           {preferences.alertSoundsEnabled === choice
                             ? 'border-transparent bg-secondary-container text-on-secondary-container'
                             : 'border-outline text-on-surface-variant'}"
                  >
                    {choice ? 'On' : 'Off'}
                  </button>
                {/each}
              </div>

              {#if !alertPlayer.available}
                <p class="mt-2 text-body-small text-warning">
                  This machine has no audio output PodSteer can reach, so nothing will be heard.
                </p>
              {/if}
            </div>

            <div class="border-t border-outline-variant pt-5">
              <h3 class="text-title-medium text-on-surface">Sound per severity</h3>
              <p class="mt-0.5 text-body-small text-on-surface-variant">
                Choosing one plays it, and what you hear here is exactly what you will hear when
                it fires. A batch arriving at once sounds once, at the worst severity in it.
              </p>

              <ul class="mt-3 flex flex-col gap-2">
                {#each ALERT_SEVERITIES as severity (severity)}
                  {@const chosen = preferences.alertSoundFor(severity)}
                  <li class="flex items-center gap-3">
                    <span class="w-20 shrink-0 text-label-large text-on-surface">
                      {SEVERITY_LABELS[severity]}
                    </span>

                    <Select
                      label="Sound for {SEVERITY_LABELS[severity].toLowerCase()} findings"
                      value={chosen}
                      options={SOUND_OPTIONS}
                      class="flex-1"
                      onchange={(id) => preferences.setAlertSound(severity, id)}
                    />

                    <!-- Hearing it again without reassigning it: the picker
                         previews on change, which is no use to somebody
                         comparing the one they already have. -->
                    <button
                      type="button"
                      onclick={() => void alertPlayer.play(chosen)}
                      disabled={chosen === SILENT}
                      aria-label="Play the {SEVERITY_LABELS[severity].toLowerCase()} sound"
                      title="Play"
                      class="state-layer flex size-8 shrink-0 items-center justify-center rounded-full
                             text-on-surface-variant transition-colors duration-100
                             hover:bg-surface-container hover:text-on-surface
                             disabled:pointer-events-none disabled:opacity-38"
                    >
                      <Play class="size-4" strokeWidth={2} />
                    </button>
                  </li>
                {/each}
              </ul>
            </div>

            <!-- Said plainly rather than discovered: an alarm somebody
                 believes is watching everything, that is watching one tab, is
                 worse than no alarm at all. -->
            <p class="border-t border-outline-variant pt-5 text-body-small text-on-surface-variant/70">
              Findings are watched on the cluster whose tab is open, whichever view you are
              reading. Clusters open in other tabs are assessed when you return to them.
            </p>
          </section>

        {:else if section === 'data'}
          <section>
            <h3 class="text-title-medium text-on-surface">Local history</h3>
            <p class="mt-0.5 text-body-small leading-relaxed text-on-surface-variant">
              Kubernetes reports only the present, so PodSteer samples each connected cluster
              while it is open and keeps the result on this machine. That is what the dashboard
              charts plot — it covers the time the application has been running, not the whole
              life of the cluster.
            </p>

            <h4 class="mt-4 text-label-large uppercase tracking-wider text-on-surface-variant">
              Keep for
            </h4>
            <div class="mt-2 flex flex-col gap-1.5">
              {#each RETENTION_OPTIONS as option (option.days)}
                <label class="flex cursor-pointer items-start gap-3">
                  <input
                    type="radio"
                    name="retention"
                    value={option.days}
                    checked={historySettings.days === option.days}
                    onchange={() => void historySettings.setRetention(option.days)}
                    class="mt-1 accent-primary"
                  />
                  <span class="flex flex-col">
                    <span class="text-body-medium text-on-surface">{option.label}</span>
                    <span class="text-body-small text-on-surface-variant/70">{option.hint}</span>
                  </span>
                </label>
              {/each}
            </div>

            <!-- Cadence. Disabled when nothing is recorded, because how often
                 to take a sample is not a question when none are taken. -->
            <h4
              class="mt-6 text-label-large uppercase tracking-wider
                     {historySettings.days === 0 ? 'text-on-surface-variant/40' : 'text-on-surface-variant'}"
            >
              Sample every
            </h4>
            <div class="mt-2 flex flex-col gap-1.5" class:opacity-50={historySettings.days === 0}>
              {#each SAMPLING_INTERVALS as option (option.seconds)}
                <label
                  class="flex items-start gap-3 {historySettings.days === 0
                    ? 'cursor-default'
                    : 'cursor-pointer'}"
                >
                  <input
                    type="radio"
                    name="sampling-interval"
                    value={option.seconds}
                    disabled={historySettings.days === 0}
                    checked={historySettings.intervalSeconds === option.seconds}
                    onchange={() => void historySettings.setInterval(option.seconds)}
                    class="mt-1 accent-primary"
                  />
                  <span class="flex flex-col">
                    <span class="text-body-medium text-on-surface">{option.label}</span>
                    <span class="text-body-small text-on-surface-variant/70">{option.hint}</span>
                  </span>
                </label>
              {/each}
            </div>

            <p class="mt-5 rounded-sm border border-outline-variant/50 bg-surface-container px-3 py-2
                      text-body-small leading-relaxed text-on-surface-variant">
              Samples are capacity figures only — no object names, no logs, no manifests. They are
              written to your own configuration directory and are never sent anywhere. Choosing
              <span class="text-on-surface">Don't record</span> erases what has already been kept.
            </p>
          </section>
        {:else if section === 'transfer'}
          <SettingsTransfer />
        {:else}
          <CreditsPane />
        {/if}
      </div>
    </div>
  </div>
{/if}
