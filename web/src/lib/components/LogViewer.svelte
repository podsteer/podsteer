<!--
  LogViewer streams and displays pod logs.

  It subscribes to log:line events from the backend and renders them in a
  scrollable container. The operator can select which container to view (for
  multi-container pods), toggle follow mode, and search within the logs.

  For deployments and other workloads, it can aggregate logs from multiple pods.
-->
<script lang="ts">
  import { EventsOn, EventsOff } from '$lib/wailsjs/runtime/runtime'
  import { StreamLogs, StopLogStream } from '$lib/wailsjs/go/wails/ManagementAPI'
  import { onMount, onDestroy } from 'svelte'
  import { preferences } from '$stores/preferences.svelte'
  import PaneToolbar from './PaneToolbar.svelte'
  import Select from './Select.svelte'
  import ToolbarSearch from './ToolbarSearch.svelte'
  import ToolbarToggle from './ToolbarToggle.svelte'
  import ToolbarButton from './ToolbarButton.svelte'
  import WrapLinesToggle from './WrapLinesToggle.svelte'
  import { splitOnMatches } from '$lib/textSearch'
  import { ArrowDownToLine, Check, Copy, Eraser, Radio } from '@lucide/svelte'

  /**
   * Shapes of the `log:line` / `log:end` event payloads.
   *
   * Wails only generates TS types for structs that appear in a *bound
   * method's* signature — these are emitted ad hoc via app.emit() instead,
   * so wailsjs/go/models has no corresponding export. Typed here to match
   * the JSON app/adapters/wails/dto.go actually sends (`streamId`, `line`).
   */
  interface LogLineEvent {
    streamId: string
    line: string
  }
  interface LogEndEvent {
    streamId: string
  }

  interface Props {
    /** The cluster ID. */
    clusterId: string
    /** The pod namespace. */
    namespace: string
    /** The pod name (for single pod mode). */
    podName?: string
    /** Available containers in the pod (for single pod mode). */
    containers?: string[]
    /** Multiple pods to aggregate logs from (for workload mode). */
    pods?: Array<{ name: string; containers: string[] }>
  }

  let { clusterId, namespace, podName, containers = [], pods = [] }: Props = $props()

  /** How many lines to ask the API server for. */
  const TAIL_SIZES = [100, 500, 1000, 5000] as const

  // Determine if we're in single pod or multi-pod mode
  const isMultiPod = $derived(pods.length > 0)
  const activePods = $derived(isMultiPod ? pods : podName ? [{ name: podName, containers }] : [])
  
  let selectedContainer = $state('')
  let follow = $state(true)
  let tailLines = $state(100)
  let searchQuery = $state('')
  let streamIds = $state<Map<string, string>>(new Map())
  let logs = $state<Array<{ podName: string; line: string }>>([])
  let isStreaming = $state(false)
  let autoScroll = $state(true)

  let logContainer: HTMLDivElement
  let copied = $state(false)

  /**
   * Copies what is on screen, which is the filtered set when filtering.
   *
   * The same rule the manifest's copy button follows: a control sitting above
   * a filtered view copies that view, or the clipboard disagrees with the
   * screen in a way nobody notices until they paste it.
   */
  async function copyLogs(): Promise<void> {
    const text = filteredLogs.map((log) => (isMultiPod && log.podName ? `${log.podName}: ${log.line}` : log.line)).join('\n')
    if (!text) return
    try {
      await navigator.clipboard.writeText(text)
      copied = true
      setTimeout(() => (copied = false), 1500)
    } catch {
      copied = false
    }
  }

  // Filter logs by search query
  const filteredLogs = $derived(
    searchQuery
      ? logs.filter((log) => log.line.toLowerCase().includes(searchQuery.toLowerCase()))
      : logs
  )

  // Get all unique containers across all pods
  const allContainers = $derived.by(() => {
    const containerSet = new Set<string>()
    for (const pod of activePods) {
      for (const container of pod.containers) {
        containerSet.add(container)
      }
    }
    return Array.from(containerSet).sort()
  })

  // Start streaming logs from all active pods
  async function startStream() {
    if (activePods.length === 0) return

    // Stop any existing streams
    for (const streamId of streamIds.values()) {
      await StopLogStream(streamId)
    }
    streamIds = new Map()
    logs = []
    isStreaming = true

    // Start a stream for each pod
    for (const pod of activePods) {
      const container = selectedContainer || pod.containers[0] || ''
      if (!container) continue

      try {
        const streamId = await StreamLogs(clusterId, namespace, pod.name, container, follow, tailLines)
        streamIds.set(pod.name, streamId)
      } catch (error) {
        console.error(`Failed to start log stream for pod ${pod.name}:`, error)
      }
    }
  }

  // Stop all streams
  async function stopStream() {
    for (const streamId of streamIds.values()) {
      await StopLogStream(streamId)
    }
    streamIds = new Map()
    isStreaming = false
  }

  // Handle incoming log lines
  function handleLogLine(event: LogLineEvent) {
    // Find which pod this log line belongs to
    let podName = ''
    for (const [name, id] of streamIds.entries()) {
      if (id === event.streamId) {
        podName = name
        break
      }
    }

    logs = [...logs, { podName, line: event.line }]

    // Keep only the last 10000 lines to prevent memory issues
    if (logs.length > 10000) {
      logs = logs.slice(-10000)
    }

    // Auto-scroll to bottom if enabled
    if (autoScroll && logContainer) {
      requestAnimationFrame(() => {
        logContainer.scrollTop = logContainer.scrollHeight
      })
    }
  }

  // Handle stream end
  function handleLogEnd(event: LogEndEvent) {
    // Remove the stream ID for the ended stream
    for (const [name, id] of streamIds.entries()) {
      if (id === event.streamId) {
        streamIds.delete(name)
        break
      }
    }
    
    // If all streams have ended, update state
    if (streamIds.size === 0) {
      isStreaming = false
    }
  }

  // Toggle follow mode
  function toggleFollow() {
    follow = !follow
    if (follow && !isStreaming) {
      startStream()
    }
  }

  // Toggle auto-scroll
  function toggleAutoScroll() {
    autoScroll = !autoScroll
    if (autoScroll && logContainer) {
      logContainer.scrollTop = logContainer.scrollHeight
    }
  }

  // Clear logs
  function clearLogs() {
    logs = []
  }

  // Restart stream with new settings
  function restartStream() {
    stopStream()
    startStream()
  }

  onMount(() => {
    EventsOn('log:line', handleLogLine)
    EventsOn('log:end', handleLogEnd)
    startStream()
  })

  onDestroy(() => {
    EventsOff('log:line')
    EventsOff('log:end')
    stopStream()
  })
