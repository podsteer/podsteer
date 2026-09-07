<!--
  The (?) itself, so a dialog with a header of its own can have one without
  copying DialogHeader's markup.

  It is deliberately the same size and shape as the close control it sits
  beside — these two are a pair, and a question mark drawn to different
  proportions than the X next to it reads as a different kind of thing.
-->
<script lang="ts">
  import { help } from '$stores/help.svelte'
  import type { HelpTopicId } from '$lib/help'
  import { CircleHelp } from '@lucide/svelte'

  interface Props {
    /** The topic to open. Typed against the registry: a name nobody wrote
     * fails to compile rather than opening an empty panel. */
    topic: HelpTopicId
    /** What the help is about, for the accessible name. */
    about: string
  }

  let { topic, about }: Props = $props()
</script>

<button
  type="button"
  onclick={() => help.open(topic)}
  aria-label="Help with {about}"
  title="What this does"
  class="state-layer grid size-8 shrink-0 place-items-center rounded-full text-on-surface-variant
         transition-colors duration-100 hover:bg-surface-container hover:text-on-surface"
>
  <CircleHelp class="size-4" strokeWidth={2} />
</button>
