import { render } from '@testing-library/svelte'
import { tick } from 'svelte'
import { describe, expect, it, vi } from 'vitest'

import RowMenuHarness from '../../test/RowMenuHarness.svelte'
import { rowActionsFor, toRowActions } from '$lib/rowActions'
import type { RowAction } from './RowMenu.svelte'

/** Opens the menu and hands back its items, in order. */
async function open(actions: RowAction[]): Promise<HTMLElement[]> {
  const { container } = render(RowMenuHarness, { actions })
  container
    .querySelector('[data-row-menu] button')
    ?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
  await tick()
  return [...document.querySelectorAll<HTMLElement>('[role="menuitem"]')]
}

/** The menu element itself, which is portalled onto the body. */
function menu(): HTMLElement {
  return document.querySelector<HTMLElement>('[role="menu"]')!
}

describe('RowMenu confirmations', () => {
  it('says Copied! only when the copy actually took', async () => {
    // THE BUG THIS SUITE EXISTS FOR. `copied.show()` used to run
    // unconditionally after the handler returned, so the confirmation stood
    // for a function having been called — while in the shipped webview
    // `navigator.clipboard` is undefined and nothing reached the clipboard at
    // all. An operator who trusted it pasted whatever was there before.
    const items = await open([
      { label: 'Copy as kubectl', kind: 'copy', onclick: () => Promise.resolve(true) },
    ])

    items[0].dispatchEvent(new MouseEvent('click', { bubbles: true }))
    await vi.waitFor(() => {
      expect(document.querySelector('[role="menuitem"]')?.textContent).toContain('Copied!')
    })
  })

  it('says the copy failed rather than confirming one that did not happen', async () => {
    const items = await open([
      { label: 'Copy as kubectl', kind: 'copy', onclick: () => Promise.resolve(false) },
    ])

    items[0].dispatchEvent(new MouseEvent('click', { bubbles: true }))
    await vi.waitFor(() => {
      const text = document.querySelector('[role="menuitem"]')?.textContent
      expect(text).toContain('Copy failed')
      expect(text).not.toContain('Copied!')
    })
  })

  it('treats a handler that reports nothing as a failure, never as a success', async () => {
    // Silence is read as failure, which is the safe direction: a copy handler
    // that has not been converted to report its outcome must not be able to
    // acquire a confirmation by saying nothing.
    const items = await open([
      { label: 'Copy as kubectl', kind: 'copy', onclick: () => undefined },
    ])

    items[0].dispatchEvent(new MouseEvent('click', { bubbles: true }))
    await vi.waitFor(() => {
      expect(document.querySelector('[role="menuitem"]')?.textContent).toContain('Copy failed')
    })
  })

  it('closes at once for an item that is not a copy, without waiting on anything', async () => {
    // The other items open the drawer, whose appearing is the feedback.
    // Awaiting them would hold the menu open over the panel underneath it.
    const items = await open([
      { label: 'Logs', kind: 'logs', onclick: () => {} },
    ])

    items[0].dispatchEvent(new MouseEvent('click', { bubbles: true }))
    await tick()

    expect(document.querySelector('[role="menu"]')).toBeNull()
  })
})

describe('RowMenu appearance', () => {
  it('draws no destructive item in the error colour, and still marks which they are', async () => {
    // The colour is gone because it made the menu read as a warning rather
    // than as a list, and because it separated nothing for anybody who cannot
    // see it. What separates a Delete from a Copy is its own word and icon,
    // which is what a screen reader was getting all along — so the flag stays
    // as a fact on the element rather than as a style.
    const items = await open(
      toRowActions(
        rowActionsFor('Pod'),
        {
          overview: () => {},
          logs: () => {},
          terminal: () => {},
          evict: () => {},
          delete: () => {},
          kubectl: () => Promise.resolve(true),
        },
        false,
      ),
    )

    for (const item of items) {
      expect(item.className).not.toContain('text-error')
      for (const icon of item.querySelectorAll('svg')) {
        expect(icon.getAttribute('class') ?? '').not.toContain('text-error')
      }
    }

    const marked = items
      .filter((item) => item.hasAttribute('data-destructive'))
      .map((item) => item.textContent?.trim())
    expect(marked).toEqual(['Evict', 'Delete'])
  })

  it('accessible names still say which item is which', async () => {
    // What replaced the colour, stated as an assertion rather than as a
    // comment: the label IS the item's accessible name, and it is the only
    // channel a screen reader ever had for this.
    const items = await open(
      toRowActions(rowActionsFor('Node'), { drain: () => {} }, false),
    )
    expect(items.map((item) => item.textContent?.trim())).toEqual(['Drain…'])
  })
})

describe('RowMenu separator', () => {
  it('draws one rule, where the menu stops acting on the cluster', async () => {
    await open(
      toRowActions(
        rowActionsFor('Pod'),
        {
          overview: () => {},
          logs: () => {},
          terminal: () => {},
          evict: () => {},
          delete: () => {},
          kubectl: () => Promise.resolve(true),
        },
        false,
      ),
    )

    const children = [...menu().children]
    const rules = children.filter((child) => child.getAttribute('role') === 'separator')
    expect(rules).toHaveLength(1)

    // And it is immediately above the copy, without anything here knowing
    // that the copy is last: the rule is `local` changing between neighbours.
    const at = children.indexOf(rules[0])
    expect(children[at + 1].textContent?.trim()).toBe('Copy as kubectl')
  })

  it('moves with the items rather than sitting at a fixed position', async () => {
    // A node's menu is a different length and offers a different pair, and a
    // read-only cluster disables items without removing them. The line has to
    // land in the same PLACE by meaning, not by index.
    await open(
      toRowActions(
        rowActionsFor('Node', { unschedulable: true }),
        {
          overview: () => {},
          uncordon: () => {},
          drain: () => {},
          nodeShell: () => {},
          kubectl: () => Promise.resolve(true),
        },
        true,
      ),
    )

    const children = [...menu().children]
    const at = children.findIndex((child) => child.getAttribute('role') === 'separator')
    expect(at).toBeGreaterThan(0)
    expect(children.filter((c) => c.getAttribute('role') === 'separator')).toHaveLength(1)
    expect(children[at + 1].textContent?.trim()).toBe('Copy as kubectl')
  })

  it('draws none in a menu that has no such division', async () => {
    // The detail pane's menus set `local` on nothing, and a rule through a
    // menu that is all reads would be a division claiming something untrue.
    await open([
      { label: 'Copy value', kind: 'copy', onclick: () => Promise.resolve(true) },
      { label: 'Reveal value', kind: 'reveal', onclick: () => {} },
    ])

    expect(menu().querySelector('[role="separator"]')).toBeNull()
  })
})
