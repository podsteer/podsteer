<!--
  The label-and-value list every detail pane states its facts in.

  A quarter for the labels and the rest for the values, both aligned left.
  Values are of wildly different lengths — "Warning" beside a sixty-character
  event name, "Running" beside a UID — and right-aligning them against a
  ragged left edge leaves nothing to read down.

  THE LABEL CARRIES THE EMPHASIS AND THE VALUE RECEDES, which is the opposite
  of what several of these panes used to do. The labels are the same on every
  object of a kind, so they are what the eye navigates by: somebody looking
  for "Node" is looking for the word, not for the name beside it. Emphasising
  the values instead made every pane a wall of equally loud text with the
  signposts greyed out.

  A real <dl>, so the pairing is in the document and not only in the grid.
  Two columns of stacked label-over-value looked similar and read worse — a
  four-item section became a 2×2 block where the reading order was ambiguous,
  and nothing lined up down the pane.
-->
<script lang="ts">
  export interface DetailRow {
    label: string
    /**
     * Already formatted, and already given its own em dash if it is missing.
     * Callers know what an absent value means for their field; this does not.
     */
    value: string
    /**
     * Whether the value wraps or is cut off at one line.
     *
     * Wrapping by default, because a value that cannot be read in full is a
     * value nobody can act on. `truncate` is for the ones whose length is
     * noise rather than content — a UID, a long annotation — where the point
     * is that the field is present and copying it is what anybody wants
     * anyway.
     */
    truncate?: boolean
    /**
     * Makes the value a link to the object it names.
     *
     * Offered rather than assumed: a Node row is worth following, and the
     * same row on a cluster whose nodes this account cannot list is not. The
     * caller decides, because only it knows whether there is anywhere to go.
     */
    onclick?: () => void
  }

  interface Props {
    rows: DetailRow[]
  }

  let { rows }: Props = $props()
</script>

<dl class="grid grid-cols-[25%_1fr] gap-x-4 gap-y-2">
  {#each rows as row (row.label)}
    <!--
      break-words on the LABEL as well, because these are not always the short
      words the events pane's are. A label column also holds annotation keys
      like kubectl.kubernetes.io/last-applied-configuration, and CSS does not
      break at a dot or a slash on its own — left alone one of those overflows
      its column and lands on top of the value beside it.

      Selectable too: for labels and annotations the key is half of what
      somebody came to copy.
    -->
    <dt class="min-w-0 break-words text-body-medium text-on-surface" data-selectable>
      {row.label}
    </dt>
    <dd
      class="min-w-0 text-body-medium text-on-surface-variant {row.truncate
        ? 'truncate'
        : 'break-words'}"
      data-selectable
    >
      {#if row.onclick}
        <!-- A button, not an anchor: this navigates within the application
             and has no address. Styled as a link because that is what it
             behaves like, and because a value that is followable should look
             different from one that is not. -->
        <button type="button" onclick={row.onclick} class="resource-link max-w-full truncate text-left">
          {row.value}
        </button>
      {:else}
        {row.value}
      {/if}
    </dd>
  {/each}
</dl>
