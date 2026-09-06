<!--
  A DataTable with one row, for DataTable.test.ts.

  A harness rather than a snippet built in the test file, because `rows` is a
  snippet taking the visibility test as an argument and the cells it draws are
  the whole subject: the row has to come from real markup, in the order a view
  writes it, or the test would be asserting against cells it invented rather
  than against the arrangement every view actually produces.
-->
<script lang="ts">
  import DataTable, { ROW_MENU_COLUMN, type Column } from '$lib/components/DataTable.svelte'
  import RowSelect from '$lib/components/RowSelect.svelte'
  import RowMenuCell from '$lib/components/RowMenuCell.svelte'

  interface Props {
    kindId?: string
    /** The value columns, between the tick box and the menu. */
    values?: Column[]
    /** Whether the table offers selection at all — the event list does not. */
    selectable?: boolean
  }

  let {
    kindId = 'v1/pods',
    values = [
      { id: 'name', label: 'Name', width: 320, pinned: true },
      { id: 'age', label: 'Age', width: 80, numeric: true },
    ],
    selectable = true,
  }: Props = $props()

  const columns = $derived<Column[]>([
    ...(selectable
      ? [{ id: 'select', label: 'Select', width: 40, pinned: true, select: true } as Column]
      : []),
    ...values,
    ROW_MENU_COLUMN,
  ])
</script>

<DataTable
  {kindId}
  {columns}
  selectAll={selectable
    ? { checked: false, indeterminate: false, ontoggle: () => {} }
    : undefined}
>
  {#snippet rows(isVisible)}
    <tr class="group/row bg-surface">
      {#if selectable}
        <RowSelect selected={false} label="alpha" ontoggle={() => {}} />
      {/if}
      {#each values as column (column.id)}
        {#if isVisible(column.id)}
          <td>{column.label} cell</td>
        {/if}
      {/each}
      <RowMenuCell actions={[{ label: 'Copy name', kind: 'copy', onclick: () => {} }]} label="alpha" />
    </tr>
  {/snippet}
</DataTable>
