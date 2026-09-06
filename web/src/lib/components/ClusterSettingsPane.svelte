<!--
  Settings → Clusters: the per-cluster switches the GO PROCESS owns.

  WHY THESE ARE NOT IN ORGANISE, BESIDE THE READ-ONLY MARK. The read-only mark
  is a flag on a GROUP, resolved through a cluster's placement, and it stays
  there on purpose: it guards against the interface's OWN bugs, so it belongs
  with the interface's own arrangement of clusters and travels in the exported
  settings file as a per-group boolean. What is here is a different kind of
  thing — a per-cluster fact about a cluster, which the Go process must act on
  without a window and which decides what reaches the network. That is the
  ownership rule in `app/domain/settings.go`, and it is why these live in
  `settings.json` keyed by context name rather than in the organisation store.

  WHICH ROWS APPEAR. Every open tab's context, so the cluster somebody is
  looking at is always adjustable; plus any other context that already has
  something stored, so a setting made for a cluster whose tab is now closed
  can still be found and changed. A context with nothing stored and no tab
  open is not listed, because a row per context in a large kubeconfig is a
  list nobody can read.

  NOTHING ON THIS PANE SENDS A QUERY, and nothing in this build does. It
  records the choice; reading from a monitoring backend is a separate change.
-->
<script lang="ts">
  import {
    clusterSettings,
    defaultClusterSettings,
    isDefault,
    FLEET_POLICIES,
    METRICS_QUERY_MODES,
  } from '$stores/clusterSettings.svelte'
  import { workspace } from '$stores/workspace.svelte'
  import type { ClusterSettings } from '$lib/api/client'
  import Select from './Select.svelte'
  import { Database, TriangleAlert } from '@lucide/svelte'

  const store = clusterSettings

  const MODE_OPTIONS = METRICS_QUERY_MODES.map((option) => ({
    value: option.value,
    label: option.label,
    hint: option.hint,
  }))

  const FLEET_OPTIONS = FLEET_POLICIES.map((option) => ({
    value: option.value,
    label: option.label,
    hint: option.hint,
  }))

  /**
   * The contexts worth asking the Go process about.
   *
   * Open tabs first, in tab order, then everything else the kubeconfig knows.
   * The second half is what makes "any context with a stored entry"
   * answerable at all: a stored entry is only discoverable by asking for it,
   * and asking is a map lookup in this process rather than a request.
   */
  const asked = $derived.by(() => {
    const open = workspace.sessions.map((session) => session.cluster.id)
    const rest = workspace.clusters
      .map((cluster) => cluster.id)
      .filter((id) => !open.includes(id))
    return [...open, ...rest]
  })

  /** Loads the switches the first time the section is shown. */
  $effect(() => {
    const ids = asked
    if (store.status === 'idle') void store.load(ids)
  })

  /**
   * The rows: every open tab, plus any other context that has something
   * stored. `isDefault` is the same rule the Go side applies before writing,
   * so a listed row that is not an open tab is a row the file genuinely
   * carries.
   */
  const rows = $derived.by(() => {
    const open = new Set(workspace.sessions.map((session) => session.cluster.id))
    return asked
      .map(
        (id) =>
          store.entries.find((entry) => entry.clusterId === id) ?? defaultClusterSettings(id),
      )
      .filter((entry) => open.has(entry.clusterId) || !isDefault(entry))
  })

  function hintFor(options: { value: string; hint: string }[], value: string): string {
    return options.find((option) => option.value === value)?.hint ?? ''
  }

  function preferredLabel(entry: ClusterSettings): string {
    if (!entry.preferredNamespace || !entry.preferredService) return ''
    return `${entry.preferredService} in ${entry.preferredNamespace}`
  }
</script>

