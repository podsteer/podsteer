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
  import { Check, ChevronDown, ExternalLink } from '@lucide/svelte'
  import RowMenu, { type RowAction } from './RowMenu.svelte'
  import ColumnDivider from './ColumnDivider.svelte'
  import Button from './Button.svelte'
  import { escapeLayer, type EscapeClaim } from '$lib/escape'
  import { flash } from '$lib/flash.svelte'

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
     * The value leaves the application when followed.
     *
     * Marked, because it does something different from every other link in
     * the panel: those move the panel, this hands the address to the
     * operating system's own browser. The glyph is the convention for that
     * and is worth more than a tooltip nobody hovers.
     */
    external?: boolean
    /**
     * One more thing this row can do, offered in its menu.
     *
     * For what a list cannot know: revealing a Secret is a deliberate,
     * audited read whose wording depends on whether it is currently shown.
     */
    action?: RowAction
    /**
     * Lets this row's value be edited in place — a Secret key already
     * revealed, or a ConfigMap key, both already plaintext in `value`.
     *
     * DELIBERATELY THIN. DetailList has no idea what a save means or what
     * should happen after one succeeds — it renders a textarea, calls
     * `onSave` with what was typed, and shows whatever it throws. The caller
     * owns the meaning: re-revealing a Secret key through its own audited
     * path, or refreshing a ConfigMap's cached contents, happens in
     * `onSave` or after it resolves, never here.
     *
     * Absent entirely, not merely disabled, for a Secret row that has not
     * been revealed yet — editing a value nobody has looked at is the
     * mistake this ordering exists to prevent, so the caller simply does not
     * offer `edit` until a reveal has resolved.
     */
    edit?: {
      /** Persists the new value. Reject to keep the editor open with an error. */
      onSave: (value: string) => Promise<void>
    }
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
   * ones are there when they mean something.
   */
  function actionsFor(row: DetailRow, index: number): RowAction[] {
    const actions: RowAction[] = []

    const reference = row.reference ?? row.onclick
    if (reference) actions.push({ label: 'Reference', kind: 'reference', onclick: reference })

    actions.push({ label: 'Copy value', kind: 'copy', onclick: () => copy(row.value) })

    if (row.edit) {
      actions.push({ label: 'Edit value', kind: 'edit', onclick: () => startEdit(index, row) })
    }

    if (row.action) actions.push(row.action)
    return actions
  }

  // --- Inline editing -------------------------------------------------------
  //
  // ONE ROW AT A TIME, keyed by position like everything else in this list.
  // A second field kept alongside `editingIndex` (rather than an editable
  // copy of every row's value) is enough because only one editor can be open,
  // and it is cleared the same way `expanded`/`clipped` are: on a shape
  // change, so a stale editor cannot survive into a pane about a different
  // object.

  /** Which row is open for editing, or null. */
  let editingIndex = $state<number | null>(null)
  /** What the textarea holds, seeded from the row's value on open. */
  let editValue = $state('')
  let editSaving = $state(false)
  /** What `onSave` rejected with, shown beside the editor rather than as a banner. */
  let editError = $state('')

  function startEdit(index: number, row: DetailRow): void {
    editingIndex = index
    editValue = row.value
    editError = ''
  }

  /** "Written", the way RowMenu's own menu item confirms a copy in place. */
  const written = flash(1200)

  function cancelEdit(): void {
    editingIndex = null
    editValue = ''
    editError = ''
    written.cancel()
  }

  async function saveEdit(row: DetailRow): Promise<void> {
    if (!row.edit || editSaving) return

    editSaving = true
    editError = ''
    try {
      await row.edit.onSave(editValue)
      editSaving = false
      // Left to the caller to decide what "shown" means afterwards — a
      // re-reveal, a refreshed cache read — so the editor does not assume
      // editValue is now the truth. It shows the confirmation and THEN
      // closes, rather than closing immediately: a save that vanished the
      // instant it succeeded looked, on a slow connection, identical to one
      // that had done nothing at all.
      written.show(() => {
        editingIndex = null
        editValue = ''
      })
    } catch (cause) {
      editSaving = false
      editError = cause instanceof Error ? cause.message : String(cause)
    }
  }

  /** Cmd/Ctrl+Enter saves, mirroring every other multi-line save control here. */
  function onEditKeydown(event: KeyboardEvent, row: DetailRow): void {
    if (event.key !== 'Enter' || !(event.metaKey || event.ctrlKey)) return
    event.preventDefault()
    void saveEdit(row)
  }

  /**
   * Escape belongs to one layer, and while the editor is open this is the
   * innermost one open inside the drawer. See $lib/escape — the same claim
   * RowMenu takes for its own popover, so a menu opened from a keyboard
   * cannot leave two things listening for the same keystroke.
   */
  let editEscape = $state<EscapeClaim | null>(null)
  $effect(() => {
    if (editingIndex === null) return
    const held = escapeLayer()
    editEscape = held
    return () => {
      held.release()
      editEscape = null
    }
  })

  function onWindowKeydown(event: KeyboardEvent): void {
    if (event.key !== 'Escape' || editingIndex === null) return
    if (!editEscape?.owns()) return
    cancelEdit()
  }

  $effect(() => {
    if (editingIndex === null) return
    window.addEventListener('keydown', onWindowKeydown)
    return () => window.removeEventListener('keydown', onWindowKeydown)
  })

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
   * Forgets per-row state when the list becomes a different list.
   *
   * ALL OF IT IS KEYED BY POSITION, which is the cheapest thing to key on and
   * the only thing that is wrong across a change of subject. Expand row five
   * on one pod, switch to another, and row five of the new one rendered
   * already open with an inherited chevron — a row nobody had touched
   * claiming to have been.
   *
   * Keyed on the labels rather than on identity, because that is what
   * position means here: the same labels in the same order are the same rows,
   * and a caller rebuilding an equivalent array on every tick must not have
   * an open row shut under it.
   */
  const shape = $derived(rows.map((row) => row.label).join('\u0000'))
  let shapeSeen = ''

  $effect(() => {
    if (shape === shapeSeen) return
    shapeSeen = shape
    expanded = []
    clipped = []
    measured = []
    // A row's position can point at a different object entirely once the
    // list itself has changed — switching pods mid-edit must not leave an
    // open textarea quietly saving into the new row underneath it.
    cancelEdit()
  })

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

  /** Which row the pointer is over, or null. */
  let hovered = $state<number | null>(null)

  /**
   * Marks the row an event landed in, and LEAVES THE PREVIOUS ONE ALONE when
   * it landed in no row at all.
   *
   * The columns have a gap between them, so crossing from the label to the
   * value passes over the list itself. Clearing on that would flicker the
   * controls off and on again in the middle of reaching for one. Leaving the
   * list is what clears it, and that has its own handler.
   */
  function hoverRow(target: EventTarget | null): void {
    const cell = (target as HTMLElement | null)?.closest?.('[data-row]')
    if (!cell) return
    const index = Number(cell.getAttribute('data-row'))
    if (Number.isInteger(index)) hovered = index
  }

  // Nothing left running behind a component that has gone away.
  $effect(() => () => written.cancel())
