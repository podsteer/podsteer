<!--
  A YAML manifest, read or edited.

  CodeMirror with YAML highlighting, line numbers, folding and bracket
  matching — the same component in the drawer's YAML tab and in the edit
  dialog, so that reading a manifest and changing one look like the same act.
  The dialog used to be a bare textarea, which made editing feel like leaving
  the application.

  EVERY COLOUR HERE IS A CSS VARIABLE, and that is the whole design.

  The editor previously shipped One Dark, a fixed palette that stayed dark on
  a light theme — a black rectangle sitting in a white panel. The obvious
  repair is to keep two palettes and swap them when the theme changes, but
  that means the component has to know the theme, subscribe to it, and rebuild
  its extensions on every change. Referencing the same custom properties the
  rest of the application uses avoids all of it: the variables are redefined
  on :root by the theme, so the editor repaints with everything else, in the
  same frame, with no reactivity and nothing to keep in step. It follows a
  theme that did not exist when this was written for free.

  The editor's own surface is transparent for the same reason: whatever it is
  dropped into decides the background, so it cannot disagree with its
  container the way a hardcoded one did.

  The GUTTER is the exception, and has to be opaque. It is `position: sticky`,
  so when wrapping is off and the content scrolls sideways the text passes
  underneath it — and a transparent gutter lets that text show through the
  line numbers. Every CodeMirror theme paints it for this reason; leaving it
  transparent looked correct until something scrolled horizontally, which is
  why it survived until the search box started jumping to matches. Hence
  `surface`: the one colour this component cannot infer and must be told.
