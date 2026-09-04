<!--
  What one of Kubernetes' own newer APIs declares, in its own words.

  The third family of typed panels, after $lib/gitops and $lib/operators and
  built on the same mechanism: selected by API group AND Kind through
  $lib/standardapis/panel, rendered from the ONE manifest the drawer already
  fetched. Gateway API, Dynamic Resource Allocation and the admission policies
  were all browsable through the generic table already; what none of them had
  was a panel saying what the object actually declares.

  ONE GET, NO LISTS, NO CROSS-OBJECT RESOLUTION. A route names its parent
  Gateway, a binding names its policy, a claim names the pods holding it —
  every one of them renders as a followable node BY KIND VERBATIM, and
  following it opens that object. Nothing here fetches one to check it exists
  or to ask what it said back: that would be a second GET per reference, and a
  panel that crawls is a panel that stalls a drawer.

  PURE QUOTATION, WITH NO EXCEPTION — not even the one $lib/operators names for
  a certificate's expiry. Accepted, Programmed, ResolvedRefs, Deny, Fail,
  ExactCount are the API's and the controller's own words, and an enum, a
  reason or a filter type none of these files has seen renders as itself. The
  conditions below therefore carry NO TONE: a colour here would be this panel's
  verdict on somebody else's boolean, and the Conditions section further down
  the drawer already renders the object's top-level conditions with the
  colouring domain.ClassifyCondition decided. Saying "this route is broken"
  would be a comparison across a parent's status and belongs in Go with a test.
