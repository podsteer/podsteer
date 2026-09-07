<!--
  Opens a shell on the OPERATOR'S OWN MACHINE, and optionally starts a coding
  agent they already have in it.

  It is the one terminal here that reaches no cluster, and the two things that
  surprise people about it are that the read-only setting does not apply and
  that the context is stated rather than pinned. Both are true of the pane too,
  and both are said there as well — this is the moment before it opens, when
  somebody can still change their mind.

  THOSE EXPLANATIONS ARE BEHIND (i) ICONS, NOT PRINTED DOWN THE DIALOG. Three
  paragraphs of prose stood between the operator and a button they press to get
  a shell, and they are paragraphs somebody reads once, if ever. What is left
  on the surface is what CHANGES between one opening and the next — which
  context the tab is on, which agents this machine has — and the reasoning sits
  under the icon beside it, in the same hint the toolbar's search field uses.

  NOTHING IS INSTALLED. The agent list is what the Go side FOUND on the adopted
  PATH; a machine with none simply has no agent row, and there is deliberately
  no "get one" link.
-->
<script lang="ts">
  import { escapeLayer, type EscapeClaim } from '$lib/escape'
  import { modal } from '$lib/modal'
  import type { CodingAgent } from '$lib/localShell'
  import Button from './Button.svelte'
  import Radio from './Radio.svelte'
  import Checkbox from './Checkbox.svelte'
  import InfoHint from './InfoHint.svelte'
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
   * What the dialog used to print in full, as hint text.
   *
   * Plain sentences rather than markup, because a hint panel is text: the
   * emphasis these paragraphs carried was doing the work of a heading in a
   * wall of prose, and there is no wall any more.
   */
  const ABOUT =
    'Your own login shell, on this machine, in your home directory. KUBECONFIG is set to the ' +
    'same files PodSteer reads, so kubectl and helm see the same clusters — whichever versions ' +
    "you already have. PodSteer installs nothing. PodSteer's read-only setting does not apply " +
    'in here either: it guards this application\'s own writes to a cluster, and a shell you ' +
    'opened yourself, with your own credentials, is not something it can or should police.'

  const CONTEXT_HINT =
    'The shell is told this context, but current-context in your kubeconfig is left exactly as ' +
    'it is, so pass --context. PodSteer never rewrites that file, and kubectl in your other ' +
    'terminals must not change target because you opened a pane here.'

  const AGENT_HINT = (label: string): string =>
    `A request in its opening prompt, not a restriction — ${label} runs with your credentials ` +
    'and PodSteer cannot narrow them. It is told which cluster and which object you have open, ' +
    'and that its access is whatever your kubeconfig grants. Nothing is sent anywhere by ' +
    'PodSteer; this starts a process on this machine.'

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
      <InfoHint text={ABOUT} label="What a local terminal is" />
    </h2>

    <fieldset class="mt-4">
      <legend class="text-body-small text-on-surface-variant">Start with</legend>
      <!-- These carry a `name` where the raw inputs before them did not, and
           that is a fix rather than a formality: radios without a shared name
           are not a group at all, so this list was one tab stop per option
           with the arrow keys doing nothing. The fieldset's legend still names
           the group, so no role is added here. -->
      <div class="mt-1 flex flex-col gap-1">
        <Radio name="local-shell-start" bind:group={agentId} value="" dense>
          Your login shell
        </Radio>

        {#each agents as agent (agent.id)}
          <Radio name="local-shell-start" bind:group={agentId} value={agent.id} dense>
            <span class="flex items-center gap-2">
              <Bot class="size-4 text-on-surface-variant" strokeWidth={1.8} aria-hidden="true" />
              {agent.label}
              <span class="truncate font-mono text-body-small text-on-surface-variant"
                >{agent.path}</span
              >
            </span>
          </Radio>
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
        <div class="flex items-center gap-1">
          <Checkbox
            checked={readOnly}
            onchange={(next) => (readOnly = next)}
            dense
            class="text-body-medium text-on-surface"
          >
            Ask it to keep to read-only kubectl
          </Checkbox>
          <InfoHint
            text={AGENT_HINT(chosenAgent.label)}
            label="What read-only asks of the agent"
          />
        </div>
      </div>
    {/if}

    <!-- THE FACT STAYS ON THE SURFACE, the caveat goes under the icon. Which
         context the shell is told is the thing that differs between one
         opening and the next, and it is one line; why your kubeconfig is
         nevertheless untouched is read once. -->
    <p class="mt-4 flex items-center gap-1.5 text-body-small text-on-surface-variant">
      {#if clusterId}
        <span>Context</span>
        <code class="font-mono text-on-surface">{clusterId}</code>
        <InfoHint text={CONTEXT_HINT} label="How the context is set" />
      {:else}
        <span>No cluster tab is open, so your kubeconfig is passed through unchanged.</span>
      {/if}
    </p>

    <div class="mt-6 flex justify-end gap-3">
      <Button variant="outlined" onclick={onclose}>Cancel</Button>
      <Button variant="filled" onclick={confirm}>
        {chosenAgent ? `Open ${chosenAgent.label}` : 'Open shell'}
      </Button>
    </div>
  </div>
{/if}
