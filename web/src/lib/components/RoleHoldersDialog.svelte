<!--
  "Who holds a role" — the reverse lookup, from the Permissions page's
  toolbar: which bindings reference a Role or ClusterRole, who they grant it
  to, and what its rules reach.

  THREE REQUESTS, MADE WHEN INSPECT IS PRESSED and never on a refresh or on
  opening. The blast-radius flags are the one verdict on the Permissions page
  and they come from the Go domain (`domain.AssessRole`), where each has a
  test; everything else is quoted. Why the lookup is cluster-wide, and what
  the flags mean, is under the (?) as the `role-holders` topic.

  The role and the bindings carry SEPARATE statuses, because an account
  routinely may read one and not the other, and one refusal must not blank the
  half that answered.
-->
<script lang="ts">
  import { escapeLayer, type EscapeClaim } from '$lib/escape'
  import { modal } from '$lib/modal'
  import { toApiError, type ApiError } from '$lib/api/errors'
  import { inspectRole as askInspectRole, type RoleInspection } from '$lib/api/client'
  import { countedSubjects, distinctSubjects, pathRows, reviewState, subjectLabel, verbRows } from '$lib/rbac'
  import Button from './Button.svelte'
  import DialogHeader from './DialogHeader.svelte'
  import ErrorBanner from './ErrorBanner.svelte'
  import RbacPathTable from './RbacPathTable.svelte'
  import RbacVerbTable from './RbacVerbTable.svelte'
  import ReviewNotice from './ReviewNotice.svelte'
  import Select from './Select.svelte'
  import { AlertOctagon, AlertTriangle, Info, UserSearch } from '@lucide/svelte'

  interface Props {
    open: boolean
    clusterId: string
    onclose: () => void
  }

  let { open, clusterId, onclose }: Props = $props()

  const ROLE_SCOPES = [
    { value: 'cluster', label: 'ClusterRole' },
    { value: 'namespace', label: 'Role' },
  ]

  let roleScope = $state<'cluster' | 'namespace'>('cluster')
  let roleNamespace = $state('')
  let roleName = $state('')

  let inspection = $state<RoleInspection | null>(null)
  let inspectionLoading = $state(false)
  let inspectionError = $state<ApiError | null>(null)
  let inspectedFor = ''

  // An answer about another tab's cluster is an answer about the wrong one.
  $effect(() => {
    if (clusterId !== inspectedFor) {
      inspection = null
      inspectionError = null
    }
  })

  const canInspect = $derived(
    roleName.trim() !== '' && (roleScope === 'cluster' || roleNamespace.trim() !== ''),
  )

  async function inspect(): Promise<void> {
    if (!canInspect || inspectionLoading) return
    inspectionLoading = true
    inspectionError = null
    inspectedFor = clusterId
    try {
      inspection = await askInspectRole(
        clusterId,
        roleScope,
        roleScope === 'cluster' ? '' : roleNamespace.trim(),
        roleName.trim(),
      )
    } catch (cause) {
      inspection = null
      inspectionError = toApiError(cause)
    } finally {
      inspectionLoading = false
    }
  }

  const roleState = $derived(inspection ? reviewState(inspection.status, inspection.refusal) : null)
  const bindingsState = $derived(
    inspection ? reviewState(inspection.bindingsStatus, inspection.bindingsRefusal) : null,
  )
  const roleRows = $derived(verbRows(inspection?.rules ?? []))
  const rolePaths = $derived(pathRows(inspection?.rules ?? []))
  const holders = $derived(distinctSubjects(inspection?.bindings ?? []))

  function severityIcon(severity: string) {
    if (severity === 'critical') return AlertOctagon
    if (severity === 'warning') return AlertTriangle
    return Info
  }

  function onKeydown(event: KeyboardEvent): void {
    if (!open || event.key !== 'Escape') return
    if (!escape?.owns()) return
    onclose()
  }

  let escape = $state<EscapeClaim | null>(null)
  $effect(() => {
    if (!open) return
    const held = escapeLayer()
    escape = held
    return () => {
      held.release()
      escape = null
    }
  })
</script>

<svelte:window onkeydown={onKeydown} />

