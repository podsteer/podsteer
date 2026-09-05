<!--
  What an operator's controller says about one of its own objects.

  The typed panels PodSteer ships for the five controllers people actually
  run — cert-manager, KEDA, External Secrets, Argo Rollouts and the Trivy
  Operator — selected by API group AND Kind through $lib/operators/panel, the
  mechanism $lib/gitops/panel established. There is no extension API to write
  one of these against, deliberately: see CLAUDE.md, "typed renderers instead
  of a plugin API".

  ONE GET, NO LISTS, NO SECRETS. Everything below is read from the manifest
  the drawer already fetched. Nothing resolves an ExternalSecret's target to
  see what is in it, nothing lists a ScaledObject's TriggerAuthentication, and
  no trigger metadata value that could be a credential is rendered at all.

  QUOTATION, NOT VERDICT — with exactly one exception, stated here so it
  cannot creep. Ready, Active, Degraded, Paused, CRITICAL are the
  controllers' own words in the controllers' own vocabularies, and PodSteer
  draws no conclusion on top of them. The exception is a cert-manager
  Certificate's expiry: status.notAfter and status.renewalTime are DATES, and
  comparing them to the clock is a verdict, so that one question is asked of
  the Go domain (assessCertificateRenewal) where a test argues with it.
-->
<script lang="ts">
  import DetailSection from './DetailSection.svelte'
  import DetailList, { type DetailRow } from './DetailList.svelte'
  import ColumnDivider from './ColumnDivider.svelte'
  import Button from './Button.svelte'
  import RolloutActionDialog from './RolloutActionDialog.svelte'
  import { follower, type OpenObject, type ServesKind } from '$lib/reference'
  import { formatAge } from '$lib/format'
  // secondsSince lives with the GitOps panels because they needed it first.
  // One implementation of "how long ago was this timestamp" rather than two
  // that could round differently in the same drawer.
  import { secondsSince } from '$lib/gitops/panel'
  import type { OperatorPanel } from '$lib/operators/panel'
  import { certManagerCertificate, remainingLabel } from '$lib/operators/certmanager'
  import { kedaScaledObject } from '$lib/operators/keda'
  import { externalSecret } from '$lib/operators/externalsecrets'
  import { argoRollout, rolloutTone } from '$lib/operators/rollouts'
  import { advisoryLink, severityTone, trivyVulnerabilityReport } from '$lib/operators/trivy'
  import {
    abortRollout,
    assessCertificateRenewal,
    promoteRollout,
    type CertificateInsight,
  } from '$lib/api/client'
  import { toApiError } from '$lib/api/errors'
  import { Browser } from '@wailsio/runtime'
  import { AlertOctagon, AlertTriangle, ExternalLink, Info } from '@lucide/svelte'

  type RowTone = DetailRow['tone']

  interface Props {
    /** Which controller's object this is, decided by group and kind upstream. */
    panel: OperatorPanel
    /** The parsed manifest the drawer already holds. */
    manifest: unknown
    /** The object's own namespace, for resolving a reference that omits one. */
    namespace?: string
    /** The object's own name — what the Rollout controls act on. */
    name?: string
    /**
     * What the domain made of the Ready condition, when there is one.
     *
     * Passed in rather than decided here, exactly as GitOpsDetail takes it:
     * ResourceOverview already asks domain.ClassifyCondition about every
     * condition on the object, and a second reading of Ready=False in this
     * file would be a second implementation of the same verdict.
     */
    readyTone?: RowTone
    /** Whether this cluster serves a kind, so a link is only offered when there is somewhere to go. */
    canOpen?: ServesKind
    /** Follows a reference to the object it names. */
    onopen?: OpenObject
    /** The cluster the object belongs to, for the two Rollout writes. */
    clusterId?: string
    /** The group's name when this cluster is marked production, else null. */
    productionGroup?: string | null
    /** Whether writes are refused for this cluster, and the sentence saying so. */
    isReadOnly?: boolean
    readOnlyReason?: string
    /** Asks the drawer to re-read the object after a write. */
    onchanged?: () => void
  }

  let {
    panel,
    manifest,
    namespace = '',
    name = '',
    readyTone,
    canOpen,
    onopen,
    clusterId = '',
    productionGroup = null,
    isReadOnly = false,
    readOnlyReason = '',
    onchanged,
  }: Props = $props()

  /** Turns a reference into a click handler, or into nothing. See $lib/reference. */
  const follow = $derived(follower(canOpen, onopen))

  const certificate = $derived(panel === 'cert-manager-certificate' ? certManagerCertificate(manifest) : null)
  const scaledObject = $derived(panel === 'keda-scaledobject' ? kedaScaledObject(manifest) : null)
  const secret = $derived(panel === 'external-secret' ? externalSecret(manifest) : null)
  const rollout = $derived(panel === 'argo-rollout' ? argoRollout(manifest) : null)
  const report = $derived(panel === 'trivy-vulnerabilityreport' ? trivyVulnerabilityReport(manifest) : null)

  /** The grid holding a list of members, so its divider has something to measure. */
  let pane = $state<HTMLElement | null>(null)

  // --- Shared rendering ------------------------------------------------------

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

  /** The chip classes for one of a controller's words, in the drawer's palette. */
  function chip(tone: RowTone): string {
    if (tone === 'critical') return 'bg-error-container text-on-error-container'
    if (tone === 'warn') return 'bg-warning-container text-on-warning-container'
    return 'bg-surface-container-high text-on-surface-variant'
  }

  /**
   * A condition as a row, in the controller's own words.
   *
   * The absence of a condition and a condition of False are kept apart: a
   * freshly created object has said nothing, and rendering that as a negative
   * reports a failure that has not happened.
   */
  function conditionRows(
    label: string,
    condition: { status: string; reason: string; message: string; since: string } | null,
    tone: RowTone,
  ): DetailRow[] {
    if (!condition) return [{ label, value: 'not yet reported' }]

    const rows: DetailRow[] = [
      {
        label,
        value: condition.reason ? `${condition.status} · ${condition.reason}` : condition.status,
        tone,
      },
    ]
    if (condition.message) rows.push({ label: `${label} message`, value: condition.message, tone })
    if (condition.since) rows.push(when(`${label} since`, condition.since))
    return rows
  }

  /** A number that may legitimately be unset, where zero would be a lie. */
  function count(value: number | null): string {
    return value === null ? '—' : String(value)
  }

  // --- cert-manager ----------------------------------------------------------

  /**
   * THE ONE VERDICT IN THIS FILE, and it is not made here.
   *
   * Asked of the Go domain, re-asked whenever the manifest changes, and left
   * empty when it cannot be reached — a panel that showed a stale conclusion
   * beside fresh dates would be worse than one that shows only the dates,
   * which are on screen either way.
   */
  let renewalInsights = $state<CertificateInsight[]>([])
  $effect(() => {
    const cert = certificate
    if (!cert) {
      renewalInsights = []
      return
    }

    let live = true
    void assessCertificateRenewal({
      notAfter: cert.notAfter,
      renewalTime: cert.renewalTime,
      readyStatus: cert.ready?.status ?? '',
      readyReason: cert.ready?.reason ?? '',
      issuingStatus: cert.issuing?.status ?? '',
      failedIssuanceAttempts: cert.failedIssuanceAttempts ?? 0,
    })
      .then((insights) => {
        if (live) renewalInsights = insights
      })
      .catch(() => {
        if (live) renewalInsights = []
      })

    return () => {
      live = false
    }
  })

  const SEVERITY_STYLE = {
    critical: { icon: AlertOctagon, card: 'border-error/40 bg-error-container/20', iconClass: 'text-error' },
    warning: { icon: AlertTriangle, card: 'border-gauge-warn/40 bg-gauge-warn/10', iconClass: 'text-gauge-warn' },
    info: {
      icon: Info,
      card: 'border-outline-variant bg-surface-container-low',
      iconClass: 'text-on-surface-variant/70',
    },
  } as const

  function styleFor(insight: CertificateInsight) {
    return SEVERITY_STYLE[insight.severity as keyof typeof SEVERITY_STYLE] ?? SEVERITY_STYLE.info
  }

  const certificateRows = $derived.by<DetailRow[]>(() => {
    const cert = certificate
    if (!cert) return []

    const rows = conditionRows('Ready', cert.ready, readyTone)
    if (cert.issuing) {
      // Said out loud: an operator looking at a certificate that is not Ready
      // needs to know whether cert-manager is already doing something about
      // it, which is the difference between waiting and investigating.
      rows.push({
        label: 'Issuing',
        value: cert.issuing.reason ? `${cert.issuing.status} · ${cert.issuing.reason}` : cert.issuing.status,
      })
    }

    rows.push({
      label: 'Expires',
      value: cert.notAfter ? remainingLabel(cert.notAfter) : 'nothing issued yet',
      info: cert.notAfter || undefined,
    })
    rows.push({
      label: 'Renewal',
      // Absent means cert-manager has scheduled none — which it also does
      // while an issuance is in flight, hence the Issuing row above rather
      // than a conclusion drawn here.
      value: cert.renewalTime ? remainingLabel(cert.renewalTime) : 'none scheduled',
      info: cert.renewalTime || undefined,
    })
    if (cert.notBefore) rows.push({ label: 'Valid from', value: cert.notBefore })
    if (cert.duration) rows.push({ label: 'Duration', value: cert.duration })
    if (cert.renewBefore) rows.push({ label: 'Renew before expiry', value: cert.renewBefore })
    if (cert.revision !== null) rows.push({ label: 'Revision', value: String(cert.revision) })
    if (cert.failedIssuanceAttempts !== null) {
      rows.push({
        label: 'Failed issuance attempts',
        value: String(cert.failedIssuanceAttempts),
        tone: 'warn',
      })
    }
    return rows
  })

  const issuerRows = $derived.by<DetailRow[]>(() => {
    const cert = certificate
    if (!cert) return []

    const rows: DetailRow[] = [
      {
        label: cert.issuerRef.kind || 'Issuer',
        value: cert.issuerRef.name || '—',
        // A ClusterIssuer is cluster-scoped, so it is opened with no
        // namespace; an Issuer lives in the Certificate's own.
        onclick: follow(
          cert.issuerRef.kind,
          cert.issuerRef.name,
          cert.issuerRef.kind === 'ClusterIssuer' ? '' : namespace,
        ),
      },
    ]
    if (cert.issuerRef.group) rows.push({ label: 'Issuer API group', value: cert.issuerRef.group })
    rows.push({
      label: 'Secret',
      value: cert.secretName || '—',
      onclick: follow('Secret', cert.secretName, namespace),
      info: 'Where cert-manager writes the key pair. Its contents are not read here.',
    })
    return rows
  })

  const nameRows = $derived.by<DetailRow[]>(() => {
    const cert = certificate
    if (!cert) return []

    const rows: DetailRow[] = []
    if (cert.commonName) rows.push({ label: 'Common name', value: cert.commonName })
    for (const dnsName of cert.dnsNames) rows.push({ label: 'DNS name', value: dnsName })
    for (const ip of cert.ipAddresses) rows.push({ label: 'IP address', value: ip })
    for (const uri of cert.uris) rows.push({ label: 'URI', value: uri })
    return rows
  })

  // --- KEDA ------------------------------------------------------------------

  const kedaRows = $derived.by<DetailRow[]>(() => {
    const scaled = scaledObject
    if (!scaled) return []

    const rows = conditionRows('Ready', scaled.ready, readyTone)
    rows.push(...conditionRows('Active', scaled.active, undefined))
    if (scaled.fallback) rows.push(...conditionRows('Fallback', scaled.fallback, 'warn'))

    if (scaled.paused) {
      // The same reason a suspended CronJob says so: a paused ScaledObject
      // looks exactly like one whose triggers are quiet, and "why is this not
      // scaling" is the question it is opened with.
      rows.push({
        label: 'Paused',
        value: scaled.pausedReplicas
          ? `yes — held at ${scaled.pausedReplicas} replicas`
          : 'yes — KEDA will not scale it until the annotation is removed',
        tone: 'warn',
      })
    }
    if (scaled.pollingInterval !== null) {
      rows.push({ label: 'Polling interval', value: `${scaled.pollingInterval}s` })
    }
    if (scaled.cooldownPeriod !== null) {
      rows.push({ label: 'Cooldown', value: `${scaled.cooldownPeriod}s` })
    }
    if (scaled.hpaName) {
      rows.push({
        label: 'HorizontalPodAutoscaler',
        value: scaled.hpaName,
        onclick: follow('HorizontalPodAutoscaler', scaled.hpaName, namespace),
        info: 'Created and driven by KEDA; editing it directly is overwritten.',
      })
    }
    if (scaled.originalReplicaCount !== null) {
      rows.push({
        label: 'Replicas before KEDA',
        value: String(scaled.originalReplicaCount),
        info: 'What the target was set to before KEDA first scaled it.',
      })
    }
    return rows
  })

  const kedaTargetRows = $derived.by<DetailRow[]>(() => {
    const scaled = scaledObject
    if (!scaled) return []

    const rows: DetailRow[] = [
      {
        label: scaled.target.kind || 'Deployment',
        value: scaled.target.name || '—',
        onclick: follow(scaled.target.kind, scaled.target.name, namespace),
      },
    ]
    if (scaled.target.containerName) {
      rows.push({ label: 'Container', value: scaled.target.containerName })
    }
    rows.push({ label: 'Min replicas', value: count(scaled.minReplicas) })
    rows.push({ label: 'Max replicas', value: count(scaled.maxReplicas) })
    if (scaled.idleReplicas !== null) {
      rows.push({ label: 'Idle replicas', value: String(scaled.idleReplicas) })
    }
    return rows
  })

  // --- External Secrets ------------------------------------------------------

  const externalSecretRows = $derived.by<DetailRow[]>(() => {
    const external = secret
    if (!external) return []

    const rows = conditionRows('Ready', external.ready, readyTone)
    if (external.refreshInterval) {
      rows.push({
        label: 'Refresh interval',
        value: external.refreshInterval,
        // "0" is a legitimate setting and means the opposite of what a zero
        // usually reads as here.
        info: external.refreshInterval === '0' ? 'Read once, then never again' : undefined,
      })
    }
    if (external.refreshTime) rows.push(when('Last refreshed', external.refreshTime))
    if (external.syncedResourceVersion) {
      rows.push({ label: 'Synced version', value: external.syncedResourceVersion })
    }
    return rows
  })

  const storeRows = $derived.by<DetailRow[]>(() => {
    const external = secret
    if (!external) return []

    return [
      {
        label: external.storeKind || 'SecretStore',
        value: external.storeName || '—',
        // A ClusterSecretStore is cluster-scoped, so following it must not
        // carry a namespace it does not live in.
        onclick: follow(
          external.storeKind,
          external.storeName,
          external.storeKind === 'ClusterSecretStore' ? '' : namespace,
        ),
      },
    ]
  })

  const externalTargetRows = $derived.by<DetailRow[]>(() => {
    const external = secret
    if (!external) return []

    const rows: DetailRow[] = [
      {
        label: 'Secret',
        value: external.targetName || '—',
        onclick: follow('Secret', external.targetName, namespace),
      },
    ]
    if (external.boundSecret && external.boundSecret !== external.targetName) {
      rows.push({ label: 'Bound to', value: external.boundSecret })
    }
    if (external.creationPolicy) rows.push({ label: 'Creation policy', value: external.creationPolicy })
    if (external.deletionPolicy) rows.push({ label: 'Deletion policy', value: external.deletionPolicy })
    if (external.templated) {
      rows.push({
        label: 'Template',
        value: 'the Secret is rendered from a template rather than copied key for key',
      })
    }
    return rows
  })

  const mappingRows = $derived.by<DetailRow[]>(() => {
    const external = secret
    if (!external) return []

    return external.mappings.map((mapping) => ({
      // The remote key is the label because it is what an operator carries
      // over from the store's own console; the local key is what lands here.
      label: mapping.property ? `${mapping.remoteKey} · ${mapping.property}` : mapping.remoteKey,
      value: mapping.localKey || mapping.match || 'every key the reference yields',
      info: mapping.origin,
    }))
  })

  // --- Argo Rollouts ---------------------------------------------------------

  const rolloutRows = $derived.by<DetailRow[]>(() => {
    const argo = rollout
    if (!argo) return []

    const rows: DetailRow[] = [
      { label: 'Phase', value: argo.phase || 'not yet reported', tone: rolloutTone(argo.phase) },
    ]
    if (argo.message) rows.push({ label: 'Message', value: argo.message, tone: rolloutTone(argo.phase) })

    rows.push({
      label: 'Strategy',
      value: argo.strategy === 'blueGreen' ? 'blue-green' : argo.strategy || '—',
    })
    if (argo.step !== null) {
      rows.push({ label: 'Step', value: `${argo.step} of ${argo.steps}` })
    }
    if (argo.canaryWeight !== null) {
      rows.push({ label: 'Canary weight', value: `${argo.canaryWeight}%` })
    }
    if (argo.paused) {
      // spec.paused is an OPERATOR holding it; a pause condition is the
      // CONTROLLER holding it. Two different facts and two different rows,
      // because promoting clears them by two different means.
      rows.push({ label: 'Paused', value: 'yes — set on the Rollout itself', tone: 'warn' })
    }
    for (const pause of argo.pauseConditions) {
      rows.push({
        label: 'Held by',
        value: pause.reason || 'unnamed pause condition',
        tone: 'warn',
        info: at(pause.startTime) || undefined,
      })
    }
    if (argo.aborted) {
      rows.push({
        label: 'Aborted',
        value: argo.abortedAt ? `yes, ${ago(argo.abortedAt)}` : 'yes',
        tone: 'critical',
      })
    }
    return rows
  })

  const rolloutReplicaRows = $derived.by<DetailRow[]>(() => {
    const argo = rollout
    if (!argo) return []

    const rows: DetailRow[] = [
      { label: 'Desired', value: count(argo.desiredReplicas) },
      { label: 'Updated', value: count(argo.updatedReplicas) },
      { label: 'Ready', value: count(argo.readyReplicas) },
      { label: 'Available', value: count(argo.availableReplicas) },
      { label: 'Total', value: count(argo.replicas) },
    ]
    if (argo.currentPodHash || argo.stableRS) {
      // Quoted, not compared: equal hashes mean the update is fully rolled
      // out, and saying so would be a verdict this file does not draw.
      rows.push({ label: 'Current pod hash', value: argo.currentPodHash || '—' })
      rows.push({ label: 'Stable ReplicaSet', value: argo.stableRS || '—' })
    }
    if (argo.activeSelector) rows.push({ label: 'Active selector', value: argo.activeSelector })
    if (argo.previewSelector) rows.push({ label: 'Preview selector', value: argo.previewSelector })
    return rows
  })

  const analysisRows = $derived.by<DetailRow[]>(() =>
    (rollout?.analysisRuns ?? []).map((run) => ({
      label: run.role,
      value: run.name || '—',
      tone: rolloutTone(run.status),
      info: run.message || run.status || undefined,
      onclick: follow('AnalysisRun', run.name, namespace),
    })),
  )

  /**
   * Whether promoting would do anything.
   *
   * The same question domain.PlanRolloutPromote answers, asked here only to
   * decide whether to OFFER the control — the backend refuses a promote with
   * nothing to promote regardless, so this is an affordance and never the
   * guard. Getting it wrong costs a disabled button, not a wrong write.
   */
  const promotable = $derived(!!rollout && (rollout.paused || rollout.pauseConditions.length > 0))

  /** Aborting is only meaningful while something is in progress. */
  const abortable = $derived(!!rollout && !rollout.aborted && rollout.phase !== 'Healthy')

  /** Why each control is or is not offered, said once for both readers. */
  const promoteHint = $derived(
    isReadOnly
      ? readOnlyReason
      : promotable
        ? 'Clear whatever is holding this Rollout and let it continue.'
        : 'Nothing is holding this Rollout.',
  )
  const abortHint = $derived(
    isReadOnly
      ? readOnlyReason
      : abortable
        ? 'Send traffic back to the stable ReplicaSet.'
        : 'There is nothing in progress to abort.',
  )

  let dialog = $state<'promote' | 'abort' | null>(null)
  let writeError = $state('')
  let writing = $state(false)

  async function runRolloutAction(action: 'promote' | 'abort'): Promise<void> {
    dialog = null
    writing = true
    writeError = ''
    try {
      if (action === 'promote') {
        await promoteRollout(clusterId, namespace, name)
      } else {
        await abortRollout(clusterId, namespace, name)
      }
      // The controller rewrites the status within the second, so the panel
      // has to re-read rather than wait for the next poll to notice.
      onchanged?.()
    } catch (cause) {
      writeError = toApiError(cause).message
    } finally {
      writing = false
    }
  }

  // --- Trivy Operator --------------------------------------------------------

  const reportRows = $derived.by<DetailRow[]>(() => {
    const trivy = report
    if (!trivy) return []

    const rows: DetailRow[] = [{ label: 'Image', value: trivy.artifact || '—' }]
    if (trivy.subject.name) {
      rows.push({
        label: trivy.subject.kind || 'Workload',
        value: trivy.subject.name,
        onclick: follow(trivy.subject.kind, trivy.subject.name, namespace),
      })
    }
    if (trivy.subject.container) rows.push({ label: 'Container', value: trivy.subject.container })
    if (trivy.scanner) rows.push({ label: 'Scanner', value: trivy.scanner })
    if (trivy.updateTimestamp) rows.push(when('Scanned', trivy.updateTimestamp))
    return rows
  })

  /** The severity buckets as the report's own summary states them. */
  const severityChips = $derived.by(() => {
    const summary = report?.summary
    if (!summary) return []
    return [
      { label: 'Critical', value: summary.critical, tone: 'critical' as RowTone },
      { label: 'High', value: summary.high, tone: 'warn' as RowTone },
      { label: 'Medium', value: summary.medium, tone: undefined },
      { label: 'Low', value: summary.low, tone: undefined },
      { label: 'Unknown', value: summary.unknown, tone: undefined },
    ]
  })

  const vulnerabilities = $derived(report?.vulnerabilities ?? [])
