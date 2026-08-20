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
    THEMES,
    type PageSize,
    type Theme,
  } from '$stores/preferences.svelte'
  import { retention, RETENTION_OPTIONS } from '$stores/history.svelte'
  import { accelerator } from '$lib/platform'
  import Button from './Button.svelte'
  import CreditsPane from './CreditsPane.svelte'
  import { RefreshCw, Palette, Database, Scale, X } from '@lucide/svelte'

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
    { id: 'data', label: 'Data', icon: Database },
    { id: 'credits', label: 'Credits', icon: Scale },
  ] as const

  type SectionID = (typeof SECTIONS)[number]['id']

  let section = $state<SectionID>('refresh')

  /** Loads the retention setting the first time Settings is opened. */
  $effect(() => {
    if (open && !retention.loaded) void retention.load()
  })

  function onKeydown(event: KeyboardEvent): void {
    if (event.key === 'Escape' && open) onclose()
  }

  const THEME_LABELS: Record<Theme, string> = { dark: 'Dark', light: 'Light' }
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
           -translate-x-1/2 -translate-y-1/2 overflow-hidden rounded-xl border
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
          class="state-layer flex items-center gap-2.5 rounded-md px-2.5 py-2 text-left
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
          class="state-layer grid size-8 place-items-center rounded-md text-on-surface-variant
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
              <div class="mt-3 flex gap-2">
                {#each THEMES as theme (theme)}
                  <button
                    type="button"
                    onclick={() => preferences.setTheme(theme)}
                    class="state-layer h-9 min-w-24 rounded-xs border px-4 text-label-large
                           transition-colors duration-150 ease-standard
                           {preferences.theme === theme
                             ? 'border-transparent bg-secondary-container text-on-secondary-container'
                             : 'border-outline text-on-surface-variant'}"
                  >
                    {THEME_LABELS[theme]}
                  </button>
                {/each}
              </div>
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
        {:else if section === 'data'}
          <section>
            <h3 class="text-title-medium text-on-surface">Local history</h3>
            <p class="mt-0.5 text-body-small leading-relaxed text-on-surface-variant">
              Kubernetes reports only the present, so K8Sense samples each connected cluster
              every 30 seconds while it is open and keeps the result on this machine. That is
              what the dashboard charts plot — it covers the time the application has been
              running, not the whole life of the cluster.
            </p>

            <div class="mt-4 flex flex-col gap-1.5">
              {#each RETENTION_OPTIONS as option (option.days)}
                <label class="flex cursor-pointer items-start gap-3">
                  <input
                    type="radio"
                    name="retention"
                    value={option.days}
                    checked={retention.days === option.days}
                    onchange={() => void retention.set(option.days)}
                    class="mt-1 accent-primary"
                  />
                  <span class="flex flex-col">
                    <span class="text-body-medium text-on-surface">{option.label}</span>
                    <span class="text-body-small text-on-surface-variant/70">{option.hint}</span>
                  </span>
                </label>
              {/each}
            </div>

            <p class="mt-5 rounded-lg border border-outline-variant/50 bg-surface-container px-3 py-2
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
