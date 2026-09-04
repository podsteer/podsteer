<!--
  The tick box at the start of a list row, for a bulk action.

  A cell rather than a bare input so every view draws the same one and none
  of them has to know what a click on it must NOT do: the row itself opens
  the detail drawer, and a click aimed at the checkbox stops here.

  THE BROWSER'S OWN TOGGLE IS CANCELLED. The box is a pure view of the
  selection — `checked` follows `selected`, nothing else — because a
  shift-click on an already ticked row ADDS the range and leaves that row
  ticked, and a browser that had already flipped it unticked would then be
  showing the opposite of the truth with nothing to correct it: Svelte only
  writes `checked` when the value changes, and it did not.
-->
<script lang="ts">
  interface Props {
    selected: boolean
    /** Names the row, for the box's accessible label. */
    label: string
    /** `range` is a shift-click: select everything between the last click and this one. */
    ontoggle: (range: boolean) => void
  }

  let { selected, label, ontoggle }: Props = $props()
</script>

<!-- Stops the click here: the row itself opens the detail drawer. -->
<td class="w-10 py-1.5 pr-1 pl-5" onclick={(event) => event.stopPropagation()}>
  <input
    type="checkbox"
    data-row-select
    checked={selected}
    aria-label="Select {label}"
    class="size-3.5 cursor-pointer align-middle accent-primary"
    onclick={(event) => {
      event.preventDefault()
      ontoggle(event.shiftKey)
    }}
  />
</td>
