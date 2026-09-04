<!--
  Opens a shell on the OPERATOR'S OWN MACHINE, and optionally starts a coding
  agent they already have in it.

  It is the one terminal here that reaches no cluster, so the dialog spends its
  words on the two things that surprise people about it: the read-only setting
  does not apply, and the context is stated rather than pinned. Both are true
  of the pane too, and both are said there as well — this is the moment before
  it opens, when somebody can still change their mind.

  NOTHING IS INSTALLED. The agent list is what the Go side FOUND on the adopted
  PATH; a machine with none simply has no agent row, and there is deliberately
  no "get one" link.
-->
<script lang="ts">
  import { escapeLayer, type EscapeClaim } from '$lib/escape'
  import { modal } from '$lib/modal'
  import type { CodingAgent } from '$lib/localShell'
  import Button from './Button.svelte'
  import { SquareTerminal, Bot } from '@lucide/svelte'

  interface Props {
    open: boolean
    /** The kubeconfig context of the tab in front, '' when none is. */
    clusterId: string
    /** The agents found on this machine, in the Go side's preference order. */
    agents: CodingAgent[]
    onclose: () => void
    /** Called with the chosen agent ('' for a plain shell) and the read-only default. */
    onconfirm: (agentId: string, readOnly: boolean) => void
  }

  let { open, clusterId, agents, onclose, onconfirm }: Props = $props()

  /**
   * '' means the operator's own login shell; anything else is an agent id.
   *
   * The PLAIN SHELL IS THE DEFAULT even on a machine with four agents
   * installed. Opening a terminal and pre-selecting a coding agent decides on
   * somebody's behalf that they wanted one, and an agent's first act is to
   * read a real cluster with real credentials — a choice worth one click.
   */
  let agentId = $state('')
  /**
   * The read-only default, ON.
   *
   * A REQUEST, not a restriction, and the dialog says so — the agent holds the
   * operator's own credentials and nothing here can narrow them. It defaults
   * on because an agent asked to look before it writes is the version most
   * people want the first time, and turning it off is one click.
   */
  let readOnly = $state(true)

  $effect(() => {
    if (!open) return
    agentId = ''
    readOnly = true
  })

  const chosenAgent = $derived(agents.find((agent) => agent.id === agentId) ?? null)

  function confirm(): void {
    onconfirm(agentId, readOnly)
  }

  function onKeydown(event: KeyboardEvent): void {
    if (!open || event.key !== 'Escape') return
    if (!escape?.owns()) return
    onclose()
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

<svelte:window onkeydown={onKeydown} />

{#if open}
  <button
    type="button"
    aria-label="Close dialog"
    tabindex="-1"
    class="fixed inset-0 z-[60] cursor-default bg-scrim/40"
    onclick={onclose}
  ></button>

  <div
    class="fixed top-1/2 left-1/2 z-[70] w-[32rem] max-w-[90vw] -translate-x-1/2 -translate-y-1/2
           rounded-sm border border-outline-variant bg-surface-container-high p-6 shadow-level-3"
    role="dialog"
    aria-modal="true"
    use:modal
    aria-label="Open a local terminal"
  >
    <h2 class="flex items-center gap-2 text-headline-small text-on-surface">
      <SquareTerminal class="size-5 text-on-surface-variant" strokeWidth={2} aria-hidden="true" />
      Local terminal
    </h2>

    <p class="mt-4 text-body-medium text-on-surface-variant">
      Your own login shell, on this machine, in your home directory.
      <strong class="text-on-surface">KUBECONFIG</strong> is set to the same files PodSteer reads,
      so <code class="font-mono">kubectl</code> and <code class="font-mono">helm</code> see the same
      clusters — whichever versions you already have. PodSteer installs nothing.
    </p>

    <fieldset class="mt-4">
      <legend class="text-body-small text-on-surface-variant">Start with</legend>
      <div class="mt-1 flex flex-col gap-1">
        <label class="flex items-center gap-2 text-body-medium text-on-surface">
          <input type="radio" bind:group={agentId} value="" />
          Your login shell
        </label>

        {#each agents as agent (agent.id)}
          <label class="flex items-center gap-2 text-body-medium text-on-surface">
            <input type="radio" bind:group={agentId} value={agent.id} />
            <Bot class="size-4 text-on-surface-variant" strokeWidth={1.8} aria-hidden="true" />
            {agent.label}
            <span class="truncate font-mono text-body-small text-on-surface-variant">{agent.path}</span>
          </label>
        {/each}
      </div>

      {#if agents.length === 0}
        <!--
          Stated rather than hidden, and deliberately without a link: which
          coding agent somebody installs is their decision, and offering to
          fetch one would be PodSteer reaching outside this machine.
        -->
        <p class="mt-2 text-body-small text-on-surface-variant">
          No coding agent was found on your PATH. PodSteer only opens one you already have.
        </p>
      {/if}
    </fieldset>

    {#if chosenAgent}
      <div class="mt-3 rounded-sm border border-outline-variant bg-surface-container px-3 py-2">
        <label class="flex items-center gap-2 text-body-medium text-on-surface">
          <input type="checkbox" bind:checked={readOnly} />
          Ask it to keep to read-only kubectl
        </label>
        <p class="mt-1 text-body-small text-on-surface-variant">
          A request in its opening prompt, not a restriction — {chosenAgent.label} runs with your credentials
          and PodSteer cannot narrow them. It is told which cluster and which object you have open, and
          that its access is whatever your kubeconfig grants. Nothing is sent anywhere by PodSteer; this
          starts a process on this machine.
        </p>
      </div>
    {/if}

    <p
      class="mt-3 rounded-sm border border-outline-variant bg-surface-container px-3 py-2
             text-body-small text-on-surface-variant"
    >
      {#if clusterId}
        The open tab's context is <code class="font-mono text-on-surface">{clusterId}</code>, and the
        shell is told so — but <code class="font-mono">current-context</code> in your kubeconfig is left
        exactly as it is, so pass <code class="font-mono">--context</code>. PodSteer never rewrites that
        file, and kubectl in your other terminals must not change target because you opened a pane here.
      {:else}
        No cluster tab is open, so the shell gets your kubeconfig unchanged and nothing is pinned.
      {/if}
    </p>

    <p
      class="mt-3 rounded-sm border border-outline-variant bg-surface-container px-3 py-2
             text-body-small text-on-surface-variant"
    >
      PodSteer's <strong class="text-on-surface">read-only</strong> setting does not apply in here. It
      guards PodSteer's own writes to a cluster; a shell you opened yourself, with your own credentials,
      is not something this application can or should police.
    </p>

    <div class="mt-6 flex justify-end gap-3">
      <Button variant="outlined" onclick={onclose}>Cancel</Button>
      <Button variant="filled" onclick={confirm}>
        {chosenAgent ? `Open ${chosenAgent.label}` : 'Open shell'}
      </Button>
    </div>
  </div>
{/if}
