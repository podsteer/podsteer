import { render, fireEvent, cleanup, screen } from '@testing-library/svelte'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

/** happy-dom has no localStorage; the store treats that as unavailable and
    falls back to defaults, so a Fixed choice would not persist. See the same
    helper in preferences.test.ts. */
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

import ColumnMenu from './ColumnMenu.svelte'
import { ROW_MENU_COLUMN, type Column } from './DataTable.svelte'
import { preferences } from '$stores/preferences.svelte'

const KIND = 'v1/pods'

const COLUMNS: Column[] = [
  { id: 'select', label: 'Select', width: 40, pinned: true, select: true },
  { id: 'name', label: 'Name', width: 320, pinned: true },
  { id: 'age', label: 'Age', width: 80 },
  ROW_MENU_COLUMN,
]

/** Opens the menu, which is closed until its control is pressed. */
async function openMenu(columns: Column[] = COLUMNS): Promise<void> {
  render(ColumnMenu, { kindId: KIND, columns })
  await fireEvent.click(screen.getByLabelText('Choose columns'))
}

/** The Fixed group's checkboxes, in the order they are offered. */
function fixedBoxes(): HTMLInputElement[] {
  const group = screen.queryByLabelText('Fixed columns')
  return group ? [...group.querySelectorAll<HTMLInputElement>('input[type="checkbox"]')] : []
}

describe('the Fixed control', () => {
  beforeEach(() => {
    preferences.fixedEdges = { select: true, menu: true }
  })

  afterEach(() => {
    // render() appends to document.body and these queries are body-scoped,
    // so without this the next test finds two menus.
    cleanup()
  })

  it('offers one entry per control column, named as the column is', async () => {
    await openMenu()

    const labels = [...(screen.getByLabelText('Fixed columns').querySelectorAll('li span'))].map(
      (span) => span.textContent,
    )
    expect(labels).toEqual(['Select', 'Row menu'])
  })

  it('offers only the menu on a list with no tick boxes', async () => {
    await openMenu(COLUMNS.filter((column) => !column.select))

    const labels = [...(screen.getByLabelText('Fixed columns').querySelectorAll('li span'))].map(
      (span) => span.textContent,
    )
    expect(labels).toEqual(['Row menu'])
  })

  it('shows both on, which is what a fresh install holds', async () => {
    await openMenu()
    expect(fixedBoxes().map((box) => box.checked)).toEqual([true, true])
  })

  it('turns an edge off, and says so the next time the menu is opened', async () => {
    await openMenu()
    await fireEvent.click(fixedBoxes()[0])

    expect(preferences.isEdgeFixed('select')).toBe(false)
    expect(preferences.isEdgeFixed('menu')).toBe(true)
    expect(fixedBoxes().map((box) => box.checked)).toEqual([false, true])
  })

  it('keeps both control columns out of the list of columns to show', async () => {
    await openMenu()

    // Neither has anything to hide, and both would be a heading over a
    // column of controls. They appear under Fixed and nowhere else.
    const chooser = screen.getByLabelText('Columns')
    expect(chooser.textContent).toContain('Name')
    expect(chooser.textContent).toContain('Age')
    expect(chooser.querySelector('ul')?.textContent).not.toContain('Row menu')
    expect(chooser.querySelector('ul')?.textContent).not.toContain('Select')
  })
})
