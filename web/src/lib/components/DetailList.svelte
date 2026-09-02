<!--
  The label-and-value list every detail pane states its facts in.

  ONE GRID FOR THE WHOLE DRAWER — see `detail-grid` in app.css. Every section
  of every panel puts its labels in the same column at the same width, so a
  panel reads as one thing from top to bottom rather than as a stack of lists
  that each chose their own proportions. That width is a share of the panel,
  so dragging the panel's edge widens both columns rather than only the values
  — which is what the clipping below responds to, since a wider label column
  is fewer rows that need opening at all.

  THE LABEL CARRIES THE EMPHASIS AND THE VALUE RECEDES. The labels are the
  same on every object of a kind, so they are what the eye navigates by:
  somebody looking for "Node" is looking for the word, not for the name beside
  it. Emphasising the values instead made every pane a wall of equally loud
  text with the signposts greyed out.

  BOTH COLUMNS CLIP TO ONE LINE, AND A ROW THAT IS CLIPPED CAN BE OPENED.
  This reverses what this list used to do — values wrapped by default, on the
  reasoning that a value nobody can read in full is a value nobody can act on.
  That is still true, and is why the expander exists; what wrapping cost was
  the shape of the pane. A `last-applied-configuration` annotation wrapped to
  fourteen lines and a probe string to three, so the rows a panel is scanned
  for were separated by paragraphs of reference material nobody was reading.
  Clipped, every row is one line and the list is scannable; expanded, the
  value is there in full. Nothing is hidden that cannot be asked for.

  A real <dl>, so the pairing is in the document and not only in the grid.
