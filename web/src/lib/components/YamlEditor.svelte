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

  The surface is transparent for the same reason. Whatever the editor is
  dropped into decides the background, so it cannot disagree with its
  container the way a hardcoded one did.
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
    /** Hands the parent the controls it cannot reach from outside. */
    onready?: (api: EditorApi) => void
  }

  export interface EditorApi {
    /** Moves the selection to the next match, wrapping at the end. */
    findNext: () => void
    /** Moves the selection to the previous match, wrapping at the start. */
    findPrevious: () => void
  }

  let { content, readonly = false, onchange, query = '', onready }: Props = $props()

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
   * `color-mix` rather than fixed tints, so a selection is the theme's own
   * primary at low strength in both schemes instead of a blue that only
   * works against one of them.
   */
  const theme = EditorView.theme({
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

    '.cm-gutters': {
      minWidth: '44px',
      backgroundColor: 'transparent',
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
      theme,
      EditorState.readOnly.of(readonly),
      EditorView.editable.of(!readonly),
      wrapping.of(preferences.wrapLines ? EditorView.lineWrapping : []),
    ]

    if (!readonly && onchange) {
      extensions.push(
        EditorView.updateListener.of((update) => {
          if (update.docChanged) onchange(update.state.doc.toString())
        }),
      )
    }

    editor = new EditorView({
      state: EditorState.create({ doc: content, extensions }),
      parent: editorContainer,
    })

    onready?.({
      findNext: () => step(1),
      findPrevious: () => step(-1),
    })
  })

  onDestroy(() => {
    editor?.destroy()
  })

  $effect(() => {
    const needle = query
    editor?.dispatch({ effects: setQuery.of(needle) })
  })

  $effect(() => {
    const wrap = preferences.wrapLines
    editor?.dispatch({
      effects: wrapping.reconfigure(wrap ? EditorView.lineWrapping : []),
    })
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

<div class="h-full w-full overflow-hidden">
  <div bind:this={editorContainer} class="h-full w-full"></div>
</div>

<style>
  :global(.cm-editor) {
    height: 100% !important;
  }
</style>
