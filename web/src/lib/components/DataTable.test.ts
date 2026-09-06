import { render, cleanup } from '@testing-library/svelte'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

/**
 * happy-dom does not implement `localStorage`, and the preferences store
 * treats that as "storage unavailable" through its own try/catch — the same
 * helper preferences.test.ts and settingsFile.test.ts use. These tests write
 * a Fixed choice through the store, so they need a real Storage.
 */
function memoryStorage(): Storage {
  const store = new Map<string, string>()
  return {
    getItem: (key: string) => store.get(key) ?? null,
    setItem: (key: string, value: string) => void store.set(key, value),
    removeItem: (key: string) => void store.delete(key),
    clear: () => store.clear(),
    key: (index: number) => [...store.keys()][index] ?? null,
    get length() {
      return store.size
    },
  } as Storage
}

vi.stubGlobal('localStorage', memoryStorage())

/**
 * ResizeObserver is not in happy-dom, and DataTable builds one to notice a
 * column being resized. A stub that never fires is enough: nothing here
 * changes a width, and the alternative is the component throwing on mount.
 */
vi.stubGlobal(
  'ResizeObserver',
  class {
    observe(): void {}
    unobserve(): void {}
    disconnect(): void {}
  },
)

import Harness from '../../test/FixedColumnsHarness.svelte'
import { preferences } from '$stores/preferences.svelte'

/** The cells of the header row, in order. */
function headerCells(): Element[] {
  return [...document.querySelectorAll('thead tr > *')]
}

/** The cells of the one body row, in order. */
function bodyCells(): Element[] {
  return [...document.querySelectorAll('tbody tr > *')]
}

function table(): HTMLTableElement {
  const found = document.querySelector('table')
  expect(found).not.toBeNull()
  return found as HTMLTableElement
}

describe('the row menu is a column of its own', () => {
  beforeEach(() => {
    preferences.fixedEdges = { select: true, menu: true }
  })

  afterEach(() => {
    // Every render() appends to document.body and the queries above are
    // document-scoped, so without this the second test reads the first
    // test's table as well as its own.
    cleanup()
  })

  it('gives the header one cell per column, the elastic one included', () => {
    render(Harness)

    // Tick box, name, age, the elastic slack, the menu.
    expect(headerCells().length).toBe(5)
    expect(bodyCells().length).toBe(5)
    expect(headerCells().length).toBe(bodyCells().length)
  })

  it('names the menu column for anyone reading the table by its headings', () => {
    render(Harness)
    const menuHeader = headerCells().at(-1)
    expect(menuHeader?.tagName).toBe('TH')
    expect(menuHeader?.getAttribute('data-edge')).toBe('menu')
    expect(menuHeader?.textContent?.trim()).toBe('Row menu')
  })

  it('puts the elastic column between the last value column and the menu', () => {
    render(Harness)

    // The colgroup is what the fixed layout sizes from, so the order that
    // matters is this one: everything with a width, then the one without,
    // then the menu. An elastic column after the menu would leave the menu
    // short of the right-hand edge on every table that does not overflow.
    const widths = [...document.querySelectorAll('colgroup col')].map((col) =>
      col.getAttribute('style'),
    )
    expect(widths.length).toBe(5)
    expect(widths[3]).toBeNull()
    // The floor every column is clamped up to — see ROW_MENU_COLUMN, which
    // declares exactly it rather than a narrower number nothing renders.
    expect(widths[4]).toContain('56px')

    // And the row agrees: the cell before the menu is the empty elastic one.
    const cells = bodyCells()
    expect(cells.at(-1)?.getAttribute('data-edge')).toBe('menu')
    expect(cells.at(-2)?.getAttribute('data-edge')).toBeNull()
    expect(cells.at(-2)?.textContent).toBe('')
  })
})

describe('which edges are fixed', () => {
  beforeEach(() => {
    preferences.fixedEdges = { select: true, menu: true }
  })

  afterEach(cleanup)

  it('fixes both edges on a fresh install', () => {
    render(Harness)
    expect(table().hasAttribute('data-fixed-select')).toBe(true)
    expect(table().hasAttribute('data-fixed-menu')).toBe(true)
  })

  it('stops fixing an edge the operator turned off, and leaves the other', () => {
    preferences.toggleEdgeFixed('select')
    render(Harness)

    expect(table().hasAttribute('data-fixed-select')).toBe(false)
    expect(table().hasAttribute('data-fixed-menu')).toBe(true)
    // The cells keep their markers either way: what changes is whether the
    // table's own switch makes the stylesheet act on them.
    expect(bodyCells()[0].getAttribute('data-edge')).toBe('select')
  })

  it('fixes no left edge on a list that offers no selection', () => {
    render(Harness, { selectable: false })

    expect(table().hasAttribute('data-fixed-select')).toBe(false)
    expect(table().hasAttribute('data-fixed-menu')).toBe(true)
    // Name, age, the elastic slack, the menu.
    expect(headerCells().length).toBe(4)
    expect(bodyCells().length).toBe(4)
  })

  it('resolves both offsets to the edge itself, there being one column each', () => {
    render(Harness)
    const style = table().getAttribute('style') ?? ''
    expect(style).toContain('--fixed-select-offset: 0px')
    expect(style).toContain('--fixed-menu-offset: 0px')
  })

  it('draws no hairline on a table nothing has scrolled', () => {
    render(Harness)
    expect(table().hasAttribute('data-scrolled-start')).toBe(false)
    expect(table().hasAttribute('data-scrolled-end')).toBe(false)
  })
})
