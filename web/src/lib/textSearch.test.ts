import { describe, expect, it } from 'vitest'
import { splitOnRegex } from './textSearch'

describe('splitOnRegex', () => {
  it('returns one unmatched run when the pattern does not appear', () => {
    expect(splitOnRegex('hello world', /xyz/i)).toEqual([{ text: 'hello world', match: false }])
  })

  it('marks a single match', () => {
    expect(splitOnRegex('connection refused', /refused/i)).toEqual([
      { text: 'connection ', match: false },
      { text: 'refused', match: true },
    ])
  })

  it('marks every occurrence, even without a "g" flag on the input regex', () => {
    expect(splitOnRegex('foo bar foo baz foo', /foo/i)).toEqual([
      { text: 'foo', match: true },
      { text: ' bar ', match: false },
      { text: 'foo', match: true },
      { text: ' baz ', match: false },
      { text: 'foo', match: true },
    ])
  })

  it('is case-insensitive when the source regex is', () => {
    expect(splitOnRegex('ERROR: boom', /error/i)).toEqual([
      { text: 'ERROR', match: true },
      { text: ': boom', match: false },
    ])
  })

  it('applies a real pattern, not a literal one', () => {
    expect(splitOnRegex('status=500 code', /\d+/)).toEqual([
      { text: 'status=', match: false },
      { text: '500', match: true },
      { text: ' code', match: false },
    ])
  })

  it('skips a zero-width match rather than inserting an empty highlight', () => {
    // \b matches a position, not a character — every run stays intact and
    // nothing is marked, since there is no text to colour.
    expect(splitOnRegex('abc', /\b/)).toEqual([{ text: 'abc', match: false }])
  })

  it('handles an empty string', () => {
    expect(splitOnRegex('', /x/)).toEqual([{ text: '', match: false }])
  })
})
