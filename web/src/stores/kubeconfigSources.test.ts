import { beforeEach, describe, expect, it, vi } from 'vitest'

const getKubeconfigSources = vi.fn()
const getSettingsState = vi.fn()
const addKubeconfigFile = vi.fn()
const addKubeconfigFolder = vi.fn()
const removeKubeconfigSource = vi.fn()
const moveKubeconfigSource = vi.fn()
const chooseFile = vi.fn()
const chooseDirectory = vi.fn()

vi.mock('$lib/api/client', () => ({
  getKubeconfigSources: () => getKubeconfigSources(),
  getSettingsState: () => getSettingsState(),
  addKubeconfigFile: (path: string) => addKubeconfigFile(path),
  addKubeconfigFolder: (path: string) => addKubeconfigFolder(path),
  removeKubeconfigSource: (path: string) => removeKubeconfigSource(path),
  moveKubeconfigSource: (path: string, delta: number) => moveKubeconfigSource(path, delta),
  chooseFile: (title: string) => chooseFile(title),
  chooseDirectory: (title: string) => chooseDirectory(title),
}))

import { kubeconfigSources } from './kubeconfigSources.svelte'

/** One entry shaped the way the Go side hands them over. */
function entry(path: string, origin: string, overrides: Record<string, unknown> = {}) {
  return {
    path,
    kind: 'file',
    origin,
    editable: origin === 'settings',
    missing: false,
    files: [path],
    contexts: [],
    shadowedBy: {},
    ...overrides,
  }
}

const writable = { path: '/config/PodSteer/settings.json', writable: true, notice: '' }

describe('the kubeconfig source list', () => {
  beforeEach(() => {
    getKubeconfigSources.mockReset().mockResolvedValue([])
    getSettingsState.mockReset().mockResolvedValue(writable)
    addKubeconfigFile.mockReset().mockResolvedValue(undefined)
    addKubeconfigFolder.mockReset().mockResolvedValue(undefined)
    removeKubeconfigSource.mockReset().mockResolvedValue(undefined)
    moveKubeconfigSource.mockReset().mockResolvedValue(undefined)
    chooseFile.mockReset()
    chooseDirectory.mockReset()
    kubeconfigSources.error = null
  })

  it('reads the composed list and the settings state together', async () => {
    getKubeconfigSources.mockResolvedValue([
      entry('/home/op/.kube/config', 'default'),
      entry('/home/op/mine.yaml', 'settings'),
    ])

    await kubeconfigSources.load()

    expect(kubeconfigSources.sources).toHaveLength(2)
    // Only the operator's own entry may be removed or reordered: nothing here
    // can change an environment variable or their own $KUBECONFIG.
    expect(kubeconfigSources.own.map((source) => source.path)).toEqual(['/home/op/mine.yaml'])
    expect(kubeconfigSources.writable).toBe(true)
  })

  it('re-reads the list after every change rather than editing it locally', async () => {
    // The composed list is derived in Go from the environment plus the stored
    // sources, so the only honest way to show what changed is to ask again.
    // A local edit would also silently drop the shadowing, which nothing in
    // the frontend is in a position to recompute.
    getKubeconfigSources.mockResolvedValue([entry('/home/op/mine.yaml', 'settings')])
    chooseFile.mockResolvedValue('/home/op/mine.yaml')

    await kubeconfigSources.addFile()

    expect(addKubeconfigFile).toHaveBeenCalledWith('/home/op/mine.yaml')
    expect(getKubeconfigSources).toHaveBeenCalled()
    expect(kubeconfigSources.sources.map((source) => source.path)).toEqual(['/home/op/mine.yaml'])
  })

  it('treats a cancelled picker as a cancellation rather than a failure', async () => {
    // An empty path is what every dialog on this seam returns when the
    // operator pressed Cancel; adding nothing is the correct outcome and an
    // error message would be a complaint about a decision they made.
    chooseFile.mockResolvedValue('')

    await kubeconfigSources.addFile()

    expect(addKubeconfigFile).not.toHaveBeenCalled()
    expect(kubeconfigSources.error).toBeNull()
  })

  it('shows a refusal and still re-reads the list', async () => {
    // A refused change may still leave the list different from what is on
    // screen — another window, or a folder that has just appeared — so the
    // reload happens whether the change succeeded or not.
    chooseDirectory.mockResolvedValue('/home/op/synced')
    addKubeconfigFolder.mockRejectedValue(
      new Error('[settings_from_future] Your settings file was written by a newer version'),
    )
    getKubeconfigSources.mockResolvedValue([entry('/home/op/.kube/config', 'default')])

    await kubeconfigSources.addFolder()

    expect(kubeconfigSources.error).toContain('newer version')
    expect(getKubeconfigSources).toHaveBeenCalled()
    expect(kubeconfigSources.sources).toHaveLength(1)
  })

  it('reports the settings file as unwritable when the backend says so', async () => {
    // `podsteer mcp` and a file from a newer PodSteer both land here, and the
    // pane hides its controls rather than offering a button that cannot work.
    getSettingsState.mockResolvedValue({
      path: '/config/PodSteer/settings.json',
      writable: false,
      notice: 'This settings file was written by a newer version of PodSteer.',
    })

    await kubeconfigSources.load()

    expect(kubeconfigSources.writable).toBe(false)
    expect(kubeconfigSources.settingsState?.notice).toContain('newer version')
  })

  it('passes a move through as a delta so the backend clamps it', async () => {
    // Clamping belongs where the list is: the frontend does not hold the
    // authoritative order, and computing a target index from a stale list is
    // how a reorder lands on the wrong row.
    await kubeconfigSources.move('/home/op/mine.yaml', -1)

    expect(moveKubeconfigSource).toHaveBeenCalledWith('/home/op/mine.yaml', -1)
  })

  it('removes an entry by path and never touches a file', async () => {
    await kubeconfigSources.remove('/home/op/mine.yaml')

    expect(removeKubeconfigSource).toHaveBeenCalledWith('/home/op/mine.yaml')
  })
})
