<!--
  Enterprise-grade interactive terminal for executing commands in pod containers.

  This implementation uses a persistent bidirectional stream with TTY allocation,
  supporting full-featured interactive programs like top, htop, vim, less, and
  interactive shells with tab completion and history.

  Architecture:
  - Backend maintains a persistent exec session with TTY via SPDY
  - Keystrokes are sent to the backend via TerminalAPI.Write()
  - Stdout/stderr comes back via "terminal:data" Wails events
  - Window resize is forwarded via TerminalAPI.Resize()
  - xterm.js handles full VT100/VT220 terminal emulation
-->
<script lang="ts">
  import { onMount, onDestroy } from 'svelte'
  import { Terminal } from '@xterm/xterm'
  import { FitAddon } from '@xterm/addon-fit'
  import { WebLinksAddon } from '@xterm/addon-web-links'
  import { StartSession, Write, Resize, StopSession } from '$lib/wailsjs/go/wails/TerminalAPI'
  import { EventsOn, EventsOff } from '$lib/wailsjs/runtime/runtime'
  import '@xterm/xterm/css/xterm.css'

  interface Props {
    clusterId: string
    namespace: string
    podName: string
    containerName: string
    /** All available containers in the pod for the container selector. */
    containers?: string[]
  }

  let { clusterId, namespace, podName, containerName, containers = [] }: Props = $props()

  // Seeded by the effect below; reading the prop here would capture only its
  // initial value and leave the selector stale after switching pods.
  let activeContainer = $state('')

  // Sync activeContainer when the containerName prop changes (e.g., switching pods)
  $effect(() => {
    activeContainer = containerName
  })

  let terminalContainer: HTMLDivElement
  let terminal: Terminal | null = null
  let fitAddon: FitAddon | null = null
  let sessionId = $state<string | null>(null)
  let connectionState = $state<'connecting' | 'connected' | 'disconnected' | 'error'>('connecting')
  let errorMessage = $state<string>('')

  onMount(() => {
    initTerminal()
    startSession()

    // Handle window resize
    const resizeObserver = new ResizeObserver(() => {
      if (fitAddon && terminal) {
        fitAddon.fit()
      }
    })
    resizeObserver.observe(terminalContainer)

    return () => {
      resizeObserver.disconnect()
    }
  })

  onDestroy(() => {
    cleanup()
  })

  function initTerminal() {
    terminal = new Terminal({
      cursorBlink: true,
      cursorStyle: 'block',
      fontSize: 13,
      fontFamily: '"JetBrains Mono", "Fira Code", "Cascadia Code", Monaco, Menlo, "Ubuntu Mono", monospace',
      fontWeight: '400',
      fontWeightBold: '700',
      letterSpacing: 0,
      lineHeight: 1.2,
      scrollback: 10000,
      allowProposedApi: true,
      theme: {
        background: '#1a1b26',
        foreground: '#a9b1d6',
        cursor: '#c0caf5',
        cursorAccent: '#1a1b26',
        selectionBackground: '#33467c',
        selectionForeground: '#c0caf5',
        black: '#15161e',
        red: '#f7768e',
        green: '#9ece6a',
        yellow: '#e0af68',
        blue: '#7aa2f7',
        magenta: '#bb9af7',
        cyan: '#7dcfff',
        white: '#a9b1d6',
        brightBlack: '#414868',
        brightRed: '#f7768e',
        brightGreen: '#9ece6a',
        brightYellow: '#e0af68',
        brightBlue: '#7aa2f7',
        brightMagenta: '#bb9af7',
        brightCyan: '#7dcfff',
        brightWhite: '#c0caf5',
      },
    })

    fitAddon = new FitAddon()
    terminal.loadAddon(fitAddon)
    terminal.loadAddon(new WebLinksAddon())

    terminal.open(terminalContainer)
    fitAddon.fit()

    // Send ALL terminal input directly to the backend (no local command building)
    terminal.onData((data) => {
      if (sessionId) {
        Write(sessionId, data).catch((err) => {
          console.error('Failed to write to terminal:', err)
        })
      }
    })

    // Handle terminal resize events
    terminal.onResize(({ cols, rows }) => {
      if (sessionId) {
        Resize(sessionId, cols, rows).catch((err) => {
          console.error('Failed to resize terminal:', err)
        })
      }
    })
  }

  async function startSession() {
    if (!terminal || !fitAddon) return

    connectionState = 'connecting'

    // Subscribe to terminal data events
    EventsOn('terminal:data', handleTerminalData)
    EventsOn('terminal:exit', handleTerminalExit)

    try {
      const cols = terminal.cols
      const rows = terminal.rows

      const id = await StartSession(clusterId, namespace, podName, activeContainer, cols, rows)
      sessionId = id
      connectionState = 'connected'

      // Focus the terminal
      terminal.focus()
    } catch (err) {
      connectionState = 'error'
      errorMessage = String(err)
      terminal.writeln(`\x1b[31mFailed to start terminal session: ${err}\x1b[0m`)
    }
  }

  function handleTerminalData(event: any) {
    if (!terminal) return
    // Only write data for our session
    if (event.sessionId === sessionId) {
      terminal.write(event.data)
    }
  }

  function handleTerminalExit(event: any) {
    if (event.sessionId !== sessionId) return

    connectionState = 'disconnected'
    if (terminal) {
      terminal.writeln('')
      terminal.writeln('\x1b[33mSession ended.\x1b[0m')
      if (event.reason) {
        terminal.writeln(`\x1b[31mReason: ${event.reason}\x1b[0m`)
      }
      terminal.writeln('\x1b[90mPress the reconnect button to start a new session.\x1b[0m')
    }
  }

  async function reconnect() {
    if (sessionId) {
      await StopSession(sessionId).catch(() => {})
      sessionId = null
    }
    if (terminal) {
      terminal.clear()
      terminal.reset()
    }
    await startSession()
  }

  async function switchContainer(newContainer: string) {
    activeContainer = newContainer
    await reconnect()
  }

  function cleanup() {
    EventsOff('terminal:data')
    EventsOff('terminal:exit')

    if (sessionId) {
      StopSession(sessionId).catch(() => {})
      sessionId = null
    }

    if (terminal) {
      terminal.dispose()
      terminal = null
    }
  }
