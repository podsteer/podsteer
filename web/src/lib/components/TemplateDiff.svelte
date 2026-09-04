<!--
  A diff between two revisions' pod templates.

  A thin wrapper over DiffView, the one diff renderer in the application:
  side by side, unchanged runs folded, n and p between hunks, the unified
  text a copy away. The history pane's only contribution is the labels — a
  revision is named by its number and its age, which is what an operator
  is choosing between — so nothing here re-implements the comparison.
-->
<script lang="ts">
  import DiffView from './DiffView.svelte'

  interface Props {
    /** The older (or left-hand) revision's template YAML. */
    before: string
    beforeLabel: string
    /** The newer (or right-hand) revision's template YAML. */
    after: string
    afterLabel: string
  }

  let { before, beforeLabel, after, afterLabel }: Props = $props()
</script>

<div class="flex min-h-0 flex-1 flex-col">
  <DiffView left={before} right={after} leftLabel={beforeLabel} rightLabel={afterLabel} />
</div>
