<!--
  Dialog for draining a node.

  Unlike RestartDialog or SuspendDialog, which only confirm an action the
  caller already knows the shape of, this one PLANS before it asks: the same
  domain.PlanDrain the backend runs is fetched as a preview on open and again
  whenever an option changes, so "Will evict N pods" is never a guess and the
  confirm button is disabled the moment the plan is not runnable — the same
  refusal `kubectl drain` would give, seen before a click rather than after.
-->
<script lang="ts">
  import { escapeLayer, type EscapeClaim } from '$lib/escape'
  import { modal } from '$lib/modal'
  import Button from './Button.svelte'
  import { planDrain, drainNode, type DrainPlan, type DrainReport } from '$lib/api/client'
  import { toApiError } from '$lib/api/errors'
  import { Loader, TriangleAlert } from '@lucide/svelte'

  interface Props {
    open: boolean
    clusterId: string
    nodeName: string | null
    onclose: () => void
    /** Called once a drain finishes (successfully or with failures) so the
     * caller can refresh the node's cordoned state and pod list. */
    ondrained: () => void
    /** A failed drain surfaces through the drawer's own actionError banner,
     * the same as every other management action here — not a second error
     * display inside the dialog. */
    onerror: (message: string) => void
  }

  let { open, clusterId, nodeName, onclose, ondrained, onerror }: Props = $props()

  let force = $state(false)
  let deleteEmptyDirData = $state(false)
  /** Blank means "pod default" — DrainOptions.GracePeriodSeconds < 0. */
  let gracePeriodInput = $state('')

  // The PREVIEW's own failure (could not even list the candidates) is shown
  // inline in the preview box rather than through actionError: it explains
  // why the box below it is empty, which is a different kind of message from
  // "the drain you just confirmed failed".
  let plan = $state<DrainPlan | null>(null)
  let planLoading = $state(false)
  let planError = $state<string | null>(null)

  let running = $state(false)
  let report = $state<DrainReport | null>(null)

  /**
   * Guards against a stale preview winning a race.
   *
   * Toggling "delete local storage" while the previous preview is still in
   * flight leaves two requests outstanding, and the one that answers last is
   * not necessarily the one that matches the checkboxes on screen right now.
   */
  let planRequest = 0

  async function loadPlan(cluster: string, node: string, forceValue: boolean, deleteValue: boolean): Promise<void> {
    const request = ++planRequest
    planLoading = true
    try {
      const result = await planDrain(cluster, node, forceValue, deleteValue)
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

  // Re-fetches the preview whenever the dialog opens, or either option
  // changes while it is open. force and deleteEmptyDirData are read directly
  // here (not inside loadPlan) so Svelte tracks them as dependencies.
  $effect(() => {
    if (!open || !nodeName) return
    const cluster = clusterId
    const node = nodeName
    const forceValue = force
    const deleteValue = deleteEmptyDirData
    void loadPlan(cluster, node, forceValue, deleteValue)
  })

  // Fresh state every time the dialog opens, so a previous node's report or
  // error does not flash under this one's name for a moment.
  $effect(() => {
    if (!open) return
    report = null
    running = false
    force = false
    deleteEmptyDirData = false
    gracePeriodInput = ''
  })

  const evictCount = $derived(plan?.evict.length ?? 0)
  const skippedCount = $derived(plan?.skipped.length ?? 0)
  const refusedCount = $derived(plan?.refused.length ?? 0)
  const refusedReasons = $derived.by(() => {
    if (!plan) return ''
    return [...new Set(plan.refused.map((entry) => entry.reason))].join('; ')
  })
  const canConfirm = $derived(!!plan?.runnable && !planLoading && !running)

  async function handleDrain(): Promise<void> {
    if (!nodeName || !canConfirm) return
    running = true
    report = null
    try {
      const trimmed = gracePeriodInput.trim()
      const gracePeriodSeconds = trimmed === '' ? -1 : Math.max(0, Math.trunc(Number(trimmed)))
      report = await drainNode(clusterId, nodeName, force, deleteEmptyDirData, gracePeriodSeconds, 0)
      ondrained()
    } catch (error) {
      onerror(toApiError(error).message)
    } finally {
      running = false
    }
  }

  /** Escape closes; there is no Enter shortcut — draining is not a one-click
   * mistake to make easy, the same reasoning DeleteDialog uses. */
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
    class="fixed top-1/2 left-1/2 z-[70] w-[30rem] max-w-[90vw] -translate-x-1/2 -translate-y-1/2
           rounded-sm border border-outline-variant bg-surface-container-high p-6 shadow-level-3"
    role="dialog"
    aria-modal="true"
    use:modal
    aria-label="Drain node"
  >
    <h2 class="text-headline-small text-on-surface">Drain {nodeName}</h2>

    <p class="mt-4 text-body-medium text-on-surface-variant">
      New pods will not be scheduled here, and every evictable pod running here now is asked to
      leave — a PodDisruptionBudget may refuse an eviction, which is why this can take a while.
    </p>

    <!-- TODO(kubectl-transparency): show the kubectl equivalent for this
         drain (cordon + drain with the chosen flags) here. -->

    <div class="mt-4 flex flex-col gap-2">
      <label class="flex cursor-pointer items-start gap-3 text-body-medium text-on-surface">
        <input type="checkbox" bind:checked={force} class="accent-primary" disabled={running} />
        <span>
          Force pods with no controller
          <span class="block text-body-small text-on-surface-variant/70">
            A bare pod is not recreated once evicted — nothing owns it.
          </span>
        </span>
      </label>

      <label class="flex cursor-pointer items-start gap-3 text-body-medium text-on-surface">
        <input
          type="checkbox"
          bind:checked={deleteEmptyDirData}
          class="accent-primary"
          disabled={running}
        />
        <span>
          Delete pods using local storage
          <span class="block text-body-small text-on-surface-variant/70">
            An emptyDir volume lives on this node and is discarded, not moved.
          </span>
        </span>
      </label>

      <label class="mt-1 block">
        <span class="text-body-small text-on-surface-variant">Grace period (seconds)</span>
        <input
          type="number"
          min="0"
          placeholder="pod default"
          bind:value={gracePeriodInput}
          disabled={running}
          class="field mt-1 w-full px-3 py-2 text-body-medium"
        />
      </label>
    </div>

    <!-- Preview, rebuilt from the same plan the drain itself will run. -->
    <div class="mt-4 min-h-[3rem] rounded-sm border border-outline-variant/60 bg-surface p-3 text-body-small">
      {#if planLoading && !plan}
        <p class="flex items-center gap-2 text-on-surface-variant">
          <Loader class="size-3.5 animate-spin" strokeWidth={2} />
          Checking what this would do…
        </p>
      {:else if planError}
        <p class="flex items-center gap-2 text-error">
          <TriangleAlert class="size-3.5 shrink-0" strokeWidth={2} />
          {planError}
        </p>
      {:else if plan}
        <p class="text-on-surface">
          Will evict {evictCount} {evictCount === 1 ? 'pod' : 'pods'}
          {#if skippedCount > 0}
            · Skipping {skippedCount} DaemonSet/static {skippedCount === 1 ? 'pod' : 'pods'}
          {/if}
          {#if refusedCount > 0}
            · Refusing {refusedCount} ({refusedReasons})
          {/if}
        </p>
      {/if}
    </div>

    <!-- Running / result state. No progress stream exists on this call, so a
         run shows as indeterminate and the counts arrive with the final
         report rather than climbing live. -->
    {#if running}
      <p class="mt-4 flex items-center gap-2 text-body-medium text-on-surface-variant">
        <Loader class="size-4 animate-spin" strokeWidth={2} />
        Draining…
      </p>
    {:else if report}
      <div class="mt-4 text-body-medium text-on-surface">
        <p>
          Evicted {report.evicted.length} of {report.evicted.length + report.failed.length}
          {#if report.timedOut}
            <span class="text-error">— timed out waiting on the rest</span>
          {/if}
        </p>
        {#if report.failed.length > 0}
          <ul class="mt-2 flex flex-col gap-1 text-body-small text-on-surface-variant">
            {#each report.failed as failure (failure.pod)}
              <li><strong class="text-on-surface" data-selectable>{failure.pod}</strong>: {failure.reason}</li>
            {/each}
          </ul>
        {/if}
      </div>
    {/if}

    <div class="mt-6 flex justify-end gap-3">
      <Button variant="outlined" onclick={onclose}>{report ? 'Close' : 'Cancel'}</Button>
      {#if !report}
        <Button variant="filled" onclick={handleDrain} disabled={!canConfirm} loading={running}>Drain</Button>
      {/if}
    </div>
  </div>
{/if}
