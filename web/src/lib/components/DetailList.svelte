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
    /**
     * A wider label column, for lists whose labels are identifiers.
     *
     * A quarter suits "Liveness" and "Service account". It does not suit
     * PLT__MONGODB_USERNAME or kubectl.kubernetes.io/last-applied-configuration,
     * which wrap mid-word into two unreadable halves — the column is not
     * narrow because the value needs the room, it is narrow because most
     * labels are short. Lists whose labels are names ask for more.
     */
    wideLabels?: boolean
  }

  let { rows, wideLabels = false }: Props = $props()
</script>

<dl class="grid {wideLabels ? 'grid-cols-[40%_1fr]' : 'grid-cols-[25%_1fr]'} gap-x-4 gap-y-2">
  <!--
    KEYED BY POSITION, NOT BY LABEL. A label is not unique and was never going
    to be: kubectl prints several "Mounts:" lines for one container and several
    "Tolerations:" for one pod, and this list renders them the same way. Keying
    by label made Svelte throw on the first pod with two volume mounts — which
    is every pod, because the service-account token is one of them.

    Position is the right key here anyway. `rows` is derived and rebuilt whole
    on each change, so there is no row identity to preserve across renders.
  -->
  {#each rows as row, index (index)}
    <!--
      break-words on the LABEL as well, because these are not always the short
      words the events pane's are. A label column also holds annotation keys
      like kubectl.kubernetes.io/last-applied-configuration, and CSS does not
      break at a dot or a slash on its own — left alone one of those overflows
      its column and lands on top of the value beside it.

      Selectable too: for labels and annotations the key is half of what
      somebody came to copy.
    -->
    <!--
      break-all rather than break-words on a wide-label list. An identifier
      has no word boundaries to break at — PLT__MONGODB_USERNAME is one
      "word" — so break-words leaves it overflowing while break-all wraps it
      where it must. On ordinary labels break-all would hyphenate English
      mid-word, which is why it is not the default.
    -->
    <dt
      class="min-w-0 text-body-medium text-on-surface {wideLabels
        ? 'break-all'
        : 'break-words'}"
      data-selectable
    >
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
