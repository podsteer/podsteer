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
  import {
    preferences,
    REFRESH_INTERVALS,
    PAGE_SIZES,
    THEME_PREFERENCES,
    THEME_LABELS,
    type PageSize,
  } from '$stores/preferences.svelte'
  import {
    historySettings,
    RETENTION_OPTIONS,
    SAMPLING_INTERVALS,
  } from '$stores/history.svelte'
  import { accelerator } from '$lib/platform'
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
  import { RefreshCw, Palette, Database, Scale, Bell, Play, X } from '@lucide/svelte'

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
    { id: 'notifications', label: 'Notifications', icon: Bell },
    { id: 'data', label: 'Data', icon: Database },
    { id: 'credits', label: 'Credits', icon: Scale },
  ] as const

  type SectionID = (typeof SECTIONS)[number]['id']

  let section = $state<SectionID>('refresh')

  /** Loads the history settings the first time Settings is opened. */
  $effect(() => {
    if (open && !historySettings.loaded) void historySettings.load()
  })

  function onKeydown(event: KeyboardEvent): void {
    if (event.key === 'Escape' && open) onclose()
  }

</script>

<svelte:window onkeydown={onKeydown} />

{#if open}
  <button
    type="button"
    aria-label="Close settings"
    tabindex="-1"
    class="fixed inset-0 z-40 cursor-default bg-scrim/40"
    onclick={onclose}
  ></button>

  <div
    class="fixed top-1/2 left-1/2 z-50 flex h-[36rem] max-h-[85vh] w-[52rem] max-w-[92vw]
           -translate-x-1/2 -translate-y-1/2 overflow-hidden rounded-sm border
           border-outline-variant bg-surface-container-high shadow-level-3"
    role="dialog"
    aria-modal="true"
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
                or press {accelerator('R')} at any time
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
              <h3 class="text-title-medium text-on-surface">Sidebar</h3>
              <label class="mt-3 flex cursor-pointer items-center gap-3 text-body-medium text-on-surface">
                <input
                  type="checkbox"
                  checked={!preferences.navigatorCollapsed}
                  onchange={preferences.toggleNavigator}
                  class="accent-primary"
                />
                Show the resource navigator
                <span class="text-body-small text-on-surface-variant/70">{accelerator('B')}</span>
              </label>
            </div>
          </section>
        {:else if section === 'notifications'}
          <section class="flex flex-col gap-6">
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
        {:else}
          <CreditsPane />
        {/if}
      </div>
    </div>
  </div>
{/if}