-->
<script lang="ts">
  import { ChevronDown } from '@lucide/svelte'
  import RowMenu, { type RowAction } from './RowMenu.svelte'
  import ColumnDivider from './ColumnDivider.svelte'

  export interface DetailRow {
    label: string
    /**
     * Already formatted, and already given its own em dash if it is missing.
     * Callers know what an absent value means for their field; this does not.
     *
     * On a `control` row this is the value in words rather than what is
     * rendered — it becomes the cell's tooltip, so a masked value still says
     * where it comes from.
     */
    value: string
    /**
     * Makes the value a link to the object it names.
     *
     * Offered rather than assumed: a Node row is worth following, and the
     * same row on a cluster whose nodes this account cannot list is not. The
     * caller decides, because only it knows whether there is anywhere to go.
     * See $lib/reference.
     */
    onclick?: () => void
    /**
     * Colours the value when it carries a verdict rather than a fact.
     *
     * A pod condition is the case this exists for: PodScheduled=False is not
     * the same kind of statement as Node=node-1, and a list that renders them
     * identically loses the only thing anybody scans a condition list for.
     * Left off for everything else, because a list where most rows are
     * coloured is a list where none of them are.
     */
    tone?: 'warn' | 'critical'
    /**
     * What the value is, when the value alone does not say.
     *
     * For a value that has been RESOLVED from somewhere else: an environment
     * variable read out of a ConfigMap shows the contents, which is what
     * somebody wants — and then nothing on screen says where it came from, or
     * why following it opens a ConfigMap.
     */
    title?: string
    /**
     * Where the value came from, when the value alone does not say it.
     *
     * The value's own tooltip rather than an icon: a value resolved out of a
     * ConfigMap or lifted off the pod's own metadata no longer names its
     * source — that is the point of resolving it — but a third control on
     * every row to say so was more to learn than it was worth.
     */
    info?: string
    /**
     * The resource this row refers to, reachable from its menu.
     *
     * Distinct from `onclick`, which makes the VALUE a link and is right when
     * the value IS the name — a node, an owner. When the value is the
     * contents of a ConfigMap, the ConfigMap is still worth reaching and the
     * contents are not a link to it.
     */
    reference?: () => void
    /**
     * One more thing this row can do, offered in its menu.
     *
     * For what a list cannot know: revealing a Secret is a deliberate,
     * audited read whose wording depends on whether it is currently shown.
     */
    action?: RowAction
  }

  interface Props {
    rows: DetailRow[]
  }

  let { rows }: Props = $props()

  /**
   * What one row's menu offers.
   *
   * Copy is on every row, because every row has a value and copying it is the
   * thing an operator does with a panel more than anything else. The other
   * two are there when they mean something.
   */
  function actionsFor(row: DetailRow): RowAction[] {
    const actions: RowAction[] = []

    const reference = row.reference ?? row.onclick
    if (reference) actions.push({ label: 'Reference', kind: 'reference', onclick: reference })

    actions.push({ label: 'Copy value', kind: 'copy', onclick: () => copy(row.value) })

    if (row.action) actions.push(row.action)
    return actions
  }

  /**
   * Copies a value.
   *
   * Deliberately silent about failure. The webview's clipboard can refuse —
   * it is a permissioned API — and a panel that raises an error banner
   * because a copy did not take is worse than one that simply did not copy:
   * the text is on screen and selectable either way.
   */
  function copy(value: string): void {
    void navigator.clipboard?.writeText(value).catch(() => {})
  }

  let list = $state<HTMLElement | null>(null)
  /**
   * The cells themselves, for asking the browser what did not fit.
   *
   * Reactive, because `bind:this` into a plain array assigns without telling
   * anything — the measurement would then run against whatever was bound on
   * the previous render.
   */
  let labelCells = $state<HTMLElement[]>([])
  let valueCells = $state<HTMLElement[]>([])

  let expanded = $state<number[]>([])
  /** Which rows lost something to the clip, and so are worth opening. */
  let clipped = $state<boolean[]>([])
  /**
   * The same, held outside the reactive graph.
   *
   * `measure` needs the previous answer for a row that is currently open —
   * an open row wraps, so the browser reports it as fitting, and measuring it
   * would remove the control that closes it again. Reading the $state version
   * would make the effect depend on what it writes.
   */
  let measured: boolean[] = []

  /**
   * The same value, laid out, when it is JSON.
   *
   * Annotations are the reason: `last-applied-configuration` and most of what
   * operators write is a single line of minified JSON, and a single line of
   * minified JSON opened to its full width is no more readable than it was
   * clipped — it is just longer. Indented, it is a document.
   *
   * Objects and arrays only. `"true"`, `"3"` and `"null"` are all valid JSON
   * and formatting them changes nothing, so a value that merely parses is not
   * enough; it has to have structure to lay out.
   *
   * PARSED AND RE-PRINTED, which is worth being explicit about: the text
   * shown is this application's rendering of the value, not the value's own
   * bytes. Key order is preserved and nothing is dropped, but whitespace is
   * ours. The YAML tab remains the place the object is quoted exactly.
   */
  function laidOut(value: string): string | null {
    const text = value.trim()
    if (!text.startsWith('{') && !text.startsWith('[')) return null

    try {
      const parsed: unknown = JSON.parse(text)
      if (parsed === null || typeof parsed !== 'object') return null
      return JSON.stringify(parsed, null, 2)
    } catch {
      // Not JSON, or JSON this browser will not parse. Either way it is text.
      return null
    }
  }

  /** Whether the browser had to cut something off to fit the column. */
  function cut(cell: HTMLElement | undefined): boolean {
    return !!cell && cell.scrollWidth > cell.clientWidth + 1
  }

  function measure(): void {
    measured = rows.map((_, index) =>
      expanded.includes(index)
        ? (measured[index] ?? true)
        : cut(labelCells[index]) || cut(valueCells[index]),
    )
    clipped = measured
  }

  function toggle(index: number): void {
    expanded = expanded.includes(index)
      ? expanded.filter((open) => open !== index)
      : [...expanded, index]
  }

  // Re-measured when the content changes, when a row is opened or closed, and
  // when the drawer is resized — all three change what fits, and only the
  // first is visible to Svelte on its own.
  $effect(() => {
    rows
    expanded
    measure()

    if (!list) return
    const observer = new ResizeObserver(() => measure())
    observer.observe(list)
    return () => observer.disconnect()
  })
</script>

