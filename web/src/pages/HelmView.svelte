<!--
  What Helm has installed in this cluster.

  EVERY ROW HERE IS A LABEL HELM WROTE, and no release payload is read at all.
  A Helm release lives in a Secret of type `helm.sh/release.v1` holding a
  base64'd gzip'd megabyte — the chart, the values and the rendered manifest —
  and Helm labels every one of those Secrets with the release name, the
  revision, the status and two timestamps. So this page is built from a
  metadata LIST that transfers no Secret contents whatsoever, which is what
  makes it possible at all under the Secrets doctrine (ADR 3, and ADR 6 which
  applies it here).

  WHAT THAT COSTS IS ON SCREEN RATHER THAN HIDDEN. Chart name, chart version
  and app version are NOT labels — they exist only inside the payload, and
  Kubernetes offers no server-side projection that would fetch them without
  fetching everything around them. `helm list` prints CHART and APP VERSION;
  this page deliberately does not, and says so, because filling those two
  columns would mean reading every release's payload on page open, which is
  the bulk Secret read the whole design refuses.

  NOTHING HERE POLLS. `ClusterSession`'s tick has a case for this view that
  fetches nothing at all: a metadata LIST of Secrets every ten seconds is six
  `list secrets` lines a minute in the operator's audit log for as long as the
  page is open — the Secrets doctrine's own signature with the bytes removed
  and the pattern intact. The page reads when it opens and when somebody
  presses Refresh, which bypasses the Go cache; a write PodSteer made drops
  that cache, so the next look is fresh without anything asking.

  ONE THING ON THIS PAGE DOES READ A PAYLOAD, AND IT IS THE ONLY ONE. The
  release pane's Read control fetches ONE revision of ONE release, on an
  explicit click — never on render, never when the drawer opens, never on the
  tick. That is `RevealSecretKey`'s shape rather than a gentler version of it,
  and it inherits the whole discipline: the values and the notes are
  re-hideable, expire after thirty seconds, and go on window blur, through the
  same holder a revealed Secret key uses ($stores/revealHolder). The rendered
  manifest arrives already MASKED — every Secret document inside it had its
  values replaced with their decoded size in the Go adapter, because a chart
  that renders `kind: Secret` puts base64 into that string and base64 is an
  encoding rather than a cipher — so it needs no timer of its own.

  ROLLBACK AND UNINSTALL ARE NOT PERFORMED, and that is a decision rather than
  a gap. A Helm rollback re-renders a previous revision, diffs it against what
  is live, applies the difference, deletes what the new manifest drops and
  writes a fresh release Secret; re-implementing that means re-implementing
  Helm, and getting it subtly wrong deletes production objects. Shelling out
  to the operator's own `helm` was refused too — starting a program on
  somebody's machine as a side effect of opening a page is a commitment this
  application makes only through the terminal pane they opened themselves. So
  the drawer shows the command and the operator runs it, which keeps the
  read-only guard coherent: that guard governs PodSteer's own writes, and a
  command somebody types is theirs.
