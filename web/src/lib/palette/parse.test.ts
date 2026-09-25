import { describe, expect, it } from 'vitest'

import { parsePaletteQuery } from './parse'

describe('parsePaletteQuery', () => {
  it('reads an empty query as no pills and no text', () => {
    expect(parsePaletteQuery('')).toEqual({
      commandsOnly: false,
      kind: undefined,
      namespace: undefined,
      cluster: undefined,
      text: '',
    })
  })

  it('leaves plain free text alone', () => {
    expect(parsePaletteQuery('nginx').text).toBe('nginx')
  })

  it('collapses internal whitespace and trims the ends', () => {
    expect(parsePaletteQuery('  nginx   pod  ').text).toBe('nginx pod')
  })

  it('reads a leading ">" as commands-only, with or without a following space', () => {
    expect(parsePaletteQuery('> settings')).toEqual({
      commandsOnly: true,
      kind: undefined,
      namespace: undefined,
      cluster: undefined,
      text: 'settings',
    })
    expect(parsePaletteQuery('>settings').commandsOnly).toBe(true)
    expect(parsePaletteQuery('>settings').text).toBe('settings')
  })

  it('reads a bare ">" as commands-only over nothing', () => {
    expect(parsePaletteQuery('>')).toEqual({
      commandsOnly: true,
      kind: undefined,
      namespace: undefined,
      cluster: undefined,
      text: '',
    })
  })

  it('extracts a kind: pill', () => {
    const parsed = parsePaletteQuery('kind:pods')
    expect(parsed.kind).toBe('pods')
    expect(parsed.text).toBe('')
  })

  it('extracts a kind: pill alongside free text, in either order', () => {
    expect(parsePaletteQuery('kind:pods nginx')).toMatchObject({ kind: 'pods', text: 'nginx' })
    expect(parsePaletteQuery('nginx kind:pods')).toMatchObject({ kind: 'pods', text: 'nginx' })
  })

  it('extracts an ns: pill', () => {
    expect(parsePaletteQuery('ns:kube-system').namespace).toBe('kube-system')
  })

  it('extracts a cluster: pill', () => {
    expect(parsePaletteQuery('cluster:prod-east').cluster).toBe('prod-east')
  })

  it('extracts all three pills together, leaving only the remaining words as text', () => {
    const parsed = parsePaletteQuery('kind:pods ns:kube-system nginx cache cluster:prod-east')
    expect(parsed).toMatchObject({
      kind: 'pods',
      namespace: 'kube-system',
      cluster: 'prod-east',
      text: 'nginx cache',
    })
  })

  it('matches a pill key case-insensitively, keeping the value as typed', () => {
    expect(parsePaletteQuery('KIND:Pods').kind).toBe('Pods')
  })

  it('does not treat an unknown key as a pill', () => {
    // "backing" is not one of the three keys this grammar knows, so the
    // whole token stays free text rather than being silently swallowed.
    const parsed = parsePaletteQuery('backing:store')
    expect(parsed.kind).toBeUndefined()
    expect(parsed.namespace).toBeUndefined()
    expect(parsed.cluster).toBeUndefined()
    expect(parsed.text).toBe('backing:store')
  })

  it('reads a pill with nothing after the colon as present but empty', () => {
    const parsed = parsePaletteQuery('kind:')
    expect(parsed.kind).toBe('')
  })

  it('keeps everything after the first colon as the pill value, colons included', () => {
    expect(parsePaletteQuery('cluster:prod:east').cluster).toBe('prod:east')
  })

  it('combines the commands-only prefix with pills', () => {
    const parsed = parsePaletteQuery('> kind:pods nginx')
    expect(parsed).toMatchObject({ commandsOnly: true, kind: 'pods', text: 'nginx' })
  })
})
