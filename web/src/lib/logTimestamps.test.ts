import { describe, expect, it } from 'vitest'
import { formatLogTimestamp, parseLogTimestamp } from './logTimestamps'

describe('parseLogTimestamp', () => {
  it('splits a Z-suffixed RFC 3339 timestamp from the rest of the line', () => {
    const { timestamp, rest } = parseLogTimestamp('2024-01-15T10:30:00.123456789Z Hello world')
    expect(timestamp).not.toBeNull()
    expect(timestamp?.toISOString()).toBe('2024-01-15T10:30:00.123Z')
    expect(rest).toBe('Hello world')
  })

  it('splits a timestamp with an explicit offset', () => {
    const { timestamp, rest } = parseLogTimestamp('2024-01-15T10:30:00+01:00 starting up')
    expect(timestamp).not.toBeNull()
    expect(rest).toBe('starting up')
  })

  it('returns the whole line as rest, and a null timestamp, when there is none', () => {
    const { timestamp, rest } = parseLogTimestamp('just a plain log line')
    expect(timestamp).toBeNull()
    expect(rest).toBe('just a plain log line')
  })

  it('does not mistake a bare date-shaped word for a timestamp', () => {
    const { timestamp, rest } = parseLogTimestamp('2024-01-15 something happened')
    expect(timestamp).toBeNull()
    expect(rest).toBe('2024-01-15 something happened')
  })

  it('keeps an empty line as itself', () => {
    expect(parseLogTimestamp('')).toEqual({ timestamp: null, rest: '' })
  })

  it('preserves an empty message after the timestamp', () => {
    const { timestamp, rest } = parseLogTimestamp('2024-01-15T10:30:00Z ')
    expect(timestamp).not.toBeNull()
    expect(rest).toBe('')
  })
})

describe('formatLogTimestamp', () => {
  const stamp = new Date(2024, 0, 15, 10, 30, 5, 250) // local components

  it('renders nothing in "off" mode', () => {
    expect(formatLogTimestamp(stamp, 'off')).toBe('')
  })

  it('renders nothing for a missing timestamp, whatever the mode', () => {
    expect(formatLogTimestamp(null, 'local')).toBe('')
    expect(formatLogTimestamp(null, 'utc')).toBe('')
    expect(formatLogTimestamp(null, 'relative')).toBe('')
  })

  it('renders local time as HH:MM:SS.mmm in the machine\'s own timezone', () => {
    expect(formatLogTimestamp(stamp, 'local')).toBe('10:30:05.250')
  })

  it('renders UTC time with a trailing Z', () => {
    const utcExpected =
      String(stamp.getUTCHours()).padStart(2, '0') +
      ':' +
      String(stamp.getUTCMinutes()).padStart(2, '0') +
      ':' +
      String(stamp.getUTCSeconds()).padStart(2, '0') +
      '.' +
      String(stamp.getUTCMilliseconds()).padStart(3, '0') +
      'Z'
    expect(formatLogTimestamp(stamp, 'utc')).toBe(utcExpected)
  })

  describe('relative', () => {
    const now = new Date(2024, 0, 15, 10, 30, 5, 250)

    it('reports "just now" for a timestamp under a second old', () => {
      expect(formatLogTimestamp(now, 'relative', now)).toBe('just now')
    })

    it('reports seconds', () => {
      const then = new Date(now.getTime() - 5_000)
      expect(formatLogTimestamp(then, 'relative', now)).toBe('5s ago')
    })

    it('reports minutes once seconds would reach 60', () => {
      const then = new Date(now.getTime() - 90_000)
      expect(formatLogTimestamp(then, 'relative', now)).toBe('1m ago')
    })

    it('reports hours once minutes would reach 60', () => {
      const then = new Date(now.getTime() - 2 * 3_600_000)
      expect(formatLogTimestamp(then, 'relative', now)).toBe('2h ago')
    })

    it('reports days once hours would reach 24', () => {
      const then = new Date(now.getTime() - 4 * 86_400_000)
      expect(formatLogTimestamp(then, 'relative', now)).toBe('4d ago')
    })

    it('clamps a timestamp slightly in the future to "just now" rather than a negative duration', () => {
      const future = new Date(now.getTime() + 500)
      expect(formatLogTimestamp(future, 'relative', now)).toBe('just now')
    })
  })
})