-->
<script lang="ts">
  import Button from '$lib/components/Button.svelte'
  import Card from '$lib/components/Card.svelte'
  import EmptyState from '$lib/components/EmptyState.svelte'
  import ErrorBanner from '$lib/components/ErrorBanner.svelte'
  import KubectlHint from '$lib/components/KubectlHint.svelte'
  import Radio from '$lib/components/Radio.svelte'
  import { toApiError, type ApiError } from '$lib/api/errors'
  import { ALL_NAMESPACES, listHelmReleases, type HelmListing, type HelmRelease } from '$lib/api/client'
  import { escapeLayer, type EscapeClaim } from '$lib/escape'
  import { formatAge, formatClockTime } from '$lib/format'
  import {
    ARGO_NOTE,
    countedRevisions,
    helmState,
    helmTime,
    servesArgoApplications,
    showsEmptyCopy,
  } from '$lib/helm'
  import { helmHistory, helmRollback, helmUninstall } from '$lib/kubectl'
  import { modal } from '$lib/modal'
  import { helmPayloadKey, helmPayloads } from '$stores/helmPayloads.svelte'
  import type { ClusterSession } from '$stores/session.svelte'
  import { Eye, EyeOff, Package, X } from '@lucide/svelte'

  interface Props {
    session: ClusterSession
  }

  let { session }: Props = $props()

  let listing = $state<HelmListing | null>(null)
  let loading = $state(false)
  let error = $state<ApiError | null>(null)

  /** Which cluster and namespace the listing on screen is about. */
  let listedFor = $state('')

  /**
   * Counts the reads issued, so a slow one cannot overwrite a later answer.
   *
   * The same guard `ClusterSession` puts on its consumption reads and
   * `RBACView` on its rules read. It matters here for the same reason: a
   * namespace changed twice quickly would otherwise land the first
   * namespace's releases under the second one's heading.
   */
  let generation = 0

  async function load(clusterId: string, namespace: string, refresh: boolean): Promise<void> {
    listedFor = `${clusterId} ${namespace}`
    loading = true
    error = null
    const issued = ++generation
    try {
      const answer = await listHelmReleases(clusterId, namespace, refresh)
      if (issued !== generation) return
      listing = answer
    } catch (cause) {
      if (issued !== generation) return
      listing = null
      error = toApiError(cause)
    } finally {
      if (issued === generation) loading = false
    }
  }

  /**
   * One read when the page opens, and one more whenever the tab's namespace
   * changes — never on the refresh tick.
   *
   * Keyed on the PAIR rather than on a boolean, because "what has Helm
   * installed here" is a different question in every namespace and an answer
   * left over from the previous one would sit under the new one's heading.
   * `refresh` is false: opening a page is not a request to bypass a cache,
   * and switching between Pods and Helm every twenty seconds must not become
   * the poll tick in disguise.
   */
  $effect(() => {
    const key = `${session.cluster.id} ${session.namespace}`
    if (key === listedFor) return
    void load(session.cluster.id, session.namespace, false)
  })

  const view = $derived(helmState(listing?.status ?? 'listed', listing?.refusal ?? ''))
  const releases = $derived(listing?.releases ?? [])
  const isEmpty = $derived(showsEmptyCopy(listing))

  /**
   * Whether the cluster serves Argo CD's Application kind.
   *
   * FROM THE DISCOVERED KINDS THE SESSION ALREADY HOLDS, and from nothing
   * else. `gitops.ts` detects Argo provenance from an ANNOTATION on one
   * object's manifest, and annotations do not ride list rows — so that signal
   * is simply not available here and reaching for it would cost a read per
   * row. This costs nothing, and it claims only what it can: Argo CD is
   * installed, not that any particular workload is managed by it.
   */
  const argoInstalled = $derived(servesArgoApplications(session.kinds))

  const listedAt = $derived(helmTime(listing?.listedAt ?? 0))

  /** The namespace the page is scoped to, for display and for the commands. */
  const scope = $derived(session.namespace === ALL_NAMESPACES ? '' : session.namespace)

  // --- The history drawer ----------------------------------------------------
  //
  // NO NEW READS. Every revision it shows already arrived on the listing —
  // Helm writes one Secret per revision and the LIST returned all of them —
  // so opening this costs exactly nothing, which is the same trade the session
  // timeline makes.

  let opened = $state<HelmRelease | null>(null)

  /** Which revision the rollback command names. Defaults to the one below the
      current, which is what a rollback almost always means, and falls back to
      the current when there is only one. */
  let target = $state(0)

  function open(release: HelmRelease): void {
    opened = release
    const history = release.revisions ?? []
    const previous = history.find((revision) => revision.revision < release.current.revision)
    target = previous?.revision ?? release.current.revision

    // The pane offers the CURRENT revision to read, and reads nothing yet.
    // Opening a drawer is not a request to decode a Secret.
    inspecting = release.current.revision
    tab = 'chart'
  }

  function close(): void {
    // NOTHING IS HELD AFTER THE DRAWER CLOSES — decision 6 states that in as
    // many words, and it covers the masked manifest too: masked or not, it is
    // what a read of somebody's release produced, and keeping it for a pane
    // nobody is looking at is holding a decoded Secret for no reason.
    if (opened) helmPayloads.forget(payloadKeyFor(opened.namespace, opened.name, inspecting))
    opened = null
  }

  // --- The payload pane ------------------------------------------------------
  //
  // THE ONE PLACE ON THIS PAGE THAT READS A SECRET'S CONTENTS. It is reached
  // from a button's click handler and from nowhere else: no $effect, no
  // lifecycle hook, nothing on the tick. An `$effect` that read a payload when
  // the drawer opened would turn opening a pane into a Secret read, which is
  // the pattern Kubernetes' own guidance tells cluster operators to alert on
  // and the exact thing this whole feature was permitted on the condition of
  // not doing.

  /**
   * NOTHING SURVIVES THIS COMPONENT.
   *
   * `close()` drops the open revision's payload, but closing the drawer is
   * not the only way to leave: this page sits inside an `{#if}` on the view
   * mode, so switching to Pods with the drawer still open destroys the
   * component and `close()` never runs. The values would expire on their own
   * timer, but the masked manifest and the chart facts would sit in the
   * singleton store until the next window blur — and SECURITY.md states, in
   * this same change, that the payload is dropped when the drawer closes.
   * This is what makes that sentence true rather than nearly true.
   *
   * An `$effect` with no reactive reads runs its teardown exactly once, on
   * destroy, which is the shape wanted here.
   */
  $effect(() => {
    return () => helmPayloads.forgetAll()
  })

  /** Which revision the pane is showing, which is not the rollback target —
      one is what you are reading, the other is what a command would name. */
  let inspecting = $state(0)

  type PayloadTab = 'values' | 'notes' | 'manifest' | 'chart'
  let tab = $state<PayloadTab>('chart')

  function payloadKeyFor(namespace: string, release: string, revision: number): string {
    return helmPayloadKey(session.cluster.id, namespace, release, revision)
  }

  const payloadKey = $derived(
    opened ? payloadKeyFor(opened.namespace, opened.name, inspecting) : '',
  )
  const payload = $derived(helmPayloads.at(payloadKey))
  const sensitive = $derived(helmPayloads.sensitiveAt(payloadKey))
  const revealed = $derived(helmPayloads.isRevealed(payloadKey))

  /** Reads one revision. Only ever from a click. */
  function readPayload(): void {
    if (!opened) return
    void helmPayloads.read(session.cluster.id, opened.namespace, opened.name, inspecting)
  }

  /** Moves the pane to another revision, dropping whatever the previous one
      decoded — one revision at a time is the rule, and holding the last one
      beside the new one would quietly make it two. */
  function inspect(revision: number): void {
    if (!opened || revision === inspecting) return
    helmPayloads.forget(payloadKeyFor(opened.namespace, opened.name, inspecting))
    inspecting = revision
    if (tab === 'values' || tab === 'notes') tab = 'chart'
  }

  /** Escape belongs to the innermost open layer. See $lib/escape. */
  let escape = $state<EscapeClaim | null>(null)
  $effect(() => {
    if (!opened) return
    const held = escapeLayer()
    escape = held
    return () => {
      held.release()
      escape = null
    }
  })

  function onKeydown(event: KeyboardEvent): void {
    if (event.key !== 'Escape' || !opened) return
    if (!escape?.owns()) return
    close()
  }

  /**
   * The namespace a command names.
   *
   * The RELEASE's own namespace, never the tab's filter: under "All
   * namespaces" the filter names nothing, and a command that omitted `-n`
   * would run against whatever the operator's current context defaults to.
   */
  const commandNamespace = $derived(opened?.namespace ?? '')

  const rollbackCommand = $derived(
    opened ? helmRollback(session.cluster.id, opened.name, commandNamespace, target) : '',
  )
  const uninstallCommand = $derived(
    opened ? helmUninstall(session.cluster.id, opened.name, commandNamespace) : '',
  )
  const historyCommand = $derived(
    opened ? helmHistory(session.cluster.id, opened.name, commandNamespace) : '',
  )

  /** Now, for ages — read once per render rather than per row. */
  const now = $derived.by(() => Date.now() / 1000)

  function ageOf(seconds: number): string {
    if (!seconds) return '—'
    return formatAge(Math.max(0, now - seconds))
  }

  /**
   * The tone a release status gets.
   *
   * Helm's own vocabulary, mapped for colour only — a status this build has
   * never seen renders as itself in the neutral tone rather than being
   * refused or relabelled.
   */
  function statusTone(status: string): string {
    if (status === 'deployed') return 'bg-success-container text-on-success-container'
    if (status === 'failed') return 'bg-error-container text-on-error-container'
    if (status.startsWith('pending') || status === 'uninstalling') {
      return 'bg-warning-container text-on-warning-container'
    }
    return 'bg-surface-container-high text-on-surface-variant'
  }
