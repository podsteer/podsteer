<!--
  What a GitOps controller says about one of its own objects.

  The sections an Argo CD Application or a Flux Kustomization or HelmRelease
  gets above the generic Identity/Labels/Annotations: the controller's verdict
  in the controller's words, where it renders from, and the objects it says it
  manages — each of which opens in this drawer by its Kubernetes Kind,
  verbatim, exactly as a node of the dependency map does.

  ONE GET, NO LISTS, NO SECRETS. Everything here is read from the manifest the
  drawer already fetched. Nothing lists the members to check on them, and
  nothing resolves their live state: the controller's own status is the
  membership it acts on, and this panel quotes it and says when the controller
  wrote it. A member whose kind this cluster does not serve — or this account
  cannot list — is text rather than a link that fails when followed; see
  $lib/reference for why that decision is made once rather than per pane.

  QUOTATION, NOT VERDICT. Synced/OutOfSync and Healthy/Degraded are Argo CD's
  conclusions; Ready=False with a reason is Flux's. PodSteer adds none of its
  own here, and the colour on a Flux condition is asked of the Go domain
  through the same path every other condition takes.
-->
<script lang="ts">
  import DetailSection from './DetailSection.svelte'
  import DetailList, { type DetailRow } from './DetailList.svelte'
  import ColumnDivider from './ColumnDivider.svelte'
  import { follower, type OpenObject, type ServesKind } from '$lib/reference'
  import { formatAge } from '$lib/format'
  import { argoApplication, argoConditionTone, argoTone, shortRevision } from '$lib/gitops/argo'
  import { fluxHelmRelease, fluxKustomization } from '$lib/gitops/flux'
  import { secondsSince, type GitOpsMember, type GitOpsPanel } from '$lib/gitops/panel'

  type RowTone = DetailRow['tone']

  interface Props {
    /** Which controller's object this is, decided by group and kind upstream. */
    panel: GitOpsPanel
    /** The parsed manifest the drawer already holds. */
    manifest: unknown
    /**
     * The object's own namespace.
     *
     * A Flux source reference with no namespace of its own means "mine", and
     * a Flux inventory entry in the same namespace needs no prefix.
     */
    namespace?: string
    /**
     * What the domain made of the Ready condition, when there is one.
     *
     * Passed in rather than decided here: ResourceOverview already asks
     * domain.ClassifyCondition about every condition on the object, and a
     * second reading of Ready=False in this file would be a second
     * implementation of the same verdict.
     */
    readyTone?: RowTone
    /** Whether this cluster serves a kind, so a link is only offered when there is somewhere to go. */
    canOpen?: ServesKind
    /** Follows a reference to the object it names. */
    onopen?: OpenObject
  }

  let { panel, manifest, namespace = '', readyTone, canOpen, onopen }: Props = $props()

  /** Turns a reference into a click handler, or into nothing. See $lib/reference. */
  const follow = $derived(follower(canOpen, onopen))

  const argo = $derived(panel === 'argo-application' ? argoApplication(manifest) : null)
  const kustomization = $derived(panel === 'flux-kustomization' ? fluxKustomization(manifest) : null)
  const helmRelease = $derived(panel === 'flux-helmrelease' ? fluxHelmRelease(manifest) : null)
  /** The two Flux kinds share their Ready, source and inventory shape. */
  const flux = $derived(kustomization ?? helmRelease)

  /** The grid holding the members, so the divider between its columns has something to measure. */
  let pane = $state<HTMLElement | null>(null)

  // --- Time ------------------------------------------------------------------

  function ago(timestamp: string): string {
    const age = formatAge(secondsSince(timestamp))
    return age === '—' ? '—' : `${age} ago`
  }

  /** The wall-clock time, for a tooltip beside an age. */
  function at(timestamp: string): string {
    if (!timestamp) return ''
    const date = new Date(timestamp)
    return Number.isFinite(date.getTime()) ? date.toLocaleString() : timestamp
  }

  /** A timestamp as a row: the age is the value, the clock time is on hover. */
  function when(label: string, timestamp: string): DetailRow {
    return { label, value: ago(timestamp), info: at(timestamp) || undefined }
  }

  // --- Argo CD ---------------------------------------------------------------

  const argoStateRows = $derived.by<DetailRow[]>(() => {
    if (!argo) return []
    const rows: DetailRow[] = []

    if (argo.health.message) {
      rows.push({ label: 'Health message', value: argo.health.message, tone: argoTone(argo.health.status) })
    }
    rows.push({
      label: 'Revision',
      value: shortRevision(argo.sync.revision) || '—',
      info: argo.sync.revision || undefined,
    })
    rows.push(when('Reconciled', argo.reconciledAt))
    rows.push({ label: 'Project', value: argo.project || '—' })

    // Said in the CRD's own terms. "Automated" with neither option is a
    // legitimate setting and reads differently to a manual policy, which is what
    // an Application with no sync policy at all is.
    const options = [argo.syncPolicy.prune && 'prune', argo.syncPolicy.selfHeal && 'self-heal'].filter(Boolean)
    rows.push({
      label: 'Sync policy',
      value: argo.syncPolicy.automated
        ? `automated${options.length > 0 ? ` · ${options.join(' · ')}` : ''}`
        : 'manual — synced only when asked',
    })
    return rows
  })

  /**
   * Where it renders from, one group of rows per source.
   *
   * A multi-source Application numbers its rows so two repositories do not
   * read as one repository listed twice.
   */
  const argoSourceRows = $derived.by<DetailRow[]>(() => {
    if (!argo) return []
    const several = argo.sources.length > 1

    return argo.sources.flatMap((source, index) => {
      const label = (word: string) => (several ? `Source ${index + 1} ${word.toLowerCase()}` : word)
      const rows: DetailRow[] = [{ label: label('Repository'), value: source.repoURL || '—' }]
      if (source.chart) {
        rows.push({ label: label('Chart'), value: source.chart })
      } else {
        rows.push({ label: label('Path'), value: source.path || '—' })
      }
      rows.push({ label: label('Target revision'), value: source.targetRevision || '—' })
      return rows
    })
  })

  const argoDestinationRows = $derived.by<DetailRow[]>(() => {
    if (!argo) return []
    const { server, name, namespace: target } = argo.destination
    return [
      // A destination is addressed by the cluster's registered name OR its
      // API server URL, never both; whichever was written is what is shown.
      { label: 'Cluster', value: name || server || '—', info: name && server ? server : undefined },
      { label: 'Namespace', value: target || '—' },
    ]
  })

  const argoOperationRows = $derived.by<DetailRow[]>(() => {
    const operation = argo?.operation
    if (!operation) return []

    const rows: DetailRow[] = [{ label: 'Phase', value: operation.phase || '—', tone: argoTone(operation.phase) }]
    if (operation.message) rows.push({ label: 'Message', value: operation.message, tone: argoTone(operation.phase) })
    rows.push({
      label: 'Revision',
      value: shortRevision(operation.revision) || '—',
      info: operation.revision || undefined,
    })
    rows.push(when('Started', operation.startedAt))
    if (operation.finishedAt) rows.push(when('Finished', operation.finishedAt))
    return rows
  })

  /**
   * Argo CD's conditions, in their own shape.
   *
   * They carry a type and a message and no status, so the generic
   * conditions list — which prints "status · reason — message" — is the
   * wrong shape for them. The type is the severity, by Argo CD's own naming.
   */
  const argoConditionRows = $derived.by<DetailRow[]>(() =>
    (argo?.conditions ?? []).map((condition) => ({
      label: condition.type,
      value: condition.message || '—',
      tone: argoConditionTone(condition.type),
      info: at(condition.lastTransitionTime) || undefined,
    })),
  )

  // --- Flux ------------------------------------------------------------------

  const fluxStateRows = $derived.by<DetailRow[]>(() => {
    if (!flux) return []
    const rows: DetailRow[] = []

    if (flux.ready) {
      rows.push({
        label: 'Ready',
        value: flux.ready.reason ? `${flux.ready.status} · ${flux.ready.reason}` : flux.ready.status,
        tone: readyTone,
      })
      if (flux.ready.message) rows.push({ label: 'Message', value: flux.ready.message, tone: readyTone })
      // "Since", not "last reconciled": a condition's transition time moves
      // only when its status does, and Flux records no reconcile time.
      rows.push(when('Ready since', flux.ready.since))
    } else {
      rows.push({ label: 'Ready', value: 'not yet reported by Flux' })
    }

    // Said out loud and coloured, for the same reason a CronJob's is: a
    // suspended object looks identical to one that simply has not changed,
    // and "why is my commit not applied" is the question it is asked.
    rows.push({
      label: 'Suspended',
      value: flux.suspended ? 'yes — Flux will not reconcile it until resumed' : 'no',
      tone: flux.suspended ? 'warn' : undefined,
    })
    if (flux.interval) rows.push({ label: 'Interval', value: flux.interval })

    if (kustomization) {
      rows.push({ label: 'Prune', value: kustomization.prune ? 'yes' : 'no' })
      rows.push({ label: 'Applied revision', value: kustomization.lastAppliedRevision || 'nothing applied yet' })
      // Only when it is a different string, because on a healthy object the
      // two are identical and the second row would say nothing. Both are
      // quoted; what the gap means is left to the reader.
      if (kustomization.lastAttemptedRevision && kustomization.lastAttemptedRevision !== kustomization.lastAppliedRevision) {
        rows.push({ label: 'Attempted revision', value: kustomization.lastAttemptedRevision })
      }
    }

    if (helmRelease) {
      rows.push({ label: 'Deployed chart', value: helmRelease.lastAppliedRevision || 'nothing deployed yet' })
      if (helmRelease.appVersion) rows.push({ label: 'App version', value: helmRelease.appVersion })
      if (helmRelease.releaseStatus) {
        rows.push({ label: 'Release', value: helmRelease.releaseStatus, info: "Helm's own status for the latest release" })
      }
      if (helmRelease.lastDeployed) rows.push(when('Last deployed', helmRelease.lastDeployed))
      if (helmRelease.lastAttemptedRevision && helmRelease.lastAttemptedRevision !== helmRelease.lastAppliedRevision) {
        rows.push({ label: 'Attempted chart', value: helmRelease.lastAttemptedRevision })
      }
      if (helmRelease.releaseName) rows.push({ label: 'Release name', value: helmRelease.releaseName })
    }

    if (flux.lastHandledReconcileAt) rows.push(when('Last requested reconcile', flux.lastHandledReconcileAt))
    return rows
  })

  const fluxSourceRows = $derived.by<DetailRow[]>(() => {
    if (!flux) return []
    const rows: DetailRow[] = []

    if (flux.source) {
      // A source with no namespace of its own is in the object's namespace —
      // Flux's rule, and the namespace the link has to open it in.
      const sourceNamespace = flux.source.namespace || namespace
      rows.push({
        label: 'Source',
        value: `${flux.source.kind}/${flux.source.name}`,
        info: sourceNamespace ? `in ${sourceNamespace}` : undefined,
        onclick: follow(flux.source.kind, flux.source.name, sourceNamespace),
      })
    } else {
      rows.push({ label: 'Source', value: '—' })
    }

    if (kustomization) rows.push({ label: 'Path', value: kustomization.path || '—' })
    if (helmRelease) {
      rows.push({ label: 'Chart', value: helmRelease.chart || '—' })
      if (helmRelease.version) rows.push({ label: 'Version', value: helmRelease.version })
    }
    if (flux.targetNamespace) rows.push({ label: 'Target namespace', value: flux.targetNamespace })
    return rows
  })

  // --- Members ---------------------------------------------------------------

  /** The controller's name, for the wording. */
  const controller = $derived(argo ? 'Argo CD' : 'Flux')

  /**
   * What the controller says it manages, or null when it has not said.
   *
   * Null and empty are kept apart on purpose: an Application with no status
   * and a Kustomization that has never applied have not reported; one that
   * applied nothing has, and said so.
   */
  const members = $derived<GitOpsMember[] | null>(argo ? argo.resources : (flux?.inventory ?? null))

  /**
   * The namespace a member is assumed to be in, so only the exceptions carry
   * a prefix: Argo CD's destination, Flux's target namespace or, failing
   * that, the object's own.
   */
  const home = $derived(argo ? argo.destination.namespace : flux?.targetNamespace || namespace)

  /** The chip classes for one of the controller's words, in the drawer's palette. */
  function chip(tone: RowTone): string {
    if (tone === 'critical') return 'bg-error-container text-on-error-container'
    if (tone === 'warn') return 'bg-warning-container text-on-warning-container'
    return 'bg-surface-container-high text-on-surface-variant'
  }

  /** The API coordinates of a member, for the tooltip on its kind. */
  function apiOf(member: GitOpsMember): string {
    const group = member.group || 'core'
    return member.version ? `${group}/${member.version}` : group
  }

  /**
   * The heading's summary: the controller's two words for an Application,
   * Flux's Ready status for the rest.
   */
  const stateHint = $derived.by(() => {
    if (argo) return `${argo.sync.status || 'Unknown'} · ${argo.health.status || 'Unknown'}`
    if (!flux?.ready) return ''
    return flux.suspended ? `Ready ${flux.ready.status} · suspended` : `Ready ${flux.ready.status}`
  })
