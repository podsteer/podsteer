<!--
  MD3 indeterminate linear progress indicator.

  Rendered as a thin bar pinned under the app bar rather than as a spinner over
  the content, so a refresh never blanks the table the operator is reading —
  data stays on screen and visibly updates in place.
-->
<script lang="ts">
  interface Props {
    /** Whether work is in progress. The element keeps its height either way,
        so showing it cannot shift the layout underneath. */
    active?: boolean
    class?: string
  }

  let { active = false, class: className = '' }: Props = $props()
</script>

<div
  class="h-1 w-full overflow-hidden bg-surface-container-highest {className}"
  role="progressbar"
  aria-busy={active}
  aria-label="Loading"
>
  {#if active}
    <div class="k8s-indeterminate h-full w-1/3 rounded-full bg-primary"></div>
  {/if}
</div>

<style>
  /*
    Scoped because it is presentation of this component alone. The MD3
    emphasized easing gives the bar its characteristic accelerate-then-glide
    motion rather than a linear sweep.
  */
  .k8s-indeterminate {
    animation: k8s-indeterminate 1.4s cubic-bezier(0.65, 0.15, 0.35, 0.85) infinite;
  }

  @keyframes k8s-indeterminate {
    0% {
      transform: translateX(-100%);
    }
    100% {
      transform: translateX(400%);
    }
  }
</style>
