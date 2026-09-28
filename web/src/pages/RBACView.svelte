<!--
  The Permissions page: what this kubeconfig may do in the tab's namespace.

  A PAGE, NOT A STACK OF CARDS. The rules review is the one question this
  page answers on arrival, so it takes the whole width the way a list does;
  the other two questions are tools, and they live where every view keeps its
  tools — the header toolbar (`ClusterWorkspace`), each opening a dialog:
  Can I… (`CanIDialog`) and Who holds a role (`RoleHoldersDialog`). Their
  explanations are help topics under each dialog's (?), not paragraphs here.

  THE API SERVER DECIDES AND PODSTEER ONLY FLAGS. The rules review and the
  access review are quotations, rendered as they arrived; the blast-radius
  flags in the role dialog are the one verdict, and they live in Go where each
  has a test (`app/domain/rbac.go`).

  NOTHING HERE POLLS. Every read happens because the page opened, its
  namespace changed, or a button was pressed — which is why
  `ClusterSession`'s tick has a case for this view that fetches nothing: an
  allow shown from a previous tick would keep reading as granted after the
  permission behind it was revoked.
-->
<script lang="ts">
  import CanIDialog from '$lib/components/CanIDialog.svelte'
  import EmptyState from '$lib/components/EmptyState.svelte'
  import ErrorBanner from '$lib/components/ErrorBanner.svelte'
  import RbacPathTable from '$lib/components/RbacPathTable.svelte'
  import RbacVerbTable from '$lib/components/RbacVerbTable.svelte'
  import ReviewNotice from '$lib/components/ReviewNotice.svelte'
  import RoleHoldersDialog from '$lib/components/RoleHoldersDialog.svelte'
  import { toApiError, type ApiError } from '$lib/api/errors'
  import { subjectRules as askSubjectRules, type SubjectRules } from '$lib/api/client'
  import { pathRows, reviewState, verbRows } from '$lib/rbac'
  import type { ClusterSession } from '$stores/session.svelte'
  import { untrack } from 'svelte'

  interface Props {
    session: ClusterSession
    /** Whether the Can I dialog is open; the toolbar opens it. */
    canIOpen?: boolean
    /** Whether the role dialog is open; the toolbar opens it. */
    rolesOpen?: boolean
  }

  let {
    session,
    canIOpen = $bindable(false),
    rolesOpen = $bindable(false),
  }: Props = $props()

  let loading = $state(false)

  let rules = $state<SubjectRules | null>(null)
  let rulesError = $state<ApiError | null>(null)

  /** Which cluster and namespace the answer on screen is about. */
  let rulesFor = $state('')

  /**
   * Counts the reads issued, so a slow one cannot overwrite a later answer.
   *
   * Switching namespace twice quickly would otherwise let the first
   * namespace's permissions land under the second one's heading — a list of
   * what somebody may do, attributed to the wrong place.
   */
  let rulesGeneration = 0

  async function loadRules(clusterId: string, namespace: string): Promise<void> {
    rulesFor = `${clusterId} ${namespace}`
    loading = true
    rulesError = null
    const generation = ++rulesGeneration
    try {
      const answer = await askSubjectRules(clusterId, namespace)
      if (generation !== rulesGeneration) return
      rules = answer
    } catch (cause) {
      if (generation !== rulesGeneration) return
      rules = null
      rulesError = toApiError(cause)
    } finally {
      if (generation === rulesGeneration) loading = false
    }
  }

  /**
   * The application's own Refresh re-asks — and ONLY a person pressing it.
   * The tick fetches nothing here by design; `manualRefreshes` counts the
   * presses, so this effect runs once per press and never on the timer.
   */
  let seenRefreshes = untrack(() => session.manualRefreshes)
  $effect(() => {
    const presses = session.manualRefreshes
    if (presses === seenRefreshes) return
    seenRefreshes = presses
    void loadRules(session.cluster.id, session.namespace)
  })

  /**
   * One request when the page opens, and one more whenever the tab's
   * namespace changes — never on the refresh tick. Keyed on the pair, because
   * "what may I do here" is a different question in every namespace.
   */
  $effect(() => {
    const key = `${session.cluster.id} ${session.namespace}`
    if (key === rulesFor) return
    void loadRules(session.cluster.id, session.namespace)
  })

  const rulesState = $derived(reviewState(rules?.status ?? 'answered', rules?.refusal ?? ''))
  const namespacedRows = $derived(verbRows(rules?.namespaced ?? []))
  const clusterScopedRows = $derived(pathRows(rules?.clusterScoped ?? []))

  /** The namespace the review actually named. */
  const reviewedNamespace = $derived(rules?.namespace || session.namespace || 'default')
</script>

<div class="min-h-0 flex-1 overflow-y-auto">
  <div class="flex flex-col gap-8 px-6 py-5">
    <section>
      <div class="flex flex-wrap items-baseline gap-x-3 gap-y-1">
        <h2 class="text-title-large text-on-surface">
          What this kubeconfig can do in
          <span class="font-mono" data-selectable>{reviewedNamespace}</span>
        </h2>
      </div>
      <p class="mt-1 text-body-medium text-on-surface-variant">
        The API server's own enumeration of your permissions, in one request.
      </p>

      <ErrorBanner error={rulesError} ondismiss={() => (rulesError = null)} class="mt-4" />

      <div class="mt-4">
        {#if rulesState.kind === 'unavailable'}
          <ReviewNotice state={rulesState} />
        {:else if rules}
          {#if rules.incomplete}
            <!-- The API server said its own answer is partial. A partial list
                 that does not say so reads as a complete one. -->
            <p class="mb-4 rounded-sm bg-warning-container/40 px-3 py-2 text-body-medium text-on-surface">
              The API server could not enumerate everything, so this list may be short.
              {rules.incompleteReason}
            </p>
          {/if}

          {#if namespacedRows.length === 0}
            <p class="text-body-medium text-on-surface-variant">
              Nothing. This account holds no permissions on objects in this namespace.
            </p>
          {:else}
            <RbacVerbTable rows={namespacedRows} />
          {/if}
        {:else if !loading}
          <EmptyState title="Nothing read yet" description="Press Refresh to ask the cluster." />
        {/if}
      </div>
    </section>

    {#if rules && rulesState.kind !== 'unavailable'}
      <section>
        <h2 class="text-title-medium text-on-surface">Cluster-scoped paths</h2>
        <p class="mt-1 text-body-medium text-on-surface-variant">
          URL paths belong to the API server rather than to a namespace, so the review reports
          them apart from the rules above.
        </p>
        <div class="mt-4">
          {#if clusterScopedRows.length === 0}
            <p class="text-body-medium text-on-surface-variant">None.</p>
          {:else}
            <RbacPathTable rows={clusterScopedRows} />
          {/if}
        </div>
      </section>
    {/if}
  </div>
</div>

<CanIDialog
  open={canIOpen}
  clusterId={session.cluster.id}
  namespace={session.namespace}
  onclose={() => (canIOpen = false)}
/>
<RoleHoldersDialog open={rolesOpen} clusterId={session.cluster.id} onclose={() => (rolesOpen = false)} />
