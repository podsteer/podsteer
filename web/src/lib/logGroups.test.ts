import { describe, expect, it } from 'vitest'
import { groupLogLines } from './logGroups'

function lines(...texts: string[]): Array<{ text: string }> {
  return texts.map((text) => ({ text }))
}

describe('groupLogLines', () => {
  it('gives every ordinary line its own group with no members', () => {
    const groups = groupLogLines(lines('one', 'two', 'three'))
    expect(groups).toHaveLength(3)
    for (const group of groups) expect(group.members).toEqual([])
  })

  it('groups indented lines under the line before them', () => {
    const groups = groupLogLines(
      lines(
        'panic: runtime error: index out of range',
        '  goroutine 1 [running]:',
        '  main.main()',
        '\t/app/main.go:42 +0x1a',
        'next log line',
      ),
    )
    expect(groups).toHaveLength(2)
    expect(groups[0].header.text).toBe('panic: runtime error: index out of range')
    expect(groups[0].members.map((m) => m.text)).toEqual([
      '  goroutine 1 [running]:',
      '  main.main()',
      '\t/app/main.go:42 +0x1a',
    ])
    expect(groups[1].header.text).toBe('next log line')
    expect(groups[1].members).toEqual([])
  })

  it('groups lines starting with "at " even when not indented', () => {
    const groups = groupLogLines(
      lines(
        'Uncaught Error: boom',
        'at Object.<anonymous> (/app/index.js:10:7)',
        'at Module._compile (node:internal/modules/cjs/loader:1105:14)',
        'unrelated line',
      ),
    )
    expect(groups).toHaveLength(2)
    expect(groups[0].members).toHaveLength(2)
    expect(groups[1].header.text).toBe('unrelated line')
  })

  it('does not treat "at " in the middle of a word as a continuation prefix', () => {
    const groups = groupLogLines(lines('header', 'attribute: value'))
    expect(groups).toHaveLength(2)
    expect(groups[0].members).toEqual([])
  })

  it('makes a leading continuation-shaped line its own header rather than dropping it', () => {
    const groups = groupLogLines(lines('  mid-trace frame', 'ordinary line'))
    expect(groups).toHaveLength(2)
    expect(groups[0].header.text).toBe('  mid-trace frame')
    expect(groups[0].members).toEqual([])
  })

  it('handles an empty input', () => {
    expect(groupLogLines([])).toEqual([])
  })

  it('starts a new group after a run of continuations ends', () => {
    const groups = groupLogLines(
      lines('error one', '  at frame 1', 'error two', '  at frame 2', '  at frame 3'),
    )
    expect(groups).toHaveLength(2)
    expect(groups[0].members).toHaveLength(1)
    expect(groups[1].header.text).toBe('error two')
    expect(groups[1].members).toHaveLength(2)
  })
})
