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
  import { drain } from '$lib/kubectl'
  import Button from './Button.svelte'
  import Checkbox from './Checkbox.svelte'
  import DialogHeader from './DialogHeader.svelte'
  import DialogFooter from './DialogFooter.svelte'
  import { planDrain, drainNode, type DrainPlan, type DrainReport } from '$lib/api/client'
  import { toApiError } from '$lib/api/errors'
  import { Loader, TriangleAlert } from '@lucide/svelte'

  interface Props {
    open: boolean
    clusterId: string
    /** The kubeconfig context this cluster connects through. See $lib/kubectl. */
    ctx: string
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

  let { open, clusterId, ctx, nodeName, onclose, ondrained, onerror }: Props = $props()

  let force = $state(false)
  let deleteEmptyDirData = $state(false)
  /** Blank means "pod default" — DrainOptions.GracePeriodSeconds < 0. */
  let gracePeriodInput = $state('')

  /**
   * The drain about to be run, as kubectl flags.
   *
   * Reads the same three controls the plan does, so the command and the
   * preview can never describe different drains.
   */
  const drainFlags = $derived({
    force,
    deleteEmptyDirData,
    gracePeriodSeconds: gracePeriodInput.trim() === '' ? null : Number(gracePeriodInput),
  })

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

  // Fresh state every time the dialog opens, and every time it is pointed at
  // a different node, so nothing from the node before stands under this one's
  // name.
  //
  // THE PREVIEW IS RESET WITH THE REST, which it was not. This block used to
  // explain itself in terms of a previous node's report or error and quietly
  // leave `plan` alone — so opening the dialog on a second node showed the
  // FIRST node's pod counts, under the second node's name, until its own
  // preview came back. That is the one number the confirm button is about.
  //
  // DECLARED BEFORE THE EFFECT THAT LOADS THE PREVIEW, and the order is
  // load-bearing: effects run in declaration order, so the two checkboxes are
  // back at their defaults before the preview below is requested, and it is
  // requested once. Declared after, it reset them underneath a request
  // already in flight and fired a second one for the same node.
  //
  // `nodeName` is read before the guard so a change of node re-runs this even
  // when the dialog was already open.
  $effect(() => {
    void nodeName
    if (!open) return
    plan = null
    planError = null
    report = null
    running = false
    force = false
    deleteEmptyDirData = false
    gracePeriodInput = ''
  })

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

  const evictCount = $derived(plan?.evict?.length ?? 0)
  const skippedCount = $derived(plan?.skipped?.length ?? 0)
  const refusedCount = $derived(plan?.refused?.length ?? 0)
  const refusedReasons = $derived.by(() => {
    if (!plan) return ''
    return [...new Set((plan.refused ?? []).map((entry) => entry.reason))].join('; ')
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
    class="fixed inset-0 z-[70] m-auto h-fit max-h-[90vh] overflow-y-auto
           w-[30rem] max-w-[90vw]
           rounded-sm border border-outline-variant bg-surface-container-high p-6 shadow-level-3"
    role="dialog"
    aria-modal="true"
    use:modal
    aria-label="Drain node"
  >
    <DialogHeader title="Drain {nodeName}" help="drain" {onclose} />

    <p class="mt-4 text-body-medium text-on-surface-variant">
      New pods will not be scheduled here, and every evictable pod running here now is asked to
      leave — a PodDisruptionBudget may refuse an eviction, which is why this can take a while.
    </p>

    <div class="mt-4 flex flex-col gap-2">
      <!-- `checked` + `onchange` rather than `bind:checked`: Checkbox draws
           purely from the prop and never decides its own state, which is what
           lets a caller that cancels the browser's toggle (RowSelect) share
           one component with these. Writing the value back here costs a line
           and keeps that single rule. -->
      <Checkbox
        checked={force}
        onchange={(next) => (force = next)}
        disabled={running}
        align="start"
        class="text-body-medium text-on-surface"
      >
        Force pods with no controller
        <span class="block text-body-medium text-on-surface-variant">
          A bare pod is not recreated once evicted — nothing owns it.
        </span>
      </Checkbox>

      <Checkbox
        checked={deleteEmptyDirData}
        onchange={(next) => (deleteEmptyDirData = next)}
        disabled={running}
        align="start"
        class="text-body-medium text-on-surface"
      >
        Delete pods using local storage
        <span class="block text-body-medium text-on-surface-variant">
          An emptyDir volume lives on this node and is discarded, not moved.
        </span>
      </Checkbox>

      <label class="mt-1 block">
        <span class="text-body-medium text-on-surface-variant">Grace period (seconds)</span>
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
    <div class="mt-4 min-h-[3rem] rounded-sm border border-outline-variant/60 bg-surface p-3 text-body-medium">
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
      <!-- An empty evicted/failed list marshals to null, same as the plan
           above — the arithmetic and the each-block both need the empty form. -->
      {@const evicted = report.evicted ?? []}
      {@const failed = report.failed ?? []}
      <div class="mt-4 text-body-medium text-on-surface">
        <p>
          Evicted {evicted.length} of {evicted.length + failed.length}
          {#if report.timedOut}
            <span class="text-error">— timed out waiting on the rest</span>
          {/if}
        </p>
        {#if failed.length > 0}
          <ul class="mt-2 flex flex-col gap-1 text-body-medium text-on-surface-variant">
            {#each failed as failure (failure.pod)}
              <li><strong class="text-on-surface" data-selectable>{failure.pod}</strong>: {failure.reason}</li>
            {/each}
          </ul>
        {/if}
      </div>
    {/if}

    <!-- The flags follow the two ticks and the grace-period field above, so
         the transcript is of THIS drain rather than of drains in general. -->
    <DialogFooter command={nodeName ? drain(ctx, nodeName, drainFlags) : ''}>
      <Button variant="outlined" onclick={onclose}>{report ? 'Close' : 'Cancel'}</Button>
      {#if !report}
        <Button variant="filled" onclick={handleDrain} disabled={!canConfirm} loading={running}>Drain</Button>
      {/if}
    </DialogFooter>
  </div>
{/if}
