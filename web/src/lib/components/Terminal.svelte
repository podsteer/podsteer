<!--
  An interactive shell in a container, with the pane furniture every other
  text surface here has.

  A persistent bidirectional exec with a TTY, so full-screen programs work:
  the Go side holds the SPDY stream, keystrokes go out through TerminalAPI,
  and output arrives as `terminal:data` events. xterm.js does the VT emulation.

  THREE THINGS SEPARATE IT FROM A BARE XTERM, and each is here because a
  terminal embedded in an application is not the same object as a terminal in
  its own window:

  - It wears the application's colours, read from the CSS custom properties and
    re-read when the theme changes. See terminalTheme.ts.
  - It has the same toolbar as Logs and YAML — search, copy, maximise — so a
    person who has learned one pane has learned this one.
  - SELECTING COPIES. Double-click a word, triple-click a line, or drag: the
    text is on the clipboard and a small chip says so. That is iTerm's
    copy-on-select, and in a pane where ⌘C may be wanted by the program running
    inside it, selection is the unambiguous gesture.
-->
<script lang="ts">
  import { flash } from '$lib/flash.svelte'
  import { onMount, onDestroy } from 'svelte'
  import { Terminal } from '@xterm/xterm'
  import { FitAddon } from '@xterm/addon-fit'
  import { SearchAddon } from '@xterm/addon-search'
  import { SerializeAddon } from '@xterm/addon-serialize'
  import { WebLinksAddon } from '@xterm/addon-web-links'
  import { Copy, Check, Maximize2, RotateCw, ChevronUp, ChevronDown, Bug } from '@lucide/svelte'
  import {
    StartSession,
    StartAttachSession,
    StartDebugSession,
    StartNodeShellSession,
    StartLocalSession,
    StartAgentSession,
    Write,
    Resize,
    StopSession,
  } from '$lib/wailsjs/go/wails/TerminalAPI'
  import { EventsOn } from '$lib/wailsjs/runtime/runtime'
  import { terminalTheme, onThemeChange } from '$lib/terminalTheme'
  import { matchFractions } from '$lib/terminalSearch'
  import { terminalSessions, sessionKey, localSessionKey } from '$stores/terminalSessions.svelte'
  import { localShellNotice } from '$lib/localShell'
  import PaneToolbar from './PaneToolbar.svelte'
  import ToolbarButton from './ToolbarButton.svelte'
  import ToolbarSearch from './ToolbarSearch.svelte'
  import '@xterm/xterm/css/xterm.css'

  /**
   * A container option for the selector, carrying just enough of the DTO
   * (`app/adapters/wails/dto.go`'s Container) to decide whether Attach makes
   * sense — see the mode select below.
   */
  interface ContainerOption {
    name: string
    /** Quotes the container's own spec.tty && spec.stdin. See mode below. */
    tty: boolean
  }

  /** A session's mode: a new process (Shell) or the container's own (Attach). */
  type SessionMode = 'shell' | 'attach'

  interface Props {
    clusterId: string
    namespace: string
    podName: string
    containerName: string
    /** Every container in the pod, for the selector. */
    containers?: ContainerOption[]
    /**
     * True when this cluster is marked read-only in PodSteer.
     *
     * An interactive shell can mutate the cluster as freely as any other
     * write — see CLAUDE.md's read-only section — so this refuses to open
     * one at all rather than opening a session that would fail on its first
     * keystroke. The backend refuses the same way (ExecInPodWithTTY and
     * AttachToPod), which is the actual guard; this is what keeps the pane
     * from starting a connection it already knows will be refused.
     */
    readOnly?: boolean
    /**
     * What kind of session this pane opens.
     *
     * 'container' is the ordinary pod terminal (Shell / Attach). 'debug' adds
     * an ephemeral debug container to the pod and opens a shell into it;
     * 'nodeshell' creates a privileged pod on a node and attaches to a host
     * shell. 'local' is the odd one out and reaches no cluster at all: it runs
     * the operator's own login shell, or a coding agent they already have, on
     * THIS machine. All three special variants reuse everything else about
     * this component — the buffer, the toolbar, search, copy-on-select — and
     * differ only in how the session is started.
     */
    variant?: 'container' | 'debug' | 'nodeshell' | 'local'
    /** debug: the container whose process namespace to share (--target). */
    debugTarget?: string
    /** debug: the image to run. */
    debugImage?: string
    /** debug: the command, already split; empty means the image's default. */
    debugCommand?: string[]
    /** nodeshell: the node to run on. */
    nodeName?: string
    /** nodeshell: the namespace the pod is created in. */
    nodeShellNamespace?: string
    /** nodeshell: the image the pod runs. */
    nodeShellImage?: string
    /** local: the coding agent to run, or null for the operator's login shell. */
    agent?: string | null
    /** local: whether the agent was asked to keep to read-only kubectl. */
    agentReadOnly?: boolean
    /** local: the object open in the drawer, named in the agent's first prompt. */
    subject?: { kind: string; namespace: string; name: string }
    /**
     * Offered on the container terminal's toolbar, beside Shell/Attach, to
     * start a debug container — the drawer wires it to the debug dialog. Absent
     * on the debug and node-shell variants, which have nothing to debug into.
     */
    ondebug?: () => void
    /** Called after a session is successfully started (not on a re-attach) —
     * the node-shell overlay uses it to refresh the activity list. */
    onstarted?: () => void
    /** Offered when the pane can still be made bigger. */
    onmaximize?: () => void
  }

  let {
    clusterId,
    namespace,
    podName,
    containerName,
    containers = [],
    readOnly = false,
    variant = 'container',
    debugTarget = '',
    debugImage = '',
    debugCommand = [],
    nodeName = '',
    nodeShellNamespace = '',
    nodeShellImage = '',
    agent = null,
    agentReadOnly = false,
    subject = { kind: '', namespace: '', name: '' },
    ondebug,
    onstarted,
    onmaximize,
  }: Props = $props()

  const READ_ONLY_REASON =
    'This cluster is marked read-only in PodSteer. Change that under Organise.'

  const ATTACH_NOTE =
    "Attached to the container's main process. Ctrl+P, Ctrl+Q would detach in Docker; " +
    'here, closing the pane detaches; Ctrl+C is sent to the process.'

  const DEBUG_NOTE =
    'This ephemeral debug container cannot be removed once added — Kubernetes keeps it in ' +
    "the pod's spec until the pod is deleted. Closing this pane ends the shell, not the container."

  const NODE_SHELL_NOTE =
    'You are root on the node, in its host namespaces. This pod is deleted when you close ' +
    'this pane; it also self-destructs after one hour as a backstop.'

  /**
   * The local pane's own note, which has to say the read-only guard does not
   * apply here — see localShellNotice for why saying it matters.
   */
  const localNote = $derived(localShellNotice(clusterId))

  // Seeded by the effect below; reading the prop here would capture only its
  // initial value and leave the selector stale after switching pods.
  let activeContainer = $state('')
  /**
   * Shell starts a new process in the container; Attach connects to its own
   * running one (PID 1) instead — the only way to interact with a process
   * that reads stdin, and to see its live stdout without a separate log
   * stream. Offered only when the active container's own spec declares both
   * tty and stdin (see attachAvailable below) — the same fields
   * AttachToPod refuses on server-side, checked here first so the control
   * simply is not there for a container it would refuse.
   */
  let mode = $state<SessionMode>('shell')

  $effect(() => {
    activeContainer = containerName
  })

  const attachAvailable = $derived(containers.find((c) => c.name === activeContainer)?.tty ?? false)

  let terminalContainer: HTMLDivElement
  let terminal: Terminal | null = null
  let fitAddon: FitAddon | null = null
  let searchAddon: SearchAddon | null = null
  let serializeAddon: SerializeAddon | null = null
  let sessionId = $state<string | null>(null)
  let connectionState = $state<'connecting' | 'connected' | 'disconnected' | 'error'>('connecting')

  let query = $state('')
  /** Where the matches fall through the whole buffer, as fractions of it. */
  let markers = $state.raw<number[]>([])
  /** What the search found, as the addon reports it. */
  let results = $state<{ index: number; count: number } | null>(null)
  const copiedAll = flash(1400)
  /** The chip shown beside a selection, and where to put it. */
  let selection = $state<{ text: string; x: number; y: number } | null>(null)

  let stopWatchingTheme: (() => void) | null = null
  let clearChip: number | null = null
  let rescan: number | null = null
  /**
   * Set while the SEARCH is moving the selection rather than the operator.
   *
   * Copy-on-select must not fire for a match the addon selected: stepping
   * through a search would then overwrite the clipboard with each hit, which
   * is the opposite of a search's purpose — somebody looking for a line has
   * not asked to copy every line on the way to it.
   */
  let searching = false

  /**
   * Unsubscribes for the two Wails events, held so they can be undone.
   *
   * Subscribing belongs to the COMPONENT, not to the session. It used to live
   * inside startSession, which is also called by reconnect and by switching
   * container — and Wails appends listeners without deduplicating, so every
   * container switch added another handler and every byte from the pod was
   * written to the terminal one more time than before.
   *
   * The returned unsubscribe is used rather than EventsOff(name), because
   * EventsOff removes every listener for that name across the whole
   * application. Harmless while only one terminal is mounted, and a trap the
   * moment a second one is.
   */
  let unsubscribe: Array<() => void> = []

  onMount(() => {
    initTerminal()

    unsubscribe = [
      EventsOn('terminal:data', handleTerminalData),
      EventsOn('terminal:exit', handleTerminalExit),
    ]

    if (readOnly && variant !== 'local') {
      // No PTY, no goroutine, no exec call at all — the same fast-fail
      // TerminalAPI.StartSession makes server-side, taken before ever
      // reaching it. See the readOnly prop's own doc comment.
      //
      // The local variant is EXEMPT, deliberately. That guard is about
      // PodSteer's writes to a cluster; this pane starts a process on the
      // operator's own machine with their own credentials, which is not
      // something this application can or should police. The backend makes no
      // such check either — see TerminalAPI.StartLocalSession — and the note
      // above the buffer says so.
      connectionState = 'disconnected'
      terminal?.writeln(`\x1b[33m${READ_ONLY_REASON}\x1b[0m`)
    } else {
      startSession()
    }

    const resizeObserver = new ResizeObserver(() => fitAddon?.fit())
    resizeObserver.observe(terminalContainer)

    // The palette is read at construction, so a theme change has to reach in
    // and set it again — xterm has no notion of a stylesheet.
    stopWatchingTheme = onThemeChange(() => {
      if (terminal) terminal.options.theme = terminalTheme()
    })

    return () => resizeObserver.disconnect()
  })

  onDestroy(() => {
    stopWatchingTheme?.()
    if (clearChip !== null) window.clearTimeout(clearChip)
    if (rescan !== null) window.clearTimeout(rescan)
    cleanup()
  })

  function initTerminal() {
    terminal = new Terminal({
      cursorBlink: true,
      cursorStyle: 'block',
      fontSize: 13,
      fontFamily:
        '"JetBrains Mono", "Fira Code", "Cascadia Code", Monaco, Menlo, "Ubuntu Mono", monospace',
      fontWeight: '400',
      fontWeightBold: '700',
      lineHeight: 1.2,
      scrollback: 10000,
      allowProposedApi: true,
      theme: terminalTheme(),
    })

    fitAddon = new FitAddon()
    searchAddon = new SearchAddon()
    serializeAddon = new SerializeAddon()
    terminal.loadAddon(fitAddon)
    terminal.loadAddon(searchAddon)
    terminal.loadAddon(serializeAddon)
    terminal.loadAddon(new WebLinksAddon())

    terminal.open(terminalContainer)
    fitAddon.fit()

    terminal.onData((data) => {
      if (sessionId) void Write(sessionId, data).catch(() => {})
    })

    terminal.onResize(({ cols, rows }) => {
      if (sessionId) void Resize(sessionId, cols, rows).catch(() => {})
    })

    terminal.onSelectionChange(() => showSelection())

    // The addon knows how many matches there are and which is current; the
    // ruler above knows where they are. Both are needed and neither has the
    // other's half.
    searchAddon.onDidChangeResults((event) => {
      results = event.resultCount > 0 ? { index: event.resultIndex + 1, count: event.resultCount } : null
    })
  }

  /**
   * Copies whatever was just selected, and marks where to show the chip.
   *
   * ONE HANDLER FOR EVERY GESTURE. Double-click gives a word, triple-click a
   * line, drag gives a range — xterm reports all three as a selection change,
   * so there is nothing to special-case and no way for one of them to behave
   * differently from the others.
   *
   * A cleared selection clears the chip rather than leaving it pointing at
   * nothing.
   */
  function showSelection(): void {
    const text = terminal?.getSelection() ?? ''
    if (text.trim() === '' || searching) {
      selection = null
      return
    }

    void navigator.clipboard.writeText(text).catch(() => {})

    // Positioned from the live DOM selection rather than from xterm's row and
    // column, which would need converting through the font metrics to get back
    // to where the glyphs actually are.
    // rangeCount FIRST: getRangeAt(0) THROWS when there is no DOM selection,
    // and copyAll() calls terminal.selectAll() programmatically, which fires
    // this handler with xterm holding a selection and the DOM holding none.
    // LogViewer guards the same API correctly; this one did not.
    const live = window.getSelection()
    const box = live && live.rangeCount > 0 ? live.getRangeAt(0).getBoundingClientRect() : null
    const pane = terminalContainer.getBoundingClientRect()
    if (!box || box.width === 0) {
      selection = null
      return
    }

    selection = {
      text,
      x: Math.min(Math.max(box.right - pane.left, 8), pane.width - 8),
      y: Math.max(box.top - pane.top - 6, 4),
    }

    if (clearChip !== null) window.clearTimeout(clearChip)
    clearChip = window.setTimeout(() => (selection = null), 1800)
  }

  /** Copies the whole scrollback, for when the selection is the wrong tool. */
  async function copyAll(): Promise<void> {
    if (!terminal) return

    const previous = terminal.getSelection()
    terminal.selectAll()
    const all = terminal.getSelection()
    terminal.clearSelection()

    await navigator.clipboard.writeText(all).catch(() => {})
    copiedAll.show()

    // Selection is a visible thing in a terminal; putting it back is politer
    // than leaving the whole buffer highlighted.
    if (previous) selection = null
  }

  function search(direction: 'next' | 'previous'): void {
    if (!searchAddon || query === '') return

    const options = { caseSensitive: false, regex: false }
    searching = true
    try {
      if (direction === 'next') searchAddon.findNext(query, options)
      else searchAddon.findPrevious(query, options)
    } finally {
      // Synchronous: xterm raises the selection change inside the find call,
      // so the flag is down again before anything the operator does next.
      searching = false
    }
  }

  /**
   * Where every match falls through the whole buffer, as a fraction of it.
   *
   * SCANNED HERE RATHER THAN ASKED OF THE ADDON, which reports how many
   * matches there are and which one is current but not where they sit. The
   * ruler needs positions, and the buffer is the only thing that has them.
   *
   * Over the WHOLE buffer, scrollback included, because the point of the ruler
   * is to show what is off screen — marking only the visible rows would draw a
   * track that agrees with what somebody can already see.
   */
  function scanMarkers(): void {
    if (!terminal || query === '') {
      markers = []
      return
    }

    const buffer = terminal.buffer.active
    markers = matchFractions(
      {
        length: buffer.length,
        lineAt: (row) => buffer.getLine(row)?.translateToString(true),
      },
      query,
    )
  }

  /**
   * Puts the terminal back as it was when the query goes away.
   *
   * CLEARING THE BOX HAS TO CLEAR THE HIGHLIGHT. The last match stays selected
   * otherwise — and in a terminal a selection is not decoration, it is the
   * clipboard: copy-on-select means an abandoned search would leave somebody's
   * clipboard holding a word they had stopped looking for.
   */
  function clearSearch(): void {
    markers = []
    results = null
    searchAddon?.clearDecorations()
    terminal?.clearSelection()
    selection = null
  }

  /**
   * Attaches to the session for this target, opening one only if none is live.
   *
   * THE RE-ATTACH IS THE POINT. Maximising the pane destroys this component
   * and builds another; without this, that would be a new `kubectl exec` —
   * a fresh shell in the default directory, with the previous one abandoned
   * mid-command. The Go side keys its sessions in a map and does not mind who
   * is talking to it, so the new component picks up where the old one left off
   * and replays the screen it saved.
   */
  async function startSession() {
    if (!terminal || !fitAddon) return

    const held = terminalSessions.take(currentKey())
    if (held) {
      sessionId = held.id
      connectionState = 'connected'
      if (held.buffer) terminal.write(held.buffer)
      // The new pane is a different size from the old one, so tell the far
      // end before anything is drawn into it.
      void Resize(held.id, terminal.cols, terminal.rows).catch(() => {})
      terminal.focus()
      return
    }

    connectionState = 'connecting'

    try {
      const id = await startForVariant(terminal.cols, terminal.rows)
      sessionId = id
      connectionState = 'connected'
      terminal.focus()
      // A newly created node-shell pod (or debug container) now exists on the
      // cluster; let whoever is listing them know so it appears at once rather
      // than on the next poll.
      onstarted?.()
    } catch (err) {
      connectionState = 'error'
      terminal.writeln(`\x1b[31mFailed to start ${sessionLabel()} session: ${err}\x1b[0m`)
    }
  }

  /**
   * Starts the right kind of session for this pane's variant, returning its id.
   *
   * The debug and node-shell starts do more work server-side than a plain
   * exec — adding a container and waiting for it, creating a pod and waiting
   * for it — so this call can take a few seconds, which is why the pane shows
   * "Connecting…" until it returns.
   */
  function startForVariant(cols: number, rows: number): Promise<string> {
    switch (variant) {
      case 'debug':
        return StartDebugSession(clusterId, namespace, podName, debugTarget, debugImage, debugCommand, cols, rows)
      case 'nodeshell':
        return StartNodeShellSession(clusterId, nodeShellNamespace, nodeName, nodeShellImage, cols, rows)
      case 'local':
        // Two calls rather than one with a nullable argument: an agent session
        // carries a prompt and a read-only request a plain shell has no notion
        // of, and the backend refuses an empty agent id outright.
        return agent === null
          ? StartLocalSession(clusterId, cols, rows)
          : StartAgentSession(
              clusterId,
              agent,
              subject.kind,
              subject.namespace,
              subject.name,
              agentReadOnly,
              cols,
              rows,
            )
      default:
        return mode === 'attach'
          ? StartAttachSession(clusterId, namespace, podName, activeContainer, cols, rows)
          : StartSession(clusterId, namespace, podName, activeContainer, cols, rows)
    }
  }

  /** Names this pane's session, for the failure message. */
  function sessionLabel(): string {
    if (variant === 'debug') return 'debug'
    if (variant === 'nodeshell') return 'node shell'
    if (variant === 'local') return agent === null ? 'local shell' : agent
    return mode === 'attach' ? 'attach' : 'terminal'
  }

  function handleTerminalData(event: { sessionId: string; data: string }): void {
    if (!terminal || event.sessionId !== sessionId) return
    terminal.write(event.data)

    // The ruler describes the buffer, and the buffer is still moving. Rescanned
    // on a trailing delay rather than per write: a build log arrives in
    // thousands of small chunks, and scanning the scrollback for each one would
    // cost more than the search does.
    if (query === '') return
    if (rescan !== null) window.clearTimeout(rescan)
    rescan = window.setTimeout(scanMarkers, 250)
  }

  function handleTerminalExit(event: { sessionId: string; reason?: string }): void {
    if (event.sessionId !== sessionId) return

    connectionState = 'disconnected'
    terminalSessions.forget(currentKey())
    if (!terminal) return
    terminal.writeln('')
    terminal.writeln('\x1b[33mSession ended.\x1b[0m')
    if (event.reason) terminal.writeln(`\x1b[31mReason: ${event.reason}\x1b[0m`)
    terminal.writeln('\x1b[90mUse Reconnect to start a new session.\x1b[0m')
  }

  async function reconnect(): Promise<void> {
    // Exempt for the same reason the first start is: a local shell is not
    // governed by the cluster's read-only setting.
    if (readOnly && variant !== 'local') return
    terminalSessions.forget(currentKey())
    if (sessionId) {
      await StopSession(sessionId).catch(() => {})
      sessionId = null
    }
    terminal?.clear()
    terminal?.reset()
    await startSession()
  }

  async function switchContainer(next: string): Promise<void> {
    activeContainer = next
    // The new container may not support Attach even if the old one did —
    // falling back here means reconnect() below asks StartSession, never a
    // StartAttachSession the backend would refuse.
    if (!(containers.find((c) => c.name === next)?.tty ?? false)) mode = 'shell'
    await reconnect()
  }

  async function switchMode(next: SessionMode): Promise<void> {
    mode = next
    await reconnect()
  }

  /**
   * The screen as it stands, with its colours, for restoring after a remount.
   *
   * `scrollback: 0` deliberately: this redraws what was ON SCREEN, and
   * replaying ten thousand retained lines into a fresh terminal is slow and
   * scrolls the prompt out of sight. It is a picture of the screen, not of the
   * session — the shell itself never went anywhere.
   */
  function captureScreen(): string {
    return serializeAddon?.serialize({ scrollback: 0 }) ?? ''
  }

  /** The key this component's session is filed under. */
  function currentKey(): string {
    // Each variant keys on what actually identifies its session: a container
    // terminal on its container and mode, a debug shell on the pod, a node
    // shell on the node. Distinct keys keep the maximise/remount handover from
    // ever re-attaching one to another.
    if (variant === 'debug') return sessionKey(clusterId, namespace, podName, '', 'debug')
    if (variant === 'nodeshell') return sessionKey(clusterId, nodeShellNamespace, nodeName, '', 'nodeshell')
    if (variant === 'local') return localSessionKey(clusterId, agent)
    return sessionKey(clusterId, namespace, podName, activeContainer, mode)
  }

  /**
   * Offers the session up rather than killing it outright.
   *
   * EVERY unmount offers, because this component cannot tell why it is being
   * destroyed — maximising hands the shell to a dialog, closing that dialog
   * hands it back, switching tabs abandons it, closing the drawer abandons it,
   * and all four look identical from in here. The store reaps anything nobody
   * claims within a few seconds, so a remount keeps the shell and a genuine
   * departure ends it, without either side having to know which happened.
   */
  function cleanup(): void {
    for (const off of unsubscribe) off()
    unsubscribe = []

    if (sessionId) {
      terminalSessions.offer(
        currentKey(),
        sessionId,
        captureScreen(),
        (id) => void StopSession(id).catch(() => {}),
      )
      sessionId = null
    }

    terminal?.dispose()
    terminal = null
  }

  // Nothing left running behind a component that has gone away.
  $effect(() => () => copiedAll.cancel())
