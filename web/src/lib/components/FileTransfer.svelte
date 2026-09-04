<!--
  Copying files to and from one container — `kubectl cp`, from the drawer.

  Two buttons and, once one is pressed, a small form beneath them: the
  container-side path, the local side chosen through a native dialog, Start.
  While a transfer runs the form becomes a progress line with Cancel; when
  it ends, a result line, or the failure. All inline in the container's own
  section rather than in a dialog, because a copy is a thing done to THIS
  container and the pane it belongs in is already open.

  NOTHING HERE TOUCHES A FILE. The local path is a string the dialog handed
  back and the Go side checks again; every byte moves through Go, where the
  archive's entries are checked before anything is written — see
  app/adapters/archive. This component is the request and the report.

  An upload is a write into the cluster's workload and is refused on a
  read-only cluster, the same way a shell is; a download is a read and is
  not. Both facts are read live from the organisation store, so a group
  toggled read-only while this pane is open disables Upload at once.
-->
<script lang="ts">
  import { onDestroy } from 'svelte'
  import { Download, FolderOpen, File, Upload, X } from '@lucide/svelte'
  import {
    cancelFileCopy,
    chooseDirectory,
    chooseFile,
    onFileCopyDone,
    onFileCopyProgress,
    startDownload,
    startUpload,
    type Unsubscribe,
  } from '$lib/api/client'
  import { toApiError } from '$lib/api/errors'
  import * as kubectl from '$lib/kubectl'
  import {
    IDLE,
    canStart,
    describeResult,
    downloadDestination,
    finished,
    formatBytes,
    isBusy,
    progressed,
    resolveRemotePath,
    started,
    starting,
    uploadDestination,
    type Direction,
    type TransferState,
  } from '$lib/fileTransfer'
  import { organisation } from '$stores/organisation.svelte'
  import KubectlHint from './KubectlHint.svelte'

  interface Props {
    clusterId: string
    namespace: string
    podName: string
    containerName: string
    /**
     * The container's working directory, from its spec. A relative path
     * typed below is resolved against it — the same thing tar does inside
     * the exec — and it is the hint the path field shows when empty.
     */
    workingDir?: string
  }

  let { clusterId, namespace, podName, containerName, workingDir }: Props = $props()

  /** Read live, like DetailDrawer does, so a group toggled read-only mid-pane applies at once. */
  const isReadOnly = $derived.by(() => {
    const placement = organisation.placementOf(clusterId)
    return organisation.settingsFor(placement.project, placement.group).readOnly
  })

  // Matches the backend's own message (see app/adapters/wails/errors.go's
  // CodeReadOnly and DetailDrawer's readOnlyReason).
  const READ_ONLY_REASON = 'This cluster is marked read-only in PodSteer. Change that under Organise.'

  /** Which form is open, if any. */
  let direction = $state<Direction | null>(null)
  let remoteTyped = $state('')
  let localPath = $state('')
  let transfer = $state<TransferState>(IDLE)
  /** A refusal from Start itself, before a transfer existed. */
  let startError = $state('')

  const remotePath = $derived(resolveRemotePath(remoteTyped, workingDir))
  const busy = $derived(isBusy(transfer))

  const startable = $derived(
    direction !== null &&
      canStart({ direction, remotePath, localPath, state: transfer, readOnly: isReadOnly }),
  )

  /**
   * The kubectl line for what Start is about to do, with placeholders for
   * whatever has not been chosen yet so the shape reads before the fields
   * are filled.
   */
  const command = $derived.by(() => {
    if (direction === 'download') {
      const dest = downloadDestination(localPath || '<folder>', remotePath || '<path>')
      return kubectl.cpFromPod(clusterId, podName, namespace, remotePath || '<path>', dest, containerName)
    }
    if (direction === 'upload') {
      const dest = uploadDestination(remotePath || '<directory>', localPath || '<file>')
      return kubectl.cpToPod(clusterId, podName, namespace, localPath || '<file>', dest, containerName)
    }
    return ''
  })

  /** The failure, parsed out of its envelope for display. */
  const failure = $derived(transfer.error ? toApiError(transfer.error) : null)

  let subscriptions: Unsubscribe[] = []

  /**
   * Subscribed only while something is worth hearing about: a pane with no
   * transfer running has no reason to wake for every other container's
   * progress events.
   */
  function listen(): void {
    if (subscriptions.length > 0) return
    subscriptions = [
      onFileCopyProgress((event) => (transfer = progressed(transfer, event))),
      onFileCopyDone((event) => (transfer = finished(transfer, event))),
    ]
  }

  function stopListening(): void {
    for (const unsubscribe of subscriptions) unsubscribe()
    subscriptions = []
  }

  $effect(() => {
    if (!busy) stopListening()
  })

  // The transfer itself is NOT cancelled on unmount. It was started
  // deliberately and completes on its own in Go — closing the drawer
  // halfway through a download should no more discard it than closing a
  // terminal window discards a running `kubectl cp`.
  onDestroy(stopListening)

  function open(next: Direction): void {
    if (busy) return
    if (direction === next) {
      direction = null
      return
    }
    direction = next
    remoteTyped = next === 'upload' ? (workingDir ?? '') : ''
    localPath = ''
    transfer = IDLE
    startError = ''
  }

  async function pickLocal(kind: 'file' | 'folder'): Promise<void> {
    try {
      const chosen =
        kind === 'folder'
          ? await chooseDirectory(direction === 'download' ? 'Save into' : 'Upload folder')
          : await chooseFile('Upload file')
      // "" is the dialog's own word for "cancelled", and not a reason to
      // clear what was chosen before.
      if (chosen) localPath = chosen
    } catch (cause) {
      startError = toApiError(cause).message
    }
  }

  async function start(): Promise<void> {
    if (!startable || direction === null) return
    startError = ''
    transfer = starting()
    listen()

    try {
      const id =
        direction === 'download'
          ? await startDownload(clusterId, namespace, podName, containerName, remotePath, localPath)
          : await startUpload(clusterId, namespace, podName, containerName, localPath, remotePath)
      transfer = started(transfer, id)
    } catch (cause) {
      startError = toApiError(cause).message
      transfer = IDLE
    }
  }

  function cancel(): void {
    if (transfer.transferId) void cancelFileCopy(transfer.transferId)
  }

  function onKeydown(event: KeyboardEvent): void {
    if (event.key === 'Enter' && startable) {
      event.preventDefault()
      void start()
    }
  }
