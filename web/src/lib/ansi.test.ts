import { describe, expect, it } from 'vitest'
import { ansiToSpans } from './ansi'

const ESC = '\x1b'

describe('ansiToSpans', () => {
  it('returns one run of plain text when there is nothing to strip', () => {
    expect(ansiToSpans('hello world')).toEqual([
      { text: 'hello world', color: undefined, bold: false },
    ])
  })

  it('returns one empty run for an empty line', () => {
    expect(ansiToSpans('')).toEqual([{ text: '', color: undefined, bold: false }])
  })

  it('applies a standard foreground colour', () => {
    expect(ansiToSpans(`${ESC}[31mred text${ESC}[0m`)).toEqual([
      { text: 'red text', color: 'red', bold: false },
    ])
  })

  it('applies all eight standard colours', () => {
    const names = ['black', 'red', 'green', 'yellow', 'blue', 'magenta', 'cyan', 'white']
    names.forEach((name, index) => {
      const code = 30 + index
      expect(ansiToSpans(`${ESC}[${code}mx`)).toEqual([{ text: 'x', color: name, bold: false }])
    })
  })

  it('applies all eight bright colours', () => {
    const names = [
      'bright-black',
      'bright-red',
      'bright-green',
      'bright-yellow',
      'bright-blue',
      'bright-magenta',
      'bright-cyan',
      'bright-white',
    ]
    names.forEach((name, index) => {
      const code = 90 + index
      expect(ansiToSpans(`${ESC}[${code}mx`)).toEqual([{ text: 'x', color: name, bold: false }])
    })
  })

  it('applies bold', () => {
    expect(ansiToSpans(`${ESC}[1mbold text`)).toEqual([
      { text: 'bold text', color: undefined, bold: true },
    ])
  })

  it('combines a colour and bold from a single SGR sequence with multiple codes', () => {
    expect(ansiToSpans(`${ESC}[1;32mbold green`)).toEqual([
      { text: 'bold green', color: 'green', bold: true },
    ])
  })

  it('resets colour and weight on code 0', () => {
    expect(ansiToSpans(`${ESC}[31;1mred bold${ESC}[0mplain`)).toEqual([
      { text: 'red bold', color: 'red', bold: true },
      { text: 'plain', color: undefined, bold: false },
    ])
  })

  it('splits text before and after a colour change into separate runs', () => {
    expect(ansiToSpans(`before${ESC}[32mafter`)).toEqual([
      { text: 'before', color: undefined, bold: false },
      { text: 'after', color: 'green', bold: false },
    ])
  })

  it('merges consecutive runs that end up with the same style', () => {
    // An intermediate SGR code that changes nothing visible (39 = default
    // foreground, already the state) must not fragment the text.
    expect(ansiToSpans(`a${ESC}[39mb`)).toEqual([{ text: 'ab', color: undefined, bold: false }])
  })

  it('strips an unsupported SGR code without applying any style', () => {
    // 38;5;208 is a 256-colour foreground — outside the 16 this module
    // supports, so it must vanish rather than being drawn as some fallback
    // colour or leaking its numbers into the text.
    expect(ansiToSpans(`${ESC}[38;5;208morange-ish`)).toEqual([
      { text: 'orange-ish', color: undefined, bold: false },
    ])
  })

  it('strips a non-SGR CSI sequence such as cursor movement, leaving no gap behind', () => {
    // The style either side is identical (nothing here changes it), so the
    // two text runs merge into one — the same rule as the "39 is a no-op"
    // case above, applied to a sequence that carries no colour at all.
    expect(ansiToSpans(`before${ESC}[2Kafter`)).toEqual([
      { text: 'beforeafter', color: undefined, bold: false },
    ])
  })

  it('strips an OSC sequence terminated by BEL', () => {
    expect(ansiToSpans(`${ESC}]8;;https://example.com${'\x07'}link text${ESC}]8;;${'\x07'}`)).toEqual([
      { text: 'link text', color: undefined, bold: false },
    ])
  })

  it('strips an OSC sequence terminated by ST', () => {
    expect(ansiToSpans(`${ESC}]0;window title${ESC}\\after`)).toEqual([
      { text: 'after', color: undefined, bold: false },
    ])
  })

  it('does not loop forever on a lone, unterminated escape', () => {
    expect(ansiToSpans(`${ESC}`)).toEqual([{ text: '', color: undefined, bold: false }])
  })

  it('treats an SGR sequence with an empty parameter list as a reset', () => {
    expect(ansiToSpans(`${ESC}[31mred${ESC}[mplain`)).toEqual([
      { text: 'red', color: 'red', bold: false },
      { text: 'plain', color: undefined, bold: false },
    ])
  })

  it('passes a script tag through as inert text rather than interpreting or dropping it', () => {
    // The defence against this running is that the caller renders `text` as
    // DOM text content / a Svelte interpolation, never `{@html}` — this
    // module's contract is just to leave the string exactly as given.
    const malicious = '<script>alert(1)</script>'
    expect(ansiToSpans(malicious)).toEqual([{ text: malicious, color: undefined, bold: false }])
  })
})
