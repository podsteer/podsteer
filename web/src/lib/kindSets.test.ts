import { describe, expect, it } from 'vitest'
import { kindSetId, cleanKindSetName, kindSetMatches, type KindSet } from './kindSets'

const set = (kinds: string[]): KindSet => ({ id: 'x', name: 'x', kinds })

describe('kindSets', () => {
  it('derives a stable handle from the name', () => {
    expect(kindSetId('Notification stack', [])).toBe('notification-stack')
  })

  it('does not collide with a set already using that handle', () => {
    expect(kindSetId('Notification stack', ['notification-stack'])).not.toBe('notification-stack')
  })

  it('bounds a typed name', () => {
    expect(cleanKindSetName('  spaced   out  ')).toBe('spaced out')
  })

  it('matches regardless of the order the kinds were added in', () => {
    // The order decides which kind's columns claim a position in the merged
    // table, so it is kept — but somebody who added Services first is still
    // looking at the set they saved.
    expect(kindSetMatches(set(['a', 'b']), ['b', 'a'])).toBe(true)
  })

  it('does not match a set that has one more kind in it', () => {
    expect(kindSetMatches(set(['a', 'b']), ['a', 'b', 'c'])).toBe(false)
  })

  it('does not match a different set of the same size', () => {
    expect(kindSetMatches(set(['a', 'b']), ['a', 'c'])).toBe(false)
  })
})