</script>

{#if certificate}
  <DetailSection
    level="h3"
    id="operator-certmanager"
    title="cert-manager"
    hint={certificate.ready ? `Ready ${certificate.ready.status}` : ''}
  >
    <!--
      THE ONE VERDICT, from the Go domain. Empty for almost every certificate,
      which is the point: a panel that always has something to say is one
      people stop reading.
    -->
    {#if renewalInsights.length > 0}
      <div class="mb-3 flex flex-col gap-2">
        {#each renewalInsights as insight, index (index)}
          {@const style = styleFor(insight)}
          {@const Icon = style.icon}
          <div class="flex items-start gap-2 rounded-sm border p-3 {style.card}">
            <Icon class="mt-0.5 size-4 shrink-0 {style.iconClass}" strokeWidth={2} />
            <div class="min-w-0 flex-1">
              <p class="text-body-medium font-medium text-on-surface">{insight.title}</p>
              <p class="mt-1 text-body-medium leading-relaxed text-on-surface-variant" data-selectable>
                {insight.detail}
              </p>
              {#if insight.advice}
                <p class="mt-1.5 text-body-medium leading-relaxed text-on-surface">{insight.advice}</p>
              {/if}
            </div>
          </div>
        {/each}
      </div>
    {/if}
    <DetailList rows={certificateRows} />
  </DetailSection>

  <DetailSection level="h3" id="operator-issuer" title="Issuer">
    <DetailList rows={issuerRows} />
  </DetailSection>

  {#if nameRows.length > 0}
    <DetailSection level="h3" id="operator-names" title="Names" hint={String(nameRows.length)}>
      <DetailList rows={nameRows} />
      <p class="mt-3 text-body-small text-on-surface-variant/60">
        What the Certificate ASKS for. What the issued certificate actually carries is in the Secret above,
        and is read only on request.
      </p>
    </DetailSection>
  {/if}
{:else if scaledObject}
  <DetailSection
    level="h3"
    id="operator-keda"
    title="KEDA"
    hint={scaledObject.active ? `Active ${scaledObject.active.status}` : ''}
  >
    <DetailList rows={kedaRows} />
  </DetailSection>

  <DetailSection level="h3" id="operator-scale-target" title="Scale target">
    <DetailList rows={kedaTargetRows} />
  </DetailSection>

  <DetailSection
    level="h3"
    id="operator-triggers"
    title="Triggers"
    hint={String(scaledObject.triggers.length)}
  >
    {#if scaledObject.triggers.length === 0}
      <p class="text-body-small text-on-surface-variant/70">
        This ScaledObject declares no triggers, so KEDA has nothing to scale on.
      </p>
    {:else}
      <div class="flex flex-col gap-3">
        {#each scaledObject.triggers as trigger, index (index)}
          <div class="rounded-sm border border-outline-variant/60 bg-surface-container-lowest p-3">
            <p class="text-body-medium font-medium text-on-surface">
              {trigger.type || 'unnamed scaler'}
              {#if trigger.name}
                <span class="text-on-surface-variant">· {trigger.name}</span>
              {/if}
            </p>
            {#if trigger.authenticationRef}
              <!-- NAMED, NEVER RESOLVED. Following it opens the object; its
                   contents are a Secret read and stay a deliberate act. -->
              <p class="mt-1 text-body-small text-on-surface-variant">
                Authenticated by
                {trigger.clusterAuthentication ? 'ClusterTriggerAuthentication' : 'TriggerAuthentication'}
                <span class="text-on-surface">{trigger.authenticationRef}</span>
              </p>
            {/if}
            {#if trigger.metadata.length > 0}
              <dl class="mt-2 grid grid-cols-[minmax(0,10rem)_minmax(0,1fr)] gap-x-3 gap-y-1">
                {#each trigger.metadata as entry (entry.key)}
                  <dt class="truncate text-body-small text-on-surface-variant" title={entry.key}>
                    {entry.key}
                  </dt>
                  <dd class="min-w-0 truncate text-body-small text-on-surface" title={entry.value}>
                    {#if entry.redacted}
                      <!-- The key is worth knowing — it says a credential is
                           configured inline rather than through a
                           TriggerAuthentication — and the value is not shown
                           anywhere, including in a screenshot of this panel. -->
                      <span class="text-on-surface-variant/60 italic">not shown</span>
                    {:else}
                      {entry.value}
                    {/if}
                  </dd>
                {/each}
              </dl>
            {/if}
          </div>
        {/each}
      </div>
    {/if}
  </DetailSection>
{:else if secret}
  <DetailSection
    level="h3"
    id="operator-externalsecret"
    title="External Secrets"
    hint={secret.ready ? `Ready ${secret.ready.status}` : ''}
  >
    <DetailList rows={externalSecretRows} />
  </DetailSection>

  <DetailSection level="h3" id="operator-store" title="Store">
    <DetailList rows={storeRows} />
  </DetailSection>

  <DetailSection level="h3" id="operator-external-target" title="Target">
    <DetailList rows={externalTargetRows} />
  </DetailSection>

  <DetailSection level="h3" id="operator-mappings" title="Data" hint={String(mappingRows.length)}>
    {#if mappingRows.length === 0}
      <p class="text-body-small text-on-surface-variant/70">
        This ExternalSecret names no keys, so nothing is copied from the store.
      </p>
    {:else}
      <DetailList rows={mappingRows} />
    {/if}
    <p class="mt-3 text-body-small text-on-surface-variant/60">
      Which remote key becomes which key of the Secret. No value is read from the store or from the
      Secret — reading one is a deliberate, audited act on the Secret itself.
    </p>
  </DetailSection>
{:else if rollout}
  <DetailSection
    level="h3"
    id="operator-rollout"
    title="Argo Rollouts"
    hint={rollout.step !== null ? `step ${rollout.step} of ${rollout.steps}` : rollout.phase}
  >
    <div class="mb-3 flex flex-wrap gap-2">
      <span class="rounded-full px-2 py-0.5 text-body-small {chip(rolloutTone(rollout.phase))}">
        {rollout.phase || 'Unknown'}
      </span>
      {#if rollout.aborted}
        <span class="rounded-full px-2 py-0.5 text-body-small {chip('critical')}">Aborted</span>
      {/if}
    </div>
    <DetailList rows={rolloutRows} />

    <!--
      THE TWO WRITES. Everything that makes them safe is behind them rather
      than here: the read-only refusal and the audit line are in Go, the
      production type-the-name gate and the kubectl equivalent are in the
      dialog. This is only the affordance.
    -->
    <!-- The reason a control is disabled, named for a screen reader and on
         hover alike. Button takes describedBy rather than a title — see its
         own doc comment — so the sentence lives once, here. -->
    <p id="rollout-write-hint" class="sr-only">{promoteHint} {abortHint}</p>
    <div class="mt-4 flex flex-wrap items-center gap-2">
      <span title={promoteHint}>
        <Button
          variant="filled"
          disabled={writing || isReadOnly || !promotable || !name}
          describedBy="rollout-write-hint"
          onclick={() => (dialog = 'promote')}
        >
          Promote
        </Button>
      </span>
      <span title={abortHint}>
        <Button
          variant="outlined"
          disabled={writing || isReadOnly || !abortable || !name}
          describedBy="rollout-write-hint"
          onclick={() => (dialog = 'abort')}
        >
          Abort
        </Button>
      </span>
      <!-- The kubectl transparency line the rest of the application shows in
           a dialog footer. Here it sits beside the buttons too, because this
           is the one panel whose controls have no equivalent anywhere else in
           PodSteer and the plugin's name is half the answer to "how do I do
           this without the GUI". -->
      <span class="text-body-small text-on-surface-variant/60">
        kubectl argo rollouts promote|abort {name || 'NAME'}
      </span>
    </div>
    {#if writeError}
      <p class="mt-2 text-body-small text-error" role="alert">{writeError}</p>
    {/if}
  </DetailSection>

  <DetailSection level="h3" id="operator-rollout-replicas" title="Replicas">
    <DetailList rows={rolloutReplicaRows} />
  </DetailSection>

  {#if analysisRows.length > 0}
    <DetailSection
      level="h3"
      id="operator-analysis"
      title="Analysis"
      hint={String(analysisRows.length)}
    >
      <DetailList rows={analysisRows} />
    </DetailSection>
  {/if}
{:else if report}
  <DetailSection level="h3" id="operator-trivy" title="Trivy Operator">
    <div class="mb-3 flex flex-wrap gap-2">
      {#each severityChips as bucket (bucket.label)}
        {#if bucket.value > 0}
          <span class="rounded-full px-2 py-0.5 text-body-small {chip(bucket.tone)}">
            {bucket.label} · {bucket.value}
          </span>
        {/if}
      {/each}
      {#if severityChips.every((bucket) => bucket.value === 0)}
        <!-- Not the same as an unscanned image, and the difference matters:
             the operator scanned this one and found nothing. -->
        <span class="rounded-full px-2 py-0.5 text-body-small {chip(undefined)}">
          No findings in this report
        </span>
      {/if}
    </div>
    <DetailList rows={reportRows} />
  </DetailSection>

  <DetailSection
    level="h3"
    id="operator-vulnerabilities"
    title="Vulnerabilities"
    hint={String(vulnerabilities.length)}
  >
    {#if vulnerabilities.length === 0}
      <p class="text-body-small text-on-surface-variant/70">
        This report lists no vulnerabilities.
      </p>
    {:else}
      <div class="relative">
        <dl class="detail-grid" bind:this={pane}>
          {#each vulnerabilities as vulnerability, index (index)}
            {@const link = advisoryLink(vulnerability)}
            <dt class="flex min-w-0 items-center gap-2 text-body-medium text-on-surface">
              <span
                class="shrink-0 rounded-full px-1.5 py-0.5 text-label-small {chip(
                  severityTone(vulnerability.severity),
                )}"
              >
                {vulnerability.severity || 'UNKNOWN'}
              </span>
              {#if link}
                <!-- Opened in the real browser, not the webview: an advisory
                     is somebody else's site, and loading it inside the
                     application would replace PodSteer. -->
                <button
                  type="button"
                  onclick={() => void Browser.OpenURL(link)}
                  class="resource-link inline-flex min-w-0 items-center gap-1 truncate text-left"
                  title={vulnerability.title || vulnerability.id}
                >
                  <span class="truncate">{vulnerability.id}</span>
                  <ExternalLink class="size-3 shrink-0" strokeWidth={1.8} />
                </button>
              {:else}
                <span class="min-w-0 truncate" title={vulnerability.title || undefined}>
                  {vulnerability.id}
                </span>
              {/if}
            </dt>
            <dd class="min-w-0 truncate text-body-medium text-on-surface-variant" data-selectable>
              {vulnerability.resource || '—'}
              {#if vulnerability.installedVersion}
                <span class="text-on-surface-variant/70">{vulnerability.installedVersion}</span>
              {/if}
              {#if vulnerability.fixedVersion}
                <span class="text-on-surface">→ {vulnerability.fixedVersion}</span>
              {:else}
                <!-- The fact an operator is looking for: there is nothing to
                     upgrade to yet, so the finding cannot be closed by a bump. -->
                <span class="text-on-surface-variant/60 italic">no fix published</span>
              {/if}
            </dd>
          {/each}
        </dl>

        <ColumnDivider {pane} />
      </div>

      <p class="mt-3 text-body-small text-on-surface-variant/60">
        As reported by the Trivy Operator{report.updateTimestamp
          ? `, scanned ${ago(report.updateTimestamp)}`
          : ''}. PodSteer scans nothing itself.
      </p>
    {/if}
  </DetailSection>
{/if}

{#if rollout && dialog}
  <RolloutActionDialog
    open={dialog !== null}
    action={dialog}
    {name}
    {namespace}
    ctx={clusterId}
    {productionGroup}
    onclose={() => (dialog = null)}
    onconfirm={() => void runRolloutAction(dialog ?? 'promote')}
  />
{/if}
