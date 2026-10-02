import { describe, expect, it } from 'vitest'
import { buildHistogram, countLevels, formatBucketWidth, pickBucketMs, type HistogramSample } from './logHistogram'

const s = (seq: number, ts: number | null, level: HistogramSample['level'] = 'info'): HistogramSample => ({
  seq,
  ts,
  level,
})

describe('pickBucketMs', () => {
  it('chooses a nice step keeping the span within the target', () => {
    expect(pickBucketMs(60_000, 60)).toBe(1000)
    expect(pickBucketMs(3_600_000, 60)).toBe(60_000)
    expect(pickBucketMs(100_000, 60)).toBe(2000)
  })
  it('never returns less than 1ms', () => {
    expect(pickBucketMs(0)).toBe(1)
  })
})

describe('buildHistogram', () => {
  it('is null with fewer than two timestamped lines', () => {
    expect(buildHistogram([])).toBeNull()
    expect(buildHistogram([s(1, 1000)])).toBeNull()
    expect(buildHistogram([s(1, 1000), s(2, null)])).toBeNull()
  })

  it('ignores unparsable timestamps', () => {
    const h = buildHistogram([s(1, 0), s(2, NaN), s(3, 5000), s(4, null)])!
    expect(h.buckets.reduce((a, b) => a + b.count, 0)).toBe(2)
  })

  it('buckets by time, counts levels and records the first seq', () => {
    const h = buildHistogram([s(1, 0, 'info'), s(2, 500, 'error'), s(3, 59_000, 'warn'), s(4, 59_500, 'unknown')])!
    expect(h.bucketMs).toBe(1000)
    expect(h.buckets).toHaveLength(60)
    expect(h.buckets[0].count).toBe(2)
    expect(h.buckets[0].levels).toMatchObject({ info: 1, error: 1 })
    expect(h.buckets[0].firstSeq).toBe(1)
    expect(h.buckets[59].count).toBe(2)
    expect(h.buckets[59].firstSeq).toBe(3)
    expect(h.buckets[1].firstSeq).toBe(-1)
    expect(h.max).toBe(2)
  })

  it('handles identical timestamps in one bucket', () => {
    const h = buildHistogram([s(1, 7000), s(2, 7000)])!
    expect(h.buckets).toHaveLength(1)
    expect(h.buckets[0].count).toBe(2)
  })

  it('keeps first seq in input order when timestamps interleave', () => {
    const h = buildHistogram([s(1, 5000), s(2, 1000), s(3, 1100)])!
    expect(h.buckets[0].firstSeq).toBe(2)
  })
})

describe('countLevels', () => {
  it('counts every level including unknown', () => {
    expect(countLevels(['error', 'unknown', 'unknown', 'info'])).toEqual({
      error: 1,
      warn: 0,
      info: 1,
      debug: 0,
      unknown: 2,
    })
  })
})

describe('formatBucketWidth', () => {
  it('formats units', () => {
    expect(formatBucketWidth(500)).toBe('500ms')
    expect(formatBucketWidth(5000)).toBe('5s')
    expect(formatBucketWidth(120_000)).toBe('2m')
    expect(formatBucketWidth(3_600_000)).toBe('1h')
  })
})
