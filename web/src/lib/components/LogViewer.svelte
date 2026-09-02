<!--
  LogViewer streams and displays pod logs.

  It subscribes to log:lines events from the backend and renders them in a
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
  import { EventsOn } from '$lib/wailsjs/runtime/runtime'
  import { StreamLogs, StopLogStream } from '$lib/wailsjs/go/wails/ManagementAPI'
  import { onMount, onDestroy, untrack } from 'svelte'
  import { SvelteMap } from 'svelte/reactivity'
  import { preferences } from '$stores/preferences.svelte'
  import PaneToolbar from './PaneToolbar.svelte'
  import Select from './Select.svelte'
  import ToolbarSearch from './ToolbarSearch.svelte'
  import ToolbarToggle from './ToolbarToggle.svelte'
  import ToolbarButton from './ToolbarButton.svelte'
  import WrapLinesToggle from './WrapLinesToggle.svelte'
  import { splitOnMatches } from '$lib/textSearch'
  import { ArrowDownToLine, Check, Copy, Eraser, ListFilter, Maximize2, Radio } from '@lucide/svelte'

  /**
   * Shapes of the `log:lines` / `log:end` event payloads.
   *
   * Wails only generates TS types for structs that appear in a *bound
   * method's* signature — these are emitted ad hoc via app.emit() instead,
   * so wailsjs/go/models has no corresponding export. Typed here to match
   * the JSON app/adapters/wails/dto.go actually sends (`streamId`, `lines`).
   */
  interface LogLinesEvent {
    streamId: string
    lines: string[]
  }
  interface LogEndEvent {
    streamId: string
    reason: string
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
    /**
     * Offers the pane the whole window. Omitted when it already has it.
     *
     * A log is the pane that most wants the room: lines are long, and a
     * column beside a table wraps or truncates most of them.
     */
    onmaximize?: () => void
  }

  let { clusterId, namespace, podName, containers = [], pods = [], onmaximize }: Props = $props()

  /** How many lines to ask the API server for. */
  const TAIL_SIZES = [100, 500, 1000, 5000] as const

  // Determine if we're in single pod or multi-pod mode
  const isMultiPod = $derived(pods.length > 0)
  const activePods = $derived(isMultiPod ? pods : podName ? [{ name: podName, containers }] : [])
  
  let selectedContainer = $state('')
  let follow = $state(true)
  let tailLines = $state(100)
  let searchQuery = $state('')
  /**
   * podName -> the backend's id for its stream.
   *
   * A SvelteMap, not a plain one in $state. Svelte 5 proxies objects and
   * arrays but NOT Map or Set, so `.set()` on a plain map mutates without
   * telling anybody — which is why the status bar read "Streaming from 0
   * pods" while lines were arriving: the template was still seeing the empty
   * map assigned before the loop that filled it.
   */
  let streamIds = new SvelteMap<string, string>()
  /**
   * The collected lines, each with an identity that outlives trimming.
   *
   * `seq` exists because the array is trimmed from the FRONT once it reaches
   * its cap, which renumbers every remaining line. Anything keyed by array
   * position — the each-block's key, a cache of measured row heights — would
   * silently point at a different line after the first trim. A counter that
   * only ever goes up cannot.
   */
  let logs = $state<Array<{ seq: number; podName: string; line: string }>>([])
  let nextSeq = 0
  let isStreaming = $state(false)
  let autoScroll = $state(true)

  let logContainer: HTMLDivElement
  /** Unsubscribes for the stream events, so teardown removes only ours. */
  let unsubscribe: Array<() => void> = []
  let copied = $state(false)
  /** Why the stream ended, when it ended badly. */
  let streamError = $state('')

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
    return logs.map((log) => ({
      log,
      matches: needle !== '' && log.line.toLowerCase().includes(needle),
    }))
  })

  const matching = $derived(searchQuery ? decorated.filter((row) => row.matches) : [])

  /** What is actually rendered. */
  const rows = $derived(filterMode && searchQuery ? matching : decorated)

  /**
   * Where the matching lines sit WITHIN `rows`.
   *
   * Positions rather than the rows themselves, because everything downstream
   * — the ruler, the jump, the current-match highlight — needs to turn a
   * match into a scroll offset, and the offset table is indexed by position.
   */
  const matchPositions = $derived.by(() => {
    if (!searchQuery) return []
    const out: number[] = []
    for (let i = 0; i < rows.length; i++) if (rows[i].matches) out.push(i)
    return out
  })

  /** Kept for the copy button, which copies what is shown. */
  const filteredLogs = $derived(rows.map((row) => row.log))

  /*
   * ------------------------------------------------------------------
   * The virtual list
   * ------------------------------------------------------------------
   *
   * Five thousand lines are five thousand DOM nodes, and building them was
   * seconds of work spread across the load even after the delivery itself was
   * fixed. Only the rows in view are rendered now; the rest are represented
   * by a blank spacer above and below, so the scrollbar behaves as if they
   * were all there.
   *
   * Heights are MEASURED rather than assumed. A fixed row height would be
   * simpler and would be wrong the moment wrapping is on, where one line of
   * JSON can occupy six rows and the next occupies one — the spacers would
   * drift and the scrollbar would lie. Unmeasured rows use an estimate taken
   * from the first row actually rendered, so the guess is at least this
   * font's line height rather than a number picked in advance.
   *
   * Spacer divs rather than absolute positioning: absolutely positioned rows
   * contribute nothing to their parent's width, which would break the
   * horizontal scrolling that unwrapped logs depend on.
   */

  /** Rows rendered beyond the viewport, so scrolling does not flicker. */
  const OVERSCAN = 12

  let scrollTop = $state(0)
  let viewportHeight = $state(0)
  let estimatedRow = $state(18)

  /**
   * seq -> measured height in pixels.
   *
   * A plain Map, deliberately outside the reactive graph: it is written from
   * a measurement pass that runs AFTER render, and making it reactive would
   * mean every measurement scheduled another render. `heightVersion` is
   * bumped instead, and only when a height actually changed, which is what
   * makes the loop converge.
   */
  const heights = new Map<number, number>()
  let heightVersion = $state(0)

  /**
   * Signals that the height table has changed.
   *
   * The increment is untracked because `heightVersion++` READS the value in
   * order to write it, which inside an effect makes that effect depend on its
   * own output — an update loop Svelte stops with
   * `effect_update_depth_exceeded`, taking the whole pane down with it.
   */
  function invalidateHeights(): void {
    heightVersion = untrack(() => heightVersion) + 1
  }

  /**
   * Running total of row heights: offsets[i] is where row i starts.
   *
   * One extra entry at the end holds the total, which is the height the
   * scroll container has to pretend to have.
   */
  const offsets = $derived.by(() => {
    void heightVersion
    const table = new Float64Array(rows.length + 1)
    for (let i = 0; i < rows.length; i++) {
      table[i + 1] = table[i] + (heights.get(rows[i].log.seq) ?? estimatedRow)
    }
    return table
  })

  const totalHeight = $derived(offsets.length > 0 ? offsets[offsets.length - 1] : 0)

  /** The last row starting at or before `y`. */
  function rowAt(y: number): number {
    let low = 0
    let high = rows.length - 1
    while (low < high) {
      const mid = (low + high + 1) >> 1
      if (offsets[mid] <= y) low = mid
      else high = mid - 1
    }
    return low
  }

  const firstRendered = $derived(Math.max(0, rowAt(scrollTop) - OVERSCAN))
  const lastRendered = $derived(
    Math.min(rows.length, rowAt(scrollTop + viewportHeight) + 1 + OVERSCAN),
  )

  /** The rows to render, each carrying its position for offset lookups. */
  const visibleRows = $derived(
    rows.slice(firstRendered, lastRendered).map((row, offset) => ({
      ...row,
      pos: firstRendered + offset,
    })),
  )

  const spacerAbove = $derived(offsets[firstRendered] ?? 0)
  const spacerBelow = $derived(Math.max(0, totalHeight - (offsets[lastRendered] ?? 0)))

  /**
   * Measures what was just rendered and corrects the table.
   *
   * Runs after paint, and only bumps the version when something actually
   * differs, so it settles in one pass for uniform rows and in a couple for
   * wrapped ones instead of oscillating.
   */
  $effect(() => {
    void visibleRows
    if (!logContainer) return

    const frame = requestAnimationFrame(() => {
      if (!logContainer) return
      let changed = false
      for (const element of logContainer.querySelectorAll<HTMLElement>('[data-log-seq]')) {
        const seq = Number(element.dataset.logSeq)
        const height = element.offsetHeight
        if (height <= 0) continue
        if (heights.get(seq) !== height) {
          heights.set(seq, height)
          changed = true
        }
        // The first real measurement replaces the guess for everything not
        // yet seen, which is most of a long log.
        if (estimatedRow !== height && heights.size === 1) estimatedRow = height
      }
      if (changed) invalidateHeights()
    })
    return () => cancelAnimationFrame(frame)
  })

  /**
   * Wrapping changes every row's height, so nothing measured under the old
   * setting is worth keeping.
   */
  $effect(() => {
    void preferences.wrapLines
    heights.clear()
    invalidateHeights()
  })

  /**
   * Drops heights for lines that have been trimmed away.
   *
   * IT ONLY EVER CLEARED ON EMPTY, which is not what trimming does: lines go
   * from the FRONT at MAX_LINES and their entries stayed. A chatty pod at a
   * hundred lines a second left a few hundred thousand entries in this map
   * after an hour, none of them reachable — measured heights for lines that
   * are no longer on screen and can never be scrolled back to.
   *
   * Sequence numbers only rise, so everything below the oldest retained line
   * is dead and can go in one pass. The pass is proportional to the map, and
   * it runs when the map is bigger than the buffer it describes.
   */
  $effect(() => {
    if (logs.length === 0) {
      if (heights.size > 0) {
        heights.clear()
        invalidateHeights()
      }
      return
    }
    if (heights.size <= logs.length) return

    const oldest = logs[0].seq
    for (const seq of heights.keys()) {
      if (seq < oldest) heights.delete(seq)
    }
  })

  function onScroll(): void {
    if (!logContainer) return
    scrollTop = logContainer.scrollTop
    viewportHeight = logContainer.clientHeight
  }

  /**
   * Whether the operator is holding a selection inside the log pane.
   *
   * Scoped to THIS pane, not the document: a selection made in the manifest
   * tab, or in another cluster's drawer, is no reason for these logs to stop
   * following. `commonAncestorContainer` is the deepest node containing the
   * whole range, so a selection spanning several log lines still resolves to
   * something inside the container.
   */
  function hasSelectionInLogs(): boolean {
    if (!logContainer) return false

    const selection = window.getSelection()
    if (!selection || selection.isCollapsed || selection.rangeCount === 0) return false

    return logContainer.contains(selection.getRangeAt(0).commonAncestorContainer)
  }

  /**
   * Keeps the viewport height current without waiting for a scroll.
   *
   * It has to be known before the first scroll or the window would be sized
   * from a height of zero and render almost nothing — and it changes whenever
   * the drawer is resized, which no scroll event reports.
   */
  $effect(() => {
    if (!logContainer) return
    viewportHeight = logContainer.clientHeight

    const observer = new ResizeObserver(() => {
      if (!logContainer) return
      viewportHeight = logContainer.clientHeight
      // A narrower pane re-wraps every line, so nothing measured at the old
      // width still applies.
      if (preferences.wrapLines) {
        heights.clear()
        invalidateHeights()
      }
    })
    observer.observe(logContainer)
    return () => observer.disconnect()
  })

  /**
   * Scrolls a row to the middle of the pane.
   *
   * Computed from the offset table rather than from the element, because with
   * virtualisation the row being jumped to is usually not rendered yet — its
   * position is known before it exists.
   */
  function revealRow(pos: number): void {
    if (!logContainer) return
    logContainer.scrollTop = Math.max(0, offsets[pos] - logContainer.clientHeight / 2)
  }

  function stepMatch(direction: 1 | -1): void {
    if (matchPositions.length === 0) return
    const next = (currentMatch + direction + matchPositions.length) % matchPositions.length
    currentMatch = next
    revealRow(matchPositions[next])
  }

  /**
   * Where the matching lines fall, as fractions of the whole list.
   *
   * Read straight from the offset table now. Measuring rendered elements —
   * which is how this worked before — cannot survive virtualisation: the
   * matches outside the viewport have no elements to measure, so the ruler
   * would only ever mark the part of the log already on screen.
   */
  const markers = $derived.by(() => {
    if (!searchQuery || filterMode || totalHeight <= 0) return []
    const seen = new Set<number>()
    const out: number[] = []
    for (const pos of matchPositions) {
      const fraction = offsets[pos] / totalHeight
      const key = Math.round(fraction * 200)
      if (seen.has(key)) continue
      seen.add(key)
      out.push(fraction)
    }
    return out
  })

  /** A new query starts from the first match rather than from wherever it left off. */
  $effect(() => {
    const needle = searchQuery
    currentMatch = -1
    if (needle && !filterMode) {
      requestAnimationFrame(() => {
        if (matchPositions.length > 0) {
          currentMatch = 0
          revealRow(matchPositions[0])
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

  /**
   * Which start is the current one.
   *
   * Starting is asynchronous — one awaited call per pod — so two starts can
   * be in flight at once, which is exactly what changing the dropdown twice
   * in quick succession does. Without this the older call would carry on
   * after the newer had cleared the map, registering its streams on top and
   * leaving two tails of different lengths writing into the same view, with
   * the loser's stream never stopped.
   */
  let streamGeneration = 0

  // Start streaming logs from all active pods
  async function startStream() {
    const generation = ++streamGeneration
    if (activePods.length === 0) return

    // Stop any existing streams
    for (const streamId of streamIds.values()) {
      await StopLogStream(streamId)
    }
    streamIds.clear()
    logs = []
    streamError = ''
    isStreaming = true

    // Start a stream for each pod
    for (const pod of activePods) {
      const container = selectedContainer || pod.containers[0] || ''
      if (!container) continue

      try {
        const streamId = await StreamLogs(clusterId, namespace, pod.name, container, follow, tailLines)
        if (generation !== streamGeneration) {
          // Superseded while this call was in flight. Close what we opened
          // rather than adding it to a view that has moved on.
          await StopLogStream(streamId)
          return
        }
        streamIds.set(pod.name, streamId)
      } catch (error) {
        console.error(`Failed to start log stream for pod ${pod.name}:`, error)
      }
    }
  }

  // Stop all streams
  async function stopStream() {
    // Invalidates any start still in flight, so it cannot register a stream
    // into a viewer that has just been told to stop.
    streamGeneration++
    for (const streamId of streamIds.values()) {
      await StopLogStream(streamId)
    }
    streamIds.clear()
    isStreaming = false
  }

  /** The most lines held before the oldest are dropped. */
  const MAX_LINES = 10_000

  /**
   * Appends a batch of lines.
   *
   * `push` into the reactive array rather than `logs = [...logs, line]`. The
   * spread copied the WHOLE array for every line that arrived, so the cost of
   * a tail was quadratic in its length: 5000 lines meant about twelve million
   * element copies, each one also re-deriving every row and re-diffing the
   * rendered list. That is what made the 5000-line setting hang the
   * application rather than merely be slow.
   *
   * Trimming uses splice for the same reason — `slice(-10_000)` allocated a
   * second ten-thousand-element array every time the cap was reached.
   */
  function handleLogLines(event: LogLinesEvent) {
    let podName = ''
    for (const [name, id] of streamIds.entries()) {
      if (id === event.streamId) {
        podName = name
        break
      }
    }

    for (const line of event.lines) {
      logs.push({ seq: nextSeq++, podName, line })
    }

    if (logs.length > MAX_LINES) {
      logs.splice(0, logs.length - MAX_LINES)
    }

    // Auto-scroll to bottom if enabled — but never while a search is active.
    // Somebody who has just jumped to a match is reading it, and a stream that
    // yanks the view back to the newest line every time a pod says something
    // makes the match impossible to read.
    //
    // And never while something is selected, for a sharper version of the same
    // reason. Selecting text in a following stream was not merely awkward, it
    // was impossible: every batch scrolled to the bottom, the rows the
    // selection was anchored in were unmounted by the windowing, and the
    // selection collapsed before anybody could reach Cmd+C. Following resumes
    // by itself the moment the selection is cleared, which a click anywhere
    // does.
    if (autoScroll && !searchQuery && !hasSelectionInLogs()) {
      requestAnimationFrame(() => {
        // Re-checked inside the frame: the drawer can close between the batch
        // arriving and the frame running, and reading scrollHeight off a
        // detached container throws.
        if (logContainer) logContainer.scrollTop = logContainer.scrollHeight
      })
    }
  }

  // Handle stream end
  /**
   * Marks a stream finished — but only one we were actually tracking.
   *
   * The emptiness check used to run for EVERY log:end, including one from a
   * stream that had already been replaced. Restarting is not atomic: the map
   * is cleared and the new ids are set after an await, so a late end event
   * landing in that window found an empty map and reported the whole viewer
   * stopped while its replacement was still opening. That is what showed as
   * "Stopped" over a stream the backend was still happily writing to.
   */
  function handleLogEnd(event: LogEndEvent) {
    let wasOurs = false
    for (const [name, id] of streamIds.entries()) {
      if (id === event.streamId) {
        streamIds.delete(name)
        wasOurs = true
        break
      }
    }

    // Why it ended, when it did not end cleanly. Without this every failure
    // was indistinguishable from a quiet pod: no permission to read logs, a
    // container that does not exist and a line over the size cap all looked
    // like the log simply stopping.
    if (wasOurs && event.reason) {
      streamError = event.reason
    }

    if (wasOurs && streamIds.size === 0) {
      isStreaming = false
    }
  }

  // Toggle follow mode
  /**
   * Starts or stops the stream, rather than only ever starting it.
   *
   * Turning follow off used to do nothing at all — the condition only fired
   * on the way back ON — so the control said "click to stop" and the lines
   * kept arriving. Off now stops the streams and leaves what has been
   * collected on screen; on opens them again, which re-fetches the tail.
   */
  async function toggleFollow(): Promise<void> {
    follow = !follow
    if (follow) await startStream()
    else await stopStream()
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

  /**
   * Restarts with the current settings.
   *
   * Just `startStream`, which already closes whatever is open before it opens
   * anything. Calling `stopStream()` first and NOT awaiting it — as this did
   * — let the two interleave: start would set `isStreaming = true`, then
   * stop's continuation would run after its own await and set it back to
   * false, leaving the pane reporting "Stopped" over a stream the backend was
   * still writing to. The status was wrong, not the stream.
   */
  function restartStream() {
    void startStream()
  }

  onMount(() => {
    // The returned unsubscribes, not EventsOff(name): EventsOff removes every
    // listener for that name across the whole application, which is harmless
    // while one log pane is mounted and wrong the moment two are.
    unsubscribe = [
      EventsOn('log:lines', handleLogLines),
      EventsOn('log:end', handleLogEnd),
    ]
    startStream()
  })

  onDestroy(() => {
    for (const off of unsubscribe) off()
    unsubscribe = []
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
      {#if onmaximize}
        <ToolbarButton
          icon={Maximize2}
          label="Maximize"
          title="Open in a larger window"
          onclick={onmaximize}
        />
      {/if}
    {/snippet}
  </PaneToolbar>

  <!-- Log output -->
  <div class="relative min-h-0 flex-1">
  <!--
    data-selectable, because the application sets `user-select: none` on the
    body: it is desktop chrome, not a web page, and dragging across a toolbar
    should not paint it blue. Log output is the clearest possible exception —
    an operator reading a stack trace wants three lines of it in a ticket, and
    the copy button in the toolbar takes the WHOLE stream, which is not the
    same thing at all.

    It goes on the scroll container rather than the rows so the text cursor
    covers the pane's padding too; a log surface that only turns into an
    I-beam over the glyphs themselves feels like it is refusing.
  -->
  <div
    bind:this={logContainer}
    onscroll={onScroll}
    data-selectable
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
        <!-- The rows that are not rendered, as height. The scrollbar has to
             behave as though the whole log were here, and these two blanks
             are what make it. -->
        <div style="height: {spacerAbove}px" aria-hidden="true"></div>

        {#each visibleRows as row (row.log.seq)}
          {@const log = row.log}
          <div
            data-log-seq={log.seq}
            class="hover:bg-surface-container-low
                   {preferences.wrapLines ? 'break-all whitespace-pre-wrap' : 'whitespace-pre'}
                   {!filterMode && currentMatch >= 0 && matchPositions[currentMatch] === row.pos
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

        <div style="height: {spacerBelow}px" aria-hidden="true"></div>
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
    <!-- A flex row, so the dot and the words are set apart by a real gap
         rather than by whatever whitespace the markup happened to leave. -->
    <span class="flex min-w-0 items-center gap-2">
      {#if isStreaming}
        <!-- Blue, not green. Green was the one colour left in the application
             asserting a fourth meaning, and "this is working" is already what
             blue says on every gauge and every status mark. -->
        <span class="inline-block size-2 shrink-0 animate-pulse rounded-full bg-gauge-normal"></span>
        {#if streamIds.size > 0}
          Streaming from {streamIds.size}
          {streamIds.size === 1 ? 'pod' : 'pods'}
        {:else}
          <!-- Streams open before the backend has answered with their ids.
               "Streaming from 0 pods" contradicted itself for that moment,
               and read as a fault rather than as a step. -->
          Connecting…
        {/if}
      {:else if streamError}
        <!-- Red, because this is the one state that is a fault rather than a
             choice. The reason is the API server's own words: "pods
             \"x\" is forbidden", "container y is not valid for pod z" — which
             say far more than any wording invented here would. -->
        <span class="inline-block size-2 shrink-0 rounded-full bg-gauge-critical"></span>
        <span class="min-w-0 truncate text-gauge-critical" title={streamError}>{streamError}</span>
      {:else}
        <span class="inline-block size-2 shrink-0 rounded-full bg-on-surface-variant/50"></span>
        {logs.length > 0 ? 'Stopped' : 'Not streaming'}
      {/if}
    </span>
    <span class="shrink-0 pl-3 tabular-nums">
      {filteredLogs.length}
      {filteredLogs.length === 1 ? 'line' : 'lines'}
    </span>
  </div>
</div>
