<!--
  A dialog's last row: the actions on the right, and — where PodSteer is about
  to do something kubectl can also do — the command behind a link on the left.

  THE COMMAND OPENS ABOVE THE ROW, ACROSS THE WHOLE DIALOG. Two reasons, and
  the second is why this is a component rather than a prop on KubectlHint:

    - Opening downward would move the buttons out from under a pointer already
      travelling towards them, which is how somebody presses the wrong one.
    - A panel opening inside the row is as wide as the row's leftovers, and a
      `kubectl` line wrapped every twenty characters is a worse transcript
      than no transcript. Full width means the command breaks where a shell
      would break it.

  IN FLOW, NEVER ABSOLUTE. These dialogs scroll their own overflow now, and an
  absolutely positioned panel is clipped by exactly the container that makes
  them scrollable.

  Closed on every open, deliberately: a dialog opens on its question, not on
  its footnote.
-->
<script lang="ts">
  import type { Snippet } from 'svelte'
  import KubectlHint from './KubectlHint.svelte'
  import { ChevronDown } from '@lucide/svelte'

  interface Props {
    /** The command this dialog is the graphical form of. Omit for a dialog
     * with no equivalent — the row is then just its buttons. */
    command?: string
    /** The link's wording, matching KubectlHint's own label. */
    label?: string
    /** The buttons, in reading order. */
    children: Snippet
  }

  let { command = '', label = 'kubectl equivalent', children }: Props = $props()

  let shown = $state(false)
</script>

<div class="mt-6 flex flex-col gap-3">
  {#if command && shown}
    <KubectlHint {command} {label} />
  {/if}

  <div class="flex items-center gap-3">
    {#if command}
      <button
        type="button"
        onclick={() => (shown = !shown)}
        aria-expanded={shown}
        class="state-layer -ml-1.5 flex cursor-pointer items-center gap-1 rounded-sm px-1.5 py-1
               text-body-medium text-on-surface-variant transition-colors duration-100
               hover:text-on-surface"
      >
        {label}
        <ChevronDown
          class="size-3.5 shrink-0 transition-transform duration-150 {shown ? 'rotate-180' : ''}"
          strokeWidth={2}
          aria-hidden="true"
        />
      </button>
    {/if}

    <div class="ml-auto flex shrink-0 items-center gap-3">
      {@render children()}
    </div>
  </div>
</div>
