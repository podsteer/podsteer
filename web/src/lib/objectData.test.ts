import { describe, expect, it } from 'vitest'

import { base64Bytes, dataEntries } from './objectData'

describe('the keys an object holds', () => {
  it('lists a Secret masked, in key order, without decoding anything', () => {
    // The values arrive already masked — the adapter replaces them before
    // they leave Go — so this function's job is to say which keys exist, not
    // to reach for what is in them.
    const entries = dataEntries('Secret', {
      data: { password: '<hidden, 12 bytes>', 'api-key': '<hidden, 40 bytes>' },
    })

    expect(entries.map((e) => e.key)).toEqual(['api-key', 'password'])
    expect(entries.every((e) => e.kind === 'secret')).toBe(true)
    expect(entries[1].display).toBe('<hidden, 12 bytes>')
  })

  it('lists a ConfigMap in the clear, because it is not a Secret', () => {
    const entries = dataEntries('ConfigMap', { data: { 'log.level': 'debug' } })
    expect(entries).toEqual([{ key: 'log.level', display: 'debug', kind: 'text' }])
  })

  it('sorts, because a map has no order and a poll must not reshuffle the panel', () => {
    const entries = dataEntries('ConfigMap', { data: { b: '2', a: '1', c: '3' } })
    expect(entries.map((e) => e.key)).toEqual(['a', 'b', 'c'])
  })

  it('lists binaryData by size and marks it as bytes', () => {
    // 'aGVsbG8=' is "hello": five bytes, not six. A text editor over base64
    // is how a keystore acquires a stray newline, which is why this kind
    // exists at all.
    const entries = dataEntries('ConfigMap', { binaryData: { 'keystore.jks': 'aGVsbG8=' } })
    expect(entries).toEqual([
      { key: 'keystore.jks', display: '<binary, 5 bytes>', kind: 'binary' },
    ])
  })

  it('includes a stringData key the API server never accepted', () => {
    // A stored Helm manifest routinely holds the object the API server
    // refused. A key that exists and is not listed is a key nobody knows to
    // change.
    const entries = dataEntries('Secret', {
      data: { a: '<hidden, 1 bytes>' },
      stringData: { b: '<hidden, 2 bytes>' },
    })
    expect(entries.map((e) => e.key)).toEqual(['a', 'b'])
  })

  it('does not list the same key twice when it is in both fields', () => {
    const entries = dataEntries('Secret', {
      data: { shared: '<hidden, 1 bytes>' },
      stringData: { shared: '<hidden, 9 bytes>' },
    })
    expect(entries).toHaveLength(1)
    expect(entries[0].display).toBe('<hidden, 1 bytes>')
  })

  it('prints a non-string value rather than dropping it', () => {
    // An unquoted YAML scalar parses as a number. The adapter masks it as
    // unreadable; this must still show that the key is there.
    const entries = dataEntries('Secret', { data: { pin: 483920 } })
    expect(entries[0]).toEqual({ key: 'pin', display: '483920', kind: 'secret' })
  })

  it('answers nothing for a kind that has no data at all', () => {
    expect(dataEntries('Pod', { spec: {} })).toEqual([])
    expect(dataEntries('Secret', null)).toEqual([])
  })
})

describe('base64 sizes', () => {
  it('accounts for padding, which the naive formula does not', () => {
    // A 1368-character certificate ending '==' is 1024 bytes, not 1026.
    expect(base64Bytes('aGVsbG8=')).toBe(5)
    expect(base64Bytes('aGVsbG9v')).toBe(6)
    expect(base64Bytes('aGVsbG8')).toBe(5)
    expect(base64Bytes('cGFzc3dvcmQ=')).toBe(8)
    expect(base64Bytes('')).toBe(0)
  })

  it('ignores the newlines a wrapped value carries', () => {
    expect(base64Bytes('aGVs\nbG8=')).toBe(5)
  })
})
