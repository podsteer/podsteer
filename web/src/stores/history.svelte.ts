/**
 * A cluster's recorded history, as the dashboard chart reads it.
 *
 * The retention setting lives on the Go side, not here with the other
 * preferences: it governs what gets written to disk, so the process doing the
 * writing has to own it. This store mirrors it for display and forwards
 * changes.
 */

import {
  getRetention,
  getSeries,
  setRetention as applyRetention,
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
 * able to say that K8Sense writes none of it down.
 */
export const RETENTION_OPTIONS = [
  { label: "Don't record", days: 0, hint: 'No history is kept, and anything already recorded is erased' },
  { label: '1 day', days: 1, hint: 'Enough for a working day' },
  { label: '7 days', days: 7, hint: 'Covers a week of app sessions' },
  { label: '30 days', days: 30, hint: 'The longest window offered' },
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

      this.samples = result.samples
      this.spanSeconds = result.spanSeconds
      this.recording = result.recording
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

/** The retention setting, shared by the whole application. */
class RetentionStore {
  days = $state<number>(1)
  loaded = $state(false)

  load = async (): Promise<void> => {
    try {
      const setting = await getRetention()
      this.days = setting.days
      this.loaded = true
    } catch {
      // A retention we cannot read is not worth interrupting anyone over; the
      // backend still enforces whatever it has.
    }
  }

  set = async (days: number): Promise<void> => {
    const previous = this.days
    this.days = days
    try {
      await applyRetention(days)
    } catch {
      this.days = previous
    }
  }
}

export const retention = new RetentionStore()
