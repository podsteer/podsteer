/**
 * Pure logic behind the log viewer's volume histogram and level counts.
 *
 * Kept free of Svelte so the bucketing is testable and so the component's
 * only job is to feed it (timestamp, level) samples it has ALREADY parsed
 * (`logTimestamps.ts`, `detectSeverity`) — this module classifies nothing.
 */

import type { Severity } from './logFormat'

/** A severity the viewer detected, or `unknown` where a line says none. */
export type LevelKey = Severity | 'unknown'

export const LEVEL_KEYS: readonly LevelKey[] = ['error', 'warn', 'info', 'debug', 'unknown']

export interface HistogramSample {
  /** Line identity, in display order (the viewer's `seq`). */
  seq: number
  /** Epoch ms, or null for a line with no (parsable) timestamp. */
  ts: number | null
  level: LevelKey
}

export interface HistogramBucket {
  /** Epoch ms of the bucket's inclusive start. */
  start: number
  count: number
  levels: Record<LevelKey, number>
  /** `seq` of the first line (in input order) that falls in this bucket. */
  firstSeq: number
}

export interface Histogram {
  start: number
  bucketMs: number
  buckets: HistogramBucket[]
  /** Largest bucket count, for scaling bar heights. */
  max: number
}

/** Human-friendly bucket widths, ms. */
const NICE_STEPS_MS = [
  1, 2, 5, 10, 20, 50, 100, 200, 500, 1000, 2000, 5000, 10_000, 15_000, 30_000, 60_000, 120_000, 300_000, 600_000,
  900_000, 1_800_000, 3_600_000, 7_200_000, 21_600_000, 43_200_000, 86_400_000,
]

export function emptyLevelCounts(): Record<LevelKey, number> {
  return { error: 0, warn: 0, info: 0, debug: 0, unknown: 0 }
}

export function countLevels(levels: Iterable<LevelKey>): Record<LevelKey, number> {
  const counts = emptyLevelCounts()
  for (const level of levels) counts[level]++
  return counts
}

/** Smallest nice step that keeps `spanMs` within `target` buckets. */
export function pickBucketMs(spanMs: number, target = 60): number {
  const raw = Math.max(1, spanMs) / Math.max(1, target)
  for (const step of NICE_STEPS_MS) if (step >= raw) return step
  return Math.ceil(raw / 86_400_000) * 86_400_000
}

/**
 * Buckets the timestamped samples. Returns null when fewer than two samples
 * carry a valid timestamp — a histogram of one point says nothing.
 * Untimestamped samples are ignored here (they still count in level chips).
 */
export function buildHistogram(samples: readonly HistogramSample[], target = 60): Histogram | null {
  let min = Infinity
  let max = -Infinity
  let timed = 0
  for (const sample of samples) {
    if (sample.ts === null || !Number.isFinite(sample.ts)) continue
    timed++
    if (sample.ts < min) min = sample.ts
    if (sample.ts > max) max = sample.ts
  }
  if (timed < 2) return null

  const bucketMs = pickBucketMs(max - min + 1, target)
  const start = Math.floor(min / bucketMs) * bucketMs
  const n = Math.floor((max - start) / bucketMs) + 1
  const buckets: HistogramBucket[] = Array.from({ length: n }, (_, i) => ({
    start: start + i * bucketMs,
    count: 0,
    levels: emptyLevelCounts(),
    firstSeq: -1,
  }))

  let peak = 0
  for (const sample of samples) {
    if (sample.ts === null || !Number.isFinite(sample.ts)) continue
    const bucket = buckets[Math.floor((sample.ts - start) / bucketMs)]
    bucket.count++
    bucket.levels[sample.level]++
    if (bucket.firstSeq === -1) bucket.firstSeq = sample.seq
    if (bucket.count > peak) peak = bucket.count
  }
  return { start, bucketMs, buckets, max: peak }
}

/** Compact label for a bucket width, e.g. "5s", "2m", "1h". */
export function formatBucketWidth(ms: number): string {
  if (ms < 1000) return `${ms}ms`
  if (ms < 60_000) return `${ms / 1000}s`
  if (ms < 3_600_000) return `${ms / 60_000}m`
  if (ms < 86_400_000) return `${ms / 3_600_000}h`
  return `${ms / 86_400_000}d`
}

/**
 * Roving-focus target: the nearest non-empty bucket from `from` in `dir`
 * (no wrap), 'first'/'last' for Home/End. Returns -1 when none exists.
 */
export function nextBucketIndex(
  buckets: readonly { count: number }[],
  from: number,
  dir: 1 | -1 | 'first' | 'last',
): number {
  if (dir === 'first') return buckets.findIndex((b) => b.count > 0)
  if (dir === 'last') {
    for (let i = buckets.length - 1; i >= 0; i--) if (buckets[i].count > 0) return i
    return -1
  }
  for (let i = from + dir; i >= 0 && i < buckets.length; i += dir) if (buckets[i].count > 0) return i
  return -1
}
