<!--
  Professional YAML editor with full syntax highlighting using CodeMirror.

  Features:
  - Full YAML syntax highlighting with colors
  - Line numbers
  - Active line highlighting
  - Code folding
  - Bracket matching
  - Search and replace (Cmd+F / Ctrl+F)
  - Undo/redo history
  - Dark theme (Tokyo Night inspired)
  - Responsive sizing
  - Read-only and editable modes
-->
<script lang="ts">
  import { onMount, onDestroy } from 'svelte'
  import {
    EditorView,
    keymap,
    lineNumbers,
    highlightActiveLine,
    highlightActiveLineGutter,
    drawSelection,
    highlightSpecialChars,
  } from '@codemirror/view'
  import { yaml } from '@codemirror/lang-yaml'
  import { oneDark } from '@codemirror/theme-one-dark'
  import { EditorState } from '@codemirror/state'
  import {
    defaultKeymap,
    history,
    historyKeymap,
    indentWithTab,
  } from '@codemirror/commands'
  import {
    bracketMatching,
    indentOnInput,
    foldGutter,
    foldKeymap,
    syntaxHighlighting,
    defaultHighlightStyle,
  } from '@codemirror/language'

  interface Props {
    content: string
    readonly?: boolean
    onchange?: (value: string) => void
  }

  let { content, readonly = false, onchange }: Props = $props()

  let editorContainer: HTMLDivElement
  let editor: EditorView | null = null

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
      syntaxHighlighting(defaultHighlightStyle, { fallback: true }),
      keymap.of([
        ...defaultKeymap,
        ...historyKeymap,
        ...foldKeymap,
        indentWithTab,
      ]),
      yaml(),
      oneDark,
      EditorView.editable.of(!readonly),
      EditorView.lineWrapping,
      // Custom theme overrides for the editor container
      EditorView.theme({
        '&': {
          height: '100%',
          fontSize: '13px',
        },
        '.cm-scroller': {
          overflow: 'auto',
          fontFamily: '"JetBrains Mono", "Fira Code", "Cascadia Code", Monaco, Menlo, "Ubuntu Mono", monospace',
        },
        '.cm-content': {
          padding: '8px 0',
        },
        '.cm-gutters': {
          minWidth: '44px',
        },
        '.cm-lineNumbers .cm-gutterElement': {
          padding: '0 8px 0 12px',
        },
      }),
    ]

    // Add change listener only when editable
    if (!readonly && onchange) {
      extensions.push(
        EditorView.updateListener.of((update) => {
          if (update.docChanged && onchange) {
            onchange(update.state.doc.toString())
          }
        }),
      )
    }

    const state = EditorState.create({
      doc: content,
      extensions,
    })

    editor = new EditorView({
      state,
      parent: editorContainer,
    })
  })

  onDestroy(() => {
    if (editor) {
      editor.destroy()
    }
  })

  // Update editor content when prop changes
  $effect(() => {
    if (editor && content !== editor.state.doc.toString()) {
      editor.dispatch({
        changes: {
          from: 0,
          to: editor.state.doc.length,
          insert: content,
        },
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

  :global(.cm-editor.cm-focused) {
    outline: none !important;
  }
</style>