-->
<script lang="ts">
  import { onMount, onDestroy } from 'svelte'
  import {
    Decoration,
    type DecorationSet,
    EditorView,
    ViewPlugin,
    type ViewUpdate,
    keymap,
    lineNumbers,
    highlightActiveLine,
    highlightActiveLineGutter,
    drawSelection,
    highlightSpecialChars,
  } from '@codemirror/view'
  import { yaml } from '@codemirror/lang-yaml'
  import { Compartment, EditorState, StateEffect, StateField } from '@codemirror/state'
  import { defaultKeymap, history, historyKeymap, indentWithTab } from '@codemirror/commands'
  import {
    bracketMatching,
    indentOnInput,
    foldGutter,
    foldKeymap,
    syntaxHighlighting,
    HighlightStyle,
  } from '@codemirror/language'
  import { tags } from '@lezer/highlight'
  import { preferences } from '$stores/preferences.svelte'
  import { findMatches } from '$lib/textSearch'

  interface Props {
    content: string
    readonly?: boolean
    onchange?: (value: string) => void
    /** Text to find and highlight. Empty clears the highlighting. */
    query?: string
    /**
     * The colour behind the editor, painted into the sticky gutter.
     *
     * It has to match whatever the editor sits on, and nothing in CSS can
     * work that out from here — `background-color: inherit` copies a
     * transparent parent's transparency. Both current hosts are
     * surface-container-lowest (the drawer body, and the dialog's field), so
     * that is the default; a host on another surface passes its own.
     */
    surface?: string
    /** Hands the parent the controls it cannot reach from outside. */
    onready?: (api: EditorApi) => void
  }

  export interface EditorApi {
    /** Moves the selection to the next match, wrapping at the end. */
    findNext: () => void
    /** Moves the selection to the previous match, wrapping at the start. */
    findPrevious: () => void
    /** Selects the text between two character offsets and scrolls it into
        view — for seeding a fresh document with the caret already on the
        field somebody is expected to fill in, rather than at offset zero. */
    select: (from: number, to: number) => void
  }

  let {
    content,
    readonly = false,
    onchange,
    query = '',
    surface = 'var(--surface-container-lowest)',
    onready,
  }: Props = $props()

  let editorContainer: HTMLDivElement
  let editor: EditorView | null = null

  /**
   * Line wrapping, reconfigurable without rebuilding the editor.
   *
   * A compartment is the only way CodeMirror lets one extension be swapped in
   * place. Recreating the view instead would work and would also throw away
   * the undo history, the fold state and the caret every time somebody
   * toggled the toolbar button.
   */
  const wrapping = new Compartment()

  /**
   * Read-only, reconfigurable.
   *
   * `editable` and `readOnly` are ordinary extensions, fixed at construction
   * unless they sit in a compartment — so toggling the prop on a live editor
   * did nothing at all, and the pane stayed read-only however the button
   * looked. It only appeared to work while editing meant mounting a fresh
   * editor in a dialog.
   */
  const editability = new Compartment()

  /** The extensions that express one readonly state. */
  function editableExtensions(isReadonly: boolean) {
    return [EditorState.readOnly.of(isReadonly), EditorView.editable.of(!isReadonly)]
  }

  /**
   * Find, without @codemirror/search.
   *
   * The package was the obvious choice and turns out to buy almost nothing
   * here: its highlighter returns no decorations unless CodeMirror's OWN
   * search panel is open, and this toolbar deliberately replaces that panel.
   * The matching that would be left — a case-insensitive substring scan over
   * a document of a few hundred lines — is the thirty lines below, and not
   * worth another entry in the shipped licence inventory.
   *
   * The matching itself lives in $lib/textSearch, because the toolbar needs
   * the same answer to show a count.
   */
  const setQuery = StateEffect.define<string>()

  const queryField = StateField.define<string>({
    create: () => '',
    update(value, transaction) {
      for (const effect of transaction.effects) if (effect.is(setQuery)) return effect.value
      return value
    },
  })

  const matchMark = Decoration.mark({ class: 'cm-yaml-match' })
  const currentMark = Decoration.mark({ class: 'cm-yaml-match cm-yaml-match-current' })

  /**
   * Decorates matches in the visible ranges only.
   *
   * Scanning the whole document on every keystroke would be wasted work on
   * lines nobody is looking at, and CodeMirror re-runs this as the viewport
   * moves, so scrolling reveals the rest.
   */
  const highlighter = ViewPlugin.fromClass(
    class {
      decorations: DecorationSet

      constructor(view: EditorView) {
        this.decorations = this.build(view)
      }

      update(update: ViewUpdate) {
        if (
          update.docChanged ||
          update.viewportChanged ||
          update.selectionSet ||
          update.startState.field(queryField) !== update.state.field(queryField)
        ) {
          this.decorations = this.build(update.view)
        }
      }

      build(view: EditorView): DecorationSet {
        const needle = view.state.field(queryField)
        if (!needle) return Decoration.none

        const ranges = []
        const head = view.state.selection.main
        for (const { from, to } of view.visibleRanges) {
          const text = view.state.sliceDoc(from, to)
          for (const [start, end] of findMatches(text, needle, from)) {
            const isCurrent = head.from === start && head.to === end
            ranges.push((isCurrent ? currentMark : matchMark).range(start, end))
          }
        }
        // Decoration sets must be sorted by position.
        ranges.sort((a, b) => a.from - b.from)
        return Decoration.set(ranges)
      }
    },
    { decorations: (plugin) => plugin.decorations },
  )

  /**
   * Where each match sits vertically, as a fraction of the whole document.
   *
   * Taken from CodeMirror's own layout (`lineBlockAt().top / contentHeight`)
   * rather than from line NUMBERS, because with wrapping on a line is not a
   * fixed height — a 300-character annotation occupies six rows and a `kind:`
   * one, so a marker placed by line index would drift further down the file
   * the more wrapped lines it passed.
   *
   * Positions within half a per cent of each other collapse into one: at a
   * few hundred pixels of track, drawing 120 separate ticks for 120 matches
   * paints a solid bar that says nothing about where they are.
   */
  let markers = $state<number[]>([])

  function updateMarkers(): void {
    if (!editor) {
      markers = []
      return
    }
    const needle = editor.state.field(queryField)
    const height = editor.contentHeight
    if (!needle || height <= 0) {
      markers = []
      return
    }

    const seen = new Set<number>()
    const out: number[] = []
    for (const [start] of findMatches(editor.state.doc.toString(), needle)) {
      const fraction = editor.lineBlockAt(start).top / height
      const key = Math.round(fraction * 200)
      if (seen.has(key)) continue
      seen.add(key)
      out.push(fraction)
    }
    markers = out
  }

  /** All matches in the whole document, for counting and for stepping. */
  function allMatches(view: EditorView): Array<[number, number]> {
    const needle = view.state.field(queryField)
    if (!needle) return []
    return findMatches(view.state.doc.toString(), needle)
  }

  /**
   * Steps to the next or previous match, wrapping at either end.
   *
   * Wrapping rather than stopping: a search that goes quiet at the last match
   * looks broken, and the alternative is making somebody scroll back to the
   * top to carry on.
   */
  function step(direction: 1 | -1): void {
    if (!editor) return
    const found = allMatches(editor)
    if (found.length === 0) return

    const from = editor.state.selection.main.from
    let index: number
    if (direction === 1) {
      index = found.findIndex(([start]) => start > from)
      if (index === -1) index = 0
    } else {
      index = found.map(([start]) => start).filter((start) => start < from).length - 1
      if (index < 0) index = found.length - 1
    }

    const [start, end] = found[index]
    editor.dispatch({
      selection: { anchor: start, head: end },
      effects: EditorView.scrollIntoView(start, { y: 'center' }),
    })
  }

  /**
   * The chrome: everything that is not the text itself.
   *
   * A function rather than a value so that reading `surface` is an explicit
   * snapshot taken at mount, which is when the editor is configured, rather
   * than a prop captured at init by accident.
   *
   * `color-mix` rather than fixed tints, so a selection is the theme's own
   * primary at low strength in both schemes instead of a blue that only
   * works against one of them.
   */
  function buildTheme() {
      return EditorView.theme({
      '&': {
        height: '100%',
        fontSize: '13px',
        // Inherited from whatever hosts the editor. See the note above.
        backgroundColor: 'transparent',
        color: 'var(--on-surface)',
      },
      '&.cm-focused': { outline: 'none' },
      '.cm-scroller': {
        overflow: 'auto',
        fontFamily: 'var(--font-mono)',
        lineHeight: '1.6',
      },
      '.cm-content': {
        padding: '8px 0',
        caretColor: 'var(--primary)',
        // The application sets `user-select: none` on the body, which a
        // read-only editor (contenteditable=false) would otherwise inherit —
        // leaving a manifest that cannot be copied, which is most of the point
        // of showing it.
        userSelect: 'text',
        cursor: 'text',
      },
      '.cm-cursor, .cm-dropCursor': { borderLeftColor: 'var(--primary)' },

      // Opaque, and deliberately so — see the note at the top of this file.
      '.cm-gutters': {
        minWidth: '44px',
        backgroundColor: surface,
        color: 'var(--code-punctuation)',
        border: 'none',
      },
      '.cm-lineNumbers .cm-gutterElement': { padding: '0 8px 0 12px' },
      '.cm-foldGutter .cm-gutterElement': { color: 'var(--code-punctuation)' },

      // A wash rather than a band: the line the caret is on should be findable
      // without the highlight competing with the text sitting on it.
      '.cm-activeLine': {
        backgroundColor: 'color-mix(in oklab, var(--on-surface) 5%, transparent)',
      },
      '.cm-activeLineGutter': {
        backgroundColor: 'color-mix(in oklab, var(--on-surface) 5%, transparent)',
        color: 'var(--on-surface-variant)',
      },

      '&.cm-focused .cm-selectionBackground, .cm-selectionBackground, ::selection': {
        backgroundColor: 'color-mix(in oklab, var(--primary) 28%, transparent)',
      },
      '&.cm-focused .cm-matchingBracket': {
        backgroundColor: 'color-mix(in oklab, var(--primary) 22%, transparent)',
        outline: 'none',
      },
      '&.cm-focused .cm-nonmatchingBracket': {
        backgroundColor: 'color-mix(in oklab, var(--error) 22%, transparent)',
      },

      // Amber for a match and a solid amber for the one you are on — the same
      // two-tier reading the gauges use, rather than the yellow-on-light and
      // cyan-on-dark CodeMirror ships, which belong to neither theme here.
      '.cm-yaml-match': {
        backgroundColor: 'color-mix(in oklab, var(--gauge-warn) 30%, transparent)',
        borderRadius: '2px',
      },
      '.cm-yaml-match-current': {
        backgroundColor: 'color-mix(in oklab, var(--gauge-warn) 70%, transparent)',
      },

    })
  }

  /**
   * The text itself.
   *
   * Sparse on purpose. The YAML grammar reports every unquoted scalar and
   * every quoted string as `content`, so a palette that gave numbers and
   * booleans their own colours would be asserting a distinction the parser
   * cannot actually make. What is left is the distinction that matters when
   * reading a manifest: keys against values.
   */
  const highlighting = HighlightStyle.define([
    { tag: tags.definition(tags.propertyName), color: 'var(--code-key)', fontWeight: '500' },
    { tag: tags.propertyName, color: 'var(--code-key)' },
    { tag: tags.lineComment, color: 'var(--code-comment)', fontStyle: 'italic' },
    {
      tag: [tags.separator, tags.punctuation, tags.squareBracket, tags.brace],
      color: 'var(--code-punctuation)',
    },
    // Anchors, aliases, tags and directives: statements about the document
    // rather than data in it, so they read as a class of their own.
    {
      tag: [tags.labelName, tags.typeName, tags.keyword, tags.meta, tags.attributeValue],
      color: 'var(--code-meta)',
    },
    { tag: tags.special(tags.string), color: 'var(--code-meta)' },
    { tag: [tags.content, tags.string], color: 'var(--on-surface)' },
    { tag: tags.invalid, color: 'var(--error)' },
  ])

  onMount(() => {
    const extensions = [
      lineNumbers(),
      highlightActiveLineGutter(),
      highlightActiveLine(),
      highlightSpecialChars(),
      drawSelection(),
      history(),
      bracketMatching(),
      indentOnInput(),
      foldGutter(),
      syntaxHighlighting(highlighting),
      queryField,
      highlighter,
      keymap.of([...defaultKeymap, ...historyKeymap, ...foldKeymap, indentWithTab]),
      yaml(),
      buildTheme(),
      editability.of(editableExtensions(readonly)),
      wrapping.of(preferences.wrapLines ? EditorView.lineWrapping : []),
    ]

    // One listener for both jobs. Geometry changes matter as much as document
    // changes here: re-wrapping moves every marker below the line that
    // reflowed, and so does the editor being resized.
    extensions.push(
      EditorView.updateListener.of((update) => {
        if (update.docChanged && !readonly) onchange?.(update.state.doc.toString())
        if (update.docChanged || update.geometryChanged || update.viewportChanged) {
          updateMarkers()
        }
      }),
    )

    editor = new EditorView({
      state: EditorState.create({ doc: content, extensions }),
      parent: editorContainer,
    })

    onready?.({
      findNext: () => step(1),
      findPrevious: () => step(-1),
      select: (from, to) => {
        if (!editor) return
        editor.focus()
        editor.dispatch({
          selection: { anchor: from, head: to },
          effects: EditorView.scrollIntoView(from, { y: 'center' }),
        })
      },
    })

    updateMarkers()
  })

  onDestroy(() => {
    editor?.destroy()
  })

  /**
   * Moves to the first match as the query is typed, not only on Enter.
   *
   * Anchored at the START of the current selection rather than at the top of
   * the document: typing `n`, `na`, `nam` should refine in place, and
   * searching from the top each time would drag the view back up on every
   * keystroke — while searching from the END of the selection would walk
   * forward through the file as the word grew.
   */
  $effect(() => {
    const needle = query
    if (!editor) return

    editor.dispatch({ effects: setQuery.of(needle) })

    if (!needle) {
      updateMarkers()
      return
    }

    const found = findMatches(editor.state.doc.toString(), needle)
    if (found.length > 0) {
      const anchorAt = editor.state.selection.main.from
      const next = found.find(([start]) => start >= anchorAt) ?? found[0]
      editor.dispatch({
        selection: { anchor: next[0], head: next[1] },
        effects: EditorView.scrollIntoView(next[0], { y: 'center' }),
      })
    }
    updateMarkers()
  })

  $effect(() => {
    const isReadonly = readonly
    editor?.dispatch({ effects: editability.reconfigure(editableExtensions(isReadonly)) })
  })

  $effect(() => {
    const wrap = preferences.wrapLines
    if (!editor) return

    editor.dispatch({
      effects: wrapping.reconfigure(wrap ? EditorView.lineWrapping : []),
    })

    // Re-measured after the reflow, not during it. CodeMirror sizes wrapped
    // lines in a later measure pass, so markers recomputed in the same tick
    // are placed against the OLD geometry — which showed up as a marker that
    // did not return to where it started after wrap was switched off and on.
    editor.requestMeasure({ read: () => updateMarkers() })
  })

  // Only when it genuinely differs, or every keystroke in an editable
  // instance would replace the document the caret is sitting in.
  $effect(() => {
    if (editor && content !== editor.state.doc.toString()) {
      editor.dispatch({
        changes: { from: 0, to: editor.state.doc.length, insert: content },
      })
    }
  })
</script>

<div class="relative h-full w-full overflow-hidden">
  <div bind:this={editorContainer} class="h-full w-full"></div>

  <!-- An overview ruler down the right edge, showing where the matches are.
       Not interactive: it answers "how many, and roughly where" at a glance,
       and clicking it would be a second, worse way of doing what Enter
       already does. `pointer-events-none` keeps it clear of the scrollbar
       underneath, which is the thing somebody actually reaches for. -->
  {#if markers.length > 0}
    <div class="pointer-events-none absolute inset-y-0 right-0 w-2.5" aria-hidden="true">
      {#each markers as fraction (fraction)}
        <span
          class="absolute right-0.5 h-0.5 w-1.5 rounded-full bg-gauge-warn"
          style="top: {(fraction * 100).toFixed(3)}%"
        ></span>
      {/each}
    </div>
  {/if}
</div>

<style>
  :global(.cm-editor) {
    height: 100% !important;
  }
</style>