</script>

<svelte:window onkeydown={onKeydown} />

<div class="min-h-0 flex-1 overflow-y-auto px-4 py-4">
  <div class="mx-auto flex max-w-5xl flex-col gap-4">
    <Card variant="outlined" class="p-4">
      <div class="mb-3 flex flex-wrap items-start justify-between gap-2">
        <div class="min-w-0">
          <h2 class="text-title-medium font-semibold text-on-surface">
            Helm releases
            {#if scope}
              in <span class="font-mono">{scope}</span>
            {:else}
              across every namespace
            {/if}
          </h2>
          <p class="mt-0.5 text-body-small text-on-surface-variant/70">
            Read from the labels Helm puts on each release Secret, through the metadata API — no
            Secret's contents are transferred. Only Helm's <span class="font-mono">secret</span>
            storage driver is read; a cluster running
            <span class="font-mono">HELM_DRIVER=configmap</span> or the SQL driver keeps its
            releases somewhere this does not look.
          </p>
        </div>

        <div class="flex shrink-0 items-center gap-3">
          <!-- The age of the answer, beside the control that replaces it. The
               listing is held for minutes rather than polled, so a page that
               did not say how old its answer was would be a cache that lies. -->
          {#if listedAt}
            <span class="text-body-small text-on-surface-variant/60">
              as of {formatClockTime(listedAt)}
            </span>
          {/if}
          <Button
            variant="outlined"
            {loading}
            onclick={() => void load(session.cluster.id, session.namespace, true)}
          >
            Refresh
          </Button>
        </div>
      </div>

      <ErrorBanner error={error} ondismiss={() => (error = null)} class="mb-3" />

      {#if view.kind === 'unavailable'}
        <!-- A refusal is not a fault — an account configured exactly as its
             owner intended cannot list Secrets — so it is drawn the way an
             unreadable count is, and only a transient failure is drawn as an
             error worth retrying. It must NEVER render the zero-row copy:
             "Helm has installed nothing here" is a claim about the cluster,
             and a refused listing established nothing about the cluster. -->
        <p
          class="rounded-sm px-3 py-2 text-body-small
                 {view.tone === 'error'
                   ? 'bg-error-container/40 text-on-error-container'
                   : 'bg-surface-container text-gauge-warn'}"
        >
          {view.message}
        </p>
        {#if view.retryable}
          <div class="mt-3">
            <Button
              variant="outlined"
              {loading}
              onclick={() => void load(session.cluster.id, session.namespace, true)}
            >
              Try again
            </Button>
          </div>
        {/if}
      {:else if isEmpty}
        <EmptyState
          title="No Helm releases here"
          description={scope
            ? `Nothing in ${scope} was installed by Helm, or its releases are stored elsewhere.`
            : 'Nothing in this cluster was installed by Helm, or its releases are stored elsewhere.'}
        />
        {#if argoInstalled}
          <!-- A fact about the CLUSTER, from the kinds discovery already
               returned — never a claim about who manages which workload. -->
          <p class="rounded-sm bg-surface-container px-3 py-2 text-body-small text-on-surface-variant">
            {ARGO_NOTE}
          </p>
        {/if}
      {:else if releases.length > 0}
        {#if listing?.truncated}
          <p class="mb-3 rounded-sm bg-warning-container/40 px-3 py-2 text-body-small text-on-surface">
            More release Secrets exist than were read, so this list is short. Every revision is its
            own Secret, so a cluster keeping a long history accumulates them quickly.
          </p>
        {/if}

        <div class="overflow-x-auto">
          <table class="w-full min-w-[40rem] border-collapse text-body-small">
            <thead>
              <tr class="border-b border-outline-variant text-left text-on-surface-variant/70">
                <th class="py-1.5 pr-4 font-medium">Release</th>
                <th class="py-1.5 pr-4 font-medium">Namespace</th>
                <th class="py-1.5 pr-4 font-medium">Revision</th>
                <th class="py-1.5 pr-4 font-medium">Status</th>
                <th class="py-1.5 pr-4 font-medium">Updated</th>
                <th class="py-1.5 font-medium">History</th>
              </tr>
            </thead>
            <tbody>
              {#each releases as release (`${release.namespace}/${release.name}`)}
                <tr class="border-b border-outline-variant/40">
                  <td class="py-1.5 pr-4">
                    <button
                      type="button"
                      class="state-layer cursor-pointer rounded-sm px-1 font-mono text-primary hover:underline"
                      onclick={() => open(release)}
                    >
                      {release.name}
                    </button>
                  </td>
                  <td class="py-1.5 pr-4 font-mono text-on-surface-variant">{release.namespace}</td>
                  <td class="py-1.5 pr-4 tabular-nums text-on-surface">{release.current.revision}</td>
                  <td class="py-1.5 pr-4">
                    <!-- Helm's own word, verbatim. A status this build has
                         never seen renders as itself in the neutral tone. -->
                    <span class="rounded-full px-1.5 py-0.5 font-mono text-label-small {statusTone(release.current.status)}">
                      {release.current.status || 'unknown'}
                    </span>
                  </td>
                  <td class="py-1.5 pr-4 tabular-nums text-on-surface-variant">
                    {ageOf(release.current.modifiedAt || release.current.createdAt)}
                  </td>
                  <td class="py-1.5 text-on-surface-variant">
                    {countedRevisions(release.revisionCount)}
                  </td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>

        <!-- The stated cost of not reading a payload, said where the columns
             would have been rather than left as an absence somebody has to
             notice. -->
        <p class="mt-3 text-body-small text-on-surface-variant/70">
          There is no chart or app version in this table. Both live only inside a release's payload
          — about a megabyte per release — and reading every release's payload to fill two columns
          is exactly the bulk Secret read this page exists to avoid. Open a release and read one
          revision to see them.
        </p>
      {:else if !loading}
        <EmptyState title="Nothing read yet" description="Press Refresh to ask the cluster." />
      {/if}
    </Card>
  </div>
</div>

{#if opened}
  <!-- The history drawer. Every row in it arrived on the listing already, so
       opening it costs no request at all. -->
  <button
    type="button"
    aria-label="Close"
    tabindex="-1"
    class="fixed inset-0 z-[60] cursor-default bg-scrim/40"
    onclick={close}
  ></button>

  <div
    class="fixed inset-y-0 right-0 z-[70] flex w-full max-w-xl flex-col overflow-hidden border-l
           border-outline-variant bg-surface-container-high shadow-level-3"
    role="dialog"
    aria-modal="true"
    use:modal
    aria-label="Helm release {opened.name}"
  >
    <header class="flex shrink-0 items-center gap-3 border-b border-outline-variant/60 px-4 py-3">
      <Package class="size-5 shrink-0 text-on-surface-variant" strokeWidth={1.8} />
      <div class="min-w-0">
        <h2 class="truncate text-title-medium font-semibold text-on-surface">{opened.name}</h2>
        <p class="text-body-small text-on-surface-variant/70">
          Helm release in {opened.namespace}
        </p>
      </div>
      <button
        type="button"
        onclick={close}
        aria-label="Close"
        title="Close"
        class="state-layer ml-auto grid size-8 shrink-0 place-items-center rounded-full
               text-on-surface-variant transition-colors duration-100
               hover:bg-surface-container hover:text-on-surface"
      >
        <X class="size-4" strokeWidth={1.8} />
      </button>
    </header>

    <div class="min-h-0 flex-1 overflow-y-auto px-4 py-4">
      <h3 class="mb-1 text-label-large font-semibold text-on-surface-variant">History</h3>
      <p class="mb-2 text-body-small text-on-surface-variant/70">
        Every revision Helm has kept, newest first. These rows came with the list — opening this
        cost no further read, and nothing on this table decodes a release. Inspect chooses which
        revision the pane below is about; reading its payload is a separate, deliberate press.
      </p>

      <div class="overflow-x-auto">
        <table class="w-full border-collapse text-body-small">
          <thead>
            <tr class="border-b border-outline-variant text-left text-on-surface-variant/70">
              <th class="py-1.5 pr-3 font-medium">Rev</th>
              <th class="py-1.5 pr-3 font-medium">Status</th>
              <th class="py-1.5 pr-3 font-medium">Created</th>
              <th class="py-1.5 pr-3 font-medium">Updated</th>
              <th class="py-1.5 font-medium">Secret</th>
              <th class="py-1.5 pl-3 text-right font-medium">Payload</th>
            </tr>
          </thead>
          <tbody>
            {#each opened.revisions ?? [] as revision (revision.secretName)}
              <tr class="border-b border-outline-variant/40">
                <td class="py-1.5 pr-3 tabular-nums text-on-surface">
                  <!-- Named for assistive technology because the visible
                       content is a bare revision number, which does not say
                       what choosing it would do — and what it does is name a
                       revision in the rollback command shown below, never
                       perform one. No radiogroup role on the column: these are
                       table cells, and a role between the row and the cell
                       breaks the table for anyone navigating it as one. -->
                  <Radio
                    name="helm-rollback-target"
                    value={revision.revision}
                    ariaLabel="Name revision {revision.revision} in the rollback command"
                    dense
                    checked={target === revision.revision}
                    onchange={() => (target = revision.revision)}
                  >
                    {revision.revision}
                    {#if revision.revision === opened.current.revision}
                      <span class="ml-2 text-label-small text-on-surface-variant/60">current</span>
                    {/if}
                  </Radio>
                </td>
                <td class="py-1.5 pr-3">
                  <span class="rounded-full px-1.5 py-0.5 font-mono text-label-small {statusTone(revision.status)}">
                    {revision.status || 'unknown'}
                  </span>
                </td>
                <td class="py-1.5 pr-3 tabular-nums text-on-surface-variant">
                  {formatClockTime(helmTime(revision.createdAt))}
                </td>
                <td class="py-1.5 pr-3 tabular-nums text-on-surface-variant">
                  <!-- An absent modifiedAt is the ORDINARY case: Helm writes
                       the label only when a revision is updated in place. A
                       dash, never the creation time. -->
                  {formatClockTime(helmTime(revision.modifiedAt))}
                </td>
                <td class="py-1.5 font-mono text-body-small break-all text-on-surface-variant/70">
                  {revision.secretName}
                </td>
                <td class="py-1.5 pl-3 text-right">
                  <!-- Selects which revision the pane below is ABOUT. It
                       reads nothing on its own: decoding still costs the
                       explicit press below. -->
                  <button
                    type="button"
                    class="state-layer cursor-pointer rounded-sm px-1.5 py-0.5 text-label-small
                           {inspecting === revision.revision
                             ? 'bg-secondary-container text-on-secondary-container'
                             : 'text-primary hover:underline'}"
                    onclick={() => inspect(revision.revision)}
                  >
                    Inspect
                  </button>
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>

      <!-- === The release payload ==========================================
           EVERYTHING ABOVE THIS POINT COST NO SECRET CONTENTS AT ALL — the
           rows came with the listing, built from the labels Helm writes.
           Everything below reads ONE revision's Secret, on an explicit press,
           and is governed by the doctrine that governs revealing a Secret's
           key: one deliberate act, re-hideable, expiring, gone on blur. -->
      <h3 class="mt-5 mb-1 text-label-large font-semibold text-on-surface-variant">
        Revision {inspecting}
      </h3>

      {#if !payload.facts && !payload.loading && !payload.error}
        <p class="mb-2 text-body-small text-on-surface-variant/70">
          The chart, the values, the notes and the rendered manifest live inside this revision's
          release Secret — about a megabyte, base64'd and gzip'd. Nothing has been read: reading it
          is a deliberate act, one revision at a time, and it leaves a line in your cluster's audit
          log exactly as revealing a Secret's key does.
        </p>
        <Button variant="filled" onclick={readPayload}>Read revision {inspecting}</Button>
      {:else}
        <div class="mb-2 flex flex-wrap items-center gap-2">
          <Button variant="outlined" loading={payload.loading} onclick={readPayload}>
            {payload.facts ? 'Read again' : `Read revision ${inspecting}`}
          </Button>
          {#if revealed}
            <!-- THE RE-HIDE CONTROL, which is the one Freelens does not offer
                 at all: once shown, its reveal cannot be undone. -->
            <button
              type="button"
              class="state-layer flex cursor-pointer items-center gap-1.5 rounded-sm px-2 py-1
                     text-label-small text-on-surface-variant hover:bg-surface-container"
              onclick={() => helmPayloads.hide(payloadKey)}
            >
              <EyeOff class="size-3.5" strokeWidth={1.8} />
              Hide values and notes
            </button>
          {/if}
        </div>

        {#if payload.error}
          <p class="rounded-sm bg-error-container/40 px-3 py-2 text-body-small text-on-error-container">
            {payload.error}
          </p>
        {/if}

        {#if payload.facts}
          {@const facts = payload.facts}
          <div class="mb-2 flex flex-wrap gap-1 border-b border-outline-variant/60">
            {#each [['chart', 'Chart'], ['values', 'Values'], ['notes', 'Notes'], ['manifest', 'Manifest']] as [id, label] (id)}
              <button
                type="button"
                class="cursor-pointer border-b-2 px-2.5 py-1.5 text-label-medium transition-colors
                       {tab === id
                         ? 'border-primary text-primary'
                         : 'border-transparent text-on-surface-variant hover:text-on-surface'}"
                onclick={() => (tab = id as PayloadTab)}
              >
                {label}
              </button>
            {/each}
          </div>

          {#if tab === 'chart'}
            <!-- WHERE THE LIST'S TWO MISSING COLUMNS FINALLY APPEAR. Chart
                 name, chart version and app version are not labels — they
                 exist only in the payload — which is why the list cannot show
                 them without reading every release's Secret on page open. -->
            <dl class="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-body-small">
              <dt class="text-on-surface-variant/70">Chart</dt>
              <dd class="font-mono text-on-surface">{facts.chart.name || '—'}</dd>
              <dt class="text-on-surface-variant/70">Chart version</dt>
              <dd class="font-mono text-on-surface">{facts.chart.version || '—'}</dd>
              <dt class="text-on-surface-variant/70">App version</dt>
              <dd class="font-mono text-on-surface">{facts.chart.appVersion || '—'}</dd>
              {#if facts.chart.description}
                <dt class="text-on-surface-variant/70">Description</dt>
                <dd class="text-on-surface">{facts.chart.description}</dd>
              {/if}
              <dt class="text-on-surface-variant/70">Status</dt>
              <dd class="font-mono text-on-surface">{facts.status || 'unknown'}</dd>
              {#if facts.description}
                <dt class="text-on-surface-variant/70">This revision</dt>
                <dd class="text-on-surface">{facts.description}</dd>
              {/if}
              <dt class="text-on-surface-variant/70">Secret</dt>
              <dd class="font-mono break-all text-on-surface-variant/70">{facts.secretName}</dd>
            </dl>
            <p class="mt-2 text-body-small text-on-surface-variant/70">
              These three are the columns the release list cannot show. They exist only inside the
              payload, so filling them on a list of forty releases would mean reading forty Secrets
              when the page opened.
            </p>
          {:else if tab === 'values' || tab === 'notes'}
            <!-- THE TWO THAT ARE SECRET MATERIAL. Values are where a chart
                 puts a database password, and notes are rendered from the
                 same values — the commonest thing a chart's NOTES.txt does is
                 print how to fetch the admin password, and several print it
                 inline. Both expire after thirty seconds and go on blur. -->
            {#if sensitive}
              <p
                class="mb-2 flex items-start gap-2 rounded-sm bg-warning-container/40 px-3 py-2
                       text-body-small text-on-surface"
              >
                <Eye class="mt-0.5 size-4 shrink-0" strokeWidth={1.8} />
                <span>
                  This may contain credentials — a chart that generates a password puts it here, and
                  a chart's notes are rendered from these same values. It hides itself after thirty
                  seconds and whenever this window loses focus.
                </span>
              </p>
              {#if tab === 'values'}
                {#if sensitive.values}
                  <pre class="max-h-96 overflow-auto rounded-sm bg-surface-container p-3 font-mono text-body-small whitespace-pre-wrap text-on-surface">{sensitive.values}</pre>
                {:else}
                  <!-- An empty `config` means installed with NO overrides,
                       which is a real answer about the release rather than
                       something that failed to load. -->
                  <p class="text-body-small text-on-surface-variant/70">
                    This revision was installed with no values of its own — everything came from the
                    chart's own defaults, which are not read here.
                  </p>
                {/if}
              {:else if sensitive.notes}
                <pre class="max-h-96 overflow-auto rounded-sm bg-surface-container p-3 font-mono text-body-small whitespace-pre-wrap text-on-surface">{sensitive.notes}</pre>
              {:else}
                <p class="text-body-small text-on-surface-variant/70">This chart renders no notes.</p>
              {/if}
            {:else}
              <!-- HIDDEN IS A REAL STATE AND SAYS SO. An empty tab where a
                   value used to be would read as a release with no values,
                   which is a different fact entirely. -->
              <p class="text-body-small text-on-surface-variant/70">
                Hidden. Values and notes are put away after thirty seconds and whenever this window
                loses focus, and they are dropped rather than merely covered — showing them again
                reads the release Secret once more.
              </p>
              <div class="mt-2">
                <Button variant="outlined" loading={payload.loading} onclick={readPayload}>
                  Show again
                </Button>
              </div>
            {/if}
          {:else}
            <!-- THE MANIFEST, AND IT ARRIVED MASKED. A chart that renders
                 `kind: Secret` puts base64 `data:` values into this string,
                 and base64 is an encoding rather than a cipher — so the Go
                 adapter replaced each one with its decoded size before the
                 string crossed the bridge, exactly as the YAML tab does. That
                 is why this tab needs no timer. -->
            {#if facts.maskedDocuments > 0}
              <p class="mb-2 rounded-sm bg-surface-container px-3 py-2 text-body-small text-on-surface-variant">
                {facts.maskedDocuments === 1
                  ? 'One Secret in this manifest has had its values replaced with their size.'
                  : `${facts.maskedDocuments} Secrets in this manifest have had their values replaced with their size.`}
                A rendered chart's Secret carries base64, which is an encoding and not a cipher, so
                nothing here is ever shown encoded. Every other document is exactly as Helm rendered
                it.
              </p>
            {/if}
            {#if facts.manifest}
              <pre class="max-h-96 overflow-auto rounded-sm bg-surface-container p-3 font-mono text-body-small whitespace-pre text-on-surface">{facts.manifest}</pre>
            {:else}
              <p class="text-body-small text-on-surface-variant/70">
                This revision rendered no manifest.
              </p>
            {/if}
          {/if}
        {/if}
      {/if}

      <h3 class="mt-5 mb-1 text-label-large font-semibold text-on-surface-variant">Commands</h3>
      <p class="mb-2 text-body-small text-on-surface-variant/70">
        PodSteer does not perform a Helm rollback or uninstall. Both re-render a chart, diff it
        against what is live, apply the difference and prune what the new manifest drops — that is
        Helm's own work, and doing it approximately deletes production objects. Run these in your
        own shell; nothing here executes them.
      </p>

      <div class="flex flex-col gap-2">
        <KubectlHint label="helm rollback" command={rollbackCommand} />
        <KubectlHint label="helm history" command={historyCommand} />
        <KubectlHint label="helm uninstall" command={uninstallCommand} />
      </div>

      <p class="mt-3 text-body-small text-on-surface-variant/70">
        The rollback command names revision {target}; pick another above to change it.
      </p>
    </div>
  </div>
{/if}
