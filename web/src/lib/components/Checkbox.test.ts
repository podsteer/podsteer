import { render, fireEvent, cleanup } from '@testing-library/svelte'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { createRawSnippet, type Snippet } from 'svelte'

import Checkbox from './Checkbox.svelte'

/**
 * The checkbox's two contracts.
 *
 * With the browser: the native input is still underneath the drawing, still
 * carries the state assistive technology reads, and is still what the label
 * names and the keyboard reaches. A `role="checkbox"` div would have to
 * re-earn every one of those.
 *
 * With whoever draws it: the mark is DOM STRUCTURE, inserted and removed by an
 * `{#if}`, and never the input's own `:checked`. That is the whole fix — see
 * the file comment for the interaction between a cancelled click and Svelte's
 * cached copy of `checked` that made the previous box stop drawing ticks
 * permanently. So the assertions below are about the MARK, never about
 * `input.checked`, which is the property that cannot be relied on.
 */
afterEach(cleanup)

/** What the drawing currently claims: checked, indeterminate or unchecked. */
function drawn(container: HTMLElement): string | null {
  return container.querySelector('.box')?.getAttribute('data-state') ?? null
}

/** The tick, which is an inserted element rather than a style. */
function tick(container: HTMLElement): Element | null {
  return container.querySelector('.box svg')
}

/** The mixed mark, which is a different inserted element. */
function dash(container: HTMLElement): Element | null {
  return container.querySelector('.box .dash')
}

function input(container: HTMLElement): HTMLInputElement {
  return container.querySelector('input') as HTMLInputElement
}

describe('the checkbox control', () => {
  it('renders a native checkbox, which is what the keyboard and labels reach', () => {
    const { container } = render(Checkbox)

    expect(input(container).getAttribute('type')).toBe('checkbox')
  })

  it('draws the tick as an element, not as a style on the input', () => {
    const { container } = render(Checkbox, { checked: true })

    expect(drawn(container)).toBe('checked')
    expect(tick(container)).not.toBeNull()
  })

  it('draws no mark at all when it is not ticked', () => {
    const { container } = render(Checkbox, { checked: false })

    expect(drawn(container)).toBe('unchecked')
    expect(tick(container)).toBeNull()
    expect(dash(container)).toBeNull()
  })

  it('adds and removes the mark as the prop moves, with nothing cached in between', async () => {
    const { container, rerender } = render(Checkbox, { checked: false })

    await rerender({ checked: true })
    expect(tick(container)).not.toBeNull()

    await rerender({ checked: false })
    expect(tick(container)).toBeNull()

    await rerender({ checked: true })
    expect(tick(container)).not.toBeNull()
  })

  it('keeps the mark when the input is flipped underneath it', async () => {
    // The mechanism, stated without any timing in it. A cancelled click ends
    // with the browser putting `input.checked` back to what it was, behind
    // everybody's back — so a drawing that read that property would lose its
    // tick while the caller still says the row is selected. Writing the
    // property here directly is that same interference, on demand.
    const { container } = render(Checkbox, { checked: true })
    expect(tick(container)).not.toBeNull()

    input(container).checked = false
    await Promise.resolve()

    expect(drawn(container)).toBe('checked')
    expect(tick(container)).not.toBeNull()
  })

  it('draws indeterminate as its own state, distinct from both the others', () => {
    const { container } = render(Checkbox, { indeterminate: true })

    expect(drawn(container)).toBe('indeterminate')
    expect(dash(container)).not.toBeNull()
    expect(tick(container)).toBeNull()
  })

  it('announces indeterminate through the native property, which reads as mixed', () => {
    // The one piece of state that has to be on the input rather than in the
    // drawing: nothing in ARIA lets a sibling span say "mixed" on a native
    // checkbox's behalf.
    const { container } = render(Checkbox, { indeterminate: true })

    expect(input(container).indeterminate).toBe(true)
    expect(input(container).checked).toBe(false)
  })

  it('lets indeterminate outrank checked, the way the platform does', () => {
    // Both at once is what a header box holds mid-transition, and "some" is
    // the more specific claim: drawing a tick there would say every row on the
    // page is selected when only part of it is.
    const { container } = render(Checkbox, { checked: true, indeterminate: true })

    expect(drawn(container)).toBe('indeterminate')
    expect(tick(container)).toBeNull()
    expect(input(container).indeterminate).toBe(true)
  })

  it('reports the browser new state through onchange', async () => {
    const onchange = vi.fn()
    const { container } = render(Checkbox, { checked: false, onchange })

    await fireEvent.click(input(container))

    expect(onchange).toHaveBeenCalledWith(true)
  })

  it('hands the raw click over, so a caller can read shift and cancel', async () => {
    const onclick = vi.fn((event: MouseEvent) => event.preventDefault())
    const onchange = vi.fn()
    const { container } = render(Checkbox, { onclick, onchange })

    await fireEvent.click(input(container), { shiftKey: true })

    expect(onclick).toHaveBeenCalled()
    expect(onclick.mock.calls[0][0].shiftKey).toBe(true)
    // A cancelled click is not a change, by the browser own rules, and the
    // caller owns the state from there.
    expect(onchange).not.toHaveBeenCalled()
  })

  it('takes its accessible name from the content beside it', () => {
    const { getByLabelText } = render(Checkbox, {
      children: createChildren('Show the resource navigator'),
    })

    expect(getByLabelText('Show the resource navigator')).toBeTruthy()
  })

  it('takes its accessible name from ariaLabel when the box stands alone', () => {
    const { getByLabelText } = render(Checkbox, {
      ariaLabel: 'Select all rows on this page',
    })

    expect(getByLabelText('Select all rows on this page')).toBeTruthy()
  })

  it('disables the native input rather than only dimming the drawing', () => {
    const { container } = render(Checkbox, { disabled: true })

    expect(input(container).disabled).toBe(true)
  })

  it('hides the box, mark and state layer from assistive technology', () => {
    // They are decoration: the input carries the state, and unlabelled spans
    // announced beside every option would be noise for the people least able
    // to skip it.
    const { container } = render(Checkbox, { checked: true })

    for (const selector of ['.state', '.box', '.box svg']) {
      expect(container.querySelector(selector)?.getAttribute('aria-hidden')).toBe('true')
    }
  })

  it('passes a data marker to the input, where other code looks for it', () => {
    const { container } = render(Checkbox, { 'data-row-select': '' })

    expect(container.querySelector('input[data-row-select]')).not.toBeNull()
  })
})

/**
 * A snippet rendering one string.
 *
 * `createRawSnippet` rather than a hand-written function, because a `.test.ts`
 * cannot declare `{#snippet}` and the shape the compiler emits for one is not
 * a contract to reproduce by hand — Svelte ships this for exactly this case.
 */
function createChildren(text: string): Snippet {
  return createRawSnippet(() => ({ render: () => `<span>${text}</span>` }))
}
