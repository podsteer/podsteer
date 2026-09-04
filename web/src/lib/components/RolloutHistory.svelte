<!--
  The History tab: a Deployment, StatefulSet or DaemonSet's recorded
  revisions, newest first, with a template diff between any two of them.

  Revisions come from the controller's OWNED ReplicaSets (Deployment) or
  ControllerRevisions (StatefulSet/DaemonSet) — never the watch store, which
  strips a ReplicaSet's template to its images — so RolloutHistory always
  reads the object's own manifest. See app/domain/revision.go and
  CLAUDE.md's "Anything rendering a pod TEMPLATE must read the object's own
  manifest, never the watch store."

  Selecting one revision diffs it against the current one; selecting two
  diffs them against each other, older on the left. A row past the current
  one offers "Roll back…", which the parent turns into RollbackDialog —
  this component only reads and compares, it never writes.
-->
<script lang="ts">
  import { rolloutHistory, type Revision } from '$lib/api/client'
  import { toApiError } from '$lib/api/errors'
  import { formatAge } from '$lib/format'
  import { currentRevision, orderByNumberDescending } from '$lib/revisions'
  import TemplateDiff from './TemplateDiff.svelte'
  import Button from './Button.svelte'
  import { History, TriangleAlert, GitCompare } from '@lucide/svelte'

  interface Props {
    clusterId: string
    /** The Kubernetes kind — 'Deployment', 'StatefulSet' or 'DaemonSet'. */
    kind: string
    namespace: string
    name: string
    isReadOnly: boolean
    readOnlyReason: string
    /** Bumped by the parent after a rollback completes, to refetch — a
     * rollback changes which revision is current and, for a Deployment,
     * routinely bumps the target ReplicaSet's own revision number. */
    reloadToken: number
    onrollback: (revision: Revision) => void
  }

  let { clusterId, kind, namespace, name, isReadOnly, readOnlyReason, reloadToken, onrollback }: Props =
    $props()

  let revisions = $state<Revision[]>([])
  let status = $state<'loading' | 'ready' | 'error'>('loading')
  let error = $state<string | null>(null)

  // Refetches whenever the drawer points at a different object, or the
  // parent bumps reloadToken after a rollback. The guard keeps a response
  // for a previous object from landing after a newer one — the same
  // pattern EventsView follows for the same reason.
  $effect(() => {
    const target = { clusterId, kind, namespace, name, reloadToken }
    let current = true
    status = 'loading'
    selected = []

    void rolloutHistory(target.clusterId, target.kind, target.namespace, target.name)
      .then((result) => {
        if (!current) return
        revisions = orderByNumberDescending(result)
        status = 'ready'
      })
      .catch((cause) => {
        if (!current) return
        error = toApiError(cause).message
        status = 'error'
      })

    return () => {
      current = false
    }
  })

  /** Revision numbers picked for comparison, in click order, capped at two. */
  let selected = $state<number[]>([])

  function toggleSelect(revision: Revision): void {
    if (selected.includes(revision.number)) {
      selected = selected.filter((n) => n !== revision.number)
      return
    }
    // A third click keeps the most recent TWO selections rather than
    // refusing the click outright — replacing the oldest is what everyone
    // reaching for a third checkbox expects to happen next.
    selected = selected.length >= 2 ? [selected[1], revision.number] : [...selected, revision.number]
  }

  /** The pair to diff, older (or lower-numbered) first — or null when
   * nothing is comparable yet: no selection, or the one selected revision
   * IS the current one. */
  const diffPair = $derived.by((): [Revision, Revision] | null => {
    const chosen = revisions.filter((r) => selected.includes(r.number))

    if (chosen.length === 2) {
      return chosen[0].number < chosen[1].number ? [chosen[0], chosen[1]] : [chosen[1], chosen[0]]
    }
    if (chosen.length === 1) {
      const current = currentRevision(revisions)
      if (!current || current.number === chosen[0].number) return null
      return current.number < chosen[0].number ? [current, chosen[0]] : [chosen[0], current]
    }
    return null
  })

  function revisionLabel(revision: Revision): string {
    return `Revision ${revision.number}${revision.current ? ' (current)' : ''}`
  }
</script>

