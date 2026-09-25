import { describe, expect, it } from 'vitest'
import { splitOnQuery, splitOnRegex } from './textSearch'
import { matches, parseQuery } from './query'
import { parseStructuredLine } from './logFormat'

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

// The exact line from the log-viewer bug report: filtering for the phrase
// "issuer unavailable" (typed unquoted, so query.ts parses it as two AND'd
// substring terms — "issuer" and "unavailable") used to highlight only
// "issuer", because the caller picked just the query's first term. A
// single-word filter such as "declined" was unaffected, since a one-word
// query has only one term to begin with — which is why the bug was easy to
// miss until somebody typed a phrase.
const LOGFMT_LINE = 'ERROR payment declined order=ord-10908 reason="issuer unavailable" retry_in=30s'

describe('splitOnQuery', () => {
  it('highlights every AND term, not only the first, on an unparsed line', () => {
    const query = parseQuery('issuer unavailable')
    const runs = splitOnQuery(LOGFMT_LINE, query)
    const matched = runs.filter((run) => run.match).map((run) => run.text)
    // Both words sit right next to each other in the raw line (one space
    // apart), so the fix must report them as ONE contiguous highlighted
    // range rather than two marks either side of an unlit space.
    expect(matched).toEqual(['issuer unavailable'])
  })

  it('still highlights correctly for a single-word filter', () => {
    const query = parseQuery('declined')
    const runs = splitOnQuery(LOGFMT_LINE, query)
    expect(runs).toEqual([
      { text: 'ERROR payment ', match: false },
      { text: 'declined', match: true },
      { text: ' order=ord-10908 reason="issuer unavailable" retry_in=30s', match: false },
    ])
  })

  it('highlights the full phrase inside a quoted logfmt value once the line is parsed structured', () => {
    const structured = parseStructuredLine(LOGFMT_LINE)
    expect(structured.kind).toBe('logfmt')
    const reasonField = structured.fields.find((field) => field.key === 'reason')
    expect(reasonField?.value).toBe('issuer unavailable')

    // LogViewer renders each field as `${key}=${value}`, so the highlight is
    // computed over that combined string exactly as the component does.
    const query = parseQuery('issuer unavailable')
    const runs = splitOnQuery(`${reasonField!.key}=${reasonField!.value}`, query)
    expect(runs).toEqual([
      { text: 'reason=', match: false },
      { text: 'issuer unavailable', match: true },
    ])
  })

  it('highlights the full phrase inside a JSON-parsed message field', () => {
    const structured = parseStructuredLine('{"level":"error","msg":"issuer unavailable for retry"}')
    expect(structured.kind).toBe('json')
    const query = parseQuery('issuer unavailable')
    const runs = splitOnQuery(structured.message!, query)
    expect(runs).toEqual([
      { text: 'issuer unavailable', match: true },
      { text: ' for retry', match: false },
    ])
  })

  it('merges an AND term with a re: regex term into one run when they meet', () => {
    // "issuer" as plain text and `re:unavailable` as a pattern — a mixed
    // query is still one merged set of ranges, not just the first term kept.
    const query = parseQuery('issuer re:unavailable')
    const runs = splitOnQuery('reason=issuer unavailable', query)
    expect(runs).toEqual([
      { text: 'reason=', match: false },
      { text: 'issuer unavailable', match: true },
    ])
  })

  it('gives a negated term no highlight of its own', () => {
    const query = parseQuery('declined -issuer')
    const runs = splitOnQuery(LOGFMT_LINE, query)
    const matched = runs.filter((run) => run.match).map((run) => run.text)
    expect(matched).toEqual(['declined'])
  })

  it('returns one unmatched run, and matches() agrees the line does not pass, when a term is absent', () => {
    const query = parseQuery('nonexistent-term')
    expect(splitOnQuery(LOGFMT_LINE, query)).toEqual([{ text: LOGFMT_LINE, match: false }])
    expect(matches(query, { text: LOGFMT_LINE })).toBe(false)
  })

  it('agrees with matches(): whenever the filter says a line matches, at least one run is highlighted', () => {
    const query = parseQuery('issuer unavailable')
    expect(matches(query, { text: LOGFMT_LINE })).toBe(true)
    const runs = splitOnQuery(LOGFMT_LINE, query)
    expect(runs.some((run) => run.match)).toBe(true)
  })
})
