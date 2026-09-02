<!--
  What a namespace actually holds.

  THE QUESTION A NAMESPACE PANEL IS OPENED TO ANSWER. Kubernetes has no
  endpoint for it — there is nothing that reports a namespace's contents — so
  every client either shows a namespace as a name, a phase and an age, or
  makes the operator visit twenty lists to find out whether it is empty. The
  answer here is assembled kind by kind in Go: see domain.NamespaceInventory.

  ON DEMAND, when the section is opened. It is around twenty requests, each
  tiny — a list of one object, plus the server's own count of the rest — and
  firing them for every namespace row nobody expanded would be twenty requests
  a namespace.

  Each row leads where it counts, filtered to this namespace, which is the
  move somebody makes immediately after reading the number.
-->
<script lang="ts">
  import { namespaceInventory, type NamespaceInventory } from '$lib/api/client'
  import { preferences } from '$stores/preferences.svelte'
  import { toApiError } from '$lib/api/errors'
  import DetailSection from './DetailSection.svelte'
  import ColumnDivider from './ColumnDivider.svelte'

  interface Props {
    clusterId: string
    /** The namespace this panel is open on. */
    namespace: string
    /** Opens a kind's list, filtered to this namespace. */
    onbrowse?: (kindId: string, namespace: string) => void
  }

  let { clusterId, namespace, onbrowse }: Props = $props()

  /** The grid, so the divider between its columns has something to measure. */
  let pane = $state<HTMLElement | null>(null)

  let inventory = $state.raw<NamespaceInventory | null>(null)
  let loading = $state(false)
  let failure = $state('')
  /** The namespace `inventory` describes, so a change of namespace refetches. */
  let loadedFor = $state('')

  async function load(): Promise<void> {
    if (loading || loadedFor === namespace) return

    loading = true
    failure = ''
    try {
      inventory = await namespaceInventory(clusterId, namespace)
      loadedFor = namespace
    } catch (error) {
      failure = toApiError(error).message
      inventory = null
    } finally {
      loading = false
    }
  }

  /**
   * A different namespace invalidates what is held — AND ASKS AGAIN IF THE
   * SECTION IS OPEN.
   *
   * It used only to clear, on the reasoning that opening the section is what
   * asks. True the first time and false every time after: the section is open
   * by default and stays open, so `onopen` never fires again, and moving from
   * one namespace to another left the cleared, never-refilled state on screen —
   * rendering "no contents" indefinitely until the operator collapsed the section
   * and expanded it. Comparing two namespaces is exactly when somebody does
   * that navigation.
   */
  $effect(() => {
    if (namespace === loadedFor) return
    inventory = null
    failure = ''
    if (preferences.sectionOpen('namespace-contents', true)) void load()
  })

  /**
   * The count beside the heading.
   *
   * Qualified when part of the picture is missing, because "412" and "412 so
   * far, 3 kinds refused" are different claims and only one of them is true
   * of an account that cannot list Secrets.
   */
  const hint = $derived.by(() => {
    if (loadedFor !== namespace || !inventory) return undefined
    if (inventory.unreadable > 0) return `${inventory.total}+ · ${inventory.unreadable} refused`
    return String(inventory.total)
  })
</script>

<DetailSection level="h3" id="namespace-contents" title="Contents" {hint} onopen={load}>
  {#if loading}
    <p class="py-2 text-body-small text-on-surface-variant/70">Counting what is in here…</p>
  {:else if failure}
    <p class="py-2 text-body-small text-error">{failure}</p>
  {:else if inventory && inventory.counts.length === 0}
    <p class="py-2 text-body-small text-on-surface-variant/70">
      Nothing of the {inventory.empty} kinds counted is in this namespace.
    </p>
  {:else if inventory}
    <!-- The drawer's own grid, so the counts line up with every other fact in
         the panel. See detail-grid in app.css. -->
    <div class="relative">
      <dl class="detail-grid" bind:this={pane}>
      {#each inventory.counts as count (count.kindId)}
        <dt class="min-w-0 truncate text-body-medium text-on-surface">
          {#if onbrowse}
            <button
              type="button"
              onclick={() => onbrowse?.(count.kindId, namespace)}
              class="resource-link max-w-full truncate text-left"
              title="Show {count.title.toLowerCase()} in {namespace}"
            >
              {count.title}
            </button>
          {:else}
            {count.title}
          {/if}
        </dt>
        <dd class="min-w-0 text-body-medium tabular-nums text-on-surface-variant">
          <!-- A refusal INSTEAD OF the number, never beside it. An account
               that may not list Secrets holds an unknown number of them, and
               a zero there would report an empty namespace as fact. -->
          {#if count.unreadable}
            <span class="text-body-small text-gauge-warn">{count.unreadable}</span>
          {:else}
            {count.count}
          {/if}
        </dd>
      {/each}
      </dl>

      <ColumnDivider {pane} />
    </div>

    <!--
      What was NOT counted, said out loud. The number above is a total of the
      built-in kinds: a namespace whose contents are mostly custom resources
      would otherwise read as nearly empty, which is the failure mode of
      counting quietly.
    -->
    <p class="mt-1 text-body-small text-on-surface-variant/60">
      Built-in kinds only — custom resources are not counted, and neither are
      events, which expire.{inventory.empty > 0
        ? ` ${inventory.empty} other ${inventory.empty === 1 ? 'kind holds' : 'kinds hold'} nothing.`
        : ''}
    </p>
  {/if}
</DetailSection>
