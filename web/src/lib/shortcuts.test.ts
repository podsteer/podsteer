import { describe, expect, it } from 'vitest'

import { isTypingTarget, shortcut, SHORTCUTS } from './shortcuts'

function keydown(init: KeyboardEventInit): KeyboardEvent {
  return new KeyboardEvent('keydown', init)
}

describe('the shortcut table', () => {
  it('gives every shortcut a unique id', () => {
    // THE WHOLE POINT OF ONE TABLE. Two entries sharing an id is exactly the
    // kind of drift a single source of truth exists to prevent — shortcut()
    // would silently return whichever came first, and a handler and the
    // sheet could end up reading different entries without either failing.
    const ids = SHORTCUTS.map((entry) => entry.id)
    expect(new Set(ids).size).toBe(ids.length)
  })

  it('gives every shortcut a non-empty description', () => {
    // The sheet renders `description` directly. A blank one is a silent row
    // nobody would notice until somebody actually opened the sheet.
    for (const entry of SHORTCUTS) {
      expect(entry.description.trim().length).toBeGreaterThan(0)
    }
  })

  it('gives every shortcut a non-empty, platform-formatted keys string', () => {
    for (const entry of SHORTCUTS) {
      expect(entry.keys.trim().length).toBeGreaterThan(0)
    }
  })

  it('scopes every shortcut as global or cluster, nothing else', () => {
    for (const entry of SHORTCUTS) {
      expect(['global', 'cluster']).toContain(entry.scope)
    }
  })

  it('throws on an unknown id rather than returning nothing', () => {
    // A typo in a call site (shortcut('refresh-view')) is a bug in this
    // codebase, not a condition to code around — finding out immediately
    // beats a handler that silently never fires.
    expect(() => shortcut('not-a-real-shortcut')).toThrow()
  })

  it('looks a real id up to the exact entry the table declares', () => {
    expect(shortcut('refresh').id).toBe('refresh')
  })
})

describe('the accelerator matcher', () => {
  it('recognises both Cmd+B and Ctrl+B, on any platform', () => {
    // The app accepts either modifier everywhere — only the DISPLAYED label
    // (⌘ vs Ctrl) depends on platform.ts's isMac.
    const toggleNavigator = shortcut('toggle-navigator')
    expect(toggleNavigator.matches(keydown({ key: 'b', metaKey: true }))).toBe(true)
    expect(toggleNavigator.matches(keydown({ key: 'b', ctrlKey: true }))).toBe(true)
  })

  it('is case-insensitive on the letter, the way Shift-typed keys arrive', () => {
    const toggleNavigator = shortcut('toggle-navigator')
    expect(toggleNavigator.matches(keydown({ key: 'B', metaKey: true }))).toBe(true)
  })

  it('requires the accelerator — a bare letter is not the shortcut', () => {
    const toggleNavigator = shortcut('toggle-navigator')
    expect(toggleNavigator.matches(keydown({ key: 'b' }))).toBe(false)
  })

  it('requires the right key — the accelerator alone is not enough', () => {
    const refresh = shortcut('refresh')
    expect(refresh.matches(keydown({ key: 'b', metaKey: true }))).toBe(false)
  })

  it('matches punctuation keys the same way as letters', () => {
    expect(shortcut('next-tab').matches(keydown({ key: ']', metaKey: true }))).toBe(true)
    expect(shortcut('previous-tab').matches(keydown({ key: '[', ctrlKey: true }))).toBe(true)
    expect(shortcut('settings').matches(keydown({ key: ',', metaKey: true }))).toBe(true)
    expect(shortcut('shortcut-sheet').matches(keydown({ key: '/', metaKey: true }))).toBe(true)
  })

  it('opens the command palette on both ⌘P and ⌘⇧P', () => {
    const palette = shortcut('command-palette')
    expect(palette.matches(keydown({ key: 'p', metaKey: true }))).toBe(true)
    expect(palette.matches(keydown({ key: 'p', metaKey: true, shiftKey: true }))).toBe(true)
    expect(palette.matches(keydown({ key: 'P', ctrlKey: true, shiftKey: true }))).toBe(true)
    expect(palette.matches(keydown({ key: 'p' }))).toBe(false)
  })

  it('recognises every digit 1 through 9 for switching tabs', () => {
    const switchTab = shortcut('switch-tab')
    for (const digit of '123456789') {
      expect(switchTab.matches(keydown({ key: digit, metaKey: true }))).toBe(true)
    }
    expect(switchTab.matches(keydown({ key: '0', metaKey: true }))).toBe(false)
    expect(switchTab.matches(keydown({ key: '1' }))).toBe(false)
  })
})

describe('isTypingTarget', () => {
  it('is true for an input, a textarea, and a contenteditable element', () => {
    expect(isTypingTarget(document.createElement('input'))).toBe(true)
    expect(isTypingTarget(document.createElement('textarea'))).toBe(true)

    const editable = document.createElement('div')
    editable.contentEditable = 'true'
    expect(isTypingTarget(editable)).toBe(true)
  })

  it('is false for an ordinary element, or nothing at all', () => {
    expect(isTypingTarget(document.createElement('div'))).toBe(false)
    expect(isTypingTarget(document.createElement('button'))).toBe(false)
    expect(isTypingTarget(null)).toBe(false)
  })
})
