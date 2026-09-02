<!--
  The cluster's applications, from the labels Kubernetes recommends.

  `app.kubernetes.io/instance` names one deployed copy of an application —
  the unit somebody means by "the bill-registry-service in development" — and
  `part-of` names the wider thing it belongs to. Grouping by them is the only
  part of "what is an application" that Kubernetes standardises at all.

  THE LABELS ARE A CONVENTION, NOT A GUARANTEE, and this page says so out
  loud. A chart that does not set them, or a hand-written manifest, is
  invisible to any grouping built on them — so what could not be grouped is
  counted and shown rather than quietly dropped. A view that omits a third of
  a namespace is worse than no view, because it looks complete.
-->
<script lang="ts">
  import DataTable, { type Column } from '$lib/components/DataTable.svelte'
  import MeterBar from '$lib/components/MeterBar.svelte'
  import EmptyState from '$lib/components/EmptyState.svelte'
  import { cpuMeter, cpuTitle, memoryMeter, memoryTitle } from '$lib/meter'
  import { preferences } from '$stores/preferences.svelte'
  import type { ClusterSession } from '$stores/session.svelte'
  import { countedKind } from '$lib/plural'
  import { Blocks } from '@lucide/svelte'

  interface Props {
    session: ClusterSession
  }

  let { session }: Props = $props()

  /** The same denominator the pod list is set to. See NamespacesView. */
  const byLimit = $derived(preferences.podMeasure === 'limits')

  const COLUMNS: Column[] = [
    { id: 'instance', label: 'Application', width: 280, pinned: true },
    { id: 'namespace', label: 'Namespace', width: 150 },
    { id: 'partOf', label: 'Part of', width: 160 },
    { id: 'managedBy', label: 'Managed by', width: 130 },
    { id: 'version', label: 'Version', width: 140 },
    { id: 'objects', label: 'Objects', width: 90, numeric: true },
    { id: 'cpu', label: 'CPU', width: 200, minWidth: 180 },
    { id: 'memory', label: 'Memory', width: 200, minWidth: 180 },
    { id: 'members', label: 'Made of', width: 300 },
  ]
</script>

<div class="flex min-h-0 flex-1 flex-col">
  <DataTable
    kindId="podsteer/applications"
    columns={COLUMNS}
    isEmpty={session.pagedApplications.length === 0}
    sort={session.sort}
    onsort={session.toggleSort}
  >
    {#snippet empty()}
      <EmptyState
        title="No applications found"
        description={session.search
          ? `Nothing matches "${session.search}".`
          : 'Nothing here carries an app.kubernetes.io/instance label, which is what applications are grouped by.'}
      />
    {/snippet}

    {#snippet rows(isVisible)}
      {#each session.pagedApplications as application (application.namespace + '/' + application.instance)}
        {@const selected =
          session.selectedName === application.instance &&
          session.selectedNamespace === application.namespace}
        <tr
          class="group cursor-pointer border-t border-outline-variant/25 transition-colors duration-75
                 {selected ? 'bg-primary/8' : 'hover:bg-surface-container-low'}"
          onclick={() => session.openApplication(application)}
        >
          <td class="px-3 py-1.5" title={application.instance}>
            <span class="flex items-center gap-2">
              <Blocks class="size-4 shrink-0 text-on-surface-variant/60" strokeWidth={1.8} />
              <span class="truncate font-medium text-on-surface">{application.instance}</span>
            </span>
          </td>
          {#if isVisible('namespace')}
            <td class="truncate px-3 py-1.5 text-on-surface-variant">{application.namespace}</td>
          {/if}
          {#if isVisible('partOf')}
            <td class="truncate px-3 py-1.5 text-on-surface-variant">
              {application.partOf || '—'}
            </td>
          {/if}
          {#if isVisible('managedBy')}
            <td class="truncate px-3 py-1.5 text-on-surface-variant">
              {application.managedBy || '—'}
            </td>
          {/if}
          {#if isVisible('version')}
            <td class="truncate px-3 py-1.5 tabular-nums text-on-surface-variant">
              {application.version || '—'}
            </td>
          {/if}
          {#if isVisible('objects')}
            <td class="truncate px-3 py-1.5 text-right tabular-nums text-on-surface-variant">
              {application.objects}
            </td>
          {/if}
          {#if isVisible('cpu')}
            {@const cpu = cpuMeter(application, byLimit)}
            <td class="overflow-hidden px-3 py-1.5 text-on-surface-variant">
              <MeterBar
                label={application.hasMetrics ? application.cpu : '—'}
                scope="pods"
                name="CPU"
                valueWidth="7ch"
                percent={cpu.percent}
                measured={application.hasMetrics}
                thresholds={cpu.thresholds}
                absent={cpu.absent}
                severity={cpu.severity}
                title={cpuTitle(application)}
              />
            </td>
          {/if}
          {#if isVisible('memory')}
            {@const memory = memoryMeter(application, byLimit)}
            <td class="overflow-hidden px-3 py-1.5 text-on-surface-variant">
              <MeterBar
                label={application.hasMetrics ? application.memory : '—'}
                scope="pods"
                name="Memory"
                percent={memory.percent}
                measured={application.hasMetrics}
                thresholds={memory.thresholds}
                absent={memory.absent}
                severity={memory.severity}
                title={memoryTitle(application)}
              />
            </td>
          {/if}
          {#if isVisible('members')}
            <!-- What it is made of, largest first: the shape of an
                 application at a glance, which is the reason to group at
                 all. -->
            <td class="truncate px-3 py-1.5 text-on-surface-variant">
              {application.members
                .map((member) => `${member.count} ${countedKind(member.kind, member.count)}`)
                .join(' · ')}
            </td>
          {/if}
          <td></td>
        </tr>
      {/each}
    {/snippet}
  </DataTable>

  <!--
    SAID OUT LOUD, ALWAYS. The count of what carries no instance label is the
    difference between "this cluster has eleven applications" and "this
    cluster has eleven applications and four hundred objects that do not say
    which one they belong to" — and only the second is true of most clusters.
  -->
  {#if session.unlabelled > 0}
    <p
      class="shrink-0 border-t border-outline-variant/40 px-4 py-2 text-body-small
             text-on-surface-variant/70"
    >
      {session.unlabelled}
      {session.unlabelled === 1 ? 'object carries' : 'objects carry'} no
      <code class="text-on-surface-variant">app.kubernetes.io/instance</code> label and are not
      grouped here. The recommended labels are a convention, not something Kubernetes enforces.
    </p>
  {/if}
</div>
