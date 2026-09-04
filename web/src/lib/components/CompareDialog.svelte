<!--
  Comparing the open object against another one — the same cluster, another
  namespace, another open cluster, or a manifest pasted in from somewhere
  else.

  The LEFT side is always fetched fresh, with `revealSecrets=false`, on every
  Compare — never `session.manifest`. That manifest can carry a Secret's real
  decoded values when the drawer's own YAML tab has them revealed (see
  ClusterSession.revealManifestSecrets), and this dialog has no business
  inheriting that state: "Secrets are read on request, never on render"
  (CLAUDE.md) means THIS read asks for redacted values on its own account,
  regardless of what the tab behind it happens to be showing right now.

  Multi-select does not exist in PodSteer yet, so there is no list-level
  "compare selected" here or anywhere else — see the TODO beside this
  dialog's own trigger in DetailDrawer.svelte for where that would go.
-->
<script lang="ts">
  import { escapeLayer, type EscapeClaim } from '$lib/escape'
  import { modal } from '$lib/modal'
  import type { Component } from 'svelte'
  import Select from './Select.svelte'
  import ToolbarToggle from './ToolbarToggle.svelte'
  import DiffView from './DiffView.svelte'
  import { normaliseForDiff } from '$lib/diff'
  import {
    getManifest,
    listEvents,
    listNamespaces,
    listNodes,
    listPods,
    listWorkloads,
    type ResourceKind,
  } from '$lib/api/client'
  import { toApiError } from '$lib/api/errors'
  import { workspace } from '$stores/workspace.svelte'
  import { RICH_KIND_IDS, WORKLOAD_KIND_BY_ID } from '$stores/session.svelte'
  import { Activity, ClipboardPaste, FileSearch, GitCompare, TriangleAlert, X } from '@lucide/svelte'

  interface Props {
    open: boolean
    /** The kind's own icon, matching the drawer's header. */
    icon?: Component
    /** The open object's identity — the comparison's LEFT side. */
    clusterId: string
    kind: ResourceKind
    namespace: string
    name: string
    onclose: () => void
  }

  let { open, icon: Icon, clusterId, kind, namespace, name, onclose }: Props = $props()

  type Mode = 'object' | 'paste'
  let mode = $state<Mode>('object')

  // Seeded to '' rather than to the prop directly: a plain `$state(prop)`
  // initializer only captures the prop's value at FIRST mount, and this
  // dialog stays mounted across different objects opened one after another
  // (the drawer only tears it down when its own `{#if session.selectedKind}`
  // goes false). The real seed happens in the `open`-gated effect below,
  // which reads `clusterId` etc. fresh every time the dialog opens — the
  // same convention CreateResourceDialog's own `draft` follows.
  let targetClusterId = $state('')
  let targetNamespace = $state('')
  let targetName = $state('')
  let pasted = $state('')
  /** Keeps `status` in the compared manifests — off by default, see
      `$lib/diff`'s own `NormaliseOptions` doc comment for why. */
  let keepStatus = $state(false)

  let comparing = $state(false)
  let error = $state<string | null>(null)

  /** The manifests as FETCHED, before normalising — kept separately from
      what DiffView is handed so toggling "Include status" re-normalises
      instead of re-fetching two objects to add or drop one section. */
  let rawLeft = $state<string | null>(null)
  let rawRight = $state<string | null>(null)
  let rightLabel = $state('')

  let suggestions = $state<string[]>([])
  const datalistId = $props.id()

  const isSecret = $derived(kind.kind === 'Secret')

  const leftManifest = $derived(rawLeft !== null ? normaliseForDiff(rawLeft, { keepStatus }) : null)
  const rightManifest = $derived(rawRight !== null ? normaliseForDiff(rawRight, { keepStatus }) : null)

  /** Every OPEN cluster, including this one — "another namespace, same
      cluster" and "another object, same namespace" both fall out of that
      rather than needing a separate control apiece. */
  const clusterOptions = $derived(
    workspace.sessions.map((session) => ({
      value: session.cluster.id,
      label: session.cluster.id === clusterId ? `${session.cluster.id} (this cluster)` : session.cluster.id,
    })),
  )

  const targetSession = $derived(
    workspace.sessions.find((session) => session.cluster.id === targetClusterId),
  )

  const namespaceOptions = $derived(
    (targetSession?.namespaces ?? []).map((entry) => ({ value: entry.name, label: entry.name })),
  )

  /**
   * The target cluster's OWN id for this Kind.
   *
   * Resolved fresh rather than reused from the source object's id: a CRD's
   * id carries a group that is per-cluster, so `apps/v1/deployments` is safe
   * to assume identical everywhere but a ScaledObject's id is not. Null means
   * the target cluster does not serve this kind at all.
   */
  const targetKindId = $derived(
    targetSession?.kinds.find((entry) => entry.kind === kind.kind)?.id ?? null,
  )

  /** The target cluster this dialog last reset `targetNamespace` for — see
      the follow-effect below. Plain, not `$state`: it is bookkeeping for
      that effect alone, and nothing renders it. */
  let previousTargetCluster = ''

  /** Resets to the fallback comparison — this object, this cluster, this
      namespace, a blank name to fill in — every time the dialog opens,
      possibly on a different object than the last time it did. */
  $effect(() => {
    if (!open) return
    targetClusterId = clusterId
    targetNamespace = namespace
    targetName = ''
    pasted = ''
    mode = 'object'
    keepStatus = false
    error = null
    rawLeft = null
    rawRight = null
    comparing = false
    previousTargetCluster = clusterId
  })

  // The namespace follows the cluster when it changes: a namespace name from
  // the SOURCE cluster means nothing on a different one, and reusing it
  // anyway would silently ask a namespace-scoped kind about a namespace that
  // may not exist over there.
  $effect(() => {
    const current = targetClusterId
    if (!open || current === previousTargetCluster) return
    previousTargetCluster = current
    targetNamespace = current === clusterId ? namespace : (targetSession?.cluster.defaultNamespace ?? '')
  })

  /**
   * Suggestions for the name field, from the target session's own polled
   * list — the same "rich kinds only" line CLAUDE.md draws for the
   * navigator: Pod, Node, Namespace, Event and the six workload controllers
   * have a purpose-built list to ask; anything else (a generic table kind, a
   * CRD) falls back to the free-text field alone rather than pretending a
   * list exists for it.
   */
  async function loadSuggestions(): Promise<void> {
    if (!targetSession) {
      suggestions = []
      return
    }
    try {
      if (kind.id === RICH_KIND_IDS.pods) {
        suggestions = (await listPods(targetClusterId, targetNamespace)).map((pod) => pod.name)
      } else if (kind.id === RICH_KIND_IDS.nodes) {
        suggestions = (await listNodes(targetClusterId)).map((node) => node.name)
      } else if (kind.id === RICH_KIND_IDS.namespaces) {
        suggestions = (await listNamespaces(targetClusterId)).map((entry) => entry.name)
      } else if (kind.id === RICH_KIND_IDS.events) {
        suggestions = (await listEvents(targetClusterId, targetNamespace)).map((event) => event.name)
      } else if (WORKLOAD_KIND_BY_ID[kind.id]) {
        suggestions = (
          await listWorkloads(targetClusterId, WORKLOAD_KIND_BY_ID[kind.id], targetNamespace)
        ).map((workload) => workload.name)
      } else {
        suggestions = []
      }
    } catch {
      // A suggestion list that failed to load is not an error worth a
      // banner over — the field still takes a typed name.
      suggestions = []
    }
  }

  $effect(() => {
    void targetClusterId
    void targetNamespace
    void mode
    if (!open || mode !== 'object') return
    void loadSuggestions()
  })

  async function compare(): Promise<void> {
    error = null

    if (mode === 'object') {
      if (!targetSession) {
        error = 'Pick a cluster.'
        return
      }
      if (!targetKindId) {
        error = `${targetClusterId} does not serve ${kind.kind}.`
        return
      }
      if (!targetName.trim()) {
        error = `Name the ${kind.singular.toLowerCase()} to compare against.`
        return
      }
    } else if (!pasted.trim()) {
      error = 'Paste a manifest to compare against.'
      return
    }

    comparing = true
    try {
      const left = await getManifest(clusterId, kind.id, namespace, name, false)

      let right: string
      let label: string
      if (mode === 'paste') {
        right = pasted
        label = 'Pasted manifest'
      } else {
        const trimmedName = targetName.trim()
        right = await getManifest(
          targetClusterId,
          targetKindId as string,
          kind.namespaced ? targetNamespace : '',
          trimmedName,
          false,
        )
        label =
          targetClusterId === clusterId
            ? `${targetNamespace}/${trimmedName}`
            : `${trimmedName} on ${targetClusterId}`
      }

      rawLeft = left
      rawRight = right
      rightLabel = label
    } catch (cause) {
      error = `Could not load that object: ${toApiError(cause).message}`
    } finally {
      comparing = false
    }
  }

  function onKeydown(event: KeyboardEvent): void {
    if (event.key !== 'Escape' || !open) return
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
    aria-label="Close"
    tabindex="-1"
    class="fixed inset-0 z-[60] cursor-default bg-scrim/40"
    onclick={onclose}
  ></button>

  <div
    class="fixed inset-6 z-[70] flex flex-col overflow-hidden rounded-sm border
           border-outline-variant bg-surface-container-high shadow-level-3"
    role="dialog"
    aria-modal="true"
    use:modal
    aria-label="Compare {kind.singular}"
  >
    <header class="flex shrink-0 items-center gap-3 border-b border-outline-variant/60 px-4 py-3">
      {#if Icon}
        <Icon class="size-5 shrink-0 text-on-surface-variant" strokeWidth={1.8} />
      {/if}
      <div class="min-w-0">
        <h2 class="truncate text-title-medium font-semibold text-on-surface">Compare {kind.singular}</h2>
        <p class="truncate text-body-small text-on-surface-variant/70">
          {namespace ? `${namespace}/${name}` : name} on {clusterId}
        </p>
      </div>

      <button
        type="button"
        onclick={onclose}
        aria-label="Close"
        title="Close"
        class="state-layer ml-auto grid size-8 shrink-0 place-items-center rounded-full
               text-on-surface-variant transition-colors duration-100
               hover:bg-surface-container hover:text-on-surface"
      >
        <X class="size-4" strokeWidth={1.8} />
      </button>
    </header>

    <!-- The picker: what the right-hand side of the diff should be. -->
    <div class="flex shrink-0 flex-col gap-3 border-b border-outline-variant/60 bg-surface-container-low/50 px-4 py-3">
      <div class="flex flex-wrap items-end gap-3">
        <div class="flex items-center gap-1 rounded-sm border border-outline-variant/60 p-0.5">
          <button
            type="button"
            onclick={() => (mode = 'object')}
            aria-pressed={mode === 'object'}
            class="flex items-center gap-1.5 rounded-sm px-2.5 py-1 text-body-small font-medium transition-colors duration-100
                   {mode === 'object' ? 'bg-primary/14 text-primary' : 'text-on-surface-variant hover:bg-surface-container'}"
          >
            <FileSearch class="size-3.5" strokeWidth={1.8} />
            Another object
          </button>
          <button
            type="button"
            onclick={() => (mode = 'paste')}
            aria-pressed={mode === 'paste'}
            class="flex items-center gap-1.5 rounded-sm px-2.5 py-1 text-body-small font-medium transition-colors duration-100
                   {mode === 'paste' ? 'bg-primary/14 text-primary' : 'text-on-surface-variant hover:bg-surface-container'}"
          >
            <ClipboardPaste class="size-3.5" strokeWidth={1.8} />
            Paste a manifest
          </button>
        </div>

        {#if mode === 'object'}
          <label class="flex flex-col gap-1">
            <span class="text-body-small text-on-surface-variant/70">Cluster</span>
            <Select
              label="Cluster"
              value={targetClusterId}
              options={clusterOptions}
              onchange={(value) => (targetClusterId = value)}
              class="w-56"
            />
          </label>

          {#if kind.namespaced}
            <label class="flex flex-col gap-1">
              <span class="text-body-small text-on-surface-variant/70">Namespace</span>
              <Select
                label="Namespace"
                value={targetNamespace}
                options={namespaceOptions}
                onchange={(value) => (targetNamespace = value)}
                onopen={() => void targetSession?.refreshNamespaces()}
                class="w-48"
              />
            </label>
          {/if}

          <label class="flex flex-col gap-1">
            <span class="text-body-small text-on-surface-variant/70">{kind.singular} name</span>
            <input
              type="text"
              bind:value={targetName}
              list={datalistId}
              placeholder="name"
              class="field h-8 w-56 px-3 text-body-medium"
            />
            <datalist id={datalistId}>
              {#each suggestions as suggestion (suggestion)}
                <option value={suggestion}></option>
              {/each}
            </datalist>
          </label>
        {:else}
          <p class="text-body-small text-on-surface-variant/70">
            Paste a manifest below — its own `kind` and `apiVersion` are shown as they are, unresolved
            against any cluster.
          </p>
        {/if}

        <div class="ml-auto flex items-center gap-2">
          <ToolbarToggle
            icon={Activity}
            label="Include status"
            pressed={keepStatus}
            title={keepStatus ? 'Including status — click to compare specs only' : 'Comparing specs only — click to include status'}
            onclick={() => (keepStatus = !keepStatus)}
          />
          <button
            type="button"
            onclick={compare}
            disabled={comparing}
            class="flex items-center gap-1.5 rounded-full bg-primary px-4 py-1.5 text-body-medium
                   font-medium text-on-primary transition-opacity duration-100
                   hover:opacity-90 disabled:pointer-events-none disabled:opacity-50"
          >
            <GitCompare class="size-4" strokeWidth={1.8} />
            {comparing ? 'Comparing…' : 'Compare'}
          </button>
        </div>
      </div>

      {#if mode === 'paste'}
        <textarea
          bind:value={pasted}
          rows="6"
          placeholder="Paste a YAML manifest to compare against…"
          class="field w-full resize-y px-3 py-2 font-mono text-body-small"
          spellcheck="false"
        ></textarea>
      {/if}

      {#if isSecret}
        <p class="flex items-start gap-2 text-body-small text-on-surface-variant/70">
          <TriangleAlert class="mt-0.5 size-3.5 shrink-0" strokeWidth={1.8} />
          Secret values are compared by their decoded SIZE, the same way the YAML tab shows them —
          never by their contents.
        </p>
      {/if}

      {#if error}
        <p class="text-body-small text-error" role="alert">{error}</p>
      {/if}
    </div>

    <!-- The diff itself, once there is one. -->
    <div class="min-h-0 flex-1 bg-surface-container-lowest">
      {#if leftManifest !== null && rightManifest !== null}
        <DiffView left={leftManifest} right={rightManifest} leftLabel="{namespace ? `${namespace}/` : ''}{name}" {rightLabel} />
      {:else}
        <div class="flex h-full flex-col items-center justify-center gap-2 p-4 text-on-surface-variant/60">
          <GitCompare class="size-8" strokeWidth={1.2} />
          <p class="text-body-medium">Pick what to compare against, then press Compare.</p>
        </div>
      {/if}
    </div>
  </div>
{/if}
