/**
 * The kubeconfig loading list, as Settings shows it.
 *
 * A MIRROR, not a source of truth. The composed list is derived in Go on every
 * read from the environment plus the stored sources — which file contributed
 * which context, and which the merge shadowed, are facts only the thing that
 * performs the merge can state — so every change here is a call followed by a
 * reload rather than a local edit. That costs one extra round trip per gesture
 * and buys the guarantee that the rows always describe what the client is
 * actually reading.
 */

import {
  addKubeconfigFile,
  addKubeconfigFolder,
  chooseDirectory,
  chooseFile,
  getKubeconfigSources,
  getSettingsState,
  moveKubeconfigSource,
  removeKubeconfigSource,
  type KubeconfigSource,
  type SettingsState,
} from '$lib/api/client'
import { toApiError } from '$lib/api/errors'

/** What each origin means, in the operator's terms. */
export const ORIGIN_LABELS: Record<string, string> = {
  default: 'Your kubeconfig',
  environment: 'Set by PODSTEER_KUBECONFIG_DIR',
  settings: 'Added here',
}

class KubeconfigSourcesStore {
  /** The composed loading list, in precedence order. */
  sources = $state<KubeconfigSource[]>([])
  /** Where the settings live and whether a change would reach the disk. */
  settingsState = $state<SettingsState | null>(null)
  status = $state<'idle' | 'loading' | 'ready' | 'error'>('idle')
  error = $state<string | null>(null)
  /** True while a change is in flight, so the controls can be disabled. */
  busy = $state(false)

  #request = 0

  /** The entries the operator may remove or reorder. */
  readonly own = $derived(this.sources.filter((source) => source.editable))

  /** Whether anything is being saved at all. False under `podsteer mcp`. */
  readonly writable = $derived(this.settingsState?.writable ?? true)

  load = async (): Promise<void> => {
    const request = ++this.#request
    if (this.status === 'idle') this.status = 'loading'

    try {
      const [sources, settingsState] = await Promise.all([
        getKubeconfigSources(),
        getSettingsState(),
      ])
      if (request !== this.#request) return

      this.sources = sources
      this.settingsState = settingsState
      this.status = 'ready'
      this.error = null
    } catch (cause) {
      if (request !== this.#request) return
      this.error = toApiError(cause).message
      this.status = 'error'
    }
  }

  /**
   * Opens the native picker and adds what was chosen.
   *
   * An empty path is a cancellation, not a failure — the convention every
   * dialog on this seam follows.
   */
  addFile = async (): Promise<void> => {
    const path = await this.#choose(() => chooseFile('Choose a kubeconfig file'))
    if (path) await this.#change(() => addKubeconfigFile(path))
  }

  addFolder = async (): Promise<void> => {
    const path = await this.#choose(() => chooseDirectory('Choose a folder of kubeconfig files'))
    if (path) await this.#change(() => addKubeconfigFolder(path))
  }

  remove = async (path: string): Promise<void> => {
    await this.#change(() => removeKubeconfigSource(path))
  }

  move = async (path: string, delta: number): Promise<void> => {
    await this.#change(() => moveKubeconfigSource(path, delta))
  }

  async #choose(open: () => Promise<string>): Promise<string> {
    try {
      return await open()
    } catch (cause) {
      this.error = toApiError(cause).message
      return ''
    }
  }

  /**
   * Runs one change and re-reads the composed list.
   *
   * ALWAYS RELOADS, including after a failure: a refused change may still have
   * left the list different from what is on screen — another window, or a file
   * that appeared — and showing the operator a stale list beside an error is
   * how a setting appears not to have worked when it did.
   */
  async #change(apply: () => Promise<void>): Promise<void> {
    this.busy = true
    let refusal: string | null = null
    try {
      await apply()
    } catch (cause) {
      refusal = toApiError(cause).message
    }
    this.busy = false

    await this.load()
    // SET AFTER the reload, not before it. `load` clears the error on success,
    // so a refusal recorded first would be wiped by the very re-read that
    // exists to show the operator what actually happened — leaving a change
    // that silently did nothing.
    if (refusal) this.error = refusal
  }
}

export const kubeconfigSources = new KubeconfigSourcesStore()
