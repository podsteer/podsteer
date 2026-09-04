<!--
  The review step before a bulk action, and the outcome after it.

  Like DrainDialog, this PLANS before it asks: every ticked row is listed
  with what will happen to it — acted on, with a note where there is
  something to know, or skipped with the reason — from `domain.PlanBulk`,
  THE SAME FUNCTION the run then executes (see ManagementService.runBulk),
  so what was reviewed is what happens. Nothing here decides anything about
  an object; it shows the verdicts and the kubectl that would do the same.

  After the run every row gets its own outcome: done, failed with the same
  classified message a single write's failure carries, or skipped. One
  failure never hides the rest, and it keeps the selection so the operator
  can read why and try exactly that row again; a clean run clears it.

  On a cluster whose group is marked production, confirming costs typing the
  CLUSTER'S name — the object-name gate DeleteDialog uses has no single
  object to name here, and the cluster is the thing a selection of forty rows
  is really being asked about.
-->
<script lang="ts">
  import { untrack } from 'svelte'
  import { escapeLayer, type EscapeClaim } from '$lib/escape'
  import { modal } from '$lib/modal'
  import Button from './Button.svelte'
  import KubectlHint from './KubectlHint.svelte'
  import { nameConfirmed } from '$lib/confirm'
  import { BULK_ACTIONS, bulkCommand, rowKey, type BulkActionId, type BulkItem } from '$lib/bulk'
  import { describeAutoscaler, type AutoscalerCheck } from '$lib/autoscalers'
  import {
    bulkCordon,
    bulkDelete,
    bulkRestart,
    bulkScale,
    planBulk,
    type BulkPlan,
    type BulkResult,
  } from '$lib/api/client'
  import { toApiError } from '$lib/api/errors'
  import type { ClusterSession } from '$stores/session.svelte'
  import { CircleCheck, CircleMinus, CircleX, Loader, TriangleAlert } from '@lucide/svelte'

  interface Props {
    open: boolean
    session: ClusterSession
    action: BulkActionId
    /**
     * The group's name, when this cluster is marked production — null or
     * undefined otherwise. Non-null both shows the banner and turns on the
     * type-the-cluster-name requirement.
     */
    productionGroup?: string | null
    onclose: () => void
  }

  let { open, session, action, productionGroup, onclose }: Props = $props()

  const copy = $derived(BULK_ACTIONS[action])
  const kind = $derived(session.selectedKind)
  const items = $derived(session.bulkItems)

  /** The target count, for scale. Seeded from the first ticked row on open. */
  let replicas = $state(0)
  /** What has been typed into the confirmation field. */
  let typed = $state('')

  // The PREVIEW's own failure is shown inline where the lines would be: it
  // explains why the list is empty, which is a different message than a
  // confirmed action having failed.
  let plan = $state<BulkPlan | null>(null)
  let planLoading = $state(false)
  let planError = $state<string | null>(null)

  let running = $state(false)
  let runError = $state<string | null>(null)
  let results = $state<BulkResult[] | null>(null)

  /** Autoscaler checks per row, for scale — keyed by rowKey. */
  let autoscalers = $state<Record<string, AutoscalerCheck>>({})

  // Fresh state every time the dialog opens, so a previous selection's plan
  // or results do not flash under this one for a moment. The seed reads the
  // rows untracked: the list behind the dialog refreshes on its own clock,
  // and a refresh must not reset a count the operator has already typed.
  $effect(() => {
    if (!open) return
    plan = null
    planError = null
    results = null
    runError = null
    running = false
    typed = ''
    autoscalers = {}
    replicas = untrack(() => session.bulkItems[0]?.replicas ?? 0)
  })

  /**
   * Guards against a stale preview winning a race — the same reasoning
   * DrainDialog's planRequest gives: two previews can be in flight after a
   * quick edit to the count, and the last to ANSWER is not necessarily the
   * one that matches what is on screen.
   */
  let planRequest = 0

  async function loadPlan(cluster: string, verb: BulkActionId, rows: BulkItem[], target: number): Promise<void> {
    const request = ++planRequest
    planLoading = true
    try {
      const result = await planBulk(cluster, verb, rows, target)
      if (request !== planRequest) return
      plan = result
      planError = null
    } catch (error) {
      if (request !== planRequest) return
      plan = null
      planError = toApiError(error).message
    } finally {
      if (request === planRequest) planLoading = false
    }
  }

  // Re-plans whenever the dialog opens, the target count changes, or the
  // rows behind it change — a poll can drop a row somebody else deleted, and
  // the review must not keep promising to act on it. Stops once a run has
  // happened: the results are the answer then, not a fresh plan over what is
  // left.
  $effect(() => {
    if (!open || results) return
    const cluster = session.cluster.id
    const rows = items
    const target = replicas
    const verb = action
    if (rows.length === 0) {
      plan = null
      return
    }
    void loadPlan(cluster, verb, rows, target)
  })

  /**
   * Whether an autoscaler owns each row's replica count, for scale only —
   * the same check ScaleDialog makes, through the same session cache, so a
   * namespace's autoscalers are read once however many rows are ticked in
   * it. Not cleared on a refresh: the rows are keyed, so a stale entry can
   * only ever describe a row that is still there.
   */
  let autoscalerRequest = 0

  $effect(() => {
    if (!open || action !== 'scale') return
    const rows = items
    const request = ++autoscalerRequest
    void Promise.all(
      rows.map(
        async (row) =>
          [rowKey(row.namespace, row.name), await session.autoscalersFor(row.kind, row.namespace, row.name)] as const,
      ),
    ).then((entries) => {
      if (request !== autoscalerRequest) return
      autoscalers = Object.fromEntries(entries)
    })
  })

  const acting = $derived(plan?.lines.filter((line) => line.act) ?? [])

  /** The kubectl for what the plan will actually touch — never the whole selection. */
  const command = $derived(
    kind && acting.length > 0 ? bulkCommand(session.cluster.id, action, kind, acting, replicas) : null,
  )

  const requiresTypedName = $derived(!!productionGroup)
  const confirmed = $derived(!requiresTypedName || nameConfirmed(typed, session.cluster.id))
  const canConfirm = $derived(
    !!plan && plan.acting > 0 && !planLoading && !running && confirmed && (action !== 'scale' || replicas >= 0),
  )

  /**
   * How many rows the dialog is about: the selection while reviewing, the
   * results once run — a clean run clears the selection, and the heading
   * over the outcome must not then read "Delete 0 pods".
   */
  const shown = $derived(results ? results.length : items.length)

  /** "3 pods", "1 deployment" — the kind as the navigator names it. */
  const noun = $derived(kind ? (shown === 1 ? kind.singular : kind.title).toLowerCase() : 'rows')

  const doneCount = $derived(results?.filter((result) => result.done).length ?? 0)
  const failedCount = $derived(results?.filter((result) => !result.done && !result.skipped).length ?? 0)
  const skippedCount = $derived(results?.filter((result) => result.skipped).length ?? 0)

  async function run(): Promise<void> {
    if (!canConfirm) return
    // Snapshotted: the rows can change under a slow run, and what was
    // reviewed is what runs.
    const rows = items
    const cluster = session.cluster.id
    running = true
    runError = null
    try {
      let outcome: BulkResult[]
      switch (action) {
        case 'delete':
          outcome = await bulkDelete(cluster, rows)
          break
        case 'restart':
          outcome = await bulkRestart(cluster, rows)
          break
        case 'scale':
          outcome = await bulkScale(cluster, rows, replicas)
          break
        case 'cordon':
          outcome = await bulkCordon(cluster, rows, true)
          break
        case 'uncordon':
          outcome = await bulkCordon(cluster, rows, false)
          break
      }
      results = outcome
      // Cleared only when nothing failed: a failed row stays ticked so the
      // operator can read why and try exactly that row again.
      if (outcome.every((result) => result.done || result.skipped)) session.selection.clear()
      await session.refresh()
    } catch (error) {
      runError = toApiError(error).message
    } finally {
      running = false
    }
  }

  /**
   * Escape closes; there is no Enter shortcut. Forty deletes are not a
   * one-keystroke mistake to make easy — the same reasoning DeleteDialog and
   * DrainDialog give for binding none.
   */
  function onKeydown(event: KeyboardEvent): void {
    if (!open || event.key !== 'Escape') return
    if (!escape?.owns()) return
    onclose()
  }

  /** Escape belongs to the innermost open layer. See $lib/escape. */
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

  /** A row's label: "namespace/name", or the bare name when cluster-scoped. */
  function label(row: { namespace: string; name: string }): string {
    return rowKey(row.namespace, row.name)
  }
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
    class="fixed top-1/2 left-1/2 z-[70] flex max-h-[85vh] w-[34rem] max-w-[92vw] -translate-x-1/2 -translate-y-1/2
           flex-col rounded-sm border border-outline-variant bg-surface-container-high p-6 shadow-level-3"
    role="dialog"
    aria-modal="true"
    use:modal
    aria-label="{copy.label} {noun}"
  >
    <h2 class="text-headline-small text-on-surface">
      {copy.label} <span class="tabular-nums">{shown}</span> {noun}
    </h2>

    {#if productionGroup}
      <p
        class="mt-4 flex items-start gap-2 rounded-sm border border-error/30 bg-error-container/40
               px-3 py-2 text-body-small text-on-error-container"
      >
        <TriangleAlert class="mt-0.5 size-4 shrink-0" strokeWidth={1.8} />
        This cluster is in {productionGroup}, marked production.
      </p>
    {/if}

    {#if action === 'scale' && !results}
      <label class="mt-4 block">
        <span class="text-body-small text-on-surface-variant">Replicas</span>
        <input
          type="number"
          bind:value={replicas}
          min="0"
          disabled={running}
          class="field mt-1 w-full px-3 py-2 text-body-medium"
        />
      </label>
    {/if}

    <div class="mt-4 min-h-0 flex-1 overflow-auto">
      {#if results}
        <!-- The outcome, one line per row, in the selection's order. -->
        <p class="text-body-medium text-on-surface">
          <span class="capitalize">{copy.done}</span>
          <span class="tabular-nums">{doneCount}</span> of
          <span class="tabular-nums">{results.length}</span>
          {#if failedCount > 0}
            · <span class="text-error"><span class="tabular-nums">{failedCount}</span> failed</span>
          {/if}
          {#if skippedCount > 0}
            · <span class="tabular-nums">{skippedCount}</span> skipped
          {/if}
        </p>
        <ul class="mt-2 flex flex-col gap-1 text-body-small">
          {#each results as result (label(result))}
            <li class="flex items-start gap-2">
              {#if result.done}
                <CircleCheck class="mt-0.5 size-3.5 shrink-0 text-success" strokeWidth={2} />
              {:else if result.skipped}
                <CircleMinus class="mt-0.5 size-3.5 shrink-0 text-on-surface-variant/60" strokeWidth={2} />
              {:else}
                <CircleX class="mt-0.5 size-3.5 shrink-0 text-error" strokeWidth={2} />
              {/if}
              <div class="min-w-0 flex-1">
                <p class="truncate text-on-surface" data-selectable>{label(result)}</p>
                <p class="text-on-surface-variant">
                  {#if result.done}
                    {copy.done}{result.note ? ` — ${result.note}` : ''}
                  {:else if result.skipped}
                    skipped — {result.reason}
                  {:else}
                    <span class="text-error">failed</span> — {result.reason}
                  {/if}
                </p>
              </div>
            </li>
          {/each}
        </ul>
      {:else if planLoading && !plan}
        <p class="flex items-center gap-2 text-body-small text-on-surface-variant">
          <Loader class="size-3.5 animate-spin" strokeWidth={2} />
          Checking what this would do…
        </p>
      {:else if planError}
        <p class="flex items-center gap-2 text-body-small text-error">
          <TriangleAlert class="size-3.5 shrink-0" strokeWidth={2} />
          {planError}
        </p>
      {:else if plan}
        <!-- The review: every ticked row and its verdict, from the same plan
             the run will execute. -->
        <p class="text-body-medium text-on-surface">
          Will {copy.label.toLowerCase()}
          <span class="tabular-nums">{plan.acting}</span> of
          <span class="tabular-nums">{plan.lines.length}</span>
          {#if plan.skipped > 0}
            · skipping <span class="tabular-nums">{plan.skipped}</span>
          {/if}
        </p>
        <ul class="mt-2 flex flex-col gap-1 text-body-small">
          {#each plan.lines as line (label(line))}
            {@const check = autoscalers[rowKey(line.namespace, line.name)]}
            <li class="flex items-start gap-2">
              {#if line.act}
                <CircleCheck class="mt-0.5 size-3.5 shrink-0 text-primary" strokeWidth={2} />
              {:else}
                <CircleMinus class="mt-0.5 size-3.5 shrink-0 text-on-surface-variant/60" strokeWidth={2} />
              {/if}
              <div class="min-w-0 flex-1">
                <p class="truncate text-on-surface" data-selectable>{label(line)}</p>
                <p class="text-on-surface-variant">
                  {#if line.act}
                    will be {copy.done}{line.note ? ` — ${line.note}` : ''}
                  {:else}
                    skipped — {line.reason}
                  {/if}
                </p>
                <!--
                  The same warning ScaleDialog gives, from the same check:
                  scaling by hand under an autoscaler is undone within its
                  next sync period. It does not block the action.
                -->
                {#if line.act && check?.status === 'known' && check.autoscalers.length > 0}
                  <p class="mt-0.5 flex items-start gap-1.5 text-gauge-warn">
                    <TriangleAlert class="mt-0.5 size-3.5 shrink-0" strokeWidth={2} />
                    <span>
                      An autoscaler manages this replica count —
                      {check.autoscalers.map((ref) => `${ref.name} (${describeAutoscaler(ref)})`).join(', ')}.
                      It will override whatever you set here within its sync period.
                    </span>
                  </p>
                {:else if line.act && check?.status === 'unknown'}
                  <p class="mt-0.5 text-on-surface-variant/70">
                    Could not check for an autoscaler: {check.reason}
                  </p>
                {/if}
              </div>
            </li>
          {/each}
        </ul>
      {:else}
        <p class="text-body-small text-on-surface-variant">Nothing is selected.</p>
      {/if}
    </div>

    {#if command && !results}
      <div class="mt-4">
        <KubectlHint {command} />
      </div>
    {/if}

    {#if requiresTypedName && !results}
      <label class="mt-4 block">
        <span class="text-body-small text-on-surface-variant">
          Type the cluster's name,
          <strong class="text-on-surface" data-selectable>{session.cluster.id}</strong>, to confirm
        </span>
        <input
          type="text"
          bind:value={typed}
          autocomplete="off"
          spellcheck="false"
          disabled={running}
          aria-describedby="bulk-confirm-hint"
          class="field mt-1 w-full px-3 py-2 text-body-medium"
        />
      </label>
      <p id="bulk-confirm-hint" class="mt-1.5 text-body-small text-on-surface-variant/70">
        {confirmed
          ? 'Name confirmed.'
          : `${copy.label} stays disabled until the name above matches exactly.`}
      </p>
    {/if}

    {#if runError}
      <p class="mt-4 flex items-start gap-2 text-body-small text-error">
        <TriangleAlert class="mt-0.5 size-3.5 shrink-0" strokeWidth={2} />
        {runError}
      </p>
    {/if}

    <div class="mt-6 flex justify-end gap-3">
      <Button variant="outlined" onclick={onclose}>{results ? 'Close' : 'Cancel'}</Button>
      {#if !results}
        <Button
          variant="filled"
          disabled={!canConfirm}
          loading={running}
          describedBy={requiresTypedName ? 'bulk-confirm-hint' : undefined}
          onclick={run}
        >
          {copy.label}{plan && plan.acting > 0 ? ` ${plan.acting}` : ''}
        </Button>
      {/if}
    </div>
  </div>
{/if}
