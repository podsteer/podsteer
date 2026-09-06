<!--
  The node list.

  Usage is shown against ALLOCATABLE rather than capacity: allocatable is what
  the scheduler actually has to give out, so a node at 90% allocatable is full
  even though capacity says otherwise.
-->
<script lang="ts">
  import DataTable, { ROW_MENU_COLUMN, type Column } from '$lib/components/DataTable.svelte'
  import type { CSVExport } from '$stores/activeTable.svelte'
  import StatusIndicator from '$lib/components/StatusIndicator.svelte'
  import MeterBar from '$lib/components/MeterBar.svelte'
  import EmptyState from '$lib/components/EmptyState.svelte'
  import { type RowAction } from '$lib/components/RowMenu.svelte'
  import RowMenuCell from '$lib/components/RowMenuCell.svelte'
  import { rowActionsFor, toRowActions } from '$lib/rowActions'
  import { isControlColumn } from '$lib/fixedColumns'
  import CustomCells from '$lib/components/CustomCells.svelte'
  import { customCell, parseCustomColumnId, toColumns } from '$lib/customColumns'
  import RowSelect from '$lib/components/RowSelect.svelte'
  import { formatAge } from '$lib/format'
  import { get as kubectlGet } from '$lib/kubectl'
  import { preferences } from '$stores/preferences.svelte'
  import { organisation } from '$stores/organisation.svelte'
  import { sessionLauncher } from '$stores/sessionLauncher.svelte'
  import type { ClusterSession, DetailIntent } from '$stores/session.svelte'
  import type { Node } from '$lib/api/client'
  import { Server, CircleDot } from '@lucide/svelte'

  interface Props {
    session: ClusterSession
  }

  let { session }: Props = $props()

  /** This cluster's guardrail settings, read fresh so a change in Organise
   * takes effect at once — the same pattern the drawer uses. */
  const placement = $derived(organisation.placementOf(session.cluster.id))
  const groupSettings = $derived(organisation.settingsFor(placement.project, placement.group))
  const groupName = $derived(
    organisation.groupsIn(placement.project).find((group) => group.id === placement.group)?.name ??
      'Default',
  )
  const productionGroup = $derived(groupSettings.environment === 'production' ? groupName : null)

  /**
   * What a node row offers. Nodes are cluster-scoped, so there is no
   * namespace to pass.
   *
   * Cordon and Uncordon are ONE item, chosen from the row's own
   * `unschedulable` flag — only one of them could change anything, and an
   * item that would refuse itself is worse than no item. Uncordon acts
   * immediately with no dialog, which is the drawer's own rule for it: it
   * undoes a visible, deliberate state.
   *
   * Drain opens the drawer's DrainDialog, which plans the drain and shows
   * the per-node preview before anything is evicted. That preview is why
   * there is no bulk drain: several nodes at once can take a cluster down,
   * and the preview is the safety that would be lost.
   *
   * Node shell keeps launching through `sessionLauncher` rather than the
   * drawer, because the terminal it opens outlives the surface that launched
   * it — see $stores/sessionLauncher.
   */
  function actionsFor(node: Node): RowAction[] {
    const open = (intent: DetailIntent) => () =>
      void session.openDetailFor(intent, node.name, '', undefined, undefined, node)

    return toRowActions(
      rowActionsFor('Node', { unschedulable: node.unschedulable }),
      {
        cordon: open({ action: 'cordon' }),
        uncordon: open({ action: 'uncordon' }),
        drain: open({ action: 'drain' }),
        nodeShell: () =>
          sessionLauncher.requestNodeShell({
            clusterId: session.cluster.id,
            node: node.name,
            readOnly: groupSettings.readOnly,
            productionGroup,
          }),
        kubectl: () => copyKubectl(kubectlGet(session.cluster.id, 'nodes', node.name)),
      },
      groupSettings.readOnly,
    )
  }

  function copyKubectl(command: string): void {
    void navigator.clipboard?.writeText(command).catch(() => {})
  }

  const COLUMNS: Column[] = [
    { id: 'select', label: 'Select', width: 40, pinned: true, select: true },
    { id: 'status', label: 'Status', width: 44, icon: CircleDot },
    { id: 'name', label: 'Name', width: 300, pinned: true },
    { id: 'roles', label: 'Roles', width: 140 },
    { id: 'cpu', label: 'CPU', width: 220, minWidth: 200 },
    { id: 'memory', label: 'Memory', width: 220, minWidth: 200 },
    { id: 'disk', label: 'Disk', width: 220, minWidth: 200 },
    { id: 'version', label: 'Version', width: 110 },
    { id: 'ip', label: 'Internal IP', width: 130, defaultHidden: true },
    { id: 'os', label: 'OS', width: 180, defaultHidden: true },
    { id: 'pods', label: 'Max pods', width: 100, numeric: true, defaultHidden: true },
    { id: 'taints', label: 'Taints', width: 80, numeric: true },
    { id: 'age', label: 'Age', width: 80, numeric: true },
  ]

  /** The built-in columns, then the operator's own — see $lib/customColumns —
      and the row menu last, because it is the end of the row. */
  const columns = $derived<Column[]>([
    ...COLUMNS,
    ...toColumns(session.customColumns),
    ROW_MENU_COLUMN,
  ])

  /** Same rule ColumnMenu and DataTable apply — see PodsView for why it is
      repeated here rather than asked of either. */
  function isColumnVisible(column: Column): boolean {
    const stored = preferences.columns[session.selectedKindId]?.[column.id]?.hidden
    return column.pinned || (stored === undefined ? !column.defaultHidden : !stored)
  }

  /** The rows on screen, in display order, for range and select-all. See
      PodsView. Nodes are cluster-scoped, so the key is the bare name. */
  $effect(() => {
    session.selection.visible = session.pagedNodes.map((node) => node.name)
    return () => {
      session.selection.visible = []
    }
  })

  /** The node list's CSV export, mirroring exactly what each cell shows. */
  function exportCSV(): CSVExport {
    // The tick box and the row menu are controls, not columns with text in
    // them: exported, each would be a heading over a column of empty cells.
    const visible = columns.filter((column) => !isControlColumn(column) && isColumnVisible(column))

    function cell(node: Node, id: string): string {
      const custom = parseCustomColumnId(id)
      if (custom) return customCell(node, custom)
      switch (id) {
        case 'status':
          return node.status
        case 'name':
          return node.name
        case 'roles':
          return node.roles?.length ? node.roles.join(', ') : 'worker'
        case 'cpu':
          return node.cpu
        case 'memory':
          return node.memory
        case 'disk':
          return node.disk
        case 'version':
          return node.version
        case 'ip':
          return node.internalIp || '—'
        case 'os':
          return node.osImage || '—'
        case 'pods':
          return node.maxPods ? String(node.maxPods) : '—'
        case 'taints':
          return String(node.taints)
        case 'age':
          return formatAge(node.ageSeconds)
        default:
          return ''
      }
    }

    return {
      columns: visible.map((column) => column.label),
      rows: session.sortedNodes.map((node) => visible.map((column) => cell(node, column.id))),
    }
  }
