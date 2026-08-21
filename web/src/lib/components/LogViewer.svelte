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
  <!-- Controls -->
  <div class="flex items-center gap-2 border-b border-outline-variant bg-surface-container-low px-3 py-2">
    <!-- Container selector (only show if multiple containers) -->
    {#if allContainers.length > 1}
      <select
        bind:value={selectedContainer}
        onchange={restartStream}
        class="h-8 rounded-sm border border-outline bg-surface px-2 text-sm text-on-surface"
      >
        <option value="">All containers</option>
        {#each allContainers as container}
          <option value={container}>{container}</option>
        {/each}
      </select>
    {/if}

    <!-- Tail lines -->
    <select
      bind:value={tailLines}
      onchange={restartStream}
      class="h-8 rounded-sm border border-outline bg-surface px-2 text-sm text-on-surface"
    >
      <option value={100}>Last 100 lines</option>
      <option value={500}>Last 500 lines</option>
      <option value={1000}>Last 1000 lines</option>
      <option value={5000}>Last 5000 lines</option>
    </select>

    <!-- Follow toggle -->
    <button
      type="button"
      onclick={toggleFollow}
      class="h-8 rounded-sm px-3 text-sm transition-colors
             {follow ? 'bg-primary text-on-primary' : 'bg-surface-container text-on-surface-variant hover:bg-surface-container-high'}"
      title={follow ? 'Disable follow mode' : 'Enable follow mode'}
    >
      Follow
    </button>

    <!-- Auto-scroll toggle -->
    <button
      type="button"
      onclick={toggleAutoScroll}
      class="h-8 rounded-sm px-3 text-sm transition-colors
             {autoScroll ? 'bg-primary text-on-primary' : 'bg-surface-container text-on-surface-variant hover:bg-surface-container-high'}"
      title={autoScroll ? 'Disable auto-scroll' : 'Enable auto-scroll'}
    >
      Auto-scroll
    </button>

    <!-- Clear -->
    <button
      type="button"
      onclick={clearLogs}
      class="h-8 rounded-sm bg-surface-container px-3 text-sm text-on-surface-variant hover:bg-surface-container-high"
      title="Clear logs"
    >
      Clear
    </button>

    <!-- Search -->
    <div class="ml-auto flex items-center gap-1">
      <input
        type="text"
        bind:value={searchQuery}
        placeholder="Search logs..."
        class="field h-8 w-48 px-2 text-sm"
      />
      {#if searchQuery}
        <span class="text-xs text-on-surface-variant">
          {filteredLogs.length}/{logs.length}
        </span>
      {/if}
    </div>
  </div>

  <!-- Log output -->
  <div
    bind:this={logContainer}
    class="flex-1 overflow-auto bg-surface-container-lowest p-3 font-mono text-xs leading-relaxed"
  >
    {#if logs.length === 0}
      <div class="flex h-full items-center justify-center text-on-surface-variant">
        {isStreaming ? 'Waiting for logs...' : 'No logs available'}
      </div>
    {:else}
      {#each filteredLogs as log, i (i)}
        <div class="whitespace-pre-wrap break-all hover:bg-surface-container-low">
          {#if isMultiPod && log.podName}
            <span class="text-primary">{log.podName}:</span>
          {/if}
          <span class="text-on-surface">{log.line}</span>
        </div>
      {/each}
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
