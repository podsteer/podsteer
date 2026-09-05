<!--
  The kubeconfig files and folders PodSteer reads.

  THE LIST IS PRECEDENCE, AND THAT IS THE WHOLE PANE. client-go keeps the
  FIRST file's definition of a context name, so a context defined twice is a
  tab that connects somewhere the operator was not expecting — with the right
  name on it. Every row therefore says where it came from and which of its
  contexts something above it already won.

  THREE THINGS THIS PANE DELIBERATELY DOES NOT OFFER, each because offering it
  would be a control that lies or a write PodSteer has no business making:

  - Choosing where new clusters are written. The one write PodSteer makes to a
    kubeconfig goes to the first entry of the precedence, and sources are only
    ever appended after the environment's entries — so a source cannot BE the
    write target, and a flag saying otherwise would have nothing to set.
  - Editing, deleting or renaming a context, or setting the current one. The
    files here are the operator's, some of them synced from somewhere else,
    and `current-context` is what kubectl in another terminal reads.
  - Pasting kubeconfig contents. That is Add cluster, which parses the paste,
    refuses a name collision and backs the file up first. Nothing here takes a
    file's contents at all — only its path.
-->
<script lang="ts">
  import type { KubeconfigSource } from '$lib/api/client'
  import { kubeconfigSources, ORIGIN_LABELS } from '$stores/kubeconfigSources.svelte'
  import Button from './Button.svelte'
  import {
    ChevronDown,
    ChevronUp,
    File as FileIcon,
    Folder,
    FolderPlus,
    FilePlus,
    Lock,
    Trash2,
    TriangleAlert,
  } from '@lucide/svelte'

  const store = kubeconfigSources

  /** Loads the list the first time the section is shown. */
  $effect(() => {
    if (store.status === 'idle') void store.load()
  })

  /**
   * The contexts a row lost, and to whom.
   *
   * Computed in Go and only rendered here: which file won a duplicated name is
   * a statement about the merge, not about this component.
   */
  function shadowed(source: KubeconfigSource): [string, string][] {
    return Object.entries(source.shadowedBy ?? {}).filter(
      (entry): entry is [string, string] => entry[1] !== undefined,
    )
  }

  function contributed(source: KubeconfigSource): string[] {
    const lost = source.shadowedBy ?? {}
    return (source.contexts ?? []).filter((name) => !(name in lost))
  }
</script>

<section>
  <h3 class="text-title-medium text-on-surface">Kubeconfig files</h3>
  <p class="mt-0.5 text-body-small leading-relaxed text-on-surface-variant">
    PodSteer reads your kubeconfig, and any extra files or folders you add here. The order is
    the order they are merged in: where two of them define the same context name, the one
    higher up wins — exactly as it does for <code class="text-on-surface">$KUBECONFIG</code>.
  </p>

  {#if store.settingsState?.notice}
    <p
      class="mt-3 flex items-start gap-2 rounded-sm border border-outline-variant/50
             bg-surface-container px-3 py-2 text-body-small leading-relaxed text-on-surface-variant"
    >
      <TriangleAlert class="mt-0.5 size-4 shrink-0 text-tertiary" aria-hidden="true" />
      <span>{store.settingsState.notice}</span>
    </p>
  {/if}

  {#if store.error}
    <p class="mt-3 text-body-small text-error" role="alert">{store.error}</p>
  {/if}

  <ul class="mt-4 flex flex-col gap-2">
    {#each store.sources as source, index (source.path + index)}
      {@const lost = shadowed(source)}
      {@const kept = contributed(source)}
      <li
        class="rounded-sm border border-outline-variant/50 bg-surface-container px-3 py-2.5"
        class:opacity-70={source.missing}
      >
        <div class="flex items-start gap-2.5">
          {#if source.kind === 'directory'}
            <Folder class="mt-0.5 size-4 shrink-0 text-on-surface-variant" aria-hidden="true" />
          {:else}
            <FileIcon class="mt-0.5 size-4 shrink-0 text-on-surface-variant" aria-hidden="true" />
          {/if}

          <div class="min-w-0 flex-1">
            <p class="truncate text-body-medium text-on-surface" title={source.path}>
              {source.path}
            </p>
            <p class="mt-0.5 flex flex-wrap items-center gap-x-2 gap-y-0.5 text-body-small text-on-surface-variant/80">
              <span>{ORIGIN_LABELS[source.origin] ?? source.origin}</span>
              {#if !source.editable}
                <!-- Read-only, and it says why rather than simply having no
                     buttons: nothing in this application can change an
                     environment variable or your own $KUBECONFIG. -->
                <span class="inline-flex items-center gap-1">
                  <Lock class="size-3" aria-hidden="true" />
                  read-only here
                </span>
              {/if}
              {#if source.missing}
                <span class="text-tertiary">not found right now — kept in the list</span>
              {:else if source.kind === 'directory'}
                <span>{source.files?.length ?? 0} file{(source.files?.length ?? 0) === 1 ? '' : 's'}</span>
              {/if}
            </p>

            {#if kept.length > 0}
              <p class="mt-1 text-body-small text-on-surface-variant">
                Provides {kept.join(', ')}
              </p>
            {/if}
            {#each lost as [name, winner] (name)}
              <p class="mt-1 text-body-small text-tertiary">
                {name} is ignored here — <span class="text-on-surface-variant">{winner}</span> defines
                it first
              </p>
            {/each}
          </div>

          {#if source.editable && store.writable}
            <div class="flex shrink-0 items-center gap-0.5">
              <Button
                variant="text"
                label="Move {source.path} earlier"
                disabled={store.busy || index === 0}
                onclick={() => void store.move(source.path, -1)}
              >
                <ChevronUp class="size-4" aria-hidden="true" />
              </Button>
              <Button
                variant="text"
                label="Move {source.path} later"
                disabled={store.busy || index === store.sources.length - 1}
                onclick={() => void store.move(source.path, 1)}
              >
                <ChevronDown class="size-4" aria-hidden="true" />
              </Button>
              <Button
                variant="text"
                label="Remove {source.path}"
                disabled={store.busy}
                onclick={() => void store.remove(source.path)}
              >
                <Trash2 class="size-4" aria-hidden="true" />
              </Button>
            </div>
          {/if}
        </div>
      </li>
    {/each}
  </ul>

  {#if store.writable}
    <div class="mt-4 flex flex-wrap gap-2">
      <Button variant="outlined" disabled={store.busy} onclick={() => void store.addFile()}>
        <FilePlus class="size-4" aria-hidden="true" />
        Add a file
      </Button>
      <Button variant="outlined" disabled={store.busy} onclick={() => void store.addFolder()}>
        <FolderPlus class="size-4" aria-hidden="true" />
        Add a folder
      </Button>
    </div>
  {/if}

  <p
    class="mt-5 rounded-sm border border-outline-variant/50 bg-surface-container px-3 py-2
           text-body-small leading-relaxed text-on-surface-variant"
  >
    PodSteer stores the <span class="text-on-surface">paths</span> only — never the contents of a
    kubeconfig, and never a credential. Removing an entry removes it from this list; the file
    itself is untouched. New clusters added through
    <span class="text-on-surface">Add cluster</span> are still written to your main kubeconfig, never
    to anything listed here, and your current context is never changed.
  </p>
</section>
