<!--
  The tick box at the start of a list row, for a bulk action.

  A cell rather than a bare input so every view draws the same one and none
  of them has to know what a click on it must NOT do: the row itself opens
  the detail drawer, and a click aimed at the checkbox stops here.

  THE BROWSER'S OWN TOGGLE IS CANCELLED. The box is a pure view of the
  selection — it follows `selected`, nothing else — because a shift-click on
  an already ticked row ADDS the range and leaves that row ticked, and a
  browser that had already flipped it unticked would then be showing the
  opposite of the truth.

  That cancelling used to be enough to break the box permanently, because the
  drawn tick WAS the input's own checked state and the browser puts that back
  after every listener has run — leaving Svelte's cached copy saying ticked, a
  DOM saying not, and every later write short-circuiting on the mismatch. The
  cancelling is load-bearing and stays; what changed is that Checkbox draws
  its tick from DOM structure the browser cannot revert. The reasoning is
  written out at the top of Checkbox.svelte; do not undo either half without
  reading it.
-->
<script lang="ts">
  import Checkbox from './Checkbox.svelte'

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
<!-- `data-edge` marks this as the left-hand control column. DataTable's own
     stylesheet is what acts on it, and only when the operator has that edge
     fixed — see $lib/fixedColumns. -->
<td
  data-edge="select"
  class="w-10 py-1.5 align-middle"
  onclick={(event) => event.stopPropagation()}
>
  <!--
    A FLEX BOX RATHER THAN AN INLINE ONE, and centred, for two reasons.

    Vertical: the control is a 16px inline-grid inside an inline-flex label,
    so left in an inline formatting context its position is decided by the
    line box's baseline and the cell font's x-height — which put it below the
    icon and text on its own row. A block-level flex box has no strut and no
    baseline to answer to, so the 16px box is simply centred in the cell, and
    the cell is centred in the row.

    Horizontal: this is a control COLUMN now, like the row menu at the other
    edge, and that one centres. Padded to one side it sat off-centre under a
    header box that was padded the same way — consistent with itself and with
    nothing else.
  -->
  <div class="flex items-center justify-center">
    <Checkbox
      checked={selected}
      ariaLabel="Select {label}"
      data-row-select=""
      onclick={(event) => {
        event.preventDefault()
        ontoggle(event.shiftKey)
      }}
    />
  </div>
</td>