<section>
  <h3 class="text-title-medium text-on-surface">Clusters</h3>
  <p class="mt-0.5 text-body-small leading-relaxed text-on-surface-variant">
    PodSteer charts a few minutes of its own samples, taken while the application is open. Where a
    cluster already runs a monitoring stack, its charts can read a longer history from that instead
    — but only for the clusters you say so for, and only on the terms you set here.
  </p>

  {#if store.settingsState?.notice}
    <p
      class="mt-3 flex items-start gap-2 rounded-sm border border-outline-variant/50
             bg-surface-container px-3 py-2 text-body-small leading-relaxed text-on-surface-variant"
    >
      <TriangleAlert class="mt-0.5 size-4 shrink-0 text-tertiary" aria-hidden="true" />
      <span>{store.settingsState.notice}</span>
    </p>
  {/if}

  {#if store.error}
    <p class="mt-3 text-body-small text-error" role="alert">{store.error}</p>
  {/if}

  {#if rows.length === 0}
    <p class="mt-4 text-body-small text-on-surface-variant">
      Nothing to set yet — open a cluster and it will appear here.
    </p>
  {:else}
    <ul class="mt-4 flex flex-col gap-3">
      {#each rows as entry (entry.clusterId)}
        {@const chosen = preferredLabel(entry)}
        <li class="rounded-sm border border-outline-variant/50 bg-surface-container px-3 py-3">
          <div class="flex items-start gap-2.5">
            <Database class="mt-0.5 size-4 shrink-0 text-on-surface-variant" aria-hidden="true" />
            <div class="min-w-0 flex-1">
              <p class="truncate text-body-medium text-on-surface" title={entry.clusterId}>
                {entry.clusterId}
              </p>

              <div class="mt-3 flex flex-col gap-4">
                <div class="flex flex-col gap-1">
                  <Select
                    label="Read history from"
                    accessibleName="Read history from the monitoring stack for {entry.clusterId}"
                    value={entry.metricsQueryMode}
                    options={MODE_OPTIONS}
                    disabled={store.busy || !store.writable}
                    class="w-full"
                    onchange={(value) =>
                      void store.save(entry.clusterId, { metricsQueryMode: value })}
                  />
                  <span class="text-body-small text-on-surface-variant/80">
                    {hintFor(MODE_OPTIONS, entry.metricsQueryMode)}
                  </span>
                </div>

                {#if entry.metricsQueryMode !== 'off'}
                  <div class="flex flex-col gap-1">
                    <span class="text-label-medium text-on-surface-variant">
                      Which backend answers
                    </span>
                    {#if chosen}
                      <p class="text-body-small text-on-surface">{chosen}</p>
                      <!--
                        The one control that clears a name out of
                        settings.json. Offered beside the choice rather than
                        buried, because the choice is the only thing on this
                        pane that writes the name of a cluster object to disk.
                      -->
                      <button
                        type="button"
                        class="self-start text-body-small text-primary underline disabled:opacity-50"
                        disabled={store.busy || !store.writable}
                        onclick={() =>
                          void store.save(entry.clusterId, {
                            preferredNamespace: '',
                            preferredService: '',
                          })}
                      >
                        Use whichever PodSteer finds
                      </button>
                    {:else}
                      <p class="text-body-small text-on-surface-variant/80">
                        Whichever PodSteer finds. Where a cluster runs more than one, the chart
                        names the service that answered and lets you pin a different one — and only
                        then is that service’s name written to your settings file.
                      </p>
                    {/if}
                  </div>

                  <div class="flex flex-col gap-1">
                    <Select
                      label="If it serves several clusters"
                      accessibleName="What to do when the backend for {entry.clusterId} serves several clusters"
                      value={entry.fleetPolicy}
                      options={FLEET_OPTIONS}
                      disabled={store.busy || !store.writable}
                      class="w-full"
                      onchange={(value) => void store.save(entry.clusterId, { fleetPolicy: value })}
                    />
                    <span class="text-body-small text-on-surface-variant/80">
                      {hintFor(FLEET_OPTIONS, entry.fleetPolicy)}
                    </span>
                  </div>
                {/if}

                {#if entry.nodeHistory}
                  <!--
                    Reported, not settable here. Turning node history off
                    erases what has already been recorded, so the control
                    belongs beside retention under Data, where a settings write
                    and a prune are already one act.
                  -->
                  <p class="text-body-small text-on-surface-variant/80">
                    Per-node history is being recorded for this cluster. Change that under Data.
                  </p>
                {/if}
              </div>
            </div>
          </div>
        </li>
      {/each}
    </ul>
  {/if}

  <p
    class="mt-5 rounded-sm border border-outline-variant/50 bg-surface-container px-3 py-2
           text-body-small leading-relaxed text-on-surface-variant"
  >
    A monitoring backend is reached through the API server your kubeconfig already names, with the
    same credentials — no new address, and nothing typed in. Choosing a specific one records that
    <span class="text-on-surface">service’s namespace and name</span> in PodSteer’s settings file;
    that is the only name of anything in a cluster the file ever holds, and leaving the choice on
    “whichever PodSteer finds” means it holds none.
  </p>
</section>
