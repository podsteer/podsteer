<!--
  Looking inside a container.

  THE OTHER HALF OF FILE COPY. Copying already worked and required knowing the
  path; this is the half that lets somebody look. It reads and nothing else:
  no editing, no deleting, no renaming, no preview of a file's contents. The
  only write it offers is the upload that already existed, aimed at the
  directory on screen.

  NOTHING RUNS WHEN IT OPENS. Every listing is one deliberate press, the rule
  the reachability probe and the Helm page already follow: starting a process
  in somebody's container as a side effect of opening a pane is a commitment
  only they can make. It also means opening this cannot fill an audit log.

  WHAT IT REFUSES TO DO, on screen and in the code below: it does not follow a
  symlink on the same click that opens a directory — following one is its own
  labelled action, because a link can leave the tree you think you are in —
  and it will not open or download an entry whose name is not valid text,
  because that name cannot survive the round trip and would fetch a different
  file.
-->
<script lang="ts">
  import { untrack } from 'svelte'
  import { escapeLayer, type EscapeClaim } from '$lib/escape'
  import { modal } from '$lib/modal'
  import {
    chooseDirectory,
    chooseFile,
    listContainerDirectory,
    startDownload,
    startUpload,
    type DirectoryEntry,
    type DirectoryListing,
  } from '$lib/api/client'
  import { toApiError, type ApiError } from '$lib/api/errors'
  import { childOf, crumbsFor, displayName, filterNames, normalise, parentOf } from '$lib/fileBrowser'
  import { formatBytes } from '$lib/fileTransfer'
  import { formatClockTime } from '$lib/format'
  import Button from './Button.svelte'
  import DialogHeader from './DialogHeader.svelte'
  import {
    ChevronUp,
    File,
    FileQuestion,
    Folder,
    Link2,
    RefreshCw,
    Search,
    Upload,
  } from '@lucide/svelte'

  /** A transfer this dialog began, for the pane that renders it. */
  export interface BrowsedTransfer {
    id: string
    direction: 'download' | 'upload'
    remotePath: string
    localPath: string
  }

  interface Props {
    open: boolean
    clusterId: string
    namespace: string
    podName: string
    containerName: string
    /** The container's working directory, as the path to start from. */
    workingDir?: string
    /** Set when this cluster is marked read-only, with the reason. */
    readOnlyReason?: string | null
    /**
     * Called with the id of a download this dialog started.
     *
     * THE DIALOG DOES NOT WATCH ITS OWN TRANSFER, and must not: the progress
     * and done events are one stream for the whole application, the pane
     * behind this one already renders them, and a second renderer would be a
     * second state machine free to disagree with the first about whether a
     * copy finished. So it hands the whole transfer back and closes.
     *
     * The paths travel with the id because the pane SHOWS them — the
     * direction decides whether a finished copy names where it landed, and
     * the two paths are what its kubectl line is made of. An id alone would
     * have the pane render a transfer it could not describe.
     */
    onstarted: (transfer: BrowsedTransfer) => void
    onclose: () => void
  }

  let {
    open,
    clusterId,
    namespace,
    podName,
    containerName,
    workingDir = '',
    readOnlyReason = null,
    onstarted,
    onclose,
  }: Props = $props()

  // SEEDED ONCE, from the container's own working directory. It is a
  // starting point rather than a binding: navigating away and having the path
  // snap back to the spec's value on any re-render would be its own bug.
  let path = $state('/')

  // Seeded from the container's own working directory when the dialog opens,
  // and NOT bound to it: navigating away and having the path snap back to the
  // spec's value on a re-render would be its own bug. Untracked for the same
  // reason — this reads the prop once, on purpose.
  $effect(() => {
    if (!open) return
    path = normalise(untrack(() => workingDir) || '/')
  })
  let listing = $state<DirectoryListing | null>(null)
  let listedAt = $state<Date | null>(null)
  let busy = $state(false)
  let failure = $state<ApiError | null>(null)
  let filter = $state('')

  const crumbs = $derived(crumbsFor(path))
  const rows = $derived(filterNames(listing?.entries ?? [], filter))

  // Escape closes this before it reaches anything under it — one layer per
  // press, innermost first. See $lib/escape for why a stack rather than
  // stopPropagation.
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

  function onKeydown(event: KeyboardEvent): void {
    if (event.key === 'Escape' && open && escape?.owns()) close()
  }

  function close(): void {
    // Everything is discarded: a listing is names inside somebody's container
    // and is never kept — not here, not in settings, not in history.
    listing = null
    listedAt = null
    failure = null
    filter = ''
    onclose()
  }

  async function list(target: string): Promise<void> {
    busy = true
    failure = null
    try {
      const next = normalise(target)
      listing = await listContainerDirectory(clusterId, namespace, podName, containerName, next)
      path = listing.path || next
      listedAt = new Date()
      filter = ''
    } catch (cause) {
      failure = toApiError(cause)
      listing = null
    } finally {
      busy = false
    }
  }

  /** A directory row opens; a symlink does not, even when it points at one. */
  function openEntry(entry: DirectoryEntry): void {
    if (!entry.nameReadable) return
    void list(childOf(path, entry.name))
  }

  async function download(entry: DirectoryEntry): Promise<void> {
    if (!entry.nameReadable) return

    const localDir = await chooseDirectory('Save into')
    if (!localDir) return

    try {
      // The SAME call the typed-path field makes, so a download from a row
      // and a download from the field are one implementation with one set of
      // limits and one progress stream — and the id goes back to the pane
      // that renders that stream, or this would copy a file while the
      // interface said nothing and swallowed any failure with it.
      const remotePath = childOf(path, entry.name)
      const id = await startDownload(
        clusterId,
        namespace,
        podName,
        containerName,
        remotePath,
        localDir,
      )
      onstarted({ id, direction: 'download', remotePath, localPath: localDir })
      close()
    } catch (cause) {
      failure = toApiError(cause)
    }
  }

  /**
   * Copies something from this machine INTO the directory on screen.
   *
   * THE ONE WRITE THIS DIALOG OFFERS, and it is the upload that already
   * existed rather than a new one: the same call, the same limits, the same
   * refusal on a read-only cluster. What the browser adds is the destination
   * — the pane behind it can only aim at a path somebody typed, which is the
   * thing you come here because you do not know.
   *
   * It hands the transfer back and closes, exactly as a download does: the
   * pane renders the one progress stream, and a second renderer here would be
   * free to disagree with it about whether the copy finished.
   */
  async function upload(kind: 'file' | 'folder'): Promise<void> {
    const localPath =
      kind === 'file' ? await chooseFile('Upload a file') : await chooseDirectory('Upload a folder')
    if (!localPath) return

    try {
      const id = await startUpload(clusterId, namespace, podName, containerName, localPath, path)
      onstarted({ id, direction: 'upload', remotePath: path, localPath })
      close()
    } catch (cause) {
      failure = toApiError(cause)
    }
  }

  function iconFor(entry: DirectoryEntry) {
    if (!entry.nameReadable) return FileQuestion
    if (entry.kind === 'dir') return Folder
    if (entry.kind === 'symlink') return Link2
    return File
  }
