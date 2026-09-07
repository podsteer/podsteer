<!--
  The toolbar's terminal control, which is a MENU rather than a button.

  It grew a second entry, so it grew a chevron: a control that opens something
  and a control that does something must not look the same, and the chevron is
  what says which this is before it is pressed. That is the same argument
  ColumnMenu's own trigger makes, and this follows its shape — a window
  pointerdown to dismiss, an escape claim so it closes before the drawer
  underneath it, and `aria-haspopup="menu"`.

  TWO ENTRIES, AND THEY REACH DIFFERENT MACHINES:

  - Local shell — a shell on the operator's OWN machine, with KUBECONFIG set
    for this tab. Exactly what this control did as a button, unchanged, and it
    is first because it is the one that was already here.
  - In-cluster shell — a throwaway pod in the cluster, attached to. New.

  EACH ENTRY IS ITS NAME AND NOTHING ELSE. Both carried a sentence of
  description underneath, which put four lines of prose in front of somebody
  who had already decided to open a terminal — and repeated, worse, what the
  dialog behind each entry says properly. The sentence still exists as the
  entry's title, where it answers a hesitation instead of interrupting a
  decision.

  A disabled entry keeps its reason in its title rather than disappearing: a
  control that is absent teaches nothing, and both of these are absent for
  reasons an operator can act on (no pseudo-terminal on this platform; the
  cluster marked read-only).
-->
<script lang="ts">
  import { escapeLayer, type EscapeClaim } from '$lib/escape'
  import { SquareTerminal, Laptop, Container, ChevronDown } from '@lucide/svelte'

  interface Props {
    /** Whether this platform can open a shell on the operator's own machine,
     * and the sentence to show when it cannot. */
    localSupported: boolean
    localReason: string
    /**
     * True when this cluster is marked read-only in PodSteer.
     *
     * It disables the IN-CLUSTER entry and NOT the local one, and that split is
     * the guard's own doctrine rather than an inconsistency: an in-cluster
     * shell creates a pod, which is a write; a shell on the operator's own
     * machine with their own credentials is not something this application can
     * or should police. See CLAUDE.md's local-terminal section.
     */
    readOnly: boolean
    /** The sentence the backend gives for a read-only refusal. */
    readOnlyReason: string
    onlocal: () => void
    oncluster: () => void
  }

  let { localSupported, localReason, readOnly, readOnlyReason, onlocal, oncluster }: Props =
    $props()

  let open = $state(false)

  function choose(run: () => void): void {
    open = false
    run()
  }

  function onWindowPointerDown(event: PointerEvent): void {
    if (!open) return
    const target = event.target as HTMLElement | null
    if (!target?.closest('[data-terminal-menu]')) open = false
  }

  function onKeydown(event: KeyboardEvent): void {
    if (event.key !== 'Escape') return
    if (!escape?.owns()) return
    open = false
  }

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
</script>

<svelte:window onpointerdown={onWindowPointerDown} onkeydown={onKeydown} />

<div class="relative" data-terminal-menu>
  <button
    type="button"
    onclick={() => (open = !open)}
    aria-expanded={open}
    aria-haspopup="menu"
    aria-label="Terminal"
    title="Open a terminal"
    class="state-layer flex h-8 shrink-0 cursor-pointer items-center gap-1 rounded-full px-2
           text-on-surface-variant transition-colors duration-100
           hover:bg-surface-container hover:text-on-surface
           {open ? 'bg-surface-container text-on-surface' : ''}"
  >
    <SquareTerminal class="size-4" strokeWidth={1.8} aria-hidden="true" />
    <ChevronDown class="size-3" strokeWidth={2} aria-hidden="true" />
  </button>

  {#if open}
    <div
      role="menu"
      aria-label="Terminal"
      class="absolute top-full right-0 z-50 mt-1.5 w-56 rounded-sm border border-outline-variant/60
             bg-surface-container-high py-1 shadow-level-2"
    >
      <button
        type="button"
        role="menuitem"
        disabled={!localSupported}
        title={localSupported
          ? 'Open a shell on this machine, with KUBECONFIG set for this cluster'
          : localReason}
        onclick={() => choose(onlocal)}
        class="state-layer flex w-full items-center gap-2 px-3 py-2 text-left
               text-body-medium text-on-surface hover:bg-surface-container-highest
               disabled:cursor-not-allowed disabled:opacity-50"
      >
        <Laptop class="size-4 shrink-0" strokeWidth={1.8} aria-hidden="true" />
        Local shell
      </button>

      <button
        type="button"
        role="menuitem"
        disabled={readOnly}
        title={readOnly
          ? readOnlyReason
          : "Run a throwaway pod in the cluster and attach to it — kubectl, dig and curl from inside the cluster's network"}
        onclick={() => choose(oncluster)}
        class="state-layer flex w-full items-center gap-2 px-3 py-2 text-left
               text-body-medium text-on-surface hover:bg-surface-container-highest
               disabled:cursor-not-allowed disabled:opacity-50"
      >
        <Container class="size-4 shrink-0" strokeWidth={1.8} aria-hidden="true" />
        In-cluster shell
      </button>
    </div>
  {/if}
</div>