<div class="flex h-full flex-col overflow-hidden">
  {#if status === 'loading'}
    <div class="flex h-full flex-col items-center justify-center gap-2 p-4 text-on-surface-variant/60">
      <History class="size-8 animate-pulse" strokeWidth={1.2} />
      <p class="text-body-medium">Loading rollout history…</p>
    </div>
  {:else if status === 'error'}
    <div class="flex h-full flex-col items-center justify-center gap-2 p-6 text-center">
      <TriangleAlert class="size-8 text-error" strokeWidth={1.2} />
      <p class="text-body-medium text-on-surface-variant">{error}</p>
    </div>
  {:else if revisions.length === 0}
    <div class="flex h-full flex-col items-center justify-center gap-2 p-6 text-center">
      <History class="size-8 text-on-surface-variant/40" strokeWidth={1.2} />
      <p class="text-body-medium text-on-surface-variant">No recorded revisions</p>
    </div>
  {:else}
    <div class="flex min-h-0 flex-1 flex-col gap-4 overflow-auto p-4">
      <ul class="flex flex-col divide-y divide-outline-variant/40 rounded-sm border border-outline-variant/60">
        {#each revisions as revision (revision.number)}
          {@const isSelected = selected.includes(revision.number)}
          <li class="flex items-start gap-3 px-3 py-2.5 {isSelected ? 'bg-primary/8' : ''}">
            <label class="mt-0.5 flex shrink-0 cursor-pointer items-center" title="Select to compare">
              <input
                type="checkbox"
                checked={isSelected}
                onchange={() => toggleSelect(revision)}
                class="accent-primary"
                aria-label="Compare revision {revision.number}"
              />
            </label>

            <div class="min-w-0 flex-1">
              <div class="flex flex-wrap items-baseline justify-between gap-x-3 gap-y-1">
                <span class="flex items-center gap-1.5 text-label-large text-on-surface">
                  Revision {revision.number}
                  {#if revision.current}
                    <span
                      class="rounded-sm bg-primary/12 px-1.5 py-px text-label-small text-primary"
                    >
                      current
                    </span>
                  {/if}
                </span>
                <span class="shrink-0 text-body-small tabular-nums text-on-surface-variant/70">
                  {formatAge(revision.ageSeconds)} ago
                </span>
              </div>

              <p class="mt-0.5 truncate text-body-small text-on-surface-variant" data-selectable>
                {revision.images.join(', ') || 'no images recorded'}
              </p>

              {#if revision.changeCause}
                <p class="mt-0.5 truncate text-body-small text-on-surface-variant/70" data-selectable>
                  {revision.changeCause}
                </p>
              {/if}

              {#if revision.replicas > 0}
                <p class="mt-0.5 text-body-small text-on-surface-variant/60">
                  {revision.replicas} {revision.replicas === 1 ? 'replica' : 'replicas'}
                </p>
              {/if}
            </div>

            {#if !revision.current}
              <Button
                variant="text"
                class="h-7 shrink-0 px-2 text-label-medium"
                disabled={isReadOnly}
                describedBy={isReadOnly ? 'history-readonly-hint' : undefined}
                onclick={() => onrollback(revision)}
              >
                Roll back…
              </Button>
            {/if}
          </li>
        {/each}
      </ul>

      {#if isReadOnly}
        <p id="history-readonly-hint" class="sr-only">{readOnlyReason}</p>
      {/if}

      <div class="flex min-h-0 flex-1 flex-col gap-2">
        <p class="flex items-center gap-1.5 text-label-medium text-on-surface-variant">
          <GitCompare class="size-3.5" strokeWidth={1.8} />
          {#if diffPair}
            Comparing two revisions
          {:else if selected.length === 1}
            Select a second revision to compare, or leave it to compare against the current one.
          {:else}
            Select a revision's checkbox to see what its template would change.
          {/if}
        </p>

        {#if diffPair}
          <TemplateDiff
            before={diffPair[0].templateYaml}
            beforeLabel={revisionLabel(diffPair[0])}
            after={diffPair[1].templateYaml}
            afterLabel={revisionLabel(diffPair[1])}
          />
        {/if}
      </div>
    </div>
  {/if}
</div>