</script>

<svelte:window onkeydown={onKeydown} />

{#if open}
  <div class="fixed inset-0 z-50 flex items-center justify-center bg-scrim/40 p-6">
    <div
      class="flex max-h-[80vh] w-full max-w-3xl flex-col rounded-sm border border-outline-variant
             bg-surface-container-high p-6 shadow-level-3"
      role="dialog"
      aria-modal="true"
      use:modal
      aria-label="Browse files in {containerName}"
    >
      <DialogHeader title="Files in {containerName}" onclose={close} />

      {#if readOnlyReason}
        <!-- DISABLED BEFORE IT IS PRESSED, never failed on a press. Listing
             runs a shell in the container, which is what the read-only mark
             is about — see ManagementService.ListDirectory. -->
        <p class="mt-3 text-body-medium text-on-surface-variant">{readOnlyReason}</p>
      {:else}
        <p class="mt-2 text-body-medium text-on-surface-variant">
          Runs a short shell script inside the container to list one directory. Nothing is
          written, nothing is followed, and no file's contents are read.
        </p>

        <!-- Where you are, and every step back to the root. -->
        <div class="mt-3 flex flex-wrap items-center gap-1 text-body-medium">
          {#each crumbs as crumb, index (crumb.path)}
            {#if index > 0}
              <span class="text-on-surface-variant/40" aria-hidden="true">/</span>
            {/if}
            <button
              type="button"
              disabled={busy}
              onclick={() => void list(crumb.path)}
              class="rounded-sm px-1 text-primary hover:bg-surface-container disabled:opacity-50"
            >
              {crumb.label}
            </button>
          {/each}
        </div>

        <div class="mt-3 flex flex-wrap items-center gap-2">
          <Button variant="outlined" disabled={busy} onclick={() => void list(path)}>
            <Search class="size-4" strokeWidth={1.8} />
            {listing ? 'Refresh' : 'List'}
          </Button>
          <Button
            variant="text"
            disabled={busy || path === '/'}
            onclick={() => void list(parentOf(path))}
          >
            <ChevronUp class="size-4" strokeWidth={1.8} />
            Up
          </Button>
          {#if listing}
            <input
              bind:value={filter}
              placeholder="Filter these rows"
              aria-label="Filter"
              class="field h-8 min-w-40 flex-1 px-2 text-body-medium"
            />
            <span class="flex items-center gap-1 text-body-medium text-on-surface-variant/70">
              <RefreshCw class="size-3" strokeWidth={2} />
              {formatClockTime(listedAt)}
            </span>
          {/if}
        </div>

        {#if failure}
          <!-- Rendered where the listing would have gone, never as a toast: a
               fact that stays true however many times the button is pressed is
               an answer rather than a rejection. -->
          <p class="mt-4 text-body-medium text-on-surface" role="status">{failure.message}</p>
        {:else if listing}
          {#if listing.truncated}
            <p class="mt-3 text-body-medium text-gauge-warn">
              More than {listing.cap} entries. PodSteer listed the first {listing.cap} and stopped —
              a listing that long is not something to read. Narrow the path, or use a terminal.
            </p>
          {/if}

          <ul class="mt-3 flex min-h-0 flex-1 flex-col gap-0.5 overflow-y-auto">
            {#each rows as entry (entry.name)}
              {@const Icon = iconFor(entry)}
              <li class="flex items-center gap-2 rounded-sm px-2 py-1 hover:bg-surface-container">
                <Icon class="size-4 shrink-0 text-on-surface-variant/70" strokeWidth={1.8} />

                {#if entry.kind === 'dir' && entry.nameReadable}
                  <button
                    type="button"
                    onclick={() => openEntry(entry)}
                    class="min-w-0 flex-1 truncate text-left text-body-medium text-primary"
                  >
                    {entry.name}
                  </button>
                {:else}
                  <span class="min-w-0 flex-1 truncate text-body-medium text-on-surface">
                    {displayName(entry.name, entry.nameReadable)}
                    {#if entry.kind === 'symlink' && entry.linkTarget}
                      <span class="text-on-surface-variant/70">→ {entry.linkTarget}</span>
                    {/if}
                  </span>
                {/if}

                <span class="shrink-0 tabular-nums text-body-medium text-on-surface-variant/70">
                  {entry.sizeKnown ? formatBytes(entry.size) : '—'}
                </span>

                {#if entry.kind === 'symlink' && entry.resolvesToDir && entry.nameReadable}
                  <!-- FOLLOWING IS ITS OWN ACTION. A link can leave the tree
                       you think you are in, so it is never the same click as
                       opening a directory. -->
                  <Button variant="text" onclick={() => void list(childOf(path, entry.name))}>
                    Follow
                  </Button>
                {/if}

                {#if entry.nameReadable}
                  <Button variant="text" onclick={() => void download(entry)}>Download</Button>
                {:else}
                  <span
                    class="shrink-0 text-body-medium text-on-surface-variant/60"
                    title="This name is not valid text. PodSteer will not open or download it, because the name cannot survive the round trip intact."
                  >
                    not valid text
                  </span>
                {/if}
              </li>
            {:else}
              <li class="px-2 py-3 text-body-medium text-on-surface-variant">
                {filter ? 'Nothing here matches that.' : 'This directory is empty.'}
              </li>
            {/each}
          </ul>

          {#each listing.notes ?? [] as note (note)}
            <p class="mt-2 text-body-medium text-on-surface-variant/70">{note}</p>
          {/each}
        {/if}
      {/if}

      <div class="mt-4 flex flex-wrap items-center justify-end gap-2">
        {#if !readOnlyReason}
          <!-- INTO THE DIRECTORY ON SCREEN, which is the whole point of
               offering it here: the pane behind can only aim at a path
               somebody typed, and finding out what the path is is why anybody
               opened this. File and folder are separate buttons rather than
               one that guesses, because the native dialogs are different and a
               chooser that silently accepted either would be picking for
               somebody. -->
          <span class="mr-auto flex items-center gap-2">
            <Button variant="text" disabled={busy} onclick={() => void upload('file')}>
              <Upload class="size-4" strokeWidth={1.8} />
              Upload file here
            </Button>
            <Button variant="text" disabled={busy} onclick={() => void upload('folder')}>
              Upload folder here
            </Button>
          </span>
        {/if}
        <Button variant="outlined" onclick={close}>Close</Button>
      </div>
    </div>
  </div>
{/if}