</script>

<p class="mt-3 mb-1 text-body-medium text-on-surface">Files</p>

<div class="flex flex-wrap items-center gap-1.5">
  <button
    type="button"
    disabled={busy}
    onclick={() => open('download')}
    aria-pressed={direction === 'download'}
    class="state-layer inline-flex h-7 shrink-0 items-center gap-1.5 rounded-sm border px-2
           text-label-large transition-colors duration-100 disabled:opacity-50
           {direction === 'download'
      ? 'border-primary bg-surface-container text-on-surface'
      : 'border-outline-variant text-on-surface-variant hover:bg-surface-container hover:text-on-surface'}"
  >
    <Download class="size-3.5" strokeWidth={1.8} />
    Download…
  </button>

  <button
    type="button"
    disabled={busy || isReadOnly}
    title={isReadOnly ? READ_ONLY_REASON : 'Copy a local file or folder into this container'}
    onclick={() => open('upload')}
    aria-pressed={direction === 'upload'}
    class="state-layer inline-flex h-7 shrink-0 items-center gap-1.5 rounded-sm border px-2
           text-label-large transition-colors duration-100 disabled:opacity-50
           {direction === 'upload'
      ? 'border-primary bg-surface-container text-on-surface'
      : 'border-outline-variant text-on-surface-variant hover:bg-surface-container hover:text-on-surface'}"
  >
    <Upload class="size-3.5" strokeWidth={1.8} />
    Upload…
  </button>
</div>

