<!--
  One application, in the drawer.

  AN APPLICATION IS NOT AN OBJECT. There is nothing to GET by that name, no
  manifest, no YAML tab and nothing to edit or delete — it is a set of objects
  that agree about a label. So this pane is built from the row rather than
  from a fetch, and it deliberately offers none of the actions the other panes
  do: there is nothing here to act on that is not one of its members.

  What it can say is what the label grouping actually buys: what the
  application is made of, and what those things cost together.
-->
<script lang="ts">
  import type { Application } from '$lib/api/client'
  import DetailSection from './DetailSection.svelte'
  import DetailList, { type DetailRow } from './DetailList.svelte'
  import UsageChart from './UsageChart.svelte'
  import type { UsageSample } from '$stores/session.svelte'
  import { countedKind } from '$lib/plural'

  interface Props {
    application: Application
    /** Its recent usage, accumulated while the list has been polling. */
    usage?: UsageSample[]
    /** Opens a kind's list, filtered to this application's namespace. */
    onbrowse?: (kindId: string, namespace: string) => void
    /** Filters the application to a namespace. */
    onnamespace?: (namespace: string) => void
  }

  let { application, usage = [], onbrowse, onnamespace }: Props = $props()

  /**
   * The labels this grouping is built on, named as what they are.
   *
   * Written out rather than summarised, because somebody looking at this is
   * often deciding whether their own charts set them correctly — and the
   * answer is the label keys, not a paraphrase of them.
   */
  const identityRows = $derived.by<DetailRow[]>(() => {
    const rows: DetailRow[] = [
      {
        label: 'Instance',
        value: application.instance,
        info: 'app.kubernetes.io/instance — one deployed copy of an application',
      },
      {
        label: 'Namespace',
        value: application.namespace,
        onclick: onnamespace ? () => onnamespace(application.namespace) : undefined,
      },
    ]

    if (application.partOf) {
      rows.push({
        label: 'Part of',
        value: application.partOf,
        info: 'app.kubernetes.io/part-of — the wider application this belongs to',
      })
    }
    if (application.name) {
      rows.push({
        label: 'Name',
        value: application.name,
        info: 'app.kubernetes.io/name — the software, shared by every instance of it',
      })
    }
    if (application.version) {
      rows.push({
        label: 'Version',
        value: application.version,
        info: 'app.kubernetes.io/version',
      })
    }
    if (application.managedBy) {
      rows.push({
        label: 'Managed by',
        value: application.managedBy,
        info: 'app.kubernetes.io/managed-by — the tool that deploys it',
      })
    }
    return rows
  })

  /** Which kind ID a member's kind opens, for the ones with a list. */
  const KIND_IDS: Record<string, string> = {
    Pod: 'core/v1/pods',
    Deployment: 'apps/v1/deployments',
    StatefulSet: 'apps/v1/statefulsets',
    DaemonSet: 'apps/v1/daemonsets',
    ReplicaSet: 'apps/v1/replicasets',
    Job: 'batch/v1/jobs',
    CronJob: 'batch/v1/cronjobs',
  }

  const memberRows = $derived.by<DetailRow[]>(() =>
    application.members.map((member) => ({
      // Named for how many there are: "Pods 4", "Pod 1". A count and a noun
      // that disagree read as a label somebody forgot to finish.
      label: countedKind(member.kind, member.count),
      value: String(member.count),
      reference:
        onbrowse && KIND_IDS[member.kind]
          ? () => onbrowse(KIND_IDS[member.kind], application.namespace)
          : undefined,
    })),
  )

  /** Axis formatters, matching how Go prints the same quantities. */
  function formatCores(value: number): string {
    return value.toFixed(3)
  }

  function formatBytes(value: number): string {
    const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB']
    let size = value
    let unit = 0
    while (size >= 1024 && unit < units.length - 1) {
      size /= 1024
      unit += 1
    }
    return `${size.toFixed(1)}${units[unit]}`
  }
</script>

<div class="flex flex-col gap-6 overflow-auto p-4">
  <!--
    Usage first, on the same terms as everywhere else: what it is doing before
    what it is. The reference lines are the SUM of its pods' requests and
    limits — an application has no capacity of its own, only what the things
    inside it asked for.
  -->
  {#if application.hasMetrics}
    <DetailSection
      level="h3"
      id="application-usage"
      title="Usage"
      hint="CPU {application.cpu} · Memory {application.memory}"
    >
      <div class="flex flex-col gap-4">
        {#each [{ metric: 'cpu' as const, label: 'CPU', used: application.cpu, request: application.requestCores, limit: application.limitCores }, { metric: 'memory' as const, label: 'Memory', used: application.memory, request: application.requestBytes, limit: application.limitBytes }] as track (track.metric)}
          <div class="flex flex-col gap-1">
            <p class="flex items-baseline justify-between text-body-small text-on-surface-variant">
              <span>{track.label}</span>
              <span class="tabular-nums">{track.used}</span>
            </p>
            <UsageChart
              samples={usage}
              metric={track.metric}
              markers={[
                { value: track.request, label: 'Request', tone: 'warn' },
                { value: track.limit, label: 'Limit', tone: 'critical' },
              ]}
              format={track.metric === 'cpu' ? formatCores : formatBytes}
            />
          </div>
        {/each}

        {#if application.measuredPods < application.measurablePods}
          <p class="text-body-small text-gauge-warn">
            Summed over {application.measuredPods} of {application.measurablePods} running pods —
            the rest reported no usage, so this is less than the whole.
          </p>
        {/if}
      </div>
    </DetailSection>
  {:else}
    <DetailSection level="h3" id="application-usage" title="Usage">
      <p class="py-2 text-body-small text-on-surface-variant/70">
        {#if !application.metricsAvailable}
          Not measured — this cluster has no metrics source.
        {:else if application.measurablePods === 0}
          Nothing of this application is running.
        {:else}
          No usage reported yet — a pod is measured a little after it starts.
        {/if}
      </p>
    </DetailSection>
  {/if}

  <DetailSection
    level="h3"
    id="application-members"
    title="Made of"
    hint={String(application.objects)}
  >
    <DetailList rows={memberRows} />
    <!--
      Said once, because it is the caveat on the whole idea: this counts what
      carries the label, and the label is a convention rather than something
      Kubernetes enforces.
    -->
    <p class="mt-3 text-body-small text-on-surface-variant/60">
      Workloads and pods carrying this instance label. A chart that does not set the
      recommended labels contributes nothing here, however much of it is running.
    </p>
  </DetailSection>

  <DetailSection level="h3" id="application-identity" title="Identity">
    <DetailList rows={identityRows} />
  </DetailSection>
</div>
