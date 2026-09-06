import { render, fireEvent, cleanup } from '@testing-library/svelte'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { tick } from 'svelte'

import RowSelect from './RowSelect.svelte'
import Harness from '../../test/RowSelectHarness.svelte'

/**
 * The row's tick box, and the thing that made it stop drawing ticks.
 *
 * RowSelect cancels the browser's own toggle on purpose — a shift-click on an
 * already ticked row adds a range and must leave that row ticked — and the
 * browser puts a cancelled toggle back AFTER every listener has run. While the
 * drawn tick was the input's `checked` property, that put Svelte's cached copy
 * of it permanently out of step with the DOM, every later write short-circuited
 * on the match, and the box never showed a tick again for the life of the page.
 *
 * SO NOTHING HERE ASSERTS `input.checked`. That property is exactly what
 * cannot be relied on under a cancelled default, and a test written against it
 * would pin the bug rather than the fix. What is asserted is what an operator
 * sees: the mark that Checkbox inserts and removes as DOM structure.
 */
afterEach(cleanup)

/** Whether the drawn box is showing a tick — the mark, not the property. */
function ticked(container: HTMLElement): boolean {
  const box = container.querySelector('.box')
  return box?.getAttribute('data-state') === 'checked' && box.querySelector('svg') !== null
}

function box(container: HTMLElement): HTMLInputElement {
  return container.querySelector('input') as HTMLInputElement
}

describe('the row tick box', () => {
  it('draws the tick after a click that cancels the browser default', async () => {
    const { container } = render(Harness)

    await fireEvent.click(box(container))

    expect(ticked(container)).toBe(true)
  })

  it('keeps drawing it on every later click, not only the first', async () => {
    // The regression itself. The first tick used to appear (nothing had gone
    // out of step yet); it was the SECOND time a row was selected that drew
    // nothing, and every time after that, for as long as the page was open.
    const { container } = render(Harness)

    await fireEvent.click(box(container))
    await fireEvent.click(box(container))
    expect(ticked(container)).toBe(false)

    await fireEvent.click(box(container))
    expect(ticked(container)).toBe(true)

    await fireEvent.click(box(container))
    await fireEvent.click(box(container))
    expect(ticked(container)).toBe(true)
  })

  it('reports a shift-click as a range, which is what makes a stretch select', async () => {
    const ontoggle = vi.fn()
    const { container } = render(Harness, { ontoggle })

    await fireEvent.click(box(container), { shiftKey: true })

    expect(ontoggle).toHaveBeenCalledWith(true)
  })

  it('leaves an already ticked row ticked when the selection does not change', async () => {
    // The reason the default is cancelled at all. A range only ever ADDS
    // (RowSelection.toggle, tested in selection.test.ts), so a shift-click
    // landing inside an already selected stretch leaves those rows selected —
    // and the box has to keep showing that, though the browser has just tried
    // to untick it. Driven straight from a `selected` that never moves,
    // because that is precisely the case: the store said yes before the click
    // and says yes after it.
    const { container } = render(RowSelect, {
      selected: true,
      label: 'alpha',
      ontoggle: () => {},
    })

    expect(ticked(container)).toBe(true)

    await fireEvent.click(box(container), { shiftKey: true })

    expect(ticked(container)).toBe(true)
  })

  it('carries the marker DataTable finds a row box by', () => {
    // The Space key on a focused row looks the box up as
    // `input[data-row-select]` and clicks it, so the marker has to be on the
    // input rather than on the label wrapped around it.
    const { container } = render(Harness)

    expect(container.querySelector('input[data-row-select]')).not.toBeNull()
  })

  it('ticks from a synthesised click, which is how the Space key reaches it', async () => {
    const ontoggle = vi.fn()
    const { container } = render(Harness, { ontoggle })

    // Exactly what DataTable's keydown handler dispatches. Dispatched raw
    // rather than through fireEvent, which is what the keyboard path actually
    // does — and which is why the render has to be awaited by hand afterwards.
    container
      .querySelector<HTMLInputElement>('input[data-row-select]')
      ?.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true, shiftKey: true }))
    await tick()

    expect(ontoggle).toHaveBeenCalledWith(true)
    expect(ticked(container)).toBe(true)
  })

  it('names the row it selects, so the box is not an unlabelled control', () => {
    const { container } = render(Harness)

    expect(box(container).getAttribute('aria-label')).toBe('Select alpha')
  })

  it('keeps the selection column marked as the left-hand fixed edge', () => {
    // The pinned-columns work reads this off the cell; a checkbox change that
    // dropped it would unpin the column with nothing looking wrong.
    const { container } = render(Harness)

    expect(container.querySelector('td')?.getAttribute('data-edge')).toBe('select')
  })
})