-->
<script lang="ts">
  import DetailSection from './DetailSection.svelte'
  import DetailList, { type DetailRow } from './DetailList.svelte'
  import { follower, type OpenObject, type ServesKind } from '$lib/reference'
  import { formatAge } from '$lib/format'
  // secondsSince lives with the GitOps panels because they needed it first —
  // one implementation of "how long ago was this timestamp" rather than three
  // that could round differently in the same drawer.
  import { secondsSince } from '$lib/gitops/panel'
  import type { StandardCondition, StandardPanel } from '$lib/standardapis/panel'
  import { gateway, gatewayClass, gatewayRoute } from '$lib/standardapis/gateway'
  import {
    deviceClass,
    resourceClaim,
    resourceClaimTemplate,
    type ClaimSpecView,
  } from '$lib/standardapis/devices'
  import {
    MUTATING_POLICY_KIND,
    VALIDATING_POLICY_KIND,
    admissionPolicyBinding,
    mutatingAdmissionPolicy,
    validatingAdmissionPolicy,
    type PolicyMatch,
  } from '$lib/standardapis/admission'

  interface Props {
    /** Which API's object this is, decided by group and kind upstream. */
    panel: StandardPanel
    /** The parsed manifest the drawer already holds. */
    manifest: unknown
    /** The object's own namespace, for resolving a reference that omits one. */
    namespace?: string
    /** Whether this cluster serves a kind, so a link is only offered when there is somewhere to go. */
    canOpen?: ServesKind
    /** Follows a reference to the object it names. */
    onopen?: OpenObject
  }

  let { panel, manifest, namespace = '', canOpen, onopen }: Props = $props()

  /** Turns a reference into a click handler, or into nothing. See $lib/reference. */
  const follow = $derived(follower(canOpen, onopen))

  const gatewayClassView = $derived(panel === 'gateway-class' ? gatewayClass(manifest) : null)
  const gatewayView = $derived(panel === 'gateway' ? gateway(manifest) : null)
  const routeView = $derived(panel === 'gateway-route' ? gatewayRoute(manifest) : null)
  const claim = $derived(panel === 'resource-claim' ? resourceClaim(manifest) : null)
  const template = $derived(
    panel === 'resource-claim-template' ? resourceClaimTemplate(manifest) : null,
  )
  const deviceClassView = $derived(panel === 'device-class' ? deviceClass(manifest) : null)
  const validating = $derived(
    panel === 'validating-admission-policy' ? validatingAdmissionPolicy(manifest) : null,
  )
  const mutating = $derived(
    panel === 'mutating-admission-policy' ? mutatingAdmissionPolicy(manifest) : null,
  )
  const binding = $derived(
    panel === 'validating-admission-policy-binding' ||
      panel === 'mutating-admission-policy-binding'
      ? admissionPolicyBinding(manifest)
      : null,
  )

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

  /**
   * A condition as rows, in the controller's own words.
   *
   * The absence of a condition and a condition of False are kept apart: a
   * Gateway its controller has not looked at yet has said nothing, and
   * rendering that as a rejection reports a failure that has not happened.
   */
  function conditionRows(label: string, condition: StandardCondition | null): DetailRow[] {
    if (!condition) return [{ label, value: 'not yet reported' }]

    const rows: DetailRow[] = [
      {
        label,
        value: condition.reason ? `${condition.status} · ${condition.reason}` : condition.status,
      },
    ]
    if (condition.message) rows.push({ label: `${label} message`, value: condition.message })
    if (condition.since) {
      rows.push({ label: `${label} since`, value: ago(condition.since), info: at(condition.since) })
    }
    return rows
  }

  /**
   * A whole condition list as rows — for the ones nested inside a listener or
   * a parent status, which the drawer's own Conditions section never sees.
   */
  function nestedConditionRows(conditions: StandardCondition[]): DetailRow[] {
    return conditions.map((condition) => {
      const explanation = [condition.reason, condition.message].filter(Boolean).join(' — ')
      return {
        label: condition.type || 'condition',
        value: explanation ? `${condition.status} · ${explanation}` : condition.status,
      }
    })
  }

  /** A list as one cell, or an em dash — the caller knows what empty means. */
  function joined(values: string[]): string {
    return values.length > 0 ? values.join(', ') : '—'
  }

  /** A match block's rows, shared by both policies and both bindings. */
  function matchRows(match: PolicyMatch): DetailRow[] {
    const rows: DetailRow[] = []
    for (const rule of match.rules) {
      rows.push({
        label: joined(rule.operations),
        value: ruleTarget(rule),
        info: rule.scope ? `Scope ${rule.scope}` : undefined,
      })
    }
    for (const rule of match.excludeRules) {
      rows.push({ label: `except ${joined(rule.operations)}`, value: ruleTarget(rule) })
    }
    if (match.matchPolicy) rows.push({ label: 'Match policy', value: match.matchPolicy })
    for (const term of match.namespaceSelector) {
      rows.push({ label: 'Namespace selector', value: term })
    }
    for (const term of match.objectSelector) {
      rows.push({ label: 'Object selector', value: term })
    }
    return rows
  }

  /**
   * A rule's target as one line.
   *
   * An empty apiGroups entry is the CORE group, which the API writes as "" and
   * which would otherwise render as a gap; it is spelt out rather than left
   * blank.
   */
  function ruleTarget(rule: {
    apiGroups: string[]
    apiVersions: string[]
    resources: string[]
    resourceNames: string[]
  }): string {
    const groups = rule.apiGroups.map((group) => group || 'core')
    const target = [joined(groups), joined(rule.apiVersions), joined(rule.resources)].join('/')
    return rule.resourceNames.length > 0 ? `${target} · ${joined(rule.resourceNames)}` : target
  }

  /** A claim spec's request rows, shared by a claim and by the template that stamps it out. */
  function requestRows(spec: ClaimSpecView): DetailRow[] {
    const rows: DetailRow[] = []

    if (spec.resourceClassName) {
      // The earliest shape of this group: one class named on the claim itself
      // rather than a list of requests. Labelled in that version's own word,
      // because a ResourceClass is not a DeviceClass and following it as one
      // would open the wrong kind.
      rows.push({ label: 'Resource class', value: spec.resourceClassName })
    }
    if (spec.claimAllocationMode) {
      rows.push({ label: 'Allocation mode', value: spec.claimAllocationMode })
    }

    for (const request of spec.requests) {
      const label = request.name || 'request'
      if (request.deviceClassName) {
        rows.push({
          label,
          value: request.deviceClassName,
          onclick: follow('DeviceClass', request.deviceClassName),
          info: 'The DeviceClass this request selects devices from.',
        })
      }
      const terms: string[] = []
      if (request.allocationMode) terms.push(request.allocationMode)
      if (request.count !== null) terms.push(`count ${request.count}`)
      if (request.adminAccess) terms.push('admin access')
      if (terms.length > 0) rows.push({ label: `${label} mode`, value: terms.join(' · ') })

      for (const selector of request.selectors) {
        rows.push({ label: `${label} selector`, value: selector })
      }
      for (const alternative of request.alternatives) {
        // A prioritised request: the scheduler tries these in order, so the
        // order they are listed in is itself information.
        const suffix = alternative.count !== null ? ` · count ${alternative.count}` : ''
        rows.push({
          label: `${label} → ${alternative.name || 'alternative'}`,
          value: `${alternative.deviceClassName || '—'}${suffix}`,
          onclick: follow('DeviceClass', alternative.deviceClassName),
        })
      }
    }

    for (const constraint of spec.constraints) {
      const scope = constraint.requests.length > 0 ? joined(constraint.requests) : 'every request'
      if (constraint.matchAttribute) {
        rows.push({ label: 'Must share', value: `${constraint.matchAttribute} · ${scope}` })
      }
      if (constraint.distinctAttribute) {
        rows.push({ label: 'Must differ on', value: `${constraint.distinctAttribute} · ${scope}` })
      }
    }

    for (const driver of spec.configDrivers) {
      rows.push({
        label: 'Driver configuration',
        value: driver,
        info: 'The driver the opaque parameters belong to. Their contents are not read here.',
      })
    }

    return rows
  }

  // --- Gateway API -----------------------------------------------------------

  const gatewayClassRows = $derived.by<DetailRow[]>(() => {
    const view = gatewayClassView
    if (!view) return []

    const rows: DetailRow[] = [
      {
        label: 'Controller',
        value: view.controllerName || '—',
        info: 'The controller that claims this class. Whether it is running is not checked here.',
      },
    ]
    if (view.description) rows.push({ label: 'Description', value: view.description })
    rows.push(...conditionRows('Accepted', view.accepted))
    if (view.parametersRef) {
      rows.push({
        label: view.parametersRef.kind || 'Parameters',
        value: view.parametersRef.name || '—',
        onclick: follow(
          view.parametersRef.kind,
          view.parametersRef.name,
          view.parametersRef.namespace,
        ),
      })
    }
    return rows
  })

  const gatewayRows = $derived.by<DetailRow[]>(() => {
    const view = gatewayView
    if (!view) return []

    const rows: DetailRow[] = [
      {
        label: 'GatewayClass',
        value: view.gatewayClassName || '—',
        // Cluster-scoped, so it is followed with no namespace.
        onclick: follow('GatewayClass', view.gatewayClassName),
      },
    ]
    rows.push(...conditionRows('Accepted', view.accepted))
    rows.push(...conditionRows('Programmed', view.programmed))

    for (const address of view.addresses) {
      rows.push({ label: address.type || 'Address', value: address.value })
    }
    for (const address of view.requestedAddresses) {
      // What was ASKED for, kept apart from what was assigned: a Gateway that
      // requested an address it did not get is the case this distinction is
      // for, and one merged list would hide it.
      rows.push({ label: `Requested ${address.type || 'address'}`, value: address.value })
    }
    return rows
  })

  const listeners = $derived(gatewayView?.listeners ?? [])

  function listenerRows(listener: (typeof listeners)[number]): DetailRow[] {
    const rows: DetailRow[] = []
    if (listener.hostname) {
      rows.push({ label: 'Hostname', value: listener.hostname })
    } else if (!listener.statusOnly) {
      // An absent hostname means EVERY hostname, which is the API's meaning
      // and the opposite of how an empty cell reads.
      rows.push({ label: 'Hostname', value: 'every hostname' })
    }
    if (listener.tlsMode) rows.push({ label: 'TLS', value: listener.tlsMode })
    for (const certificate of listener.certificateRefs) {
      rows.push({
        label: certificate.kind || 'Certificate',
        value: certificate.name || '—',
        onclick: follow(certificate.kind, certificate.name, certificate.namespace || namespace),
        info: 'Where the listener gets its key pair. Its contents are not read here.',
      })
    }
    if (listener.routesFrom) {
      rows.push({
        label: 'Routes from',
        value: listener.routesFrom,
        info:
          listener.routesFrom === 'Same'
            ? 'Only routes in this Gateway’s own namespace may attach.'
            : undefined,
      })
    }
    for (const term of listener.routesSelector) {
      rows.push({ label: 'Namespace selector', value: term })
    }
    if (listener.routeKinds.length > 0) {
      rows.push({ label: 'Route kinds', value: joined(listener.routeKinds) })
    }
    if (listener.supportedKinds.length > 0) {
      rows.push({
        label: 'Supported kinds',
        value: joined(listener.supportedKinds),
        info: 'What the controller says it will serve here, which can be narrower than what was asked for.',
      })
    }
    rows.push(...nestedConditionRows(listener.conditions))
    return rows
  }

  /** A listener's headline: what it is, in one line beside its name. */
  function listenerSummary(listener: (typeof listeners)[number]): string {
    const parts: string[] = []
    if (listener.protocol) parts.push(listener.protocol)
    if (listener.port !== null) parts.push(String(listener.port))
    return parts.join(' · ')
  }

  const rules = $derived(routeView?.rules ?? [])

  const routeRows = $derived.by<DetailRow[]>(() => {
    const view = routeView
    if (!view) return []

    const rows: DetailRow[] = []
    for (const parent of view.parents) {
      const section = parent.sectionName ? ` · listener ${parent.sectionName}` : ''
      rows.push({
        label: parent.kind,
        value: `${parent.name || '—'}${section}`,
        onclick: follow(parent.kind, parent.name, parent.namespace || namespace),
        info: parent.namespace ? `In namespace ${parent.namespace}` : undefined,
      })
    }
    if (view.hostnames.length > 0) {
      for (const hostname of view.hostnames) rows.push({ label: 'Hostname', value: hostname })
    } else {
      rows.push({
        label: 'Hostname',
        value: 'every hostname the parent listener allows',
      })
    }
    return rows
  })

  /** One match as a line, or the sentence an empty match block actually means. */
  function matchLine(parts: { kind: string; type: string; name: string; value: string }[]): string {
    if (parts.length === 0) return 'every request the parent sends here'
    return parts
      .map((part) => {
        const subject = part.name ? `${part.kind} ${part.name}` : part.kind
        const how = part.type ? ` ${part.type}` : ''
        return `${subject}${how} ${part.value}`.trim()
      })
      .join(' · ')
  }

  function backendRows(rule: (typeof rules)[number]): DetailRow[] {
    return rule.backends.map((backend) => {
      const port = backend.port !== null ? `:${backend.port}` : ''
      // An omitted weight is 1 by the API's own default and an explicit 0
      // takes the backend out of the pool entirely — opposite facts, so the
      // default is spelt out rather than left to read as absent.
      const weight = backend.weight === null ? 'weight 1 (default)' : `weight ${backend.weight}`
      return {
        label: backend.kind,
        value: `${backend.name || '—'}${port} · ${weight}`,
        onclick: follow(backend.kind, backend.name, backend.namespace || namespace),
        info: backend.group ? `API group ${backend.group}` : undefined,
      }
    })
  }

  const parentStatuses = $derived(routeView?.parentStatuses ?? [])

  // --- Devices ---------------------------------------------------------------

  const claimRequestRows = $derived.by<DetailRow[]>(() => (claim ? requestRows(claim) : []))
  const templateRequestRows = $derived.by<DetailRow[]>(() =>
    template ? requestRows(template.spec) : [],
  )

  const allocationRows = $derived.by<DetailRow[]>(() => {
    const view = claim
    if (!view) return []

    const rows: DetailRow[] = []
    for (const allocation of view.allocations) {
      const where = [allocation.pool, allocation.device].filter(Boolean).join('/')
      rows.push({
        label: allocation.request || allocation.driver || 'device',
        value: where || allocation.driver || '—',
        info: allocation.adminAccess
          ? `Driver ${allocation.driver} · administrative access`
          : `Driver ${allocation.driver}`,
      })
    }
    if (view.allocationTimestamp) {
      rows.push({
        label: 'Allocated',
        value: ago(view.allocationTimestamp),
        info: at(view.allocationTimestamp),
      })
    }
    for (const term of view.nodeSelector) {
      rows.push({
        label: 'Node',
        value: term,
        info: 'Where the allocated devices are, so the pod can only run there.',
      })
    }
    if (view.shareable !== null) {
      rows.push({ label: 'Shareable', value: view.shareable ? 'yes' : 'no' })
    }
    if (view.deallocationRequested) {
      rows.push({ label: 'Deallocation requested', value: 'yes' })
    }
    return rows
  })

  const reservedRows = $derived.by<DetailRow[]>(() => {
    const view = claim
    if (!view) return []

    return view.reservedFor.map((consumer) => ({
      // The resource is quoted as the API wrote it; only a core "pods" is
      // resolved to a Kind, and only that one is offered as a link — see
      // ClaimConsumerView.
      label: consumer.kind || consumer.resource || 'consumer',
      value: consumer.name || '—',
      onclick: consumer.kind ? follow(consumer.kind, consumer.name, namespace) : undefined,
      info: consumer.kind ? undefined : `Resource ${consumer.resource}`,
    }))
  })

  const deviceStatuses = $derived(claim?.deviceStatuses ?? [])

  function deviceStatusRows(status: (typeof deviceStatuses)[number]): DetailRow[] {
    const rows: DetailRow[] = []
    for (const address of status.addresses) rows.push({ label: 'Address', value: address })
    if (status.interfaceName) rows.push({ label: 'Interface', value: status.interfaceName })
    if (status.hardwareAddress) rows.push({ label: 'Hardware address', value: status.hardwareAddress })
    rows.push(...nestedConditionRows(status.conditions))
    return rows
  }

  const deviceClassRows = $derived.by<DetailRow[]>(() => {
    const view = deviceClassView
    if (!view) return []

    const rows: DetailRow[] = []
    for (const selector of view.selectors) rows.push({ label: 'Selector', value: selector })
    for (const driver of view.configDrivers) {
      rows.push({
        label: 'Driver configuration',
        value: driver,
        info: 'The driver the opaque parameters belong to. Their contents are not read here.',
      })
    }
    if (view.extendedResourceName) {
      rows.push({ label: 'Extended resource', value: view.extendedResourceName })
    }
    for (const term of view.suitableNodes) rows.push({ label: 'Suitable nodes', value: term })
    return rows
  })

  // --- Admission policies ----------------------------------------------------

  const validatingRows = $derived.by<DetailRow[]>(() => {
    const view = validating
    if (!view) return []

    const rows: DetailRow[] = [
      {
        label: 'Failure policy',
        // Unset means Fail, which is the API's own default and the difference
        // between a broken policy blocking every write and being ignored.
        value: view.failurePolicy || 'Fail (default)',
      },
    ]
    if (view.paramKind) {
      rows.push({
        label: 'Parameters',
        value: `${view.paramKind.kind} · ${view.paramKind.apiVersion}`,
        info: 'What `params` refers to in the expressions below. A binding names the object.',
      })
    }
    if (view.observedGeneration !== null) {
      rows.push({ label: 'Observed generation', value: String(view.observedGeneration) })
    }
    for (const warning of view.typeCheckWarnings) {
      // THE API SERVER'S OWN WORDS, not ours: it type-checks the CEL against
      // the kinds the policy matches and writes what it found. Nothing here
      // evaluates anything.
      rows.push({ label: warning.fieldRef || 'Type check', value: warning.warning })
    }
    return rows
  })

  const mutatingRows = $derived.by<DetailRow[]>(() => {
    const view = mutating
    if (!view) return []

    const rows: DetailRow[] = [
      { label: 'Failure policy', value: view.failurePolicy || 'Fail (default)' },
      { label: 'Reinvocation policy', value: view.reinvocationPolicy || 'Never (default)' },
    ]
    if (view.paramKind) {
      rows.push({
        label: 'Parameters',
        value: `${view.paramKind.kind} · ${view.paramKind.apiVersion}`,
      })
    }
    return rows
  })

  const bindingRows = $derived.by<DetailRow[]>(() => {
    const view = binding
    if (!view) return []

    const policyKind =
      panel === 'mutating-admission-policy-binding' ? MUTATING_POLICY_KIND : VALIDATING_POLICY_KIND

    const rows: DetailRow[] = [
      {
        label: policyKind,
        value: view.policyName || '—',
        // Cluster-scoped, so it is followed with no namespace. Whether it
        // exists is not checked here.
        onclick: follow(policyKind, view.policyName),
      },
    ]

    if (panel === 'validating-admission-policy-binding') {
      rows.push({
        label: 'Validation actions',
        value:
          view.validationActions.length > 0
            ? joined(view.validationActions)
            : 'none — this binding enforces nothing',
      })
    }

    if (view.paramRef) {
      rows.push({
        label: 'Parameters',
        value: view.paramRef.name || (view.paramRef.selector.length > 0 ? 'by selector' : '—'),
        info: view.paramRef.namespace ? `In namespace ${view.paramRef.namespace}` : undefined,
      })
      for (const term of view.paramRef.selector) {
        rows.push({ label: 'Parameter selector', value: term })
      }
      if (view.paramRef.notFoundAction) {
        rows.push({ label: 'If no parameters found', value: view.paramRef.notFoundAction })
      }
    }
    return rows
  })

  const policyMatch = $derived(validating?.match ?? mutating?.match ?? binding?.match ?? null)
  const policyMatchRows = $derived.by<DetailRow[]>(() =>
    policyMatch ? matchRows(policyMatch) : [],
  )

  const policyExpressionRows = $derived.by<DetailRow[]>(() => {
    const view = validating ?? mutating
    if (!view) return []

    const rows: DetailRow[] = []
    for (const condition of view.matchConditions) {
      rows.push({ label: condition.name || 'match condition', value: condition.expression })
    }
    for (const variable of view.variables) {
      rows.push({ label: `variables.${variable.name}`, value: variable.expression })
    }
    return rows
  })

  const validations = $derived(validating?.validations ?? [])
  const mutations = $derived(mutating?.mutations ?? [])

  const auditRows = $derived.by<DetailRow[]>(() =>
    (validating?.auditAnnotations ?? []).map((annotation) => ({
      label: annotation.key,
      value: annotation.valueExpression,
    })),
  )
