import { describe, expect, it } from 'vitest'

import {
  bindableEvent,
  bindingFromEvent,
  formatBinding,
  matchesBinding,
  readBinding,
  sameBinding,
  type Binding,
} from './shortcutBinding'

function keydown(init: Partial<KeyboardEvent> & { key: string }): KeyboardEvent {
  return new KeyboardEvent('keydown', init)
}

const accel = (key: string): Binding => ({ accel: true, shift: false, alt: false, key })

describe('what may be bound at all', () => {
  it('refuses a bare letter, which would fire while somebody typed', () => {
    // THE RULE THE SEARCH BOX DEPENDS ON. A handler cannot tell a shortcut
    // from a keystroke into a field without knowing what has focus.
    expect(bindableEvent(keydown({ key: 'r' }))).toBe(false)
    expect(bindableEvent(keydown({ key: 'r', metaKey: true }))).toBe(true)
    expect(bindableEvent(keydown({ key: 'r', ctrlKey: true }))).toBe(true)
    expect(bindableEvent(keydown({ key: 'r', altKey: true }))).toBe(true)
  })

  it('allows a function key on its own, because nobody types one into a field', () => {
    expect(bindableEvent(keydown({ key: 'F5' }))).toBe(true)
    expect(bindableEvent(keydown({ key: 'F13' }))).toBe(true)
  })

  it('refuses the keys the rest of the application needs', () => {
    // Escape unwinds every dialog through one layered handler; the others
    // move through a list. Neither is this table's to take.
    for (const key of ['Escape', 'Tab', 'Enter', 'ArrowDown', ' ']) {
      expect(bindableEvent(keydown({ key, metaKey: true })), key).toBe(false)
    }
  })

  it('refuses a modifier held on its own', () => {
    for (const key of ['Control', 'Meta', 'Shift', 'Alt']) {
      expect(bindableEvent(keydown({ key, metaKey: true })), key).toBe(false)
    }
  })
})

describe('reading a keystroke as a binding', () => {
  it('treats Cmd and Ctrl as one modifier', () => {
    // The application has always accepted either and only ever displayed the
    // platform's own; a binding that distinguished them would stop working
    // when somebody moved machine.
    const fromMeta = bindingFromEvent(keydown({ key: 'b', metaKey: true }))
    const fromCtrl = bindingFromEvent(keydown({ key: 'b', ctrlKey: true }))

    expect(sameBinding(fromMeta, fromCtrl)).toBe(true)
    expect(fromMeta.accel).toBe(true)
  })

  it('ignores shift on a character key, which already carries it', () => {
    // ⇧2 arrives as "@" on a US layout and as something else elsewhere.
    // Recording both would make a binding that matches on one keyboard only.
    expect(bindingFromEvent(keydown({ key: '@', metaKey: true, shiftKey: true }).valueOf() as KeyboardEvent).shift).toBe(false)
    // A named key is different: its `key` does not change under shift.
    expect(bindingFromEvent(keydown({ key: 'F5', shiftKey: true })).shift).toBe(true)
  })

  it('lower-cases the key, so the binding does not depend on caps lock', () => {
    expect(bindingFromEvent(keydown({ key: 'B', metaKey: true })).key).toBe('b')
  })
})

describe('matching', () => {
  it('fires for either modifier, and not without one', () => {
    const binding = accel('b')

    expect(matchesBinding(binding, keydown({ key: 'b', metaKey: true }))).toBe(true)
    expect(matchesBinding(binding, keydown({ key: 'b', ctrlKey: true }))).toBe(true)
    expect(matchesBinding(binding, keydown({ key: 'b' }))).toBe(false)
  })

  it('does not fire for a different key or an extra alt', () => {
    const binding = accel('b')

    expect(matchesBinding(binding, keydown({ key: 'v', metaKey: true }))).toBe(false)
    expect(matchesBinding(binding, keydown({ key: 'b', metaKey: true, altKey: true }))).toBe(false)
  })

  it('lets shift through when the binding did not ask for it', () => {
    // ⌘⇧P has to reach a ⌘P binding: that is how half the world opens a
    // command palette, and the default table relies on it.
    expect(matchesBinding(accel('p'), keydown({ key: 'p', metaKey: true, shiftKey: true }))).toBe(true)
  })

  it('requires shift when the binding asked for it', () => {
    const binding: Binding = { accel: true, shift: true, alt: false, key: 'f5' }
    expect(matchesBinding(binding, keydown({ key: 'f5', metaKey: true }))).toBe(false)
    expect(matchesBinding(binding, keydown({ key: 'f5', metaKey: true, shiftKey: true }))).toBe(true)
  })
})

describe('reading a stored binding back', () => {
  it('keeps a whole one', () => {
    expect(readBinding({ accel: true, shift: false, alt: false, key: 'b' })).toEqual(accel('b'))
  })

  it('refuses what a fresh keystroke would have been refused', () => {
    // A stored bare letter would fire inside the search field, however it got
    // into storage — a hand-edited file, or a build that once allowed it.
    expect(readBinding({ accel: false, shift: false, alt: false, key: 'r' })).toBeNull()
    expect(readBinding({ accel: true, key: 'escape' })).toBeNull()
    expect(readBinding({ accel: true, key: '' })).toBeNull()
    expect(readBinding('⌘B')).toBeNull()
    expect(readBinding(null)).toBeNull()
  })

  it('keeps a stored function key with no modifier', () => {
    expect(readBinding({ accel: false, shift: false, alt: false, key: 'f5' })).toEqual({
      accel: false,
      shift: false,
      alt: false,
      key: 'f5',
    })
  })
})

describe('how a binding reads', () => {
  it('spells a key the way the platform does', () => {
    // happy-dom reports a non-Mac user agent, so this asserts the Ctrl form;
    // the Mac form is the same function with isMac true.
    expect(formatBinding(accel('b'))).toBe('Ctrl+B')
    expect(formatBinding({ accel: true, shift: true, alt: false, key: 'p' })).toBe('Ctrl+Shift+P')
    expect(formatBinding({ accel: false, shift: false, alt: false, key: 'f5' })).toBe('F5')
    expect(formatBinding(accel(','))).toBe('Ctrl+,')
  })
})