</script>

<DataTable
  kindId={session.selectedKindId}
  {columns}
  isEmpty={session.pagedNodes.length === 0}
  sort={session.sort}
  onsort={session.toggleSort}
  exportRows={exportCSV}
  selectAll={{
    checked: session.selection.allVisibleSelected,
    indeterminate: session.selection.someVisibleSelected,
    ontoggle: () => session.selection.toggleAllVisible(),
  }}
>
  {#snippet empty()}
    <EmptyState title="No nodes" description="This cluster reports no nodes you can see." />
  {/snippet}

  {#snippet rows(isVisible)}
    {#each session.pagedNodes as node (node.name)}
      {@const selected = session.selectedName === node.name}
      {@const ticked = session.selection.has(node.name)}
      <!-- The state grounds are OPAQUE tokens rather than the translucent
           `bg-primary/8` they used to be: the pinned columns inherit this
           row's own colour, and a translucent one lets the cells scrolling
           underneath show through them. See the row grounds in app.css. -->
      <tr
        class="group/row cursor-pointer border-t border-outline-variant/25 transition-colors duration-75
               {selected
          ? 'bg-row-open'
          : ticked
            ? 'bg-row-ticked'
            : 'bg-surface hover:bg-surface-container-low'}"
        aria-selected={ticked}
        onclick={() => session.openDetail(node.name, '', undefined, undefined, node)}
      >
        <RowSelect
          selected={ticked}
          label={node.name}
          ontoggle={(range) => session.selection.toggle(node.name, range)}
        />
        {#if isVisible('status')}
          <td class="overflow-hidden py-1.5 pr-3 pl-5">
            <StatusIndicator
              tone={node.isHealthy ? (node.unschedulable ? 'warning' : 'success') : 'error'}
              label={node.status}
              icon={Server}
            />
          </td>
        {/if}
        <td class="px-3 py-1.5" title={node.name}>
          <span class="flex items-center gap-2">
            <span class="truncate font-medium text-on-surface">{node.name}</span>
          </span>
        </td>
        {#if isVisible('roles')}
          <td class="truncate px-3 py-1.5">
            <!--
              ONE TREATMENT FOR EVERY ROLE. "worker" used to render dimmer
              than "control-plane", on a distinction the colour was not
              actually making: a worker is not a lesser node, it is the
              default one, and shading it made the column look like it
              carried a severity it does not. Reading down a mixed column is
              easier when the only thing changing is the word.
            -->
            <span class="rounded bg-surface-container-high px-1.5 py-0.5 text-body-small text-on-surface-variant">
              {node.roles?.length ? node.roles.join(', ') : 'worker'}
            </span>
          </td>
        {/if}
        <!--
          An unmeasured node prints the dash dimmed and explains itself in the
          tooltip, rather than spelling "no metrics" out on every row. Fifteen
          rows each saying it is noise, and the explanation of WHY belongs
          once, in the notice above the table — the same treatment the pod
          list already used, now that both lists draw the same meter.
        -->
        {#if isVisible('cpu')}
          <td class="overflow-hidden px-3 py-1.5 text-on-surface-variant">
            <MeterBar
              label={node.cpu}
              scope="nodes"
              name="CPU"
              valueWidth="7ch"
              percent={node.cpuPercent}
              measured={node.hasMetrics}
              title={node.hasMetrics
                ? `${node.cpu} of ${node.allocatableCpu} allocatable`
                : // A NODE REPORTING NOTHING SAYS NOTHING ABOUT THE CLUSTER. This
                  // row has no MetricsStatus to consult — the overview pane
                  // does, and said the opposite — so it claims neither.
                  'Not measured — nothing has reported for it, or this cluster serves no metrics'}
            />
          </td>
        {/if}
        {#if isVisible('memory')}
          <td class="overflow-hidden px-3 py-1.5 text-on-surface-variant">
            <MeterBar
              label={node.memory}
              scope="nodes"
              name="Memory"
              percent={node.memoryPercent}
              measured={node.hasMetrics}
              title={node.hasMetrics
                ? `${node.memory} of ${node.allocatableMemory} allocatable`
                : // A NODE REPORTING NOTHING SAYS NOTHING ABOUT THE CLUSTER. This
                  // row has no MetricsStatus to consult — the overview pane
                  // does, and said the opposite — so it claims neither.
                  'Not measured — nothing has reported for it, or this cluster serves no metrics'}
            />
          </td>
        {/if}
        <!--
          The FULLEST filesystem, not the average and not only nodefs.
          Whichever of nodefs and imagefs is closer to full is the one that
          decides whether the kubelet starts evicting, and a node whose image
          filesystem is full while its root disk is empty is in trouble that
          an average would hide.

          Its own thresholds are the node ones, because that is what this is:
          a share of a node's finite capacity, where high is bad. Unlike CPU
          and memory, though, the kubelet's real line here is a percentage —
          it evicts at 10% free by default — so these thresholds correspond to
          something the cluster actually does.
        -->
        {#if isVisible('disk')}
          <td class="overflow-hidden px-3 py-1.5 text-on-surface-variant">
            <MeterBar
              label={node.disk}
              scope="nodes"
              name="Disk"
              percent={node.hasDisk ? node.diskPercent : null}
              measured={node.hasDisk}
              absent="not readable"
              title={node.hasDisk
                ? `${node.disk} of ${node.diskCapacity} on the fullest of this node's filesystems`
                : 'Disk occupancy needs the nodes/proxy permission, which this account does not have'}
            />
          </td>
        {/if}
        {#if isVisible('version')}
          <!-- Not monospaced. A kubelet version is read, not aligned against
               the row above it, and the mono face made one column of this
               table look like it came from somewhere else. -->
          <td class="truncate px-3 py-1.5 text-body-medium tabular-nums text-on-surface-variant">{node.version}</td>
        {/if}
        {#if isVisible('ip')}
          <td class="truncate px-3 py-1.5 font-mono text-body-medium text-on-surface-variant" data-selectable>
            {node.internalIp || '—'}
          </td>
        {/if}
        {#if isVisible('os')}
          <td class="truncate px-3 py-1.5 text-on-surface-variant" title={node.osImage}>
            {node.osImage || '—'}
          </td>
        {/if}
        {#if isVisible('pods')}
          <td class="truncate px-3 py-1.5 text-right tabular-nums text-on-surface-variant">
            {node.maxPods || '—'}
          </td>
        {/if}
        {#if isVisible('taints')}
          <td class="truncate px-3 py-1.5 text-right tabular-nums {node.taints > 0 ? 'text-warning font-medium' : 'text-on-surface-variant'}">
            {node.taints}
          </td>
        {/if}
        {#if isVisible('age')}
          <td class="truncate px-3 py-1.5 text-right tabular-nums text-on-surface-variant">
            {formatAge(node.ageSeconds)}
          </td>
        {/if}
        <CustomCells specs={session.customColumns} row={node} {isVisible} />
        <RowMenuCell actions={actionsFor(node)} label={node.name} />
      </tr>
    {/each}
  {/snippet}
</DataTable>
