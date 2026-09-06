/**
 * The per-cluster switches the GO PROCESS owns, as Settings → Clusters shows
 * them.
 *
 * A MIRROR, not a source of truth, exactly as `kubeconfigSources` is: the
 * value lives in `settings.json`, so every change here is a call followed by a
 * re-read rather than a local edit. That costs one round trip per gesture and
 * buys the guarantee that a row always describes what the Go process will
 * actually act on — which matters more here than for most settings, because
 * the thing being described is whether anything is sent to a monitoring stack.
 *
 * WHY THIS IS NOT IN THE ORGANISE DIALOG, where the read-only mark lives.
 * The read-only mark is a flag on a GROUP, resolved through a cluster's
 * placement, and it stays there: it guards against the interface's own bugs,
 * so it belongs with the interface's own arrangement of clusters. These are
 * per-cluster FACTS about a cluster — whether its monitoring stack may be
 * read, and which one — and the Go process has to act on them without a
 * window. See `app/domain/settings.go` for the ownership rule.
 */

import {
  getClusterSettings,
  getSettingsState,
  setMetricsQuery,
  type ClusterSettings,
  type SettingsState,
} from '$lib/api/client'
import { toApiError } from '$lib/api/errors'

/** The modes, in the order the control offers them. */
export const METRICS_QUERY_MODES = [
  {
    value: 'off',
    label: 'Off',
    hint: 'Nothing is ever sent to a monitoring backend for this cluster.',
  },
  {
    value: 'manual',
    label: 'On request',
    hint: 'A control on each chart. Nothing is sent until you press it.',
  },
  {
    value: 'auto',
    label: 'When a chart opens',
    hint: 'Sent when a chart opens or you change its range — never on the refresh tick.',
  },
] as const

/** What to do when the backend turns out to hold more than this cluster. */
export const FLEET_POLICIES = [
  {
    value: 'filter',
    label: 'Narrow to this cluster',
    hint: "Every expression is filtered to this cluster's own nodes.",
  },
  {
    value: 'refuse',
    label: 'Draw nothing',
    hint: 'No aggregate is drawn, and the chart says why.',
  },
] as const

/** The value a cluster nobody has configured has. */
export function defaultClusterSettings(clusterId: string): ClusterSettings {
  return {
    clusterId,
    nodeHistory: false,
    metricsQueryMode: 'off',
    preferredNamespace: '',
    preferredService: '',
    fleetPolicy: 'filter',
  }
}

/**
 * Whether an entry says anything a fresh cluster does not.
 *
 * The same rule the Go side applies before writing — an entry equal to the
 * defaults is dropped rather than persisted — so a row shown here for a
 * cluster with no tab open is a row that genuinely has something stored.
 */
export function isDefault(entry: ClusterSettings): boolean {
  return (
    !entry.nodeHistory &&
    entry.metricsQueryMode === 'off' &&
    entry.preferredNamespace === '' &&
    entry.preferredService === '' &&
    entry.fleetPolicy === 'filter'
  )
}

class ClusterSettingsStore {
  /** The rows, in the order they were asked for. */
  entries = $state<ClusterSettings[]>([])
  /** Where the settings live and whether a change would reach the disk. */
  settingsState = $state<SettingsState | null>(null)
  status = $state<'idle' | 'loading' | 'ready' | 'error'>('idle')
  error = $state<string | null>(null)
  /** True while a change is in flight, so the controls can be disabled. */
  busy = $state(false)

  /** The contexts last asked about, so a reload asks about the same set. */
  #asked: string[] = []
  #request = 0

  /** Whether anything is being saved at all. False under `podsteer mcp`. */
  readonly writable = $derived(this.settingsState?.writable ?? true)

  /** One row by context name, for a component that has a cluster in hand. */
  for = (clusterId: string): ClusterSettings =>
    this.entries.find((entry) => entry.clusterId === clusterId) ??
    defaultClusterSettings(clusterId)

  /**
   * Reads the switches for `clusterIds`.
   *
   * The caller decides the set — Settings passes every open tab's context plus
   * every context the kubeconfig knows — because which contexts are worth
   * asking about is a question about the workspace, not about this store.
   */
  load = async (clusterIds: string[]): Promise<void> => {
    const request = ++this.#request
    this.#asked = clusterIds
    if (this.status === 'idle') this.status = 'loading'

    try {
      const [entries, settingsState] = await Promise.all([
        getClusterSettings(clusterIds),
        getSettingsState(),
      ])
      if (request !== this.#request) return

      this.entries = entries
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
   * Records one cluster's metrics-query settings.
   *
   * NOTHING IS SENT TO ANY MONITORING BACKEND BY THIS, in this build or by
   * this call: it writes the switch a later reader will consult.
   */
  save = async (
    clusterId: string,
    change: Partial<Pick<
      ClusterSettings,
      'metricsQueryMode' | 'preferredNamespace' | 'preferredService' | 'fleetPolicy'
    >>,
  ): Promise<void> => {
    const current = this.for(clusterId)
    const next = { ...current, ...change }

    this.busy = true
    let refusal: string | null = null
    try {
      await setMetricsQuery(
        clusterId,
        next.metricsQueryMode,
        next.preferredNamespace,
        next.preferredService,
        next.fleetPolicy,
      )
    } catch (cause) {
      refusal = toApiError(cause).message
    }
    this.busy = false

    // ALWAYS RELOADS, refusal included: the Go side normalises and may drop an
    // entry that ended up saying nothing, so what is on screen after a change
    // has to come from the file rather than from what was sent to it.
    await this.load(this.#asked)
    // Set AFTER the reload, which clears the error on success — the same order
    // `kubeconfigSources` keeps, and for the same reason.
    if (refusal) this.error = refusal
  }
}

export const clusterSettings = new ClusterSettingsStore()