</script>

<div class="relative">
  <!--
    Hover is tracked HERE, on the list, rather than by a CSS group on the row.
    A row is two grid items — the label and the value — and they are siblings
    with no element around them, so a `group` could only ever sit on one of
    them: the controls appeared when the pointer was over the value and not
    when it was over the label, which is half a row behaving differently from
    the other half for no reason a reader could see.

    Wrapping the pair in a `display: contents` element would have been the
    tidy CSS answer, and is deliberately not what this does — that element
    generates no box, and whether `:hover` matches on one is exactly the kind
    of thing that silently stops working on one of the three webviews this
    ships against. One listener on the list, and the row the pointer is over
    is then a fact rather than an inference.
  -->
  <dl
    class="detail-grid"
    bind:this={list}
    onpointerover={(event) => hoverRow(event.target)}
    onpointerleave={() => (hovered = null)}
  >
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
      data-row={index}
      data-selectable
    >
      {row.label}
    </dt>
    <!--
      `group/row` for the controls inside it, driven by `data-row-hover` from
      the list above rather than by `:hover` on this cell alone.
    -->
    <dd
      data-row={index}
      data-row-hover={hovered === index ? '' : undefined}
      class="group/row flex min-w-0 items-start gap-1 text-body-medium {row.tone === 'critical'
        ? 'text-error'
        : row.tone === 'warn'
          ? 'text-gauge-warn'
          : 'text-on-surface-variant'}"
    >
      {#if editingIndex === index}
        <!--
          THE VALUE CELL BECOMES THE EDITOR, not a dialog over it — editing a
          Secret key or a ConfigMap key is a small, local act, and a modal
          over the whole drawer would suggest it is a bigger one than it is.

          Monospace, and several rows tall by default: a certificate or a
          JSON blob is what most keys worth editing actually hold, and a
          single-line input would hide that on the first keystroke.
        -->
        <form
          class="flex min-w-0 flex-1 flex-col gap-1.5"
          onsubmit={(event) => {
            event.preventDefault()
            void saveEdit(row)
          }}
        >
          <label class="sr-only" for="detail-list-edit-{index}">Edit {row.label}</label>
          <!-- svelte-ignore a11y_autofocus -->
          <textarea
            id="detail-list-edit-{index}"
            bind:value={editValue}
            onkeydown={(event) => onEditKeydown(event, row)}
            rows="4"
            spellcheck="false"
            disabled={editSaving || written.on}
            autofocus
            class="w-full resize-y rounded-xs border border-outline-variant bg-surface px-2 py-1.5
                   font-mono text-body-small text-on-surface outline-none
                   focus:border-primary disabled:opacity-60"
            data-selectable
          ></textarea>
          {#if editError}
            <p class="text-body-small text-error" role="alert">{editError}</p>
          {/if}
          <div class="flex items-center gap-2">
            {#if written.on}
              <!-- The same confirmation RowMenu's own "Copy value" gives,
                   held on screen long enough to read before the editor
                   closes on its own — a save button that vanishes the
                   instant it is pressed looks, on a slow connection,
                   identical to one that silently did nothing. -->
              <span class="inline-flex items-center gap-1.5 text-body-medium text-success">
                <Check class="size-3.5" strokeWidth={2.5} />
                Written
              </span>
            {:else}
              <Button type="submit" variant="filled" loading={editSaving}>Save</Button>
              <Button type="button" variant="outlined" disabled={editSaving} onclick={cancelEdit}>
                Cancel
              </Button>
            {/if}
          </div>
        </form>
      {:else if open && laidOut(row.value)}
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
          <button
            type="button"
            onclick={row.onclick}
            class="resource-link inline-flex max-w-full items-baseline gap-1 text-left"
          >
            <span class="min-w-0 truncate">{row.value}</span>
            {#if row.external}
              <ExternalLink class="size-3 shrink-0 self-center" strokeWidth={1.8} />
            {/if}
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

        AND THEY APPEAR WHEN THE POINTER IS ON THE ROW. A panel of thirty
        rows carried sixty controls that were mostly not wanted, which is
        more furniture than text. Faded rather than removed from the
        document: they stay focusable, so a keyboard reaches them in order —
        `group-focus-within` shows the pair the moment one is tabbed to, and
        `focus-visible` on the control itself covers the case where the row
        has nothing else focusable.
      -->
      {#if editingIndex !== index}
      <!--
        Absent entirely while this row is being edited: Save and Cancel are
        already on screen inside the editor, and a chevron or a "More" menu
        floating beside them offers nothing the form does not already do.
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
                   text-on-surface-variant/60 opacity-0 transition-all duration-100
                   group-data-[row-hover]/row:opacity-100 group-focus-within/row:opacity-100
                   hover:text-on-surface focus-visible:opacity-100"
          >
            <ChevronDown
              class="size-3.5 transition-transform duration-150 ease-standard {open
                ? 'rotate-180'
                : ''}"
              strokeWidth={2}
            />
          </button>
        {/if}

        <RowMenu actions={actionsFor(row, index)} label={row.label} />
      </span>
      {/if}
    </dd>
  {/each}
  </dl>

  <ColumnDivider pane={list} />
</div>