{#if open}
  <button
    type="button"
    aria-label="Close dialog"
    tabindex="-1"
    class="fixed inset-0 z-[60] cursor-default bg-scrim/40"
    onclick={onclose}
  ></button>

  <div
    class="fixed inset-0 z-[70] m-auto flex h-fit max-h-[88vh] w-[52rem] max-w-[94vw] flex-col
           overflow-hidden rounded-sm border border-outline-variant bg-surface-container-high
           shadow-level-3"
    role="dialog"
    aria-modal="true"
    use:modal
    aria-label="Who holds a role"
  >
    <div class="shrink-0 p-6 pb-4">
      <DialogHeader title="Who holds a role" icon={UserSearch} help="role-holders" {onclose} />

      <form
        class="mt-5 flex flex-wrap items-end gap-3"
        onsubmit={(event) => {
          event.preventDefault()
          void inspect()
        }}
      >
        <Select
          label="Kind"
          value={roleScope}
          options={ROLE_SCOPES}
          onchange={(value) => (roleScope = value as 'cluster' | 'namespace')}
          class="w-44"
        />
        {#if roleScope === 'namespace'}
          <label class="flex w-48 flex-col gap-1">
            <span class="text-label-medium text-on-surface-variant">Namespace</span>
            <input
              type="text"
              bind:value={roleNamespace}
              autocomplete="off"
              spellcheck="false"
              class="field w-full px-3 py-2 text-body-medium"
            />
          </label>
        {/if}
        <label class="flex min-w-48 flex-1 flex-col gap-1">
          <span class="text-label-medium text-on-surface-variant">Name</span>
          <input
            type="text"
            bind:value={roleName}
            autocomplete="off"
            spellcheck="false"
            placeholder="cluster-admin"
            class="field w-full px-3 py-2 text-body-medium"
          />
        </label>
        <Button type="submit" loading={inspectionLoading} disabled={!canInspect}>Inspect</Button>
      </form>

      <ErrorBanner error={inspectionError} ondismiss={() => (inspectionError = null)} class="mt-4" />
    </div>

    {#if inspection}
      <div class="min-h-0 flex-1 overflow-y-auto border-t border-outline-variant/60 px-6 py-5">
        <!-- Blast radius: the one verdict on the page, from the Go domain. -->
        {#if (inspection.findings ?? []).length > 0}
          <h3 class="mb-2 text-title-small font-semibold text-on-surface">What this role reaches</h3>
          <ul class="flex flex-col divide-y divide-outline-variant/40 rounded-sm border border-outline-variant/60">
            {#each inspection.findings ?? [] as finding (finding.id)}
              {@const Icon = severityIcon(finding.severity)}
              <li class="flex items-start gap-3 px-3 py-2.5">
                <Icon
                  class="mt-0.5 size-4 shrink-0
                         {finding.severity === 'critical'
                    ? 'text-error'
                    : finding.severity === 'warning'
                      ? 'text-gauge-warn-ink'
                      : 'text-on-surface-variant'}"
                  strokeWidth={2}
                />
                <div class="min-w-0">
                  <p class="text-body-medium font-medium text-on-surface">{finding.title}</p>
                  <p class="mt-0.5 text-body-medium text-on-surface-variant">{finding.detail}</p>
                  <p class="mt-1 text-body-medium text-on-surface-variant/80">{finding.advice}</p>
                </div>
              </li>
            {/each}
          </ul>
        {:else if roleState?.kind === 'answered'}
          <!-- A role with nothing to flag says so. -->
          <p class="text-body-medium text-on-surface-variant">
            Nothing in this {inspection.kind}'s rules raises a blast-radius flag.
          </p>
        {/if}

        <h3 class="mt-6 mb-2 text-title-small font-semibold text-on-surface">Its rules</h3>
        {#if roleState?.kind === 'unavailable'}
          <ReviewNotice state={roleState} />
        {:else if roleRows.length === 0 && rolePaths.length === 0}
          <p class="text-body-medium text-on-surface-variant">
            This {inspection.kind} carries no rules, so it grants nothing.
          </p>
        {:else}
          {#if roleRows.length > 0}
            <RbacVerbTable rows={roleRows} />
          {/if}
          {#if rolePaths.length > 0}
            <div class="mt-4"><RbacPathTable rows={rolePaths} /></div>
          {/if}
        {/if}

        <h3 class="mt-6 mb-2 text-title-small font-semibold text-on-surface">
          Bound by
          {#if bindingsState?.kind === 'answered'}
            <span class="font-normal text-on-surface-variant">
              · {countedSubjects(holders.length)} across {(inspection.bindings ?? []).length}
              {(inspection.bindings ?? []).length === 1 ? 'binding' : 'bindings'}
            </span>
          {/if}
        </h3>
        {#if bindingsState?.kind === 'unavailable'}
          <ReviewNotice state={bindingsState} />
        {:else if (inspection.bindings ?? []).length === 0}
          <p class="text-body-medium text-on-surface-variant">
            Nothing references this {inspection.kind}, so nobody holds it through one.
          </p>
        {:else}
          <ul class="flex flex-col divide-y divide-outline-variant/40 rounded-sm border border-outline-variant/60">
            {#each inspection.bindings ?? [] as binding (`${binding.kind}/${binding.namespace}/${binding.name}`)}
              <li class="flex flex-wrap items-center gap-x-3 gap-y-1.5 px-3 py-2.5">
                <p class="font-mono text-body-small text-on-surface" data-selectable>
                  <span class="text-on-surface-variant">{binding.kind}</span>
                  {binding.namespace ? `${binding.namespace}/` : ''}{binding.name}
                </p>
                {#if (binding.subjects ?? []).length === 0}
                  <p class="text-body-medium text-on-surface-variant">
                    Names no subjects, so it grants the role to nobody.
                  </p>
                {:else}
                  <div class="flex flex-wrap gap-1">
                    {#each binding.subjects ?? [] as holder (`${holder.kind}/${holder.namespace}/${holder.name}`)}
                      <span
                        class="rounded-full bg-surface-container px-2 py-0.5 text-label-small
                               text-on-surface-variant"
                        data-selectable
                      >
                        {holder.kind}: {subjectLabel(holder)}
                      </span>
                    {/each}
                  </div>
                {/if}
              </li>
            {/each}
          </ul>
        {/if}
      </div>
    {/if}
  </div>
{/if}
