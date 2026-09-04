<!--
  The cells of a row's custom columns — see $lib/customColumns.

  One component rather than six copies of the same loop, because every list
  renders these identically: the value verbatim, a dash when the object does
  not carry the key, selectable so a value can be copied off the row. The
  views hand it the same `isVisible` test DataTable hands them, so a hidden
  custom column is skipped exactly as a hidden built-in one is.
-->
<script lang="ts">
  import {
    customCell,
    customColumnId,
    type CustomColumnSpec,
    type MetadataRow,
  } from '$lib/customColumns'

  interface Props {
    /** The kind's custom columns, in display order. */
    specs: CustomColumnSpec[]
    /** The row, carrying its labels and projected annotations. */
    row: MetadataRow
    /** DataTable's visibility test, passed through from the rows snippet. */
    isVisible: (columnId: string) => boolean
  }

  let { specs, row, isVisible }: Props = $props()
</script>

{#each specs as spec (customColumnId(spec))}
  {#if isVisible(customColumnId(spec))}
    {@const value = customCell(row, spec)}
    <td
      class="truncate px-3 py-1.5 {value === '—' ? 'text-on-surface-variant/40' : 'text-on-surface-variant'}"
      title={value === '—' ? `No ${spec.source} ${spec.key}` : value}
      data-selectable
    >
      {value}
    </td>
  {/if}
{/each}