</script>

<div class="flex h-full flex-col">
  <PaneToolbar>
    <!--
      The session's own state first, because everything else is meaningless
      while it is not connected.
    -->
    <span class="flex shrink-0 items-center gap-1.5 pl-1 text-body-small">
      {#if connectionState === 'connecting'}
        <span class="size-2 animate-pulse rounded-full bg-gauge-warn"></span>
        <span class="text-on-surface-variant">Connecting…</span>
      {:else if connectionState === 'connected'}
        <span class="size-2 rounded-full bg-gauge-normal"></span>
        <span class="text-on-surface-variant">Connected</span>
      {:else if connectionState === 'disconnected'}
        <span class="size-2 rounded-full bg-on-surface-variant/50"></span>
        <span class="text-on-surface-variant">Disconnected</span>
      {:else}
        <span class="size-2 rounded-full bg-gauge-critical"></span>
        <span class="text-error">Error</span>
      {/if}
    </span>

    {#if variant === 'container'}
      {#if containers.length > 1}
        <div class="mx-1 h-5 w-px shrink-0 bg-outline-variant/60" aria-hidden="true"></div>
        <select
          value={activeContainer}
          onchange={(event) => void switchContainer(event.currentTarget.value)}
          disabled={readOnly}
          aria-label="Container"
          title={readOnly ? READ_ONLY_REASON : undefined}
          class="field h-7 shrink-0 px-1.5 text-body-small disabled:opacity-38"
        >
          {#each containers as container (container.name)}
            <option value={container.name}>{container.name}</option>
          {/each}
        </select>
      {:else}
        <span class="shrink-0 truncate pl-1 text-body-small text-on-surface-variant">
          {activeContainer}
        </span>
      {/if}

      {#if attachAvailable}
        <!--
          Offered only for a container whose own spec declares both tty and
          stdin — the same fields AttachToPod refuses on without, checked here
          first so the control is simply absent rather than present and
          failing.
        -->
        <div class="mx-1 h-5 w-px shrink-0 bg-outline-variant/60" aria-hidden="true"></div>
        <select
          value={mode}
          onchange={(event) => void switchMode(event.currentTarget.value as SessionMode)}
          disabled={readOnly}
          aria-label="Session mode"
          title={readOnly
            ? READ_ONLY_REASON
            : 'Shell starts a new process in the container; Attach connects to its own running one'}
          class="field h-7 shrink-0 px-1.5 text-body-small disabled:opacity-38"
        >
          <option value="shell">Shell</option>
          <option value="attach">Attach</option>
        </select>
      {/if}

      {#if ondebug}
        <!--
          Debug sits beside Shell/Attach because it is the third way into this
          pod: a `kubectl debug` ephemeral container with its own tools. It
          opens its own dialog (image, target) rather than switching this
          session, because it changes the pod rather than reconnecting.
        -->
        <div class="mx-1 h-5 w-px shrink-0 bg-outline-variant/60" aria-hidden="true"></div>
        <ToolbarButton
          icon={Bug}
          label="Debug with an ephemeral container"
          title={readOnly ? READ_ONLY_REASON : 'Add an ephemeral debug container (kubectl debug)'}
          disabled={readOnly}
          onclick={() => ondebug?.()}
        />
      {/if}
    {:else}
      <span class="shrink-0 truncate pl-1 text-body-small text-on-surface-variant">
        {#if variant === 'debug'}
          Debug container
        {:else if variant === 'local'}
          {agent === null ? 'Your shell' : agent} · {clusterId || 'no cluster'}
        {:else}
          Node shell · {nodeName}
        {/if}
      </span>
    {/if}

    {#snippet trailing()}
      <ToolbarSearch
        value={query}
        placeholder="Find in terminal…"
        label="Find in the terminal"
        count={results ? `${results.index}/${results.count}` : ''}
        empty={query !== '' && results === null}
        onchange={(value) => {
          query = value
          if (value === '') {
            clearSearch()
            return
          }
          search('next')
          scanMarkers()
        }}
        onnext={() => search('next')}
        onprevious={() => search('previous')}
      />

      <!--
        Stepping controls are separate from the field because a terminal's
        buffer keeps moving underneath a search: finding again is a thing
        somebody does repeatedly, not once per query.
      -->
      <ToolbarButton
        icon={ChevronUp}
        label="Previous match"
        title="Previous match  ⇧⏎"
        disabled={query === ''}
        onclick={() => search('previous')}
      />
      <ToolbarButton
        icon={ChevronDown}
        label="Next match"
        title="Next match  ⏎"
        disabled={query === ''}
        onclick={() => search('next')}
      />

      <div class="mx-0.5 h-5 w-px shrink-0 bg-outline-variant/60" aria-hidden="true"></div>

      <ToolbarButton
        icon={copiedAll.on ? Check : Copy}
        label="Copy everything"
        title="Copy the whole buffer — selecting copies on its own"
        active={copiedAll.on}
        onclick={() => void copyAll()}
      />
      <ToolbarButton
        icon={RotateCw}
        label="Reconnect"
        title={readOnly && variant !== 'local'
          ? READ_ONLY_REASON
          : connectionState === 'connected'
            ? 'Reconnect this session'
            : 'Start a new session'}
        disabled={readOnly && variant !== 'local'}
        onclick={() => void reconnect()}
      />
      {#if onmaximize}
        <ToolbarButton
          icon={Maximize2}
          label="Maximise"
          title="Maximise — the session keeps running"
          onclick={onmaximize}
        />
      {/if}
    {/snippet}
  </PaneToolbar>

  {#if variant === 'container' && mode === 'attach'}
    <!--
      A banner, not something written into the buffer: the buffer is the
      attached process's own terminal, and this is PodSteer's own note about
      it, not a line that process printed.
    -->
    <p
      class="shrink-0 border-b border-outline-variant/60 bg-surface-container-low px-3 py-1
             text-body-small text-on-surface-variant"
    >
      {ATTACH_NOTE}
    </p>
  {:else if variant === 'debug'}
    <p
      class="shrink-0 border-b border-outline-variant/60 bg-surface-container-low px-3 py-1
             text-body-small text-on-surface-variant"
    >
      {DEBUG_NOTE}
    </p>
  {:else if variant === 'nodeshell'}
    <p
      class="shrink-0 border-b border-gauge-warn/40 bg-gauge-warn/10 px-3 py-1
             text-body-small text-on-surface-variant"
    >
      {NODE_SHELL_NOTE}
    </p>
  {:else if variant === 'local'}
    <!--
      A banner rather than a line in the buffer, like the attach note: the
      buffer belongs to the operator's shell, and this is PodSteer's own
      statement about the pane. The one-line notice naming the context IS
      written into the buffer, by the Go side, before the shell starts — that
      one is about the session, this one is about the surface.
    -->
    <p
      class="shrink-0 border-b border-outline-variant/60 bg-surface-container-low px-3 py-1
             text-body-small text-on-surface-variant"
    >
      {localNote}
    </p>
  {/if}

  <!--
    `bg-surface-container-lowest` matches what terminalTheme hands xterm, so the
    padding around the canvas is the same colour as the canvas rather than a
    frame around it.
  -->
  <div class="relative min-h-0 flex-1 bg-surface-container-lowest">
    <div bind:this={terminalContainer} class="h-full overflow-hidden p-1"></div>

    <!-- Where the matches fall through the whole buffer. The same track the
         log viewer draws, in the same place and the same colour, because a
         person who has learned one pane should not have to learn this one. -->
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

    {#if selection}
      <!--
        Confirmation, not a button. The text is already on the clipboard by the
        time this appears — offering a control to do what has been done would
        invite somebody to select, wait, and click nothing.
      -->
      <div
        class="pointer-events-none absolute z-10 flex -translate-x-1 -translate-y-full items-center
               gap-1 rounded-full border border-outline-variant/60 bg-surface-container-high px-2
               py-0.5 text-label-small text-on-surface shadow-sm"
        style="left: {selection.x}px; top: {selection.y}px"
      >
        <Check class="size-3 text-gauge-normal" strokeWidth={2.5} />
        Copied
      </div>
    {/if}
  </div>
</div>

<style>
  :global(.xterm) {
    height: 100%;
    padding: 4px;
  }

  :global(.xterm-viewport) {
    overflow-y: auto !important;
    /* The viewport paints its own background over the theme's, so it has to be
       told to keep out of the way. */
    background-color: transparent !important;
  }

  :global(.xterm-screen) {
    height: 100%;
  }
</style>
