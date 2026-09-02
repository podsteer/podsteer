<!--
  The namespace list.

  Kubernetes reports a namespace as a name and a phase, which is why every
  client's namespace list is three columns that answer nothing. The questions
  actually asked of one are whether anything is still running in it, whether
  any of that is broken, and how much of the cluster it is holding — none of
  which is knowable without looking inside, so PodSteer looks: see
  domain.NewNamespaceSummaries.

  Requests rather than usage as the default columns. Reservations are what
  fill a cluster and are readable whether or not anything is measured, so they
  are the pair that is always there; measured usage sits beside them for the
  clusters that serve it.
-->
<script lang="ts">
  import DataTable, { type Column } from '$lib/components/DataTable.svelte'
  import StatusIndicator from '$lib/components/StatusIndicator.svelte'
  import EmptyState from '$lib/components/EmptyState.svelte'
  import { formatAge } from '$lib/format'
  import type { ClusterSession } from '$stores/session.svelte'
  import { Boxes, CircleDot } from '@lucide/svelte'

  interface Props {
    session: ClusterSession
  }

  let { session }: Props = $props()

  const COLUMNS: Column[] = [
    { id: 'status', label: 'Status', width: 44, icon: CircleDot },
    { id: 'name', label: 'Name', width: 320, pinned: true },
    { id: 'pods', label: 'Pods', width: 90, numeric: true },
    { id: 'notReady', label: 'Not ready', width: 100, numeric: true },
    { id: 'cpuRequests', label: 'CPU requested', width: 130, numeric: true },
    { id: 'memoryRequests', label: 'Memory requested', width: 150, numeric: true },
    { id: 'cpu', label: 'CPU used', width: 110, numeric: true, defaultHidden: true },
    { id: 'memory', label: 'Memory used', width: 130, numeric: true, defaultHidden: true },
    { id: 'age', label: 'Age', width: 80, numeric: true },
  ]
</script>

<DataTable
  kindId={session.selectedKindId}
  columns={COLUMNS}
  isEmpty={session.pagedNamespaces.length === 0}
  sort={session.sort}
  onsort={session.toggleSort}
>
  {#snippet empty()}
    <EmptyState
      title="No namespaces"
      description="This cluster reports no namespaces you can see."
    />
  {/snippet}

  {#snippet rows(isVisible)}
    {#each session.pagedNamespaces as namespace (namespace.name)}
      {@const selected = session.selectedName === namespace.name}
      <tr
        class="group cursor-pointer border-t border-outline-variant/25 transition-colors duration-75
               {selected ? 'bg-primary/8' : 'hover:bg-surface-container-low'}"
        onclick={() => session.openDetail(namespace.name, '')}
      >
        {#if isVisible('status')}
          <td class="overflow-hidden py-1.5 pr-3 pl-5">
            <!-- Terminating is a warning rather than an error: it is a
                 namespace doing what it was asked to. One that has been
                 doing it for an hour is a different matter, and that is what
                 the age column beside it is for. -->
            <StatusIndicator
              tone={namespace.isActive ? 'success' : 'warning'}
              label={namespace.phase}
              icon={Boxes}
            />
          </td>
        {/if}
        <td class="px-3 py-1.5" title={namespace.name}>
          <span class="truncate font-medium text-on-surface">{namespace.name}</span>
        </td>
        {#if isVisible('pods')}
          <!-- Dimmed at zero. An empty namespace is worth spotting and a
               column of bold noughts is not — it is the one number here that
               means "nothing to see". -->
          <td
            class="truncate px-3 py-1.5 text-right tabular-nums {namespace.pods === 0
              ? 'text-on-surface-variant/40'
              : 'text-on-surface-variant'}"
          >
            {namespace.pods}
          </td>
        {/if}
        {#if isVisible('notReady')}
          <td
            class="truncate px-3 py-1.5 text-right tabular-nums {namespace.notReady > 0
              ? 'font-medium text-warning'
              : 'text-on-surface-variant/40'}"
          >
            {namespace.notReady}
          </td>
        {/if}
        {#if isVisible('cpuRequests')}
          <td class="truncate px-3 py-1.5 text-right tabular-nums text-on-surface-variant">
            {namespace.cpuRequests}
          </td>
        {/if}
        {#if isVisible('memoryRequests')}
          <td class="truncate px-3 py-1.5 text-right tabular-nums text-on-surface-variant">
            {namespace.memoryRequests}
          </td>
        {/if}
        <!--
          Unmeasured prints a dimmed dash and explains itself in the tooltip
          rather than spelling "no metrics" out on every row — the same
          treatment the pod and node lists give the same absence.
        -->
        {#if isVisible('cpu')}
          <td
            class="truncate px-3 py-1.5 text-right tabular-nums {namespace.hasMetrics
              ? 'text-on-surface-variant'
              : 'text-on-surface-variant/40'}"
            title={namespace.hasMetrics
              ? `${namespace.cpu} measured across this namespace's pods`
              : 'Not measured — this cluster has no metrics source'}
          >
            {namespace.hasMetrics ? namespace.cpu : '—'}
          </td>
        {/if}
        {#if isVisible('memory')}
          <td
            class="truncate px-3 py-1.5 text-right tabular-nums {namespace.hasMetrics
              ? 'text-on-surface-variant'
              : 'text-on-surface-variant/40'}"
            title={namespace.hasMetrics
              ? `${namespace.memory} measured across this namespace's pods`
              : 'Not measured — this cluster has no metrics source'}
          >
            {namespace.hasMetrics ? namespace.memory : '—'}
          </td>
        {/if}
        {#if isVisible('age')}
          <td class="truncate px-3 py-1.5 text-right tabular-nums text-on-surface-variant">
            {formatAge(namespace.ageSeconds)}
          </td>
        {/if}
        <td></td>
      </tr>
    {/each}
  {/snippet}
</DataTable>
