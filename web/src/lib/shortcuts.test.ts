import { describe, expect, it } from 'vitest'

import { isTypingTarget, shortcut, SHORTCUTS, resolveShortcuts, conflictWith } from './shortcuts'
import type { Binding } from './shortcutBinding'

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

describe('an operator’s own bindings', () => {
  const rebound = (id: string, binding: Binding) => resolveShortcuts({ [id]: binding })

  it('changes what fires AND what is displayed, from one binding', () => {
    // THE WHOLE POINT OF THE REFACTOR. The keys string and the predicate used
    // to be two hand-written halves that agreed because one person wrote both
    // lines; neither can be a literal once an operator picks a key, so both
    // are derived and cannot disagree.
    const table = rebound('refresh', { accel: true, shift: false, alt: false, key: 'f5' })
    const refresh = shortcut('refresh', table)

    expect(refresh.keys).toBe('Ctrl+F5')
    expect(refresh.matches(keydown({ key: 'f5', metaKey: true }))).toBe(true)
    expect(refresh.matches(keydown({ key: 'r', metaKey: true }))).toBe(false)
  })

  it('drops the alternate when the operator picks their own key', () => {
    // ⌘P alongside ⌘⇧P is a default worth having; an alternate that survived
    // a rebinding would be a key somebody cannot get rid of.
    const table = rebound('command-palette', { accel: true, shift: false, alt: false, key: 'j' })
    const palette = shortcut('command-palette', table)

    expect(palette.matches(keydown({ key: 'j', metaKey: true }))).toBe(true)
    expect(palette.matches(keydown({ key: 'p', metaKey: true }))).toBe(false)
    expect(palette.keys).toBe('Ctrl+J')
  })

  it('ignores an override on a shortcut that is not one combination', () => {
    // Switching to the Nth tab is nine keys behind one id. A stored override
    // — from a hand-edited file, or a future build — must not cost somebody
    // the ability to switch tabs.
    const table = rebound('switch-tab', { accel: true, shift: false, alt: false, key: 'z' })
    const switchTab = shortcut('switch-tab', table)

    expect(switchTab.matches(keydown({ key: '3', metaKey: true }))).toBe(true)
    expect(switchTab.matches(keydown({ key: 'z', metaKey: true }))).toBe(false)
    expect(switchTab.rebindable).toBe(false)
  })

  it('leaves every other shortcut exactly as it was', () => {
    const table = rebound('refresh', { accel: true, shift: false, alt: false, key: 'f5' })

    for (const entry of table) {
      if (entry.id === 'refresh') continue
      expect(entry.keys, entry.id).toBe(shortcut(entry.id).keys)
    }
  })
})

describe('conflicts', () => {
  it('names the shortcut a proposed binding would collide with', () => {
    // Two handlers on one combination is not a preference somebody can have:
    // whichever fired first would look like the other being broken.
    const table = resolveShortcuts()
    const clash = conflictWith(table, 'refresh', { accel: true, shift: false, alt: false, key: 'b' })

    expect(clash?.id).toBe('toggle-navigator')
  })

  it('does not report a shortcut colliding with itself', () => {
    const table = resolveShortcuts()
    expect(conflictWith(table, 'refresh', shortcut('refresh').binding)).toBeNull()
  })

  it('frees a key as soon as the shortcut holding it is rebound', () => {
    // Asked against the RESOLVED table, so the answer follows a rebinding in
    // the same tick rather than after a reload.
    const table = resolveShortcuts({
      'toggle-navigator': { accel: true, shift: false, alt: false, key: 'f2' },
    })

    expect(conflictWith(table, 'refresh', { accel: true, shift: false, alt: false, key: 'b' })).toBeNull()
  })
})
