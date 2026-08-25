<!--
  LogViewer streams and displays pod logs.

  It subscribes to log:line events from the backend and renders them in a
  scrollable container. The operator can select which container to view (for
  multi-container pods), toggle follow mode, and search within the logs.

  Searching marks matches in place rather than hiding what does not match, the
  same as the manifest pane, and a ruler down the right edge shows where they
  fall in the whole stream. The filter control collapses the view to the
  matching lines when that is what is wanted — which is often, on a long
  stream — and the ruler goes away with it, because against a filtered list
  every visible line matches and the track would say nothing.

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
  import { ArrowDownToLine, Check, Copy, Eraser, ListFilter, Radio } from '@lucide/svelte'

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
   * Whether the query hides the lines it does not match.
   *
   * Off by default, so searching behaves as it does on a manifest: every line
   * stays, the matches are marked, and the ruler shows where they fall. That
   * ruler is the reason for the default — markers need the non-matching lines
   * present to mark against, and against a filtered list every visible line
   * matches and the track is a solid bar.
   *
   * On, it does what the box used to do unconditionally, which is the right
   * tool once a stream is long enough that the matches are what you want and
   * the rest is what you are trying to get rid of.
   */
  let filterMode = $state(false)

  /** Which match Enter last moved to, as an index into `matching`. */
  let currentMatch = $state(-1)

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

  /**
   * Every line, tagged with whether it matches and where it started.
   *
   * The original index is carried through because filtering renames nothing:
   * the ruler, the jump and the highlight all have to refer to the same line
   * whether or not its neighbours are on screen.
   */
  const decorated = $derived.by(() => {
    const needle = searchQuery.toLowerCase()
    return logs.map((log, index) => ({
      log,
      index,
      matches: needle !== '' && log.line.toLowerCase().includes(needle),
    }))
  })

  const matching = $derived(searchQuery ? decorated.filter((row) => row.matches) : [])

  /** What is actually rendered. */
  const rows = $derived(filterMode && searchQuery ? matching : decorated)

  /** Kept for the copy button, which copies what is shown. */
  const filteredLogs = $derived(rows.map((row) => row.log))

  /**
   * Scrolls a line to the middle of the pane.
   *
   * `scrollTop` rather than scrollIntoView: the latter walks up the ancestor
   * chain and would scroll the drawer itself to bring the pane into view,
   * moving things the reader did not ask to move.
   */
  function revealLine(index: number): void {
    if (!logContainer) return
    const element = logContainer.querySelector<HTMLElement>(`[data-log-index="${index}"]`)
    if (!element) return
    logContainer.scrollTop = element.offsetTop - logContainer.clientHeight / 2
  }

  function stepMatch(direction: 1 | -1): void {
    if (matching.length === 0) return
    const next = (currentMatch + direction + matching.length) % matching.length
    currentMatch = next
    revealLine(matching[next].index)
  }

  /**
   * Where the matching lines fall, as fractions of the scrollable height.
   *
   * Measured from the rendered rows rather than computed from line counts,
   * because a wrapped line is several rows tall and a marker placed by index
   * would drift down the pane. Only the matching rows are measured, so the
   * cost is the size of the result rather than of the log.
   *
   * Read after a frame: the rows have to exist and be laid out before their
   * offsets mean anything.
   */
  let markers = $state<number[]>([])

  $effect(() => {
    // Anything that moves a line, changes which lines match, or changes how
    // tall a line is.
    const active = searchQuery !== '' && !filterMode
    void logs.length
    void preferences.wrapLines
    void matching.length

    if (!active || !logContainer) {
      markers = []
      return
    }

    const frame = requestAnimationFrame(() => {
      if (!logContainer) return
      const height = logContainer.scrollHeight
      if (height <= 0) {
        markers = []
        return
      }
      const seen = new Set<number>()
      const found: number[] = []
      for (const element of logContainer.querySelectorAll<HTMLElement>('[data-log-match]')) {
        const fraction = element.offsetTop / height
        const key = Math.round(fraction * 200)
        if (seen.has(key)) continue
        seen.add(key)
        found.push(fraction)
      }
      markers = found
    })
    return () => cancelAnimationFrame(frame)
  })

  /** A new query starts from the first match rather than from wherever it left off. */
  $effect(() => {
    const needle = searchQuery
    currentMatch = -1
    if (needle && !filterMode) {
      requestAnimationFrame(() => {
        if (matching.length > 0) {
          currentMatch = 0
          revealLine(matching[0].index)
        }
      })
    }
  })

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

    // Auto-scroll to bottom if enabled — but never while a search is active.
    // Somebody who has just jumped to a match is reading it, and a stream that
    // yanks the view back to the newest line every time a pod says something
    // makes the match impossible to read.
    if (autoScroll && !searchQuery && logContainer) {
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
        count="{matching.length}/{logs.length}"
        empty={matching.length === 0}
        autofocus
        onchange={(value) => (searchQuery = value)}
        onnext={filterMode ? undefined : () => stepMatch(1)}
        onprevious={filterMode ? undefined : () => stepMatch(-1)}
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
        icon={ListFilter}
        label="Filter to matches"
        pressed={filterMode}
        disabled={!searchQuery}
        title={!searchQuery
          ? 'Type something to filter by'
          : filterMode
            ? 'Showing only matching lines — click to show them all again'
            : 'Showing every line with the matches marked — click to hide the rest'}
        onclick={() => (filterMode = !filterMode)}
      />
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
  <div class="relative min-h-0 flex-1">
  <div
    bind:this={logContainer}
    class="relative h-full overflow-auto bg-surface-container-lowest p-3 font-mono text-xs leading-relaxed"
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
        {#each rows as row (row.index)}
          {@const log = row.log}
          <div
            data-log-index={row.index}
            data-log-match={row.matches && !filterMode ? '' : undefined}
            class="hover:bg-surface-container-low
                   {preferences.wrapLines ? 'break-all whitespace-pre-wrap' : 'whitespace-pre'}
                   {!filterMode && currentMatch >= 0 && matching[currentMatch]?.index === row.index
              ? 'bg-gauge-warn/12'
              : ''}"
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

  <!-- Where the matches fall in the whole stream. Only when the rest of the
       lines are still there: against a filtered list every visible line
       matches, and the track would be one unbroken bar. -->
  {#if markers.length > 0}
    <div class="pointer-events-none absolute inset-y-0 right-0 w-2.5" aria-hidden="true">
      {#each markers as fraction (fraction)}
        <span
          class="absolute right-0.5 h-0.5 w-1.5 rounded-full bg-gauge-warn"
          style="top: {(fraction * 100).toFixed(3)}%"
        ></span>
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