{#if direction}
  <div class="mt-2 flex flex-col gap-2 rounded-sm border border-outline-variant/40 p-3">
    <!-- The container side: a path, with the working directory as the hint
         a relative path resolves against. -->
    <label class="flex flex-col gap-1 text-body-small text-on-surface-variant">
      <span>{direction === 'download' ? 'Path in container' : 'Directory in container'}</span>
      <input
        type="text"
        bind:value={remoteTyped}
        onkeydown={onKeydown}
        disabled={busy}
        placeholder={direction === 'download'
          ? `e.g. ${workingDir ? workingDir.replace(/\/+$/, '') : ''}/config.yaml`
          : (workingDir ?? '/')}
        spellcheck={false}
        autocomplete="off"
        aria-label={direction === 'download' ? 'Path in container' : 'Directory in container'}
        class="h-7 rounded-sm border border-outline-variant bg-transparent px-1.5 font-mono
               text-body-medium text-on-surface placeholder:text-on-surface-variant/50
               focus:border-primary focus:outline-none disabled:opacity-50"
      />
      {#if workingDir && remoteTyped.trim() && !remoteTyped.trim().startsWith('/')}
        <span class="font-mono text-on-surface-variant/70">→ {remotePath}</span>
      {/if}
    </label>

    <!-- The local side is only ever chosen through a native dialog: there
         is no text field here to type a path into, because the webview has
         no business naming where files go. -->
    <div class="flex flex-wrap items-center gap-1.5 text-body-small text-on-surface-variant">
      <span class="shrink-0">{direction === 'download' ? 'Save into' : 'Upload'}</span>
      {#if direction === 'upload'}
        <button
          type="button"
          disabled={busy}
          onclick={() => void pickLocal('file')}
          class="state-layer inline-flex h-7 shrink-0 items-center gap-1.5 rounded-sm border
                 border-outline-variant px-2 text-label-large text-on-surface-variant
                 transition-colors duration-100 hover:bg-surface-container hover:text-on-surface
                 disabled:opacity-50"
        >
          <File class="size-3.5" strokeWidth={1.8} />
          Choose file…
        </button>
      {/if}
      <button
        type="button"
        disabled={busy}
        onclick={() => void pickLocal('folder')}
        class="state-layer inline-flex h-7 shrink-0 items-center gap-1.5 rounded-sm border
               border-outline-variant px-2 text-label-large text-on-surface-variant
               transition-colors duration-100 hover:bg-surface-container hover:text-on-surface
               disabled:opacity-50"
      >
        <FolderOpen class="size-3.5" strokeWidth={1.8} />
        Choose folder…
      </button>
      {#if localPath}
        <span class="min-w-0 truncate font-mono text-on-surface" title={localPath} data-selectable>
          {localPath}
        </span>
      {/if}
    </div>

    <div class="flex flex-wrap items-center gap-2">
      {#if busy}
        <!-- Indeterminate: a tar stream carries no total, so a bar that
             filled up would be a guess. The byte count is what moves. -->
        <div
          class="h-1 w-32 overflow-hidden rounded-full bg-surface-container-highest"
          role="progressbar"
          aria-busy="true"
          aria-label="Transferring"
        >
          <div class="k8s-file-progress h-full w-1/3 rounded-full bg-primary"></div>
        </div>
        <span class="text-body-small tabular-nums text-on-surface-variant">
          {transfer.phase === 'starting' ? 'Starting…' : `${formatBytes(transfer.bytes)} transferred`}
        </span>
        <button
          type="button"
          disabled={transfer.phase !== 'running'}
          onclick={cancel}
          class="state-layer ml-auto inline-flex h-7 shrink-0 items-center gap-1.5 rounded-sm border
                 border-outline-variant px-2 text-label-large text-on-surface-variant
                 transition-colors duration-100 hover:bg-surface-container hover:text-on-surface
                 disabled:opacity-50"
        >
          <X class="size-3.5" strokeWidth={1.8} />
          Cancel
        </button>
      {:else}
        <button
          type="button"
          disabled={!startable}
          title={direction === 'upload' && isReadOnly ? READ_ONLY_REASON : undefined}
          onclick={() => void start()}
          class="state-layer inline-flex h-7 shrink-0 items-center gap-1.5 rounded-sm border
                 border-outline-variant px-2 text-label-large text-on-surface-variant
                 transition-colors duration-100 hover:bg-surface-container hover:text-on-surface
                 disabled:opacity-50"
        >
          {#if direction === 'download'}
            <Download class="size-3.5" strokeWidth={1.8} />
            Download
          {:else}
            <Upload class="size-3.5" strokeWidth={1.8} />
            Upload
          {/if}
        </button>
      {/if}
    </div>

    {#if startError}
      <p class="text-body-small text-error" role="alert">{startError}</p>
    {/if}

    {#if transfer.phase === 'done' && transfer.result}
      <p class="text-body-small text-on-surface" role="status">
        <span class="text-success">Done</span> — {describeResult(transfer.result)}
        {#if direction === 'download'}
          → <span class="font-mono" data-selectable>{transfer.result.localPath}</span>
        {/if}
      </p>
    {:else if transfer.phase === 'cancelled'}
      <p class="text-body-small text-on-surface-variant" role="status">
        Cancelled after {formatBytes(transfer.bytes)}. Whatever had already landed was left in place.
      </p>
    {:else if transfer.phase === 'failed' && failure}
      <p class="text-body-small text-error" role="alert">{failure.message}</p>
    {/if}

    {#if transfer.result?.notes.length}
      <!-- What was deliberately left out — a device node, a link pointing
           outside the selection. Said, never silently dropped. -->
      <ul class="flex flex-col gap-0.5 text-body-small text-on-surface-variant">
        {#each transfer.result.notes as note (note)}
          <li class="font-mono break-all">{note}</li>
        {/each}
      </ul>
    {/if}

    {#if !busy && transfer.phase === 'idle'}
      <p class="text-body-small text-on-surface-variant/70">
        Runs <code>tar</code> inside the container, exactly as <code>kubectl cp</code> does — the
        image needs a tar binary.
      </p>
    {/if}

    <KubectlHint {command} />
  </div>
{/if}

<style>
  .k8s-file-progress {
    animation: k8s-file-progress 1.4s cubic-bezier(0.65, 0.15, 0.35, 0.85) infinite;
  }

  @keyframes k8s-file-progress {
    0% {
      transform: translateX(-100%);
    }
    100% {
      transform: translateX(400%);
    }
  }
</style>