<div class="relative">
  <dl class="detail-grid" bind:this={list}>
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
    {@const open = expanded.includes(index)}
    <!--
      break-all on an opened label, because a label column also holds
      annotation keys, and an identifier has no word boundaries to break at:
      kubectl.kubernetes.io/last-applied-configuration is one "word", so
      break-words leaves it overflowing while break-all wraps it where it
      must.

      Selectable, for labels and annotations where the key is half of what
      somebody came to copy.
    -->
    <dt
      bind:this={labelCells[index]}
      class="min-w-0 text-body-medium text-on-surface {open ? 'break-all' : 'truncate'}"
      data-selectable
    >
      {row.label}
    </dt>
    <dd
      class="flex min-w-0 items-start gap-1 text-body-medium {row.tone === 'critical'
        ? 'text-error'
        : row.tone === 'warn'
          ? 'text-gauge-warn'
          : 'text-on-surface-variant'}"
    >
      {#if open && laidOut(row.value)}
        <!--
          Laid out, in the monospace face indentation needs to mean anything.
          `pre-wrap` rather than `pre`: a long string value inside the JSON
          would otherwise scroll the pane sideways, and a detail panel that
          scrolls horizontally has lost.
        -->
        <pre
          class="min-w-0 flex-1 font-mono text-body-small leading-relaxed break-words
                 whitespace-pre-wrap"
          data-selectable>{laidOut(row.value)}</pre>
      {:else}
      <span
        bind:this={valueCells[index]}
        class="min-w-0 flex-1 {open ? 'break-words' : 'truncate'}"
        title={row.info ?? row.title}
        data-selectable
      >
        {#if row.onclick}
          <!-- A button, not an anchor: this navigates within the application
               and has no address. Styled as a link because that is what it
               behaves like, and because a value that is followable should
               look different from one that is not. It sets no width of its
               own — the span around it is what clips, so a link and a plain
               value are cut off in the same place. -->
          <button type="button" onclick={row.onclick} class="resource-link text-left">
            {row.value}
          </button>
        {:else}
          {row.value}
        {/if}
      </span>
      {/if}

      <!--
        ONE CLUSTER, AT THE END OF THE VALUE COLUMN, IN ONE ORDER: what the
        row can do, what it is, whether it fits. They used to be scattered —
        a reveal button wherever the masked value happened to end, an
        expander at the far right — so a column of rows had controls at three
        different distances from the edge and no two rows agreed.

        `ml-auto` on the group rather than a column of its own, because the
        cluster is one to three buttons wide depending on the row, and a
        fixed column would leave a gap on every row that has fewer.
      -->
      <!--
        TWO CONTROLS, THE SAME TWO ON EVERY ROW: whether it fits, and what it
        can do. It was a cluster of up to three icons that changed from row to
        row — a reveal here, an information note there — which had to be
        learnt one at a time and read as clutter in a column of values.

        COLOUR IS THE ONLY HOVER. They used to take a filled circle and a
        state layer as well, which on a small glyph reads as the icon itself
        thickening — three effects where one says the same thing, in a column
        whose whole job is the text beside them.
      -->
      <span class="ml-auto flex shrink-0 items-center gap-0.5">

        <!--
          Only on rows that lost something. A chevron on every row is a column
          of controls that mostly do nothing, and it would be the loudest thing
          in a pane whose whole job is the text beside it.
        -->
        {#if clipped[index]}
          <button
            type="button"
            onclick={() => toggle(index)}
            aria-expanded={open}
            aria-label={open ? `Collapse ${row.label}` : `Expand ${row.label}`}
            title={open ? 'Show less' : 'Show the whole value'}
            class="grid size-5 shrink-0 cursor-pointer place-items-center rounded-full
                   text-on-surface-variant/60 transition-colors duration-100
                   hover:text-on-surface"
          >
            <ChevronDown
              class="size-3.5 transition-transform duration-150 ease-standard {open
                ? 'rotate-180'
                : ''}"
              strokeWidth={2}
            />
          </button>
        {/if}

        <RowMenu actions={actionsFor(row)} label={row.label} />
      </span>
    </dd>
  {/each}
  </dl>

  <ColumnDivider pane={list} />
</div>
