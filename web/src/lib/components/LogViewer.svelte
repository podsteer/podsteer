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

  Kubernetes timestamps are ALWAYS requested from the backend — see
  CLAUDE.md, "Log timestamps are always requested". `timestampMode` is a pure
  display choice applied here, at render, over a line the backend already
  sent stamped; changing it never re-opens the stream. `sinceSeconds` and
  `previousContainer`, by contrast, change what the API server is ASKED for,
  so they live with the other stream-shaping controls and DO restart it.
-->
<script lang="ts">
  import { flash } from '$lib/flash.svelte'
  import { subscribe } from '$lib/api/client'
  import { StreamLogs, StopLogStream } from '$bindings/managementapi'
  import { onMount, onDestroy, untrack } from 'svelte'
  import { SvelteMap } from 'svelte/reactivity'
  import { preferences } from '$stores/preferences.svelte'
  import { saveTextFile } from '$lib/api/client'
  import PaneToolbar from './PaneToolbar.svelte'
  import Select from './Select.svelte'
  import ToolbarSearch from './ToolbarSearch.svelte'
  import ToolbarToggle from './ToolbarToggle.svelte'
  import ToolbarButton from './ToolbarButton.svelte'
  import WrapLinesToggle from './WrapLinesToggle.svelte'
  import { splitOnMatches, splitOnRegex } from '$lib/textSearch'
  import { matches, parseQuery, type Query } from '$lib/query'
  import { formatLogTimestamp, parseLogTimestamp, type TimestampMode } from '$lib/logTimestamps'
  import { detectSeverity, parseStructuredLine, type Severity, type StructuredLine } from '$lib/logFormat'
  import { ansiToSpans, type AnsiColor, type AnsiSpan } from '$lib/ansi'
  import { groupLogLines } from '$lib/logGroups'
  import { buildLogFilename } from '$lib/exportFilename'
  import {
    ArrowDownToLine,
    Braces,
    Check,
    ChevronDown,
    Copy,
    Download,
    Eraser,
    ListFilter,
    Maximize2,
    Radio,
    RotateCcw,
  } from '@lucide/svelte'

  /**
   * Shapes of the `log:lines` / `log:end` event payloads.
   *
   * Wails only generates TS types for structs that appear in a *bound
   * method's* signature — these are emitted ad hoc via app.emit() instead,
   * so the generated models have no corresponding export. Typed here to match
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

  /** How far back "Since" reaches, in seconds — 0 asks for everything
      available, the same "unset" convention `domain.LogOptions.SinceSeconds`
      documents on the Go side. */
  const SINCE_OPTIONS = [
    { seconds: 0, label: 'All' },
    { seconds: 300, label: '5m' },
    { seconds: 900, label: '15m' },
    { seconds: 3600, label: '1h' },
    { seconds: 21600, label: '6h' },
    { seconds: 86400, label: '24h' },
  ] as const

  const TIMESTAMP_MODE_OPTIONS: Array<{ value: TimestampMode; label: string }> = [
    { value: 'off', label: 'Off' },
    { value: 'local', label: 'Local' },
    { value: 'utc', label: 'UTC' },
    { value: 'relative', label: 'Relative' },
  ]

  const SEVERITIES: Severity[] = ['error', 'warn', 'info', 'debug']

  /** Tailwind classes for a severity chip in its ACTIVE (filtering) state —
      the same three gauge colours the rest of the application already uses
      for "bad/caution/fine", plus a neutral tone for debug, which is none of
      those. */
  const SEVERITY_ACTIVE_CLASS: Record<Severity, string> = {
    error: 'bg-gauge-critical/16 text-gauge-critical',
    warn: 'bg-gauge-warn/16 text-gauge-warn',
    info: 'bg-gauge-normal/16 text-gauge-normal',
    debug: 'bg-surface-container-high text-on-surface',
  }

  /** Tailwind text-colour classes for the 16 ANSI colours. Literal palette
      colours rather than the application's semantic tokens (primary, gauge-*)
      on purpose: an ANSI colour code is the PROCESS choosing red, not this
      application assigning a meaning to it, so it should read as an actual
      red rather than borrow a token that already means something else here
      (gauge-normal is blue, not green — see CLAUDE.md). */
  const ANSI_COLOR_CLASS: Record<AnsiColor, string> = {
    black: 'text-neutral-500',
    red: 'text-red-500',
    green: 'text-emerald-500',
    yellow: 'text-amber-500',
    blue: 'text-blue-500',
    magenta: 'text-fuchsia-500',
    cyan: 'text-cyan-500',
    white: 'text-neutral-300',
    'bright-black': 'text-neutral-400',
    'bright-red': 'text-red-400',
    'bright-green': 'text-emerald-400',
    'bright-yellow': 'text-amber-400',
    'bright-blue': 'text-blue-400',
    'bright-magenta': 'text-fuchsia-400',
    'bright-cyan': 'text-cyan-400',
    'bright-white': 'text-neutral-100',
  }

  function ansiSpanClass(span: AnsiSpan): string {
    const color = span.color ? ANSI_COLOR_CLASS[span.color] : ''
    return `${color} ${span.bold ? 'font-semibold' : ''}`
  }

  // Determine if we're in single pod or multi-pod mode
  const isMultiPod = $derived(pods.length > 0)
  const activePods = $derived(isMultiPod ? pods : podName ? [{ name: podName, containers }] : [])

  let selectedContainer = $state('')
  let follow = $state(true)
  let tailLines = $state(100)
  let searchQuery = $state('')

  /** Stream-shaping controls — changing any of these re-opens the stream,
      because each one changes what the request itself asks the API server
      for, unlike the display-only controls below. */
  let sinceSeconds = $state(0)
  let previousContainer = $state(false)

  /** Display-only: applied to lines already in the buffer, never touches the
      stream. */
  let timestampMode = $state<TimestampMode>('off')
  let structuredParsing = $state(true)

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
  const copied = flash(1500)
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

  /** Severity chips currently narrowing the view. Empty means "show every
      level" — a plain filter, not a special case, since an empty Set never
      matches anything in `.has()` either way. */
  let activeSeverities = $state<Set<Severity>>(new Set())

  /** Which fold groups (keyed by the header line's `seq`) are expanded. A
      group not in this set — the default for one just formed — is shown
      collapsed, matching "the affordance is there to expand it". */
  let expandedGroups = $state<Set<number>>(new Set())

  /**
   * Per-line parses, memoised by `seq` rather than recomputed.
   *
   * Both the timestamp split and the structured-line parse are pure
   * functions of `line` alone, so once a line has been parsed it never needs
   * parsing again — which is what keeps typing in the filter box cheap even
   * on a five-figure buffer: every derived value that depends on the parsed
   * form (search text, severity, the rendered chips) re-runs on each
   * keystroke, but every call in here after the first is a Map lookup, not a
   * regex or a JSON.parse. Plain Maps, not SvelteMap: nothing reads these
   * reactively on their own, only through the $derived values that call the
   * functions below, so making the maps themselves reactive would just cost
   * an invalidation nothing needs.
   */
  const timestampCache = new Map<number, { timestamp: Date | null; rest: string }>()
  const structuredCache = new Map<number, StructuredLine>()

  function timestampOf(log: { seq: number; line: string }): { timestamp: Date | null; rest: string } {
    let cached = timestampCache.get(log.seq)
    if (!cached) {
      cached = parseLogTimestamp(log.line)
      timestampCache.set(log.seq, cached)
    }
    return cached
  }

  /** Parses the MESSAGE (the Kubernetes timestamp already split off by
      `timestampOf`) — otherwise every line would start with an RFC 3339
      prefix and never look like JSON or logfmt at all. */
  function structuredOf(log: { seq: number; line: string }): StructuredLine {
    let cached = structuredCache.get(log.seq)
    if (!cached) {
      cached = parseStructuredLine(timestampOf(log).rest)
      structuredCache.set(log.seq, cached)
    }
    return cached
  }

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
      copied.show()
    } catch {
      copied.cancel()
    }
  }

  /**
   * Saves either the currently filtered view or the whole buffered stream —
   * an operator attaching a log to a ticket usually wants one or the other,
   * never a third option, so this is the two-item menu the Download control
   * offers rather than a dialog of its own.
   *
   * Goes through `SystemAPI.SaveTextFile` — the same native-save path the
   * table's CSV export already uses (`ClusterWorkspace.svelte`'s
   * `onExportCSV`) — rather than a second way of writing a file to disk.
   */
  async function downloadLogs(scope: 'filtered' | 'full'): Promise<void> {
    const source = scope === 'filtered' ? filteredLogs : logs
    const text = source
      .map((log) => (isMultiPod && log.podName ? `${log.podName}: ${log.line}` : log.line))
      .join('\n')
    if (!text) return

    const pod = isMultiPod ? `${activePods.length}-pods` : (activePods[0]?.name ?? 'pod')
    const container = selectedContainer || activePods[0]?.containers[0] || 'container'

    try {
      await saveTextFile(buildLogFilename(pod, container), text)
    } catch (error) {
      console.error('Failed to save logs:', error)
    }
  }

  /** The invalid-regex-aware query behind the filter box — `re:`/`/pattern/`
      parse exactly as they do in a table's search field (`$lib/query`),
      because it is the same grammar and an operator should not have to
      learn it twice. */
  const query = $derived(parseQuery(searchQuery))
  const queryActive = $derived(searchQuery !== '' && query.error === undefined)

  /**
   * Every line, tagged with whether it matches and the message text
   * (Kubernetes' own timestamp prefix already split off) everything
   * downstream — search, severity, structured parsing — reads instead of
   * the raw line.
   */
  const decorated = $derived.by(() => {
    return logs.map((log) => {
      const rest = timestampOf(log).rest
      return {
        log,
        text: rest,
        matches: queryActive && matches(query, { text: rest }),
      }
    })
  })

  /** Each line's detected severity, or `undefined` — computed once per line
      (through the memoised parse) regardless of how often this recomputes. */
  const rowSeverity = $derived.by(() => {
    const map = new Map<number, Severity | undefined>()
    for (const row of decorated) map.set(row.log.seq, detectSeverity(structuredOf(row.log)))
    return map
  })

  /** How many currently-decorated lines fall under each chip — shown on the
      chips themselves so "error 0" is visibly different from "error 12". */
  const severityCounts = $derived.by(() => {
    const counts: Record<Severity, number> = { error: 0, warn: 0, info: 0, debug: 0 }
    for (const severity of rowSeverity.values()) if (severity) counts[severity]++
    return counts
  })

  /** Narrowed to the active severity chips, or everything when none are
      active — a plain filter, applied before search so the search count
      reflects what severity has already excluded. */
  const severityFilteredRows = $derived(
    activeSeverities.size === 0
      ? decorated
      : decorated.filter((row) => {
          const severity = rowSeverity.get(row.log.seq)
          return severity !== undefined && activeSeverities.has(severity)
        }),
  )

  const matchingRows = $derived(queryActive ? severityFilteredRows.filter((row) => row.matches) : [])

  /** What is actually rendered, before stack-trace folding decides which of
      these are visually hidden. */
  const rows = $derived(filterMode && queryActive ? matchingRows : severityFilteredRows)

  /** Kept for the copy and download actions, which act on what is shown. */
  const filteredLogs = $derived(rows.map((row) => row.log))

  /**
   * Groups consecutive indented/`at `-prefixed lines under the line before
   * them — a stack trace read as one event rather than N independent ones.
   * See `logGroups.ts`.
   */
  const foldGroups = $derived(groupLogLines(rows))

  /** header seq -> how many lines it folds away, for the "+N more" affordance. */
  const foldCounts = $derived.by(() => {
    const map = new Map<number, number>()
    for (const group of foldGroups) if (group.members.length > 0) map.set(group.header.log.seq, group.members.length)
    return map
  })

  /**
   * Which lines are visually collapsed right now.
   *
   * FOLDING NEVER REMOVES A LINE FROM `rows` — it only decides whether this
   * render shows it, the same "the graph is always complete, folding is a
   * view decision" rule `graphFold.ts` follows for the dependency map. Copy
   * and Download read `filteredLogs`, not this, so collapsing a trace can
   * never make its content silently absent from what is copied.
   */
  const hiddenSeqs = $derived.by(() => {
    const hidden = new Set<number>()
    for (const group of foldGroups) {
      if (group.members.length > 0 && !expandedGroups.has(group.header.log.seq)) {
        for (const member of group.members) hidden.add(member.log.seq)
      }
    }
    return hidden
  })

  function toggleGroup(seq: number): void {
    const next = new Set(expandedGroups)
    if (next.has(seq)) next.delete(seq)
    else next.add(seq)
    expandedGroups = next
  }

  function toggleSeverity(severity: Severity): void {
    const next = new Set(activeSeverities)
    if (next.has(severity)) next.delete(severity)
    else next.add(severity)
    activeSeverities = next
  }

  /**
   * Where the query's own highlight comes from: the first non-negated
   * text/regex term. A label term (`key=value`) never applies to a log line
   * — logs carry no labels — so it is skipped rather than highlighted as
   * literal text nobody typed.
   */
  function highlightRuns(text: string, q: Query): Array<{ text: string; match: boolean }> {
    if (!queryActive) return [{ text, match: false }]
    const term = q.terms.find(
      (t) => !t.negated && ((t.kind === 'text' && t.value !== '') || (t.kind === 'regex' && t.regex !== null)),
    )
    if (!term) return [{ text, match: false }]
    if (term.kind === 'text') return splitOnMatches(text, term.value)
    if (term.kind === 'regex' && term.regex) return splitOnRegex(text, term.regex)
    return [{ text, match: false }]
  }

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
   * A folded stack-trace member is a row here exactly like any other — it is
   * given `hidden` (display:none) rather than removed from `rows`, so it
   * measures to 0px and the offset table already accounts for it correctly
   * with no separate code path. Expanding a group is then just a class
   * change on rows already in the array, not a change to the array's shape.
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
   * wrapped ones instead of oscillating. A row hidden by folding measures 0
   * here — no special case, `offsetHeight` already reports it.
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
        if (heights.get(seq) !== height) {
          heights.set(seq, height)
          changed = true
        }
        // The first real measurement replaces the guess for everything not
        // yet seen, which is most of a long log.
        if (estimatedRow !== height && height > 0 && heights.size === 1) estimatedRow = height
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
   * Drops heights (and the timestamp/structured-parse caches) for lines that
   * have been trimmed away.
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
      timestampCache.clear()
      structuredCache.clear()
      return
    }
    if (heights.size <= logs.length && timestampCache.size <= logs.length && structuredCache.size <= logs.length) {
      return
    }

    const oldest = logs[0].seq
    for (const seq of heights.keys()) {
      if (seq < oldest) heights.delete(seq)
    }
    for (const seq of timestampCache.keys()) {
      if (seq < oldest) timestampCache.delete(seq)
    }
    for (const seq of structuredCache.keys()) {
      if (seq < oldest) structuredCache.delete(seq)
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
   * Where the matching lines sit WITHIN `rows`.
   *
   * Positions rather than the rows themselves, because everything downstream
   * — the ruler, the jump, the current-match highlight — needs to turn a
   * match into a scroll offset, and the offset table is indexed by position.
   *
   * A match folded away inside a collapsed stack trace is excluded: it
   * cannot be seen at its current position (the row renders at 0 height),
   * so counting it as something to jump to would land the ruler and Enter on
   * a line that is not actually visible.
   */
  const matchPositions = $derived.by(() => {
    if (!queryActive) return []
    const out: number[] = []
    for (let i = 0; i < rows.length; i++) {
      if (rows[i].matches && !hiddenSeqs.has(rows[i].log.seq)) out.push(i)
    }
    return out
  })

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
    timestampCache.clear()
    structuredCache.clear()
    streamError = ''
    isStreaming = true

    // Start a stream for each pod
    for (const pod of activePods) {
      const container = selectedContainer || pod.containers[0] || ''
      if (!container) continue

      try {
        // timestamps (arg9) is always true — the backend keeps sending them
        // regardless of `timestampMode`, which only decides whether THIS
        // component displays what it already received. limitBytes (arg10) is
        // left at 0 (unset): domain.LogOptions.LimitBytes exists for an
        // operator to raise the API server's own cap, which this pane does
        // not yet expose a control for.
        const streamId = await StreamLogs(
          clusterId,
          namespace,
          pod.name,
          container,
          follow,
          tailLines,
          sinceSeconds,
          previousContainer,
          true,
          0,
        )
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
    timestampCache.clear()
    structuredCache.clear()
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
      subscribe<LogLinesEvent>('log:lines', handleLogLines),
      subscribe<LogEndEvent>('log:end', handleLogEnd),
    ]
    startStream()
  })

  onDestroy(() => {
    for (const off of unsubscribe) off()
    unsubscribe = []
    stopStream()
  })

  // Nothing left running behind a component that has gone away.
  $effect(() => () => copied.cancel())
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
        placeholder="Filter lines (try re:pattern)"
        label="Filter the log lines"
        count="{matchingRows.length}/{logs.length}"
        empty={matchingRows.length === 0}
        onchange={(value) => (searchQuery = value)}
        onnext={filterMode ? undefined : () => stepMatch(1)}
        onprevious={filterMode ? undefined : () => stepMatch(-1)}
        invalid={Boolean(query.error)}
        description={query.error}
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

      <Select
        compact
        label="Since"
        accessibleName="How far back to look"
        value={String(sinceSeconds)}
        options={SINCE_OPTIONS.map((option) => ({ value: String(option.seconds), label: option.label }))}
        onchange={(next) => {
          sinceSeconds = Number(next)
          restartStream()
        }}
      />

      <ToolbarToggle
        icon={RotateCcw}
        label="Previous container"
        pressed={previousContainer}
        title={previousContainer
          ? 'Showing the crashed/restarted container\'s previous run — click to show the current one'
          : "Showing the container's current run — click to show its previous run instead (kubectl logs -p)"}
        onclick={() => {
          previousContainer = !previousContainer
          restartStream()
        }}
      />

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

      <!-- Display-only from here down: none of these re-open the stream. -->
      <WrapLinesToggle />
      <ToolbarToggle
        icon={Braces}
        label="Parse structured lines"
        pressed={structuredParsing}
        title={structuredParsing
          ? 'Showing JSON/logfmt lines as key=value chips — click to show raw text'
          : 'Showing every line as raw text — click to parse JSON/logfmt lines'}
        onclick={() => (structuredParsing = !structuredParsing)}
      />
      <Select
        compact
        label="Timestamps"
        accessibleName="How to show each line's timestamp"
        value={timestampMode}
        options={TIMESTAMP_MODE_OPTIONS}
        onchange={(next) => (timestampMode = next as TimestampMode)}
      />
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
        icon={copied.on ? Check : Copy}
        label="Copy logs"
        title={copied.on ? 'Copied' : 'Copy the lines shown'}
        active={copied.on}
        disabled={filteredLogs.length === 0}
        onclick={copyLogs}
      />
      <!-- A one-shot menu, not a persistent setting — see Select's own doc
           comment on `placeholder`. `value` stays '', which is why every
           choice fires `onchange` even when picked twice in a row. -->
      <Select
        compact
        label="Download logs"
        accessibleName="Download logs"
        placeholder="Download"
        value=""
        disabled={filteredLogs.length === 0}
        options={[
          { value: 'filtered', label: 'Filtered view (what is shown)' },
          { value: 'full', label: 'Full stream (whole buffer)' },
        ]}
        onchange={(scope) => downloadLogs(scope as 'filtered' | 'full')}
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

  <!-- Severity chips: a coarse view of what the stream contains BY LEVEL,
       WHERE A LINE SAYS ONE. A structured line's chip is a quotation of its
       own level field; a plain line's is a heuristic — a level-shaped word
       in its first 64 characters (see logFormat.ts) — and the wording below
       says so rather than claiming every matched line was actually tagged
       at that severity by the process that wrote it. -->
  {#if logs.length > 0}
    <div class="flex flex-wrap items-center gap-1.5 border-b border-outline-variant bg-surface-container-low px-3 py-1.5">
      <span class="text-label-small text-on-surface-variant/70">By level, where a line says one:</span>
      {#each SEVERITIES as severity (severity)}
        <button
          type="button"
          onclick={() => toggleSeverity(severity)}
          aria-pressed={activeSeverities.has(severity)}
          title="{activeSeverities.has(severity) ? 'Showing only' : 'Show only'} {severity} lines"
          class="rounded-full px-2 py-0.5 text-label-small capitalize transition-colors
                 {activeSeverities.has(severity)
            ? SEVERITY_ACTIVE_CLASS[severity]
            : 'text-on-surface-variant/60 hover:bg-surface-container'}"
        >
          {severity} {severityCounts[severity]}
        </button>
      {/each}
      {#if activeSeverities.size > 0}
        <button
          type="button"
          class="text-label-small text-primary hover:underline"
          onclick={() => (activeSeverities = new Set())}
        >
          Clear
        </button>
      {/if}
    </div>
  {/if}

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
          {@const parsedTimestamp = timestampOf(log)}
          {@const structured = structuredParsing ? structuredOf(log) : null}
          {@const memberCount = foldCounts.get(log.seq)}
          {@const isHidden = hiddenSeqs.has(log.seq)}
          <div
            data-log-seq={log.seq}
            class="hover:bg-surface-container-low
                   {isHidden ? 'hidden' : ''}
                   {preferences.wrapLines ? 'break-all whitespace-pre-wrap' : 'whitespace-pre'}
                   {!filterMode && currentMatch >= 0 && matchPositions[currentMatch] === row.pos
              ? 'bg-gauge-warn/12'
              : ''}"
          >
            {#if isMultiPod && log.podName}
              <span class="text-primary">{log.podName}:</span>
            {/if}

            {#if timestampMode !== 'off'}
              <span class="mr-2 text-on-surface-variant/60 tabular-nums">
                {formatLogTimestamp(parsedTimestamp.timestamp, timestampMode)}
              </span>
            {/if}

            {#if structured && structured.kind !== 'plain'}
              <!-- Structured: level/message/timestamp/error promoted ahead
                   of the rest, as key=value chips — see logFormat.ts. -->
              <span class="inline">
                {#if structured.level}
                  <span
                    class="mr-1 rounded px-1 py-px text-label-small font-medium
                           {detectSeverity(structured) ? SEVERITY_ACTIVE_CLASS[detectSeverity(structured)!] : 'bg-surface-container text-on-surface-variant'}"
                    >{structured.level}</span
                  >
                {/if}
                {#if structured.message}
                  <span class="text-on-surface"
                    >{#each ansiToSpans(structured.message) as span, i (i)}<span class={ansiSpanClass(span)}
                        >{span.text}</span
                      >{/each}</span
                  >
                {/if}
                {#if structured.error}
                  <span class="ml-1 text-gauge-critical">{structured.error}</span>
                {/if}
                {#if structured.timestamp}
                  <span class="ml-1 text-on-surface-variant/60">{structured.timestamp}</span>
                {/if}
                {#each structured.fields as field (field.key)}
                  <span class="ml-1 rounded bg-surface-container px-1 py-px text-label-small text-on-surface-variant"
                    >{field.key}={field.value}</span
                  >
                {/each}
              </span>
            {:else if row.text.includes('\x1b')}
              <!-- ANSI colour codes, stripped of everything else — see
                   ansi.ts. Mutually exclusive with search highlighting on
                   the same line: a coloured tool's own output is already
                   doing the work a highlight would, and combining the two
                   would mean splitting one run of text by two independent
                   partitions at once. -->
              <span class="text-on-surface"
                >{#each ansiToSpans(row.text) as span, i (i)}<span class={ansiSpanClass(span)}
                    >{span.text}</span
                  >{/each}</span
              >
            {:else}
              <!-- The filter has already decided WHICH lines are here; the
                   highlight says WHERE in each one, which is the part a
                   filtered view otherwise leaves you hunting for on a
                   400-character line. Same amber as the manifest's matches. -->
              <span class="text-on-surface"
                >{#each highlightRuns(row.text, query) as run, i (i)}{#if run.match}<mark
                      class="rounded-xs bg-gauge-warn/30 text-on-surface"
                      >{run.text}</mark
                    >{:else}{run.text}{/if}{/each}</span
              >
            {/if}

            {#if memberCount}
              <button
                type="button"
                class="ml-2 inline-flex items-center gap-0.5 rounded border border-outline-variant/50
                       px-1.5 py-px align-middle text-label-small text-on-surface-variant
                       hover:bg-surface-container hover:text-on-surface"
                onclick={() => toggleGroup(log.seq)}
              >
                <ChevronDown
                  class="size-3 transition-transform {expandedGroups.has(log.seq) ? '' : '-rotate-90'}"
                  strokeWidth={2}
                />
                {expandedGroups.has(log.seq) ? 'Collapse' : `${memberCount} more`}
              </button>
            {/if}
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