</script>

<div class="flex h-full flex-col">
  <!-- Header with connection status -->
  <div class="flex items-center justify-between border-b border-outline-variant bg-surface-container-low px-3 py-1.5">
    <div class="flex items-center gap-2 text-body-small">
      <span class="flex items-center gap-1.5">
        {#if connectionState === 'connecting'}
          <span class="inline-block size-2 animate-pulse rounded-full bg-yellow-500"></span>
          <span class="text-on-surface-variant">Connecting...</span>
        {:else if connectionState === 'connected'}
          <span class="inline-block size-2 rounded-full bg-green-500"></span>
          <span class="text-on-surface-variant">Connected</span>
        {:else if connectionState === 'disconnected'}
          <span class="inline-block size-2 rounded-full bg-on-surface-variant"></span>
          <span class="text-on-surface-variant">Disconnected</span>
        {:else}
          <span class="inline-block size-2 rounded-full bg-red-500"></span>
          <span class="text-error">Error</span>
        {/if}
      </span>
      <span class="text-on-surface-variant/60">|</span>
      <span class="font-medium text-on-surface">{podName}</span>
      {#if containers.length > 1}
        <select
          value={activeContainer}
          onchange={(e) => switchContainer((e.target as HTMLSelectElement).value)}
          class="h-6 rounded-xs border border-outline bg-surface px-1.5 text-xs text-on-surface"
        >
          {#each containers as c}
            <option value={c}>{c}</option>
          {/each}
        </select>
      {:else}
        <span class="text-on-surface-variant">/ {activeContainer}</span>
      {/if}
    </div>

    <div class="flex items-center gap-1">
      {#if connectionState === 'disconnected' || connectionState === 'error'}
        <button
          type="button"
          onclick={reconnect}
          class="flex items-center gap-1 rounded-xs bg-primary px-2 py-1 text-xs text-on-primary hover:bg-primary/90"
        >
          <svg viewBox="0 0 24 24" class="size-3" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <path d="M3 12a9 9 0 0 1 9-9 9.75 9.75 0 0 1 6.74 2.74L21 8" />
            <path d="M21 3v5h-5" />
            <path d="M21 12a9 9 0 0 1-9 9 9.75 9.75 0 0 1-6.74-2.74L3 16" />
            <path d="M3 21v-5h5" />
          </svg>
          Reconnect
        </button>
      {/if}
    </div>
  </div>

  <!-- Terminal container -->
  <div
    bind:this={terminalContainer}
    class="flex-1 overflow-hidden bg-[#1a1b26] p-1"
  ></div>
</div>

<style>
  :global(.xterm) {
    height: 100%;
    padding: 4px;
  }

  :global(.xterm-viewport) {
    overflow-y: auto !important;
  }

  :global(.xterm-screen) {
    height: 100%;
  }
</style>
