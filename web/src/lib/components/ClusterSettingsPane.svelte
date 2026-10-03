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

  NOTHING ON THIS PANE ITSELF SENDS A QUERY — but what it records now decides
  whether a chart may. Turning a cluster on here means PodSteer may send PromQL
  it composed to that cluster's monitoring backend, through the API server's
  own proxy on your credential, when you open a chart or press its control and
  never on the refresh tick. The backend logs those expressions and your
  cluster's audit log records each as a `get` on that Service; SECURITY.md
  says so in full, and the hints below say the short version where the choice
  is made rather than only where it is documented.
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
  import { backendOptions, backendValue, listMetricsBackends, parseBackendValue, type BackendCandidate } from '$lib/metricsBackends'
  import { toApiError } from '$lib/api/errors'
  import { Database, TriangleAlert } from '@lucide/svelte'

  const store = clusterSettings

  // The hints are said ONCE, under the control, for the value chosen; the
  // options carry labels only, or the trigger and the line under it repeat
  // the same sentence.
  const MODE_OPTIONS = METRICS_QUERY_MODES.map((option) => ({ value: option.value, label: option.label }))
  const FLEET_OPTIONS = FLEET_POLICIES.map((option) => ({ value: option.value, label: option.label }))

  /**
   * Discovered backends per cluster, read when the picker opens — discovery
   * is a few LISTs on that cluster, worth doing when somebody is choosing and
   * not on every render. Null
   * means this build cannot list them; an open tab is needed to list at all.
   */
  let candidates = $state<Record<string, BackendCandidate[] | null>>({})
  let candidateError = $state<Record<string, string>>({})

  async function loadCandidates(clusterId: string): Promise<void> {
    if (!workspace.sessions.some((session) => session.cluster.id === clusterId)) return
    try {
      candidates[clusterId] = await listMetricsBackends(clusterId)
      delete candidateError[clusterId]
    } catch (error) {
      candidateError[clusterId] = toApiError(error).message
    }
  }

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

  /**
   * Loads every context shown that has not been read yet — NOT only "the
   * first time": the overview loads its own cluster's row before this pane
   * ever opens, and a load guarded on the store being untouched never read
   * the others, which then showed as Off. Loads merge, so asking again is
   * safe. Contexts gone from the kubeconfig are pruned first.
   */
  $effect(() => {
    const ids = asked
    store.prune(workspace.clusters.map((cluster) => cluster.id).concat(workspace.sessions.map((s) => s.cluster.id)))
    const missing = ids.filter((id) => !store.isLoaded(id))
    if (missing.length > 0) void store.load(missing)
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

  function hintFor(options: readonly { value: string; hint: string }[], value: string): string {
    return options.find((option) => option.value === value)?.hint ?? ''
  }

  function preferredLabel(entry: ClusterSettings): string {
    if (!entry.preferredNamespace || !entry.preferredService) return ''
    return `${entry.preferredService} in ${entry.preferredNamespace}`
  }
</script>

<section>
  <h3 class="text-title-medium text-on-surface">Clusters</h3>
  <p class="mt-0.5 text-body-medium leading-relaxed text-on-surface-variant">
    PodSteer charts a few minutes of its own samples, taken while the application is open. Where a
    cluster already runs a monitoring stack, its charts can read a longer history from that instead
    — but only for the clusters you say so for, and only on the terms you set here.
  </p>

  {#if store.settingsState?.notice}
    <p
      class="mt-3 flex items-start gap-2 rounded-sm border border-outline-variant/50
             bg-surface-container px-3 py-2 text-body-medium leading-relaxed text-on-surface-variant"
    >
      <TriangleAlert class="mt-0.5 size-4 shrink-0 text-tertiary" aria-hidden="true" />
      <span>{store.settingsState.notice}</span>
    </p>
  {/if}

  {#if store.error}
    <p class="mt-3 text-body-medium text-error" role="alert">{store.error}</p>
  {/if}

  {#if rows.length === 0}
    <p class="mt-4 text-body-medium text-on-surface-variant">
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
                    disabled={store.busy || !store.writable || !store.isLoaded(entry.clusterId)}
                    class="w-full"
                    onchange={(value) =>
                      void store.save(entry.clusterId, { metricsQueryMode: value })}
                  />
                  <span class="text-body-medium text-on-surface-variant/80">
                    {hintFor(METRICS_QUERY_MODES, entry.metricsQueryMode)}
                  </span>
                </div>

                {#if entry.metricsQueryMode !== 'off'}
                  <!--
                    THE PICKER. "Whichever PodSteer finds" is the state in which
                    no object name is written anywhere; choosing a backend is
                    the one thing on this pane that records a name in the
                    settings file, and the paragraph at the foot says so.
                  -->
                  <div class="flex flex-col gap-1">
                    <Select
                      label="Which backend answers"
                      accessibleName="Which monitoring backend answers for {entry.clusterId}"
                      value={backendValue(entry.preferredNamespace, entry.preferredService)}
                      options={backendOptions(candidates[entry.clusterId] ?? null, entry)}
                      disabled={store.busy || !store.writable || !store.isLoaded(entry.clusterId)}
                      class="w-full"
                      onopen={() => void loadCandidates(entry.clusterId)}
                      onchange={(value) => void store.save(entry.clusterId, parseBackendValue(value))}
                    />
                    <span class="text-body-medium text-on-surface-variant/80">
                      {#if candidateError[entry.clusterId]}
                        Could not list this cluster's backends: {candidateError[entry.clusterId]}
                      {:else if chosen}
                        Pinned: {chosen}. Its namespace and name are in your settings file.
                      {:else if candidates[entry.clusterId] === null}
                        Whichever PodSteer finds. A chart names the backend that answered and lets
                        you pin a different one.
                      {:else}
                        Whichever PodSteer finds — the highest ranked of those listed. Nothing is
                        written to your settings file until you pick one.
                      {/if}
                    </span>
                  </div>

                  <div class="flex flex-col gap-1">
                    <Select
                      label="If it serves several clusters"
                      accessibleName="What to do when the backend for {entry.clusterId} serves several clusters"
                      value={entry.fleetPolicy}
                      options={FLEET_OPTIONS}
                      disabled={store.busy || !store.writable || !store.isLoaded(entry.clusterId)}
                      class="w-full"
                      onchange={(value) => void store.save(entry.clusterId, { fleetPolicy: value })}
                    />
                    <span class="text-body-medium text-on-surface-variant/80">
                      {hintFor(FLEET_POLICIES, entry.fleetPolicy)}
                    </span>
                  </div>
                {/if}

                <!--
                  NOTHING IS SAID ABOUT NODE HISTORY HERE, and its absence is
                  the honest state rather than an omission. ADR 8 was accepted
                  and deliberately not built: `ClusterSettings.NodeHistory`
                  exists, nothing sets it, and no code appends a per-node
                  sample — HistoryPort is keyed by cluster alone. This pane
                  used to render "Per-node history is being recorded for this
                  cluster" behind that flag, which could only ever have been
                  false, and pointed at a control under Data that does not
                  exist. A line that cannot be true is worse than no line: it
                  would have told somebody their nodes were being recorded.
                  Put it back with the feature, not before it.
                -->
              </div>
            </div>
          </div>
        </li>
      {/each}
    </ul>
  {/if}

  <p
    class="mt-5 rounded-sm border border-outline-variant/50 bg-surface-container px-3 py-2
           text-body-medium leading-relaxed text-on-surface-variant"
  >
    A monitoring backend is reached through the API server your kubeconfig already names, with the
    same credentials — no new address, and nothing typed in. Choosing a specific one records that
    <span class="text-on-surface">service’s namespace and name</span> in PodSteer’s settings file;
    that is the only name of anything in a cluster the file ever holds, and leaving the choice on
    “whichever PodSteer finds” means it holds none.
  </p>
</section>
