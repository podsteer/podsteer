<!--
  Adding a cluster by asking a cloud CLI the operator already has.

  WHAT THIS IS NOT: it is not PodSteer reading a cloud account. Decision 11
  refuses that and still does — no provider SDK, no cloud credential read here,
  no new host contacted by this process. Every call is made by a CLI the
  operator installed and already signed in to, as them, exactly as an exec
  credential plugin already works. Decision 12 has the whole argument.

  THE FAILURE STATES ARE THE FEATURE. Most operators opening this will hit one
  of two: a CLI that is not installed, or one that is not signed in. Neither is
  a fault in PodSteer, and if either reads like one this pane has failed at the
  only thing it does. So a CLI's own words are shown verbatim, under a heading
  naming which CLI said them, and the word "error" appears nowhere.

  It never writes anything. Choosing a cluster asks the CLI for a kubeconfig
  entry and hands the TEXT back to the dialog, which previews and merges it the
  same way it does a pasted one — one writer of that file, with its backup, its
  atomic rename and its refusal to touch current-context.
-->
<script lang="ts">
  import {
    vendorCancel,
    vendorKubeconfigFor,
    vendorListClusters,
    vendorProviders,
    type VendorCluster,
    type VendorClusterList,
    type VendorProvider,
  } from '$lib/api/client'
  import { toApiError, type ApiError } from '$lib/api/errors'
  import Button from './Button.svelte'
  import { CloudOff, Search, Terminal } from '@lucide/svelte'

  interface Props {
    /** Called with the kubeconfig text a CLI wrote, for the dialog to merge. */
    onkubeconfig: (raw: string) => void
  }

  let { onkubeconfig }: Props = $props()

  let providers = $state<VendorProvider[]>([])
  let chosen = $state<string | null>(null)
  let listing = $state<VendorClusterList | null>(null)
  let busy = $state(false)
  let adding = $state<string | null>(null)
  let failure = $state<ApiError | null>(null)

  /** Which CLIs are on this machine, asked once when the pane appears. */
  $effect(() => {
    void vendorProviders()
      .then((rows) => {
        providers = rows
        // Chosen for them when there is only one thing to choose: a list of
        // one is not a decision.
        const installed = rows.filter((row) => row.installed)
        if (installed.length === 1) chosen = installed[0]?.id ?? null
      })
      .catch(() => {
        providers = []
      })
  })

  const current = $derived(providers.find((row) => row.id === chosen) ?? null)
  const anyInstalled = $derived(providers.some((row) => row.installed))

  async function list(provider: string): Promise<void> {
    busy = true
    failure = null
    listing = null
    try {
      listing = await vendorListClusters(provider)
    } catch (cause) {
      failure = toApiError(cause)
    } finally {
      busy = false
    }
  }

  /** Asks the CLI for this cluster's kubeconfig entry, and hands it over. */
  async function choose(cluster: VendorCluster): Promise<void> {
    if (!chosen) return
    adding = cluster.selection
    failure = null
    try {
      onkubeconfig(await vendorKubeconfigFor(chosen, cluster.selection))
    } catch (cause) {
      failure = toApiError(cause)
    } finally {
      adding = null
    }
  }

  function cancel(): void {
    if (chosen) void vendorCancel(chosen)
  }
</script>

