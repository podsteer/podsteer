<!--
  The controls for starting ONE forward: a local port, and Start.

  A CHILD COMPONENT rather than inline markup in an `{#each}`, because the
  debounced probe below needs state of its own per port — a timer, a request
  counter, what has been typed — and a `{#each}` body cannot hold `$state`
  that belongs to one iteration rather than the whole list.

  Pre-filled from preferences.proposeLocalPort, so an operator who has
  forwarded this remote port — or this NAMED port — before sees the port they
  used last rather than a blank box. Left blank, Start still works: an empty
  field means "let the operating system choose", exactly as it always has.
-->
<script lang="ts">
  import { untrack } from 'svelte'
  import { forwards } from '$stores/forwards.svelte'
  import { preferences } from '$stores/preferences.svelte'
  import { probeLocalPort, freeLocalPort } from '$lib/api/client'
  import { Loader, Plug, Wand2 } from '@lucide/svelte'

  interface Props {
    clusterId: string
    namespace: string
    podName: string
    podUID: string
    remotePort: number
    portName: string
    protocol: string
    labels: Record<string, string>
    /** Whether a start or stop for THIS port is already in flight. */
    busy: boolean
  }

  let { clusterId, namespace, podName, podUID, remotePort, portName, protocol, labels, busy }: Props =
    $props()

  /**
   * What is typed, as text. Empty means "let the operating system choose".
   *
   * `untrack` because this reads remotePort/portName ONCE, at mount, to seed
   * the field — it is not meant to overwrite whatever the operator has since
   * typed if either prop were ever to change under an existing instance.
   */
  let typed = $state(untrack(() => String(preferences.proposeLocalPort(remotePort, portName) ?? '')))

  type ProbeState = 'idle' | 'checking' | 'free' | 'inUse' | 'invalid'
  let probe = $state<ProbeState>('idle')

  let debounceTimer: ReturnType<typeof setTimeout> | null = null
  /** Probes asked for so far, so a slow answer cannot land after a faster,
      later one already replaced it — the same guard AddClusterDialog's
      preview debounce uses, for the same reason. */
  let probeRequest = 0

  $effect(() => {
    const value = typed.trim()
    if (debounceTimer) clearTimeout(debounceTimer)

    if (value === '') {
      probe = 'idle'
      return
    }

    const port = Number(value)
    if (!Number.isInteger(port) || port < 1 || port > 65535) {
      probe = 'invalid'
      return
    }

    // CHECKING SHOWS IMMEDIATELY; the request itself is debounced. Otherwise
    // Start stays enabled for the whole 350ms after a keystroke that made the
    // typed port invalid or already-taken, which is exactly the window
    // somebody fast enough to type-and-click would hit.
    probe = 'checking'
    const asked = ++probeRequest

    debounceTimer = setTimeout(async () => {
      try {
        const free = await probeLocalPort(port)
        if (asked !== probeRequest) return
        probe = free ? 'free' : 'inUse'
      } catch {
        if (asked !== probeRequest) return
        // A probe that could not even run is not worth blocking Start over —
        // the forward itself refuses a genuinely occupied port. This only
        // exists to say so earlier.
        probe = 'idle'
      }
    }, 350)

    return () => {
      if (debounceTimer) clearTimeout(debounceTimer)
    }
  })

  async function pickFreePort(): Promise<void> {
    try {
      typed = String(await freeLocalPort())
    } catch {
      // Left as it was. The operator can still type a port, or clear the
      // field and let Start ask the operating system to choose.
    }
  }

  function start(): void {
    const value = typed.trim()
    const localPort = value === '' ? 0 : Number(value)
    void forwards.start(
      clusterId,
      namespace,
      podName,
      podUID,
      remotePort,
      portName,
      protocol,
      labels,
      localPort,
    )
  }

  const startDisabled = $derived(busy || probe === 'checking' || probe === 'inUse' || probe === 'invalid')
</script>

<span class="ml-auto flex shrink-0 items-center gap-1.5">
  <input
    type="text"
    inputmode="numeric"
    placeholder="auto"
    bind:value={typed}
    aria-label="Local port for {portName || `port ${remotePort}`}"
    aria-invalid={probe === 'inUse' || probe === 'invalid'}
    class="h-7 w-16 rounded-sm border bg-transparent px-1.5 text-body-medium tabular-nums
           text-on-surface placeholder:text-on-surface-variant/50 focus:outline-none
           {probe === 'inUse' || probe === 'invalid'
      ? 'border-error focus:border-error'
      : 'border-outline-variant focus:border-primary'}"
  />

  <button
    type="button"
    onclick={() => void pickFreePort()}
    title="Pick a free port"
    aria-label="Pick a free port"
    class="state-layer grid size-7 shrink-0 place-items-center rounded-sm
           text-on-surface-variant transition-colors duration-100
           hover:bg-surface-container hover:text-on-surface"
  >
    <Wand2 class="size-3.5" strokeWidth={1.8} />
  </button>

  <button
    type="button"
    disabled={startDisabled}
    onclick={start}
    class="state-layer inline-flex h-7 shrink-0 items-center gap-1.5 rounded-sm
           border border-outline-variant px-2 text-label-large
           text-on-surface-variant transition-colors duration-100
           hover:bg-surface-container hover:text-on-surface disabled:opacity-50"
  >
    {#if busy || probe === 'checking'}
      <Loader class="size-3.5 animate-spin" strokeWidth={2} />
    {:else}
      <Plug class="size-3.5" strokeWidth={1.8} />
    {/if}
    Forward
  </button>
</span>

{#if probe === 'inUse'}
  <span class="text-body-small text-error">Port {typed} is in use on this machine</span>
{:else if probe === 'invalid'}
  <span class="text-body-small text-error">Enter a port between 1 and 65535</span>
{/if}
