<!--
  One row menu, in the table cell it actually lives in.

  RowMenu portals its popup onto the body and measures itself against the
  window, so a test cannot reach it by rendering the component and reading its
  own subtree — which is the same reason DetailList's menu tests query
  `document` rather than the container. What this adds over rendering RowMenu
  directly is the CELL: the menu in a list is drawn by RowMenuCell inside a
  `<tr>`, and mounting a `<td>` outside a table is a hydration error in Svelte
  before any assertion runs.

  The actions are the caller's, so a test can hand it the real ones from
  `$lib/rowActions` or a hand-made pair.
-->
<script lang="ts">
  import RowMenuCell from '$lib/components/RowMenuCell.svelte'
  import type { RowAction } from '$lib/components/RowMenu.svelte'

  interface Props {
    actions: RowAction[]
  }

  let { actions }: Props = $props()
</script>

<table>
  <tbody>
    <tr>
      <RowMenuCell {actions} label="alpha" />
    </tr>
  </tbody>
</table>