<div class="flex flex-col gap-3">
  {#if providers.length === 0}
    <p class="text-body-medium text-on-surface-variant">
      PodSteer has no cloud CLI it can drive on this build.
    </p>
  {:else}
    <!-- Which CLI. Named, with where it was found, so an operator can see
         WHICH one would run when several are installed. -->
    <div class="flex flex-wrap gap-2">
      {#each providers as provider (provider.id)}
        <button
          type="button"
          disabled={!provider.installed}
          onclick={() => (chosen = provider.id)}
          class="flex items-center gap-2 rounded-sm border px-3 py-2 text-label-large
                 transition-colors duration-100
                 {chosen === provider.id
            ? 'border-primary/50 bg-primary/12 text-primary'
            : 'border-outline-variant/60 text-on-surface-variant hover:bg-surface-container'}
                 disabled:cursor-not-allowed disabled:opacity-50"
        >
          <Terminal class="size-4 shrink-0" strokeWidth={1.8} />
          {provider.label}
        </button>
      {/each}
    </div>

    {#if !anyInstalled}
      <!-- NAMING WHAT WAS LOOKED FOR IS NOT OFFERING TO GET IT. PodSteer never
           installs anything — the local shell's rule, "never installed, only
           found" — so there is no link and no button here, and the sentence
           says who owns the next step. -->
      <p class="text-body-medium text-on-surface-variant">
        PodSteer looked for
        <span class="font-medium text-on-surface">
          {providers.map((provider) => provider.binary).join(' and ')}
        </span>
        on your PATH and found neither. It does not install or download one — if you
        use one of these, install it the way you normally do and it will appear here.
      </p>
    {:else if current}
      {#if current.installed}
        <p class="text-body-small text-on-surface-variant/80">
          Runs <span class="font-mono">{current.path}</span> on this machine, as you.
          PodSteer does not read your cloud credentials and does not contact your
          provider itself.
        </p>
      {/if}

      <div class="flex items-center gap-2">
        <Button variant="outlined" disabled={busy} onclick={() => void list(current.id)}>
          <Search class="size-4" strokeWidth={1.8} />
          {busy ? 'Asking…' : 'List clusters'}
        </Button>
        {#if busy}
          <Button variant="text" onclick={cancel}>Cancel</Button>
        {/if}
      </div>

      {#if failure}
        <!-- A failure to RUN it — missing, timed out, unreadable. The CLI
             declining is not this: that arrives as a listing, below. -->
        <p class="flex items-start gap-2 text-body-small text-gauge-warn">
          <CloudOff class="mt-0.5 size-4 shrink-0" strokeWidth={2} />
          <span>{failure.message}</span>
        </p>
      {/if}

      {#if listing?.status === 'declined'}
        <div class="flex flex-col gap-2 rounded-sm border border-outline-variant/60 p-3">
          <p class="text-body-medium text-on-surface">
            {current.label} isn't signed in on this machine.
          </p>
          <p class="text-body-small text-on-surface-variant">
            PodSteer ran the list command and {current.label} declined. Nothing is
            wrong with PodSteer or with your clusters — sign in the way you normally
            do, then list again.
          </p>
          {#if current.signInHint}
            <p class="text-body-small text-on-surface-variant">{current.signInHint}</p>
          {/if}
          {#if listing.reason}
            <p class="text-label-small text-on-surface-variant/70">
              What {current.label} said
            </p>
            <pre
              class="max-h-40 overflow-auto rounded-sm bg-surface-container p-2 font-mono
                     text-body-small whitespace-pre-wrap text-on-surface-variant">{listing.reason}</pre>
          {/if}
        </div>
      {:else if listing?.status === 'listed' && (listing.clusters?.length ?? 0) === 0}
        <p class="text-body-medium text-on-surface-variant">
          {current.label} answered and listed no clusters. PodSteer passes no region,
          subscription or project of its own — this is what your CLI is currently
          configured to see.
        </p>
      {:else if listing?.status === 'listed'}
        <ul class="flex max-h-64 flex-col gap-1 overflow-y-auto">
          {#each listing.clusters ?? [] as cluster (cluster.selection)}
            <li
              class="flex items-center justify-between gap-3 rounded-sm border
                     border-outline-variant/40 px-3 py-2"
            >
              <span class="min-w-0">
                <span class="block truncate text-body-medium text-on-surface">{cluster.name}</span>
                {#if cluster.params && Object.keys(cluster.params).length > 0}
                  <span class="block truncate text-body-small text-on-surface-variant/70">
                    {Object.values(cluster.params).join(' · ')}
                  </span>
                {/if}
              </span>
              <Button
                variant="outlined"
                disabled={adding !== null}
                onclick={() => void choose(cluster)}
              >
                {adding === cluster.selection ? 'Asking…' : 'Choose'}
              </Button>
            </li>
          {/each}
        </ul>
      {/if}
    {/if}
  {/if}
</div>
