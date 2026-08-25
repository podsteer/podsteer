<!--
  The strip of controls above a full-height pane.

  One shell for every pane that shows text somebody reads rather than a table
  they scan: the YAML tab, the edit dialog, the log viewer. It owns nothing but
  the frame — height, rule, surface, spacing — so that a control put in one of
  them lands in the same place in all of them, and adding a pane later is a
  matter of rendering this rather than deciding those four things again.

  It is deliberately not a container for a pane's *content* controls. A log
  viewer's container picker belongs to logs and would mean nothing on a
  manifest; what goes here are the controls that govern how the text is
  DISPLAYED, which is the part every such pane has in common.

  `trailing` is pushed to the far end. Controls read left to right in the
  order they are reached for, and the ones that are searched for rather than
  toggled — a filter box, a count — belong at the other end where they are not
  in the way.
-->
<script lang="ts">
  import type { Snippet } from 'svelte'

  interface Props {
    /** Controls at the leading edge. */
    children?: Snippet
    /** Controls pushed to the trailing edge. */
    trailing?: Snippet
  }

  let { children, trailing }: Props = $props()
</script>

<div
  class="flex h-10 shrink-0 items-center gap-1 border-b border-outline-variant/60
         bg-surface-container-low/50 px-2"
>
  {@render children?.()}
  {#if trailing}
    <div class="ml-auto flex items-center gap-1.5">{@render trailing()}</div>
  {/if}
</div>
