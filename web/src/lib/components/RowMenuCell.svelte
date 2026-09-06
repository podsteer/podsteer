<!--
  The last two cells of a list row: the table's elastic slack, then the row
  menu in a column of its own.

  TWO CELLS FROM ONE COMPONENT, and that is the point of it existing. The
  elastic column has to sit between the last real column and the menu — see
  DataTable's `menuColumn` for why — and it is the row that has to supply the
  cell for it, since DataTable renders no cells at all. Six views drawing two
  bare <td>s in the right order, each carrying its own copy of the
  stopPropagation rule, is six chances to get one of them wrong; here it is
  one place, and a view that renders this cannot put them the wrong way round.

  The menu itself was never a column before this: it fell into the elastic
  slot by arithmetic, took whatever width the table happened to have left, and
  scrolled off the moment a table was wider than its window — which is exactly
  when somebody is looking for it.
-->
<script lang="ts">
  import RowMenu, { type RowAction } from './RowMenu.svelte'

  interface Props {
    /** What this row offers. An empty list draws the cell and no control. */
    actions: RowAction[]
    /** Names the row, for the control's accessible label. */
    label: string
  }

  let { actions, label }: Props = $props()
</script>

<!-- The elastic column. Empty by definition: it is the leftover width. -->
<td class="p-0"></td>

<!--
  Stops the click here: the row itself opens the detail drawer, and a click
  aimed at the menu — or at one of its items — must not also do that.

  `data-edge` marks this as the right-hand control column; DataTable's own
  stylesheet acts on it, and only while the operator has that edge fixed.
-->
<td data-edge="menu" class="px-2" onclick={(event) => event.stopPropagation()}>
  <div class="flex justify-end">
    <RowMenu {actions} {label} />
  </div>
</td>
