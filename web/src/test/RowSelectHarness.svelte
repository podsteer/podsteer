<!--
  One selectable row, holding its own selection.

  The bug this exists to pin is a matter of ORDER, so a test cannot get at it
  by rendering RowSelect and pushing a new `selected` prop in afterwards: by
  then the browser has already put its cancelled toggle back, and the write
  that used to be skipped is made. What breaks is the store updating and
  Svelte flushing INSIDE the click handler, before the browser's canceled
  activation steps run. So the state has to live in a component that reacts to
  the toggle, which is what this is — the same two lines every list view
  writes around its own RowSelect.
-->
<script lang="ts">
  import RowSelect from '$lib/components/RowSelect.svelte'

  interface Props {
    /** Told what each toggle asked for, so a test can check the shift key. */
    ontoggle?: (range: boolean) => void
  }

  let { ontoggle }: Props = $props()

  let selected = $state(false)
</script>

<table>
  <tbody>
    <tr>
      <RowSelect
        {selected}
        label="alpha"
        ontoggle={(range) => {
          selected = !selected
          ontoggle?.(range)
        }}
      />
    </tr>
  </tbody>
</table>
