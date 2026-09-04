import { describe, expect, it } from 'vitest'
import { detectSeverity, parseStructuredLine } from './logFormat'

describe('parseStructuredLine', () => {
  it('parses a JSON object line and promotes level, msg and time', () => {
    const line = '{"level":"info","msg":"started server","time":"2024-01-15T10:30:00Z","port":8080}'
    const result = parseStructuredLine(line)
    expect(result.kind).toBe('json')
    expect(result.level).toBe('info')
    expect(result.message).toBe('started server')
    expect(result.timestamp).toBe('2024-01-15T10:30:00Z')
    expect(result.fields).toEqual([{ key: 'port', value: '8080' }])
  })

  it('recognises alternate key spellings for the same promoted fields', () => {
    const line = '{"severity":"error","message":"boom","ts":"now","err":"disk full"}'
    const result = parseStructuredLine(line)
    expect(result.level).toBe('error')
    expect(result.message).toBe('boom')
    expect(result.timestamp).toBe('now')
    expect(result.error).toBe('disk full')
    expect(result.fields).toEqual([])
  })

  it('stringifies a nested object field rather than reporting [object Object]', () => {
    const line = '{"level":"info","request":{"method":"GET","path":"/health"}}'
    const result = parseStructuredLine(line)
    expect(result.fields).toEqual([{ key: 'request', value: '{"method":"GET","path":"/health"}' }])
  })

  it('falls back to plain for malformed JSON that merely looks bracketed', () => {
    const line = '{not actually json, just a Go %v dump}'
    const result = parseStructuredLine(line)
    expect(result.kind).toBe('plain')
    expect(result.raw).toBe(line)
  })

  it('falls back to plain for a JSON array rather than an object', () => {
    const line = '["a","b","c"]'
    const result = parseStructuredLine(line)
    expect(result.kind).toBe('plain')
  })

  it('parses a logfmt line and promotes level, msg and time', () => {
    const line = 'level=warn msg="disk usage high" time=2024-01-15T10:30:00Z component=disk-monitor'
    const result = parseStructuredLine(line)
    expect(result.kind).toBe('logfmt')
    expect(result.level).toBe('warn')
    expect(result.message).toBe('disk usage high')
    expect(result.timestamp).toBe('2024-01-15T10:30:00Z')
    expect(result.fields).toEqual([{ key: 'component', value: 'disk-monitor' }])
  })

  it('decodes a quoted logfmt value with an escaped quote', () => {
    const line = 'level=info msg="said \\"hello\\""'
    const result = parseStructuredLine(line)
    expect(result.message).toBe('said "hello"')
  })

  it('does not misread a sentence containing one incidental key=value as logfmt', () => {
    const line = 'retrying request, attempt=3 of many after a transient failure talking to the upstream service'
    const result = parseStructuredLine(line)
    expect(result.kind).toBe('plain')
  })

  it('treats an ordinary sentence as plain', () => {
    const line = 'Starting HTTP server on :8080'
    const result = parseStructuredLine(line)
    expect(result.kind).toBe('plain')
    expect(result.fields).toEqual([])
    expect(result.raw).toBe(line)
  })

  it('treats an empty line as plain', () => {
    expect(parseStructuredLine('').kind).toBe('plain')
  })
})

describe('detectSeverity', () => {
  it('reads the level field of a structured line', () => {
    expect(detectSeverity(parseStructuredLine('{"level":"error","msg":"x"}'))).toBe('error')
    expect(detectSeverity(parseStructuredLine('{"level":"warning","msg":"x"}'))).toBe('warn')
    expect(detectSeverity(parseStructuredLine('{"level":"trace","msg":"x"}'))).toBe('debug')
  })

  it('returns undefined for an unrecognised level spelling', () => {
    expect(detectSeverity(parseStructuredLine('{"level":"weird","msg":"x"}'))).toBeUndefined()
  })

  it('falls back to a token in the first 64 characters of a plain line', () => {
    const line = detectSeverity(parseStructuredLine('ERROR: could not connect to the database'))
    expect(line).toBe('error')
  })

  it('is case-insensitive on the plain-line token', () => {
    expect(detectSeverity(parseStructuredLine('warn: retrying in 5s'))).toBe('warn')
  })

  it('ignores a level-shaped token past the first 64 characters', () => {
    const padding = 'x'.repeat(70)
    const line = detectSeverity(parseStructuredLine(`${padding} ERROR happened`))
    expect(line).toBeUndefined()
  })

  it('does not match a level word as a substring of another word', () => {
    expect(detectSeverity(parseStructuredLine('information about the deployment'))).toBeUndefined()
  })

  it('returns undefined for a plain line with no level token at all', () => {
    expect(detectSeverity(parseStructuredLine('server listening on port 8080'))).toBeUndefined()
  })
})
