import { readFileSync, readdirSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'

const HERE = dirname(fileURLToPath(import.meta.url))
const PAGES = join(HERE, '..', '..', 'pages')

function sources(dir: string): { name: string; text: string }[] {
  return readdirSync(dir)
    .filter((name) => name.endsWith('.svelte'))
    .map((name) => ({ name, text: readFileSync(join(dir, name), 'utf8') }))
}

/** Every `class="…"` in one file, newlines and all. */
function classAttributes(text: string): string[] {
  return [...text.matchAll(/class="([^"]*)"/g)].map((match) => match[1])
}

describe('how a dialog is centred', () => {
  it('is never by transform, because a transform breaks every fixed child inside it', () => {
    // THE BUG THIS TEST EXISTS FOR, twice over. A transformed element becomes
    // the containing block for `position: fixed` descendants, so anything
    // inside a dialog centred with `-translate-1/2` — a dropdown, a hint
    // panel, a portalled menu — is positioned against the DIALOG while its
    // code says it is positioned against the window, and it lands in the
    // corner. SettingsDialog hit it with a dropdown and switched to
    // `inset-0 m-auto`; sixteen dialogs kept the transform and the next hint
    // panel found it again.
    //
    // `inset-0 m-auto` centres identically and leaves fixed positioning
    // meaning what it says.
    const offenders: string[] = []
    for (const dir of [HERE, PAGES]) {
      for (const { name, text } of sources(dir)) {
        for (const attribute of classAttributes(text)) {
          const classes = attribute.split(/\s+/)
          if (!classes.includes('fixed')) continue
          if (classes.some((one) => one.startsWith('-translate-') || one.startsWith('translate-'))) {
            offenders.push(name)
          }
        }
      }
    }
    expect(offenders).toEqual([])
  })
})

describe('the controls a dialog offers', () => {
  it('gives every dialog a visible way out', () => {
    // Escape and a click behind the dialog are both invisible, and Cancel
    // beside a destructive verb reads as deciding rather than as leaving.
    const missing = sources(HERE)
      .filter(({ name }) => name.endsWith('Dialog.svelte'))
      .filter(({ text }) => !text.includes('<DialogHeader') && !/aria-label="Close[ "]/.test(text))
      .map(({ name }) => name)
    expect(missing).toEqual([])
  })

  it('offers help from every dialog that has something to explain', () => {
    // Not a demand that every dialog carry a topic — a pane holding a
    // terminal explains itself — but the ones that reach a cluster do.
    const shouldExplain = [
      'DeleteDialog.svelte',
      'EvictDialog.svelte',
      'DrainDialog.svelte',
      'CordonDialog.svelte',
      'RestartDialog.svelte',
      'ScaleDialog.svelte',
      'SetImageDialog.svelte',
      'RollbackDialog.svelte',
      'SuspendDialog.svelte',
      'TriggerDialog.svelte',
      'RolloutActionDialog.svelte',
      'BulkActionDialog.svelte',
      'DebugDialog.svelte',
      'NodeShellDialog.svelte',
      'ClusterShellDialog.svelte',
      'LocalShellDialog.svelte',
      'AddClusterDialog.svelte',
      'CreateResourceDialog.svelte',
      'CompareDialog.svelte',
      'OrganiseDialog.svelte',
      'SettingsDialog.svelte',
    ]
    const byName = new Map(sources(HERE).map(({ name, text }) => [name, text]))
    const silent = shouldExplain.filter((name) => {
      const text = byName.get(name) ?? ''
      return !/help="[a-z-]+"/.test(text) && !/topic="[a-z-]+"/.test(text)
    })
    expect(silent).toEqual([])
  })
})

describe('what a text field looks like', () => {
  it('is not monospaced, unless the field holds a document', () => {
    // A namespace, an image reference and a pod name are TEXT. Setting them
    // in the code face made every form read as a terminal, and made the two
    // fields that really do hold code — a pasted kubeconfig, a pasted
    // manifest — say nothing by saying it too.
    const documents = new Set([
      'AddClusterDialog.svelte',
      'CompareDialog.svelte',
      'CreateResourceDialog.svelte',
    ])
    const offenders = sources(HERE)
      .filter(({ name }) => !documents.has(name))
      .filter(({ text }) => /class="field[^"]*font-mono/.test(text))
      .map(({ name }) => name)
    expect(offenders).toEqual([])
  })
})
