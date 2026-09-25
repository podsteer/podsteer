<!--
  The kubectl equivalent of one RUNNING forward, on the row that shows it.

  The last two `TODO(kubectl-transparency)` markers in the tree were both
  here: item 29 puts the command beside every action PodSteer performs, and a
  port-forward — the action an operator is most likely to want to reproduce in
  a terminal, and the one they most often want to hand to somebody else — had
  none, on either the container row that starts it or the panel that lists it.

  AN ICON, NOT A STRIP. KubectlHint is a bordered block with the command in
  it; these two rows are a line each in a list, and a block per row would
  triple the height of a panel whose whole job is to show what is open at a
  glance. The command is in the tooltip and on the clipboard, which is what it
  is for.

  A SERVICE FORWARD PRINTS `service/…`, not the pod it happens to have landed
  on: PodSteer keeps the Service's selector and moves to another pod behind it
  when this one goes away, so `pod/<today's pod>` would be a narrower command
  than what is actually running. See $lib/kubectl.portForwardService.
-->
<script lang="ts">
  import { copyText } from '$lib/clipboard'
  import { flash } from '$lib/flash.svelte'
  import { portForward, portForwardService } from '$lib/kubectl'
  import { forwards } from '$stores/forwards.svelte'
  import type { PortForward } from '$lib/api/client'
  import { Check, Terminal } from '@lucide/svelte'

  interface Props {
    forward: PortForward
  }

  let { forward }: Props = $props()

  const service = $derived(forwards.serviceOf(forward.id))

  const command = $derived(
    service
      ? portForwardService(
          forward.clusterId,
          service.service,
          forward.namespace,
          forward.localPort,
          service.servicePort,
        )
      : portForward(
          forward.clusterId,
          forward.pod,
          forward.namespace,
          forward.localPort,
          forward.remotePort,
        ),
  )

  const copied = flash(900)

  // The tick appears only if the command actually reached the clipboard —
  // the same rule RowMenu's copy follows, and for the same reason: somebody
  // who trusts it and pastes has no fallback in mind.
  async function copy(): Promise<void> {
    if (await copyText(command)) copied.show()
  }
</script>

<button
  type="button"
  onclick={() => void copy()}
  title={copied.on ? 'Copied' : command}
  aria-label={copied.on ? 'Copied' : `Copy kubectl command: ${command}`}
  class="state-layer grid size-6 shrink-0 place-items-center rounded-xs transition-colors duration-100
         {copied.on
    ? 'text-success'
    : 'text-on-surface-variant hover:bg-surface-container-highest hover:text-on-surface'}"
>
  {#if copied.on}
    <Check class="size-3.5" strokeWidth={2.5} />
  {:else}
    <Terminal class="size-3.5" strokeWidth={1.8} />
  {/if}
</button>