</script>

{#if argo}
  <DetailSection level="h3" id="gitops-state" title="Argo CD" hint={stateHint}>
    <!--
      The two words the Application is opened for, as chips in the
      controller's own vocabulary and the controller's own grading. Empty
      means the controller has not said, and "Unknown" is what it would have
      said in that case.
    -->
    <div class="mb-3 flex flex-wrap gap-2">
      <span class="rounded-full px-2 py-0.5 text-body-small {chip(argoTone(argo.sync.status))}">
        Sync · {argo.sync.status || 'Unknown'}
      </span>
      <span class="rounded-full px-2 py-0.5 text-body-small {chip(argoTone(argo.health.status))}">
        Health · {argo.health.status || 'Unknown'}
      </span>
    </div>
    <DetailList rows={argoStateRows} />
  </DetailSection>

  <DetailSection level="h3" id="gitops-source" title="Source" hint={argo.sources.length > 1 ? String(argo.sources.length) : ''}>
    <DetailList rows={argoSourceRows} />
  </DetailSection>

  <DetailSection level="h3" id="gitops-destination" title="Destination">
    <DetailList rows={argoDestinationRows} />
  </DetailSection>

  <DetailSection level="h3" id="gitops-operation" title="Last sync" hint={argo.operation?.phase ?? ''}>
    {#if argoOperationRows.length > 0}
      <DetailList rows={argoOperationRows} />
    {:else}
      <p class="text-body-small text-on-surface-variant/70">
        No sync operation has been recorded for this Application.
      </p>
    {/if}
  </DetailSection>

  {#if argoConditionRows.length > 0}
    <DetailSection level="h3" id="gitops-conditions" title="Conditions" hint={String(argoConditionRows.length)}>
      <DetailList rows={argoConditionRows} />
    </DetailSection>
  {/if}
{:else if flux}
  <DetailSection level="h3" id="gitops-state" title="Flux" hint={stateHint}>
    {#if flux.ready}
      <div class="mb-3 flex flex-wrap gap-2">
        <span class="rounded-full px-2 py-0.5 text-body-small {chip(readyTone)}">
          Ready · {flux.ready.status}{flux.ready.reason ? ` · ${flux.ready.reason}` : ''}
        </span>
        {#if flux.suspended}
          <span class="rounded-full px-2 py-0.5 text-body-small {chip('warn')}">Suspended</span>
        {/if}
      </div>
    {/if}
    <DetailList rows={fluxStateRows} />
  </DetailSection>

  <DetailSection level="h3" id="gitops-source" title="Source">
    <DetailList rows={fluxSourceRows} />
  </DetailSection>
{/if}

{#if argo || flux}
  <DetailSection
    level="h3"
    id="gitops-members"
    title={argo ? 'Resources' : 'Inventory'}
    hint={members ? String(members.length) : ''}
  >
    {#if members === null}
      {#if helmRelease}
        <!-- Not empty, and not unknown either: the objects exist, in the Helm
             release, which Flux stores in a Secret. Saying so is better than
             a blank that reads as "nothing". -->
        <p class="text-body-small text-on-surface-variant/70">
          Flux keeps no inventory on a HelmRelease. The objects it installed belong to the
          Helm release, which Flux stores in a Secret and which is not read here.
        </p>
      {:else}
        <p class="text-body-small text-on-surface-variant/70">
          {controller} has not reported what this {argo ? 'Application' : 'Kustomization'} manages yet.
        </p>
      {/if}
    {:else if members.length === 0}
      <p class="text-body-small text-on-surface-variant/70">
        {controller} reports nothing under this {argo ? 'Application' : 'Kustomization'}.
      </p>
    {:else}
      <!--
        On the drawer's own grid, like a node's pods: the kind in the label
        column, the name and the controller's words for it beside. A name is
        a link when the navigator can open it and text when it cannot — the
        difference between a CRD this cluster serves and one it does not.
      -->
      <div class="relative">
        <dl class="detail-grid" bind:this={pane}>
          {#each members as member, index (index)}
            {@const go = follow(member.kind, member.name, member.namespace)}
            {@const prefixed = member.namespace !== '' && member.namespace !== home}
            <dt class="min-w-0 truncate text-body-medium text-on-surface" title={apiOf(member)} data-selectable>
              {member.kind}
            </dt>
            <dd class="flex min-w-0 items-center gap-2 text-body-medium text-on-surface-variant">
              {#if go}
                <button
                  type="button"
                  onclick={go}
                  class="resource-link min-w-0 flex-1 truncate text-left"
                  title={member.healthMessage || undefined}
                >{#if prefixed}<span class="text-on-surface-variant/50">{member.namespace}/</span>{/if}{member.name}</button>
              {:else}
                <span
                  class="min-w-0 flex-1 truncate"
                  title="{member.kind} is not a kind this cluster's navigator can open"
                >{#if prefixed}<span class="text-on-surface-variant/50">{member.namespace}/</span>{/if}{member.name}</span>
              {/if}
              {#if member.requiresPruning}
                <!-- Argo CD's own flag: live in the cluster, gone from Git. -->
                <span class="shrink-0 rounded-full px-1.5 py-0.5 text-label-small {chip('warn')}">to prune</span>
              {/if}
              {#if member.sync}
                <span class="shrink-0 rounded-full px-1.5 py-0.5 text-label-small {chip(argoTone(member.sync))}">
                  {member.sync}
                </span>
              {/if}
              {#if member.health}
                <span
                  class="shrink-0 rounded-full px-1.5 py-0.5 text-label-small {chip(argoTone(member.health))}"
                  title={member.healthMessage || undefined}
                >
                  {member.health}
                </span>
              {/if}
            </dd>
          {/each}
        </dl>

        <ColumnDivider {pane} />
      </div>

      <!--
        Provenance, stated: this is the controller's list and its time, not a
        fresh read of the cluster. A member deleted a minute ago is still
        here until the controller notices, and that is the honest thing to
        show under this heading.
      -->
      <p class="mt-3 text-body-small text-on-surface-variant/60">
        {#if argo}
          As reported by Argo CD{argo.reconciledAt ? `, reconciled ${ago(argo.reconciledAt)}` : ''}; not re-read from the cluster.
        {:else}
          As recorded by Flux{kustomization?.lastAppliedRevision ? ` for revision ${kustomization.lastAppliedRevision}` : ''}; not re-read from the cluster.
        {/if}
      </p>
    {/if}
  </DetailSection>
{/if}
