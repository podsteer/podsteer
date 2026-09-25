/**
 * A cluster's recorded history, as the dashboard chart reads it.
 *
 * The retention setting lives on the Go side, not here with the other
 * preferences: it governs what gets written to disk, so the process doing the
 * writing has to own it. This store mirrors it for display and forwards
 * changes.
 */

import {
  getHistorySettings,
  getSeries,
  setRetention as applyRetention,
  setSamplingInterval as applyInterval,
  type Sample,
  type SeriesResult,
} from '$lib/api/client'
import { toApiError } from '$lib/api/errors'

/** Windows the dashboard offers, in minutes. */
export const TREND_WINDOWS = [
  { label: '15m', minutes: 15 },
  { label: '1h', minutes: 60 },
  { label: '6h', minutes: 360 },
  { label: '24h', minutes: 1440 },
] as const

/**
 * Retention choices offered in Settings.
 *
 * Zero is a real choice rather than a disabled state: a cluster's capacity
 * profile is commercially sensitive on some sites, and an operator has to be
 * able to say that PodSteer writes none of it down.
 */
export const RETENTION_OPTIONS = [
  { label: "Don't record", days: 0, hint: 'No history is kept, and anything already recorded is erased' },
  { label: '1 day', days: 1, hint: 'Enough for a working day' },
  { label: '7 days', days: 7, hint: 'Covers a week of app sessions' },
  { label: '30 days', days: 30, hint: 'The longest window offered' },
] as const

/**
 * Sampling cadences offered.
 *
 * Every sample costs a full cluster assessment — nodes, pods, controllers,
 * events and metrics — so on a large cluster the choice is about load on the
 * API server as much as about resolution. The hints say so.
 */
export const SAMPLING_INTERVALS = [
  { label: 'Every 10 seconds', seconds: 10, hint: 'Finest detail, heaviest on the API server' },
  { label: 'Every 30 seconds', seconds: 30, hint: 'The default — a good balance' },
  { label: 'Every minute', seconds: 60, hint: 'Lighter, still enough to see a trend' },
  { label: 'Every 5 minutes', seconds: 300, hint: 'Lightest; long sessions only' },
] as const

/**
 * How many points to request.
 *
 * The backend averages down to this rather than dropping samples, so a spike
 * survives as a bump. Roughly one point per two pixels of a wide chart is as
 * much resolution as anyone can see.
 */
const MAX_POINTS = 240

/** One cluster's trend data. */
export class ClusterHistory {
  readonly clusterId: string

  samples = $state<Sample[]>([])
  /** How long the returned samples actually cover, in seconds. */
  spanSeconds = $state(0)
  /** False when the operator has turned recording off. */
  recording = $state(true)
  /** How often a sample is taken, so the UI can say when a line will appear. */
  intervalSeconds = $state(30)
  windowMinutes = $state<number>(60)
  status = $state<'idle' | 'loading' | 'ready' | 'error'>('idle')
  error = $state<string | null>(null)

  #request = 0

  constructor(clusterId: string) {
    this.clusterId = clusterId
  }

  /** Whether there is enough data to draw a line rather than a dot. */
  readonly hasTrend = $derived(this.samples.length >= 2)

  load = async (): Promise<void> => {
    const request = ++this.#request
    if (this.status === 'idle') this.status = 'loading'

    try {
      const result: SeriesResult = await getSeries(this.clusterId, this.windowMinutes, MAX_POINTS)
      if (request !== this.#request) return

      this.samples = result.samples ?? []
      this.spanSeconds = result.spanSeconds
      this.recording = result.recording
      this.intervalSeconds = result.intervalSeconds
      this.status = 'ready'
      this.error = null
    } catch (cause) {
      if (request !== this.#request) return
      this.error = toApiError(cause).message
      this.status = 'error'
    }
  }

  setWindow = async (minutes: number): Promise<void> => {
    if (minutes === this.windowMinutes) return
    this.windowMinutes = minutes
    await this.load()
  }
}

/**
 * What PodSteer records locally, and how often.
 *
 * Mirrored here for display only — both settings are owned by the Go side,
 * because they govern what reaches the disk and what load the API server
 * carries. Failed writes roll the mirror back rather than leaving the UI
 * claiming a setting the backend never accepted.
 */
class HistorySettingsStore {
  days = $state<number>(1)
  intervalSeconds = $state<number>(30)
  loaded = $state(false)

  load = async (): Promise<void> => {
    try {
      const settings = await getHistorySettings()
      this.days = settings.retentionDays
      this.intervalSeconds = settings.intervalSeconds
      this.loaded = true
    } catch {
      // Settings we cannot read are not worth interrupting anyone over; the
      // backend still enforces whatever it has.
    }
  }

  setRetention = async (days: number): Promise<void> => {
    const previous = this.days
    this.days = days
    try {
      await applyRetention(days)
    } catch {
      this.days = previous
    }
  }

  setInterval = async (seconds: number): Promise<void> => {
    const previous = this.intervalSeconds
    this.intervalSeconds = seconds
    try {
      await applyInterval(seconds)
    } catch {
      this.intervalSeconds = previous
    }
  }
}

export const historySettings = new HistorySettingsStore()
