<!--
  An access review that did not answer. A refusal is not a fault: it is drawn
  as information, and only a transient failure is drawn as an error worth
  retrying — the distinction `reviewState` in $lib/rbac makes.
-->
<script lang="ts">
  interface Props {
    state: { tone: string; message: string }
  }

  let { state }: Props = $props()
</script>

<p
  class="rounded-sm px-3 py-2 text-body-medium
         {state.tone === 'error'
    ? 'bg-error-container/40 text-on-error-container'
    : 'bg-surface-container text-on-surface-variant'}"
>
  {state.message}
</p>