</script>

{#if gatewayClassView}
  <DetailSection
    level="h3"
    id="standard-gatewayclass"
    title="Gateway API"
    hint={gatewayClassView.accepted ? `Accepted ${gatewayClassView.accepted.status}` : ''}
  >
    <DetailList rows={gatewayClassRows} />
  </DetailSection>
{:else if gatewayView}
  <DetailSection
    level="h3"
    id="standard-gateway"
    title="Gateway API"
    hint={gatewayView.programmed ? `Programmed ${gatewayView.programmed.status}` : ''}
  >
    <DetailList rows={gatewayRows} />
    {#if gatewayView.addresses.length === 0}
      <!-- Ordinary rather than an error, and often the answer to "why does
           this not work": nothing has claimed it yet. -->
      <p class="mt-3 text-body-small text-on-surface-variant/60">
        No controller has published an address for this Gateway yet.
      </p>
    {/if}
  </DetailSection>

  <DetailSection
    level="h3"
    id="standard-listeners"
    title="Listeners"
    hint={String(listeners.length)}
  >
    {#if listeners.length === 0}
      <p class="text-body-small text-on-surface-variant/70">
        This Gateway declares no listeners, so nothing can attach to it.
      </p>
    {:else}
      <div class="flex flex-col gap-3">
        {#each listeners as listener, index (index)}
          <div class="rounded-sm border border-outline-variant/60 bg-surface-container-lowest p-3">
            <p class="text-body-medium font-medium text-on-surface">
              {listener.name || 'unnamed listener'}
              {#if listenerSummary(listener)}
                <span class="text-on-surface-variant">· {listenerSummary(listener)}</span>
              {/if}
              {#if listener.attachedRoutes !== null}
                <!-- The figure people open a Gateway for: a listener with
                     zero attached routes and a route that says it attached
                     are the two halves of the same investigation. -->
                <span class="text-on-surface-variant">
                  · {listener.attachedRoutes} attached
                </span>
              {/if}
            </p>
            {#if listener.statusOnly}
              <p class="mt-1 text-body-small text-on-surface-variant">
                Reported by the controller, and not declared in this Gateway's spec.
              </p>
            {/if}
            <div class="mt-2">
              <DetailList rows={listenerRows(listener)} />
            </div>
          </div>
        {/each}
      </div>
    {/if}
  </DetailSection>
{:else if routeView}
  <DetailSection level="h3" id="standard-route" title="Gateway API" hint={String(rules.length)}>
    <DetailList rows={routeRows} />
    {#if routeView.parents.length === 0}
      <p class="mt-3 text-body-small text-on-surface-variant/60">
        This route names no parent, so no Gateway has been asked to serve it.
      </p>
    {/if}
  </DetailSection>

  <DetailSection level="h3" id="standard-rules" title="Rules" hint={String(rules.length)}>
    {#if rules.length === 0}
      <p class="text-body-small text-on-surface-variant/70">
        This route declares no rules, so it sends nothing anywhere.
      </p>
    {:else}
      <div class="flex flex-col gap-3">
        {#each rules as rule, index (index)}
          <div class="rounded-sm border border-outline-variant/60 bg-surface-container-lowest p-3">
            <p class="text-body-medium font-medium text-on-surface">
              {rule.name || `rule ${index + 1}`}
            </p>
            <ul class="mt-1 flex flex-col gap-0.5">
              {#each rule.matches.length > 0 ? rule.matches : [[]] as parts, matchIndex (matchIndex)}
                <li class="text-body-small text-on-surface-variant" data-selectable>
                  {matchLine(parts)}
                </li>
              {/each}
            </ul>
            {#each rule.filters as filter, filterIndex (filterIndex)}
              <p class="mt-1 text-body-small text-on-surface-variant">
                <span class="text-on-surface">{filter.type || 'filter'}</span>
                {#if filter.detail}
                  · {filter.detail}
                {/if}
              </p>
            {/each}
            {#if rule.backends.length > 0}
              <div class="mt-2">
                <DetailList rows={backendRows(rule)} />
              </div>
            {:else}
              <!-- A rule with no backend is served a 500 by the API's own
                   definition, which is a fact about the spec rather than a
                   verdict about the cluster. -->
              <p class="mt-2 text-body-small text-on-surface-variant/70">
                This rule names no backend.
              </p>
            {/if}
          </div>
        {/each}
      </div>
    {/if}
  </DetailSection>

  <DetailSection
    level="h3"
    id="standard-route-parents"
    title="Per-parent status"
    hint={String(parentStatuses.length)}
  >
    {#if parentStatuses.length === 0}
      <p class="text-body-small text-on-surface-variant/70">
        No controller has answered about this route yet.
      </p>
    {:else}
      <div class="flex flex-col gap-3">
        {#each parentStatuses as entry, index (index)}
          <div class="rounded-sm border border-outline-variant/60 bg-surface-container-lowest p-3">
            <p class="text-body-medium font-medium text-on-surface">
              {entry.parent.kind}
              <span class="text-on-surface-variant">
                {entry.parent.namespace ? `${entry.parent.namespace}/` : ''}{entry.parent.name ||
                  '—'}
              </span>
              {#if entry.parent.sectionName}
                <span class="text-on-surface-variant">· listener {entry.parent.sectionName}</span>
              {/if}
            </p>
            {#if entry.controllerName}
              <!-- WHICH controller answered, which is half of "who rejected
                   this": a route can be accepted by one Gateway and refused
                   by another in the same status. -->
              <p class="mt-1 text-body-small text-on-surface-variant">{entry.controllerName}</p>
            {/if}
            <div class="mt-2">
              <DetailList rows={nestedConditionRows(entry.conditions)} />
            </div>
          </div>
        {/each}
      </div>
      <p class="mt-3 text-body-small text-on-surface-variant/60">
        One entry per Gateway that answered. A route carries no conditions of its own — this is
        where a parent says whether it accepted it.
      </p>
    {/if}
  </DetailSection>
{:else if claim}
  <DetailSection
    level="h3"
    id="standard-claim"
    title="Device requests"
    hint={String(claim.requests.length)}
  >
    {#if claimRequestRows.length === 0}
      <p class="text-body-small text-on-surface-variant/70">
        This claim requests no devices.
      </p>
    {:else}
      <DetailList rows={claimRequestRows} />
    {/if}
  </DetailSection>

  <DetailSection
    level="h3"
    id="standard-allocation"
    title="Allocation"
    hint={claim.allocated ? String(claim.allocations.length) : ''}
  >
    {#if !claim.allocated}
      <!-- Not allocated is a state, not a failure: a claim created ahead of
           the workload that names it sits here until a pod asks for it. -->
      <p class="text-body-small text-on-surface-variant/70">
        Nothing has been allocated to this claim yet.
      </p>
    {:else}
      <DetailList rows={allocationRows} />
    {/if}
  </DetailSection>

  <DetailSection
    level="h3"
    id="standard-reserved"
    title="Reserved for"
    hint={String(claim.reservedFor.length)}
  >
    {#if claim.reservedFor.length === 0}
      <p class="text-body-small text-on-surface-variant/70">
        Nothing is holding this claim.
      </p>
    {:else}
      <DetailList rows={reservedRows} />
    {/if}
  </DetailSection>

  {#if deviceStatuses.length > 0}
    <DetailSection
      level="h3"
      id="standard-device-status"
      title="Device status"
      hint={String(deviceStatuses.length)}
    >
      <div class="flex flex-col gap-3">
        {#each deviceStatuses as status, index (index)}
          <div class="rounded-sm border border-outline-variant/60 bg-surface-container-lowest p-3">
            <p class="text-body-medium font-medium text-on-surface">
              {[status.pool, status.device].filter(Boolean).join('/') || 'device'}
              <span class="text-on-surface-variant">· {status.driver}</span>
            </p>
            <div class="mt-2">
              <DetailList rows={deviceStatusRows(status)} />
            </div>
          </div>
        {/each}
      </div>
      <p class="mt-3 text-body-small text-on-surface-variant/60">
        As reported by the device's own driver.
      </p>
    </DetailSection>
  {/if}
{:else if template}
  <DetailSection
    level="h3"
    id="standard-template"
    title="Device requests"
    hint={String(template.spec.requests.length)}
  >
    {#if templateRequestRows.length === 0}
      <p class="text-body-small text-on-surface-variant/70">
        This template requests no devices.
      </p>
    {:else}
      <DetailList rows={templateRequestRows} />
    {/if}
    <!-- Future tense, and the heading above carries it: this is what the NEXT
         claim generated from the template will ask for, not what anything
         holds. The claims themselves carry the allocations. -->
    <p class="mt-3 text-body-small text-on-surface-variant/60">
      What every ResourceClaim generated from this template asks for. The generated claims carry
      the allocations.
    </p>
  </DetailSection>
{:else if deviceClassView}
  <DetailSection
    level="h3"
    id="standard-deviceclass"
    title="Device class"
    hint={String(deviceClassView.selectors.length)}
  >
    {#if deviceClassRows.length === 0}
      <p class="text-body-small text-on-surface-variant/70">
        This class selects on nothing, so every device a driver publishes matches it.
      </p>
    {:else}
      <DetailList rows={deviceClassRows} />
    {/if}
  </DetailSection>
{:else if validating}
  <DetailSection
    level="h3"
    id="standard-validating-policy"
    title="Admission policy"
    hint={String(validations.length)}
  >
    <DetailList rows={validatingRows} />
  </DetailSection>

  <DetailSection
    level="h3"
    id="standard-validations"
    title="Validations"
    hint={String(validations.length)}
  >
    {#if validations.length === 0}
      <p class="text-body-small text-on-surface-variant/70">
        This policy declares no validations, so it admits everything it matches.
      </p>
    {:else}
      <div class="flex flex-col gap-3">
        {#each validations as validation, index (index)}
          <div class="rounded-sm border border-outline-variant/60 bg-surface-container-lowest p-3">
            <p class="text-body-small text-on-surface" data-selectable>{validation.expression}</p>
            {#if validation.message}
              <p class="mt-1 text-body-small text-on-surface-variant">{validation.message}</p>
            {/if}
            {#if validation.messageExpression}
              <p class="mt-1 text-body-small text-on-surface-variant" data-selectable>
                {validation.messageExpression}
              </p>
            {/if}
            {#if validation.reason}
              <p class="mt-1 text-body-small text-on-surface-variant/70">{validation.reason}</p>
            {/if}
          </div>
        {/each}
      </div>
      <p class="mt-3 text-body-small text-on-surface-variant/60">
        The expressions as written. Nothing here evaluates one or decides what it would admit.
      </p>
    {/if}
  </DetailSection>
{:else if mutating}
  <DetailSection
    level="h3"
    id="standard-mutating-policy"
    title="Admission policy"
    hint={String(mutations.length)}
  >
    <DetailList rows={mutatingRows} />
  </DetailSection>

  <DetailSection
    level="h3"
    id="standard-mutations"
    title="Mutations"
    hint={String(mutations.length)}
  >
    {#if mutations.length === 0}
      <p class="text-body-small text-on-surface-variant/70">
        This policy declares no mutations, so it changes nothing it matches.
      </p>
    {:else}
      <div class="flex flex-col gap-3">
        {#each mutations as mutation, index (index)}
          <div class="rounded-sm border border-outline-variant/60 bg-surface-container-lowest p-3">
            <p class="text-body-medium font-medium text-on-surface">
              {mutation.patchType || 'patch'}
            </p>
            <p class="mt-1 text-body-small text-on-surface" data-selectable>{mutation.expression}</p>
          </div>
        {/each}
      </div>
      <p class="mt-3 text-body-small text-on-surface-variant/60">
        The expressions as written. Nothing here evaluates one or decides what it would change.
      </p>
    {/if}
  </DetailSection>
{:else if binding}
  <DetailSection level="h3" id="standard-binding" title="Policy binding">
    <DetailList rows={bindingRows} />
  </DetailSection>
{/if}

{#if policyMatch}
  <DetailSection
    level="h3"
    id="standard-match"
    title="Matches"
    hint={String(policyMatch.rules.length)}
  >
    {#if policyMatchRows.length === 0}
      <!-- A policy with no match constraints matches nothing, and a binding
           with no matchResources narrows nothing — the same empty block
           meaning opposite things, so which object this is decides the
           sentence. -->
      <p class="text-body-small text-on-surface-variant/70">
        {binding
          ? 'This binding narrows nothing, so the policy applies wherever its own constraints match.'
          : 'This policy declares no match constraints.'}
      </p>
    {:else}
      <DetailList rows={policyMatchRows} />
    {/if}
  </DetailSection>
{/if}

{#if policyExpressionRows.length > 0}
  <DetailSection
    level="h3"
    id="standard-policy-expressions"
    title="Conditions and variables"
    hint={String(policyExpressionRows.length)}
  >
    <DetailList rows={policyExpressionRows} />
    <p class="mt-3 text-body-small text-on-surface-variant/60">
      A match condition narrows what the policy is asked about; a variable is an expression the
      others reuse.
    </p>
  </DetailSection>
{/if}

{#if auditRows.length > 0}
  <DetailSection
    level="h3"
    id="standard-audit-annotations"
    title="Audit annotations"
    hint={String(auditRows.length)}
  >
    <DetailList rows={auditRows} />
  </DetailSection>
{/if}
