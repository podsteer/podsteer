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

  ONE THING ON THIS PAGE DOES READ A PAYLOAD, AND IT IS THE ONLY ONE. Choosing
  the release drawer's Values, Manifest or Notes tab fetches ONE revision of
  ONE release — never on render, never when the drawer opens (it opens on an
  Overview built from labels), never on the tick. That is `RevealSecretKey`'s shape rather than a gentler version of it,
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
  import DataTable, { type Column } from '$lib/components/DataTable.svelte'
  import EmptyState from '$lib/components/EmptyState.svelte'
  import ErrorBanner from '$lib/components/ErrorBanner.svelte'
  import KubectlHint from '$lib/components/KubectlHint.svelte'
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
  import { helmGetValues, helmHistory, helmRollback, helmUninstall, helmUpgrade } from '$lib/kubectl'
  import { helmPayloadKey, helmPayloads } from '$stores/helmPayloads.svelte'
  import type { ClusterSession } from '$stores/session.svelte'
  import type { CSVExport } from '$stores/activeTable.svelte'
  import { preferences } from '$stores/preferences.svelte'
  import { isControlColumn } from '$lib/fixedColumns'
  import { matches } from '$lib/query'
  import { sortRows, type SortAccessors } from '$lib/sort'
  import { untrack, type Component } from 'svelte'
  import {
    CircleDot,
    Copy,
    Eye,
    FileCode,
    Info,
    NotebookText,
    Package,
    SlidersHorizontal,
    SquareTerminal,
    X,
  } from '@lucide/svelte'
  import DetailList, { type DetailRow } from '$lib/components/DetailList.svelte'
  import DetailSection from '$lib/components/DetailSection.svelte'
  import HelpButton from '$lib/components/HelpButton.svelte'
  import PaneToolbar from '$lib/components/PaneToolbar.svelte'
  import Select from '$lib/components/Select.svelte'
  import ToolbarButton from '$lib/components/ToolbarButton.svelte'
  import ToolbarToggle from '$lib/components/ToolbarToggle.svelte'
  import YamlPane from '$lib/components/YamlPane.svelte'
  import { copyText } from '$lib/clipboard'
  import { DETAIL_MAX_REM, DETAIL_MAX_SHARE, DETAIL_MIN_REM } from '$stores/preferences.svelte'
  import StatusIndicator from '$lib/components/StatusIndicator.svelte'
  import type { Tone } from '$lib/format'

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

  /**
   * The application's Refresh re-lists, bypassing the Go cache — and only a
   * person pressing it; the tick fetches nothing here. See RBACView.
   */
  let seenRefreshes = untrack(() => session.manualRefreshes)
  $effect(() => {
    const presses = session.manualRefreshes
    if (presses === seenRefreshes) return
    seenRefreshes = presses
    void load(session.cluster.id, session.namespace, true)
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

  // --- The release drawer ----------------------------------------------------
  //
  // THE OVERVIEW COSTS NO NEW READS. Every revision it shows already arrived
  // on the listing — Helm writes one Secret per revision and the LIST returned
  // all of them — so opening the drawer costs exactly nothing, which is the
  // same trade the session timeline makes.

  let opened = $state<HelmRelease | null>(null)

  /** Which revision the rollback command names. Defaults to the one below the
      current, which is what a rollback almost always means, and falls back to
      the current when there is only one. */
  let target = $state(0)

  type DrawerTab = 'overview' | 'values' | 'manifest' | 'notes' | 'commands'
  let tab = $state<DrawerTab>('overview')

  const TABS: { id: DrawerTab; label: string; icon: Component }[] = [
    { id: 'overview', label: 'Overview', icon: Info },
    { id: 'values', label: 'Values', icon: SlidersHorizontal },
    { id: 'manifest', label: 'Manifest', icon: FileCode },
    { id: 'notes', label: 'Notes', icon: NotebookText },
    { id: 'commands', label: 'Commands', icon: SquareTerminal },
  ]

  function isPayloadTab(id: DrawerTab): boolean {
    return id === 'values' || id === 'manifest' || id === 'notes'
  }

  function open(release: HelmRelease): void {
    if (opened) helmPayloads.forget(payloadKeyFor(opened.namespace, opened.name, inspecting))
    opened = release
    const history = release.revisions ?? []
    const previous = history.find((revision) => revision.revision < release.current.revision)
    target = previous?.revision ?? release.current.revision

    // The drawer opens on the Overview, which is built from labels. Opening a
    // drawer is not a request to decode a Secret.
    inspecting = release.current.revision
    tab = 'overview'
  }

  function close(): void {
    // NOTHING IS HELD AFTER THE DRAWER CLOSES — decision 6 states that in as
    // many words, and it covers the masked manifest too: masked or not, it is
    // what a read of somebody's release produced, and keeping it for a pane
    // nobody is looking at is holding a decoded Secret for no reason.
    if (opened) helmPayloads.forget(payloadKeyFor(opened.namespace, opened.name, inspecting))
    opened = null
  }

  // --- The payload ---------------------------------------------------------
  //
  // THE ONE PLACE ON THIS PAGE THAT READS A SECRET'S CONTENTS, and it is
  // reached only from somebody choosing a Values, Manifest or Notes tab, a
  // revision, or the show control — every one an event handler. No $effect,
  // no lifecycle hook, nothing on the tick: an `$effect` that read a payload
  // when the drawer opened would turn opening a pane into a Secret read, which
  // is the pattern Kubernetes' own guidance tells cluster operators to alert
  // on. Choosing the tab IS the deliberate act; a separate Read button after
  // it only asked the same question twice.

  /**
   * NOTHING SURVIVES THIS COMPONENT.
   *
   * `close()` drops the open revision's payload, but closing the drawer is
   * not the only way to leave: this page sits inside an `{#if}` on the view
   * mode, so switching to Pods with the drawer still open destroys the
   * component and `close()` never runs. The values would expire on their own
   * timer, but the masked manifest and the chart facts would sit in the
   * singleton store until the next window blur — and SECURITY.md states that
   * the payload is dropped when the drawer closes. This is what makes that
   * sentence true rather than nearly true.
   *
   * An `$effect` with no reactive reads runs its teardown exactly once, on
   * destroy, which is the shape wanted here.
   */
  $effect(() => {
    return () => helmPayloads.forgetAll()
  })

  /** Which revision the payload tabs show, which is not the rollback target —
      one is what you are reading, the other is what a command would name. */
  let inspecting = $state(0)

  function payloadKeyFor(namespace: string, release: string, revision: number): string {
    return helmPayloadKey(session.cluster.id, namespace, release, revision)
  }

  const payloadKey = $derived(
    opened ? payloadKeyFor(opened.namespace, opened.name, inspecting) : '',
  )
  const payload = $derived(helmPayloads.at(payloadKey))
  const sensitive = $derived(helmPayloads.sensitiveAt(payloadKey))
  const revealed = $derived(helmPayloads.isRevealed(payloadKey))

  /** Reads one revision. Only ever from an event handler. */
  function readPayload(): void {
    if (!opened) return
    void helmPayloads.read(session.cluster.id, opened.namespace, opened.name, inspecting)
  }

  /**
   * Whether showing this tab needs a read: nothing decoded yet, or — for the
   * values and the notes — they were put away since.
   */
  function needsRead(id: DrawerTab): boolean {
    if (!isPayloadTab(id) || payload.loading) return false
    if (!payload.facts) return true
    return id !== 'manifest' && !revealed
  }

  /** Moves to a tab. Choosing a payload tab reads the revision it shows. */
  function selectTab(id: DrawerTab): void {
    tab = id
    if (needsRead(id)) readPayload()
  }

  /** Moves the payload tabs to another revision, dropping whatever the
      previous one decoded — one revision at a time is the rule, and holding
      the last one beside the new one would quietly make it two. */
  function inspect(revision: number): void {
    if (!opened || revision === inspecting) return
    helmPayloads.forget(payloadKeyFor(opened.namespace, opened.name, inspecting))
    inspecting = revision
    if (isPayloadTab(tab)) readPayload()
  }

  /** The show/hide control on Values and Notes: hiding drops them, showing
      reads the revision again. */
  function toggleRevealed(): void {
    if (revealed) helmPayloads.hide(payloadKey)
    else readPayload()
  }

  function onTabKeydown(event: KeyboardEvent): void {
    const step = event.key === 'ArrowRight' ? 1 : event.key === 'ArrowLeft' ? -1 : 0
    if (step === 0 && event.key !== 'Home' && event.key !== 'End') return
    const at = TABS.findIndex((entry) => entry.id === tab)
    const next =
      event.key === 'Home'
        ? 0
        : event.key === 'End'
          ? TABS.length - 1
          : (at + step + TABS.length) % TABS.length
    event.preventDefault()
    selectTab(TABS[next].id)
    document.getElementById(`helm-tab-${TABS[next].id}`)?.focus()
  }

  const revisionOptions = $derived(
    (opened?.revisions ?? []).map((revision) => ({
      value: String(revision.revision),
      label:
        revision.revision === opened?.current.revision
          ? `Revision ${revision.revision} (current)`
          : `Revision ${revision.revision}`,
    })),
  )

  /** Copy's moment of acknowledgement — see ToolbarButton's `active`. */
  let copied = $state(false)
  async function copy(text: string): Promise<void> {
    if (!(await copyText(text))) return
    copied = true
    setTimeout(() => (copied = false), 1200)
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

  /**
   * The chart the upgrade command names, when it is known.
   *
   * ONLY AFTER A REVISION HAS BEEN READ. Chart name is not a label — it lives
   * inside the release payload — so until a payload tab has been opened the
   * command carries a placeholder there too, rather than a name PodSteer
   * guessed from the release name. They are routinely the same and routinely
   * not: one chart installs under as many release names as anybody likes.
   */
  const chartName = $derived(payload.facts?.chart.name ?? '')

  const valuesCommand = $derived(
    opened ? helmGetValues(session.cluster.id, opened.name, commandNamespace) : '',
  )
  const upgradeCommand = $derived(
    opened ? helmUpgrade(session.cluster.id, opened.name, commandNamespace, chartName) : '',
  )

  /** Whether any revision carries Helm's createdAt label — older Helm
      versions never wrote it, and a column of dashes says nothing. */
  const hasCreated = $derived((opened?.revisions ?? []).some((revision) => revision.createdAt))

  /** The Overview's facts, from the listing's labels and — once a payload
      tab has been opened — the chart the payload named. */
  const releaseRows = $derived.by((): DetailRow[] => {
    if (!opened) return []
    const current = opened.current
    const updated = current.modifiedAt || current.createdAt
    const rows: DetailRow[] = [
      { label: 'Status', value: current.status || 'unknown' },
      { label: 'Revision', value: String(current.revision) },
      { label: 'Namespace', value: opened.namespace },
      { label: 'Updated', value: updated ? `${formatClockTime(helmTime(updated))} (${ageOf(updated)} ago)` : '—' },
    ]
    const facts = payload.facts
    if (facts) {
      rows.push(
        { label: 'Chart', value: facts.chart.name || '—' },
        { label: 'Chart version', value: facts.chart.version || '—' },
        { label: 'App version', value: facts.chart.appVersion || '—' },
      )
      if (facts.chart.description) rows.push({ label: 'Description', value: facts.chart.description })
    }
    rows.push({ label: 'Secret', value: current.secretName })
    return rows
  })

  // --- The table -----------------------------------------------------------

  const COLUMNS: Column[] = [
    { id: 'mark', label: 'Status', width: 44, icon: CircleDot },
    { id: 'status', label: 'State', width: 140 },
    { id: 'name', label: 'Release', width: 280, pinned: true },
    { id: 'namespace', label: 'Namespace', width: 180 },
    { id: 'revision', label: 'Revision', width: 110, numeric: true },
    { id: 'history', label: 'History', width: 140 },
    { id: 'updated', label: 'Updated', width: 120, numeric: true },
  ]

  const SORT: SortAccessors<HelmRelease> = {
    mark: (release) => release.current.status,
    status: (release) => release.current.status,
    name: (release) => release.name,
    namespace: (release) => release.namespace,
    revision: (release) => release.current.revision,
    history: (release) => release.revisionCount,
    // Newest first reads as ascending age, so sort on the negated timestamp.
    updated: (release) => -(release.current.modifiedAt || release.current.createdAt || 0),
  }

  const visibleReleases = $derived(
    session.query.terms.length === 0
      ? releases
      : releases.filter((release) =>
          matches(session.query, {
            text: [release.name, release.namespace, release.current.status].join(' '),
            labels: {},
            cluster: session.cluster.id,
          }),
        ),
  )
  const sortedReleases = $derived(sortRows(visibleReleases, session.sort, SORT))
  const pagedReleases = $derived(
    sortedReleases.slice(session.pageStart, session.pageStart + preferences.pageSize),
  )

  $effect(() => {
    session.standaloneCount = visibleReleases.length
  })

  function isColumnVisible(column: Column): boolean {
    const stored = preferences.columns[session.selectedKindId]?.[column.id]?.hidden
    return column.pinned || (stored === undefined ? !column.defaultHidden : !stored)
  }

  /** The CSV export, built from the listing's labels only — never a payload. */
  function exportCSV(): CSVExport {
    const visible = COLUMNS.filter((column) => !isControlColumn(column) && isColumnVisible(column))
    const cell = (release: HelmRelease, id: string): string => {
      switch (id) {
        case 'mark':
        case 'status':
          return release.current.status || 'unknown'
        case 'name':
          return release.name
        case 'namespace':
          return release.namespace
        case 'revision':
          return String(release.current.revision)
        case 'history':
          return countedRevisions(release.revisionCount)
        case 'updated':
          return ageOf(release.current.modifiedAt || release.current.createdAt)
        default:
          return ''
      }
    }
    return {
      columns: visible.map((column) => column.label),
      rows: sortedReleases.map((release) => visible.map((column) => cell(release, column.id))),
    }
  }

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
  function statusTone(status: string): Tone {
    if (status === 'deployed') return 'success'
    if (status === 'failed') return 'error'
    if (status.startsWith('pending') || status === 'uninstalling') return 'warning'
    return 'neutral'
  }

</script>

<svelte:window onkeydown={onKeydown} />

<!-- THE SAME TABLE EVERY LIST USES, so a release list reads like a Pods list:
     the toolbar's search, pager, column chooser, sorting and CSV export. It
     is a table and not a list (`session.hasTable`): this page filters, sorts
     and pages its own rows, and nothing here is fetched on the tick. -->
<DataTable
  kindId={session.selectedKindId}
  columns={COLUMNS}
  isEmpty={pagedReleases.length === 0}
  sort={session.sort}
  onsort={session.toggleSort}
  exportRows={exportCSV}
>
  {#snippet notice()}
    {#if listing?.truncated}
      <p class="border-b border-outline-variant/60 px-6 py-2 text-body-medium text-gauge-warn-ink" role="status">
        More release Secrets exist than were read, so this list is short.
      </p>
    {/if}
    <ErrorBanner error={error} ondismiss={() => (error = null)} class="mx-6 my-3" />
  {/snippet}

  {#snippet empty()}
    {#if view.kind === 'unavailable'}
      <!-- A refusal is not a fault, and it must NEVER render the zero-row
           copy: a refused listing established nothing about the cluster. -->
      <EmptyState title="Helm releases could not be listed" description={view.message} />
    {:else if loading && !listing}
      <EmptyState title="Reading Helm's labels…" description="One metadata list of release Secrets." />
    {:else if session.search && releases.length > 0}
      <EmptyState title="No releases match" description={`Nothing matches "${session.search}".`} />
    {:else if isEmpty}
      <EmptyState
        title="No Helm releases here"
        description={(scope
          ? `Nothing in ${scope} was installed by Helm, or its releases are stored elsewhere.`
          : 'Nothing in this cluster was installed by Helm, or its releases are stored elsewhere.') +
          (argoInstalled ? ' ' + ARGO_NOTE : '')}
      />
    {:else}
      <EmptyState title="Nothing read yet" description="Press Refresh to ask the cluster." />
    {/if}
  {/snippet}

  {#snippet rows(isVisible)}
    {#each pagedReleases as release (`${release.namespace}/${release.name}`)}
      {@const selected = opened?.name === release.name && opened?.namespace === release.namespace}
      <tr
        class="group/row cursor-pointer border-t border-outline-variant/40 transition-colors duration-100
               {selected ? 'bg-row-open-secondary' : 'bg-surface hover:bg-surface-container-low'}"
        onclick={() => open(release)}
      >
        {#if isVisible('mark')}
          <td class="overflow-hidden py-1.5 pr-3 pl-6">
            <StatusIndicator tone={statusTone(release.current.status)} label={release.current.status || 'unknown'} icon={Package} />
          </td>
        {/if}
        {#if isVisible('status')}
          <!-- Helm's own word, verbatim; an unseen status renders as itself. -->
          <td class="truncate px-3 py-1.5 text-on-surface-variant">{release.current.status || 'unknown'}</td>
        {/if}
        <td class="px-3 py-1.5" title={release.name}>
          <span class="truncate font-medium text-on-surface">{release.name}</span>
        </td>
        {#if isVisible('namespace')}
          <td class="truncate px-3 py-1.5 text-on-surface-variant">{release.namespace}</td>
        {/if}
        {#if isVisible('revision')}
          <td class="truncate px-3 py-1.5 text-right tabular-nums text-on-surface-variant">
            {release.current.revision}
          </td>
        {/if}
        {#if isVisible('history')}
          <td class="truncate px-3 py-1.5 text-on-surface-variant">
            {countedRevisions(release.revisionCount)}
          </td>
        {/if}
        {#if isVisible('updated')}
          <td class="truncate px-3 py-1.5 text-right tabular-nums text-on-surface-variant">
            {ageOf(release.current.modifiedAt || release.current.createdAt)}
          </td>
        {/if}
      </tr>
    {/each}
  {/snippet}
</DataTable>

{#if opened}
  <!-- THE SAME PANEL AS AN OBJECT'S DETAILS — shell, header, tabs and
       width — so a release opens like a Deployment does. What each tab
       costs, and why nothing here performs a Helm command, is under the (?)
       as the `helm-release` topic rather than written on every open. -->
  <button
    type="button"
    aria-label="Close release"
    tabindex="-1"
    class="fixed inset-0 z-40 cursor-default bg-scrim/30"
    onclick={close}
  ></button>

  <div
    style="width: min({DETAIL_MAX_SHARE * 100}vw, clamp({DETAIL_MIN_REM}rem, {preferences.detailWidthFraction *
      100}vw, {DETAIL_MAX_REM}rem))"
    class="fixed top-0 right-0 bottom-0 z-50 flex flex-col
           border-l border-outline-variant/60 bg-surface shadow-level-3"
    role="dialog"
    aria-label="Helm release {opened.name}"
  >
    <header class="flex shrink-0 items-center gap-3 border-b border-outline-variant/60 px-4 py-3">
      <Package class="size-5 shrink-0 text-on-surface-variant/60" strokeWidth={1.8} />
      <div class="min-w-0 flex-1">
        <h2 class="truncate text-title-medium font-semibold text-on-surface" title={opened.name} data-selectable>
          {opened.name}
        </h2>
        <p class="truncate text-body-small text-on-surface-variant/70">
          Helm release / {opened.namespace}
        </p>
      </div>
      <div class="flex shrink-0 items-center gap-1">
        <HelpButton topic="helm-release" about="the Helm release panel" />
        <div class="mx-1 h-5 w-px bg-outline-variant/40"></div>
        <button
          type="button"
          onclick={close}
          aria-label="Close release"
          class="state-layer grid size-8 shrink-0 place-items-center rounded-full
                 text-on-surface-variant transition-colors duration-100 hover:bg-surface-container hover:text-on-surface"
        >
          <X class="size-4" strokeWidth={2} />
        </button>
      </div>
    </header>

    <div
      class="flex shrink-0 border-b border-outline-variant/60 bg-surface-container-low/50 px-2"
      role="tablist"
      aria-label="Release views"
      tabindex={-1}
      onkeydown={onTabKeydown}
    >
      {#each TABS as entry (entry.id)}
        {@const TabIcon = entry.icon}
        {@const active = tab === entry.id}
        <button
          type="button"
          role="tab"
          id="helm-tab-{entry.id}"
          aria-selected={active}
          aria-controls="helm-panel"
          tabindex={active ? 0 : -1}
          onclick={() => selectTab(entry.id)}
          class="flex items-center gap-1.5 border-b-2 px-3 py-2 text-label-medium font-medium
                 transition-colors duration-100
                 {active
                   ? 'border-primary text-primary'
                   : 'border-transparent text-on-surface-variant hover:text-on-surface hover:border-outline-variant/50'}"
        >
          <TabIcon class="size-3.5" strokeWidth={active ? 2 : 1.8} />
          {entry.label}
        </button>
      {/each}
    </div>

    <div
      class="flex min-h-0 flex-1 flex-col overflow-auto bg-surface-container-lowest"
      id="helm-panel"
      role="tabpanel"
      aria-labelledby="helm-tab-{tab}"
    >
      {#if tab === 'overview'}
        <!-- Labels only: every row arrived on the listing. -->
        <div class="flex flex-col gap-6 p-4">
          <DetailSection level="h3" id="helm-release" title="Release">
            <DetailList rows={releaseRows} />
          </DetailSection>

          <DetailSection
            level="h3"
            id="helm-history"
            title="History"
            hint={countedRevisions(opened.revisionCount)}
          >
            <table class="w-full border-collapse text-body-medium">
              <thead>
                <tr class="text-left text-label-medium text-on-surface-variant/70">
                  <th class="w-8 py-1.5 font-medium"><span class="sr-only">Status</span></th>
                  <th class="py-1.5 pr-3 font-medium">Revision</th>
                  <th class="py-1.5 pr-3 font-medium">Status</th>
                  {#if hasCreated}<th class="py-1.5 pr-3 font-medium">Created</th>{/if}
                  <th class="py-1.5 font-medium">Updated</th>
                </tr>
              </thead>
              <tbody>
                {#each opened.revisions ?? [] as revision (revision.secretName)}
                  <tr class="border-t border-outline-variant/40">
                    <td class="py-1.5">
                      <StatusIndicator
                        tone={statusTone(revision.status)}
                        label={revision.status || 'unknown'}
                        icon={Package}
                      />
                    </td>
                    <td class="py-1.5 pr-3 tabular-nums text-on-surface">
                      {revision.revision}
                      {#if revision.revision === opened.current.revision}
                        <span class="ml-1.5 text-body-small text-on-surface-variant/60">current</span>
                      {/if}
                    </td>
                    <td class="py-1.5 pr-3 text-on-surface-variant">{revision.status || 'unknown'}</td>
                    {#if hasCreated}
                      <td class="py-1.5 pr-3 tabular-nums text-on-surface-variant">
                        {formatClockTime(helmTime(revision.createdAt))}
                      </td>
                    {/if}
                    <!-- An absent modifiedAt is the ORDINARY case: Helm
                         writes the label only when a revision is updated in
                         place. A dash, never the creation time. -->
                    <td class="py-1.5 tabular-nums text-on-surface-variant">
                      {formatClockTime(helmTime(revision.modifiedAt))}
                    </td>
                  </tr>
                {/each}
              </tbody>
            </table>
          </DetailSection>
        </div>
      {:else if tab === 'commands'}
        <!-- Shown, never run: see the `helm-release` topic. -->
        <div class="flex flex-col gap-3 p-4">
          <KubectlHint label="helm get values" command={valuesCommand} />
          <KubectlHint label="helm upgrade" command={upgradeCommand} />
          <div class="flex flex-col gap-2">
            <div class="flex items-center gap-3">
              <span class="text-label-medium text-on-surface-variant" aria-hidden="true">Roll back to</span>
              <Select
              label="Roll back to"
              value={String(target)}
              options={revisionOptions}
                onchange={(value) => (target = Number(value))}
                class="w-fit"
              />
            </div>
            <KubectlHint label="helm rollback" command={rollbackCommand} />
          </div>
          <KubectlHint label="helm history" command={historyCommand} />
          <KubectlHint label="helm uninstall" command={uninstallCommand} />
        </div>
      {:else}
        {@const facts = payload.facts}
        {#snippet payloadActions(text: string)}
          <Select
            label="Revision"
            compact
            value={String(inspecting)}
            options={revisionOptions}
            onchange={(value) => inspect(Number(value))}
          />
          {#if tab !== 'manifest'}
            <!-- THE RE-HIDE CONTROL. Values and notes put themselves away
                 after thirty seconds and on blur; this does it sooner, and
                 pressed again reads the revision once more. -->
            <ToolbarToggle
              icon={Eye}
              label="Show values and notes"
              pressed={revealed}
              title={revealed ? 'Shown — hides after thirty seconds' : 'Hidden — press to read again'}
              onclick={toggleRevealed}
              disabled={payload.loading}
            />
          {/if}
          <ToolbarButton
            icon={Copy}
            label="Copy"
            title={copied ? 'Copied' : 'Copy'}
            active={copied}
            disabled={!text}
            onclick={() => void copy(text)}
          />
        {/snippet}

        {#if payload.error}
          <div
            class="flex items-center gap-2 border-b border-error/20 bg-error-container/50 px-4 py-2
                   text-body-small text-on-error-container"
            role="alert"
          >
            <X class="size-3.5 shrink-0 text-error" strokeWidth={2} />
            <span data-selectable>{payload.error}</span>
          </div>
        {/if}

        {#if payload.loading && !facts}
          <p class="p-4 text-body-medium text-on-surface-variant/70" role="status">
            Reading revision {inspecting}…
          </p>
        {:else if facts && tab === 'manifest'}
          <!-- Arrived MASKED from Go: no timer of its own. -->
          <div class="flex min-h-0 flex-1 flex-col">
            <YamlPane content={facts.manifest} readonly managedFields={false}>
              {#snippet actions()}{@render payloadActions(facts.manifest)}{/snippet}
              {#snippet banner()}
                {#if facts.maskedDocuments > 0}
                  <p class="border-b border-outline-variant/60 px-4 py-1.5 text-body-small text-on-surface-variant">
                    {facts.maskedDocuments === 1
                      ? 'One Secret shows its values as their size.'
                      : `${facts.maskedDocuments} Secrets show their values as their size.`}
                  </p>
                {/if}
              {/snippet}
            </YamlPane>
          </div>
        {:else if facts && tab === 'values' && sensitive?.values}
          <div class="flex min-h-0 flex-1 flex-col">
            <YamlPane content={sensitive.values} readonly managedFields={false}>
              {#snippet actions()}{@render payloadActions(sensitive.values)}{/snippet}
            </YamlPane>
          </div>
        {:else if facts}
          <!-- Notes, and every empty or hidden state, share one plain pane. -->
          {@const text = tab === 'notes' ? (sensitive?.notes ?? '') : ''}
          <PaneToolbar>
            {#snippet trailing()}{@render payloadActions(text)}{/snippet}
          </PaneToolbar>
          {#if !sensitive}
            <!-- HIDDEN IS A REAL STATE AND SAYS SO: an empty pane would read
                 as a release with no values, which is a different fact. -->
            <p class="p-4 text-body-medium text-on-surface-variant/70">
              Hidden. Press <Eye class="inline size-3.5 align-[-2px]" strokeWidth={1.8} /> to read revision {inspecting} again.
            </p>
          {:else if tab === 'values'}
            <p class="p-4 text-body-medium text-on-surface-variant/70">
              No values of its own — this revision uses the chart's defaults.
            </p>
          {:else if text}
            <pre
              class="min-h-0 flex-1 overflow-auto p-4 font-mono text-body-small whitespace-pre-wrap text-on-surface"
              data-selectable>{text}</pre>
          {:else}
            <p class="p-4 text-body-medium text-on-surface-variant/70">This chart renders no notes.</p>
          {/if}
        {/if}
      {/if}
    </div>
  </div>
{/if}