</script>

<div class="flex h-full flex-col">
  <!-- The same shell and the same reading order as the manifest panes: what
       you are looking for, then what is streamed, then how it is shown, then
       what can be done to it. The rules separate those groups.

       The one deliberate difference is what the query DOES. On a manifest it
       finds and jumps; here it filters, because a log is a stream of
       independent lines and showing only the ones that match is the whole
       point — jumping between occurrences of "error" through ten thousand
       lines is a worse tool for the same job. The matched text is still
       highlighted in the same amber, so the two panes look alike even where
       they behave differently. -->
  <PaneToolbar>
    {#snippet children()}
      <ToolbarSearch
        value={searchQuery}
        placeholder="Filter lines"
        label="Filter the log lines"
        count="{filteredLogs.length}/{logs.length}"
        empty={filteredLogs.length === 0}
        autofocus
        onchange={(value) => (searchQuery = value)}
      />
    {/snippet}

    {#snippet trailing()}
      <!-- What is streamed. These re-open the stream rather than changing the
           view, which is why they sit apart from the toggles. -->
      {#if allContainers.length > 1}
        <Select
          compact
          label="Container"
          accessibleName="Container to stream"
          value={selectedContainer}
          options={[
            { value: '', label: 'All' },
            ...allContainers.map((container) => ({ value: container, label: container })),
          ]}
          onchange={(next) => {
            selectedContainer = next
            restartStream()
          }}
        />
      {/if}

      <!-- The number alone. "Last 100" spelled the same word out on every
           option and on the trigger, where the panel's own heading already
           says what the number counts — and a toolbar has no room to say
           anything twice. -->
      <Select
        compact
        label="Lines"
        accessibleName="Lines to fetch"
        value={String(tailLines)}
        options={TAIL_SIZES.map((size) => ({ value: String(size), label: String(size) }))}
        onchange={(next) => {
          tailLines = Number(next)
          restartStream()
        }}
      />

      <div class="mx-1 h-5 w-px bg-outline-variant/40"></div>

      <WrapLinesToggle />
      <ToolbarToggle
        icon={Radio}
        label="Follow"
        pressed={follow}
        title={follow ? 'Following the stream — click to stop' : 'Not following — click to stream new lines'}
        onclick={toggleFollow}
      />
      <ToolbarToggle
        icon={ArrowDownToLine}
        label="Scroll to newest"
        pressed={autoScroll}
        title={autoScroll
          ? 'Sticking to the newest line — click to stay where you are'
          : 'Staying put — click to follow the newest line'}
        onclick={toggleAutoScroll}
      />

      <div class="mx-1 h-5 w-px bg-outline-variant/40"></div>

      <ToolbarButton
        icon={copied ? Check : Copy}
        label="Copy logs"
        title={copied ? 'Copied' : 'Copy the lines shown'}
        active={copied}
        disabled={filteredLogs.length === 0}
        onclick={copyLogs}
      />
      <ToolbarButton
        icon={Eraser}
        label="Clear logs"
        title="Clear what has been collected so far"
        disabled={logs.length === 0}
        onclick={clearLogs}
      />
    {/snippet}
  </PaneToolbar>

  <!-- Log output -->
  <div
    bind:this={logContainer}
    class="min-h-0 flex-1 overflow-auto bg-surface-container-lowest p-3 font-mono text-xs leading-relaxed"
  >
    {#if logs.length === 0}
      <div class="flex h-full items-center justify-center text-on-surface-variant">
        {isStreaming ? 'Waiting for logs...' : 'No logs available'}
      </div>
    {:else}
      <!-- Unwrapped, the lines have to be allowed to be wider than the pane or
           there is nothing to scroll to: a block child shrinks to its
           container and the long text simply overflows invisibly. `w-max`
           sizes this to the LONGEST line and `min-w-full` keeps it at least
           the pane's width, so every hover stripe still spans the full
           width instead of ending raggedly at each line's own length. -->
      <div class={preferences.wrapLines ? '' : 'w-max min-w-full'}>
        {#each filteredLogs as log, i (i)}
          <div
            class="hover:bg-surface-container-low
                   {preferences.wrapLines ? 'break-all whitespace-pre-wrap' : 'whitespace-pre'}"
          >
            {#if isMultiPod && log.podName}
              <span class="text-primary">{log.podName}:</span>
            {/if}
            <!-- The filter has already decided WHICH lines are here; the
                 highlight says WHERE in each one, which is the part a filtered
                 view otherwise leaves you hunting for on a 400-character
                 line. Same amber as the manifest's matches. -->
            <span class="text-on-surface"
              >{#each splitOnMatches(log.line, searchQuery) as run, i (i)}{#if run.match}<mark
                    class="rounded-xs bg-gauge-warn/30 text-on-surface"
                    >{run.text}</mark
                  >{:else}{run.text}{/if}{/each}</span
            >
          </div>
        {/each}
      </div>
    {/if}
  </div>

  <!-- Status bar -->
  <div class="flex items-center justify-between border-t border-outline-variant bg-surface-container-low px-3 py-1 text-xs text-on-surface-variant">
    <span>
      {#if isStreaming}
        <span class="inline-block h-2 w-2 animate-pulse rounded-full bg-success"></span>
        Streaming from {streamIds.size} {streamIds.size === 1 ? 'pod' : 'pods'}
      {:else}
        <span class="inline-block h-2 w-2 rounded-full bg-on-surface-variant"></span>
        Stopped
      {/if}
    </span>
    <span>{filteredLogs.length} lines</span>
  </div>
</div>
