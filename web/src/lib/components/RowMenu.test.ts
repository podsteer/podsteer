import { render, fireEvent, cleanup } from '@testing-library/svelte'
import { afterEach, describe, expect, it } from 'vitest'

import RowMenu from './RowMenu.svelte'

afterEach(cleanup)

const actions = [{ label: 'Copy as kubectl', kind: 'copy' as const, onclick: () => {} }]

/**
 * These assert SHAPE rather than appearance, because the two things that went
 * wrong here are invisible to a DOM without layout: a menu clipped by an
 * ancestor's scrollport, and a control drawn at zero opacity. happy-dom
 * computes neither, so the tests pin the mechanism that produces them — the
 * menu's positioning scheme, and whether the trigger is asked to be
 * transparent at rest — which is what a regression would have to undo.
 */
describe('the row menu', () => {
  it('positions its menu against the window, not against the cell', async () => {
    const view = render(RowMenu, { props: { actions, label: 'web-1' } })

    await fireEvent.click(view.getByRole('button', { name: 'More for web-1' }))

    const menu = view.getByRole('menu')
    // `absolute` anchors it inside the table's horizontal scrollport, which
    // clips — and the menu's own column is pinned to the edge it clips at.
    expect(menu.classList.contains('fixed')).toBe(true)
    expect(menu.classList.contains('absolute')).toBe(false)
  })

  it('hides the control until the row is hovered, in a detail pane', () => {
    const view = render(RowMenu, { props: { actions, label: 'web-1' } })

    // The default: beside a value rather than in a column, where one dot per
    // row would be noise on every row of every pane.
    expect(view.getByRole('button', { name: 'More for web-1' }).className).toContain('opacity-0')
  })

  it('shows the control without hovering, in a column of its own', () => {
    const view = render(RowMenu, { props: { actions, label: 'web-1', persistent: true } })

    // A column whose contents appear only under the pointer reads as an empty
    // column, and the control cannot be found without sweeping the table.
    const trigger = view.getByRole('button', { name: 'More for web-1' })
    expect(trigger.className).not.toContain('opacity-0')
    expect(trigger.className).toContain('opacity-100')
  })

  it('draws the persistent control dim at rest, so it does not compete with the row', () => {
    const view = render(RowMenu, { props: { actions, label: 'web-1', persistent: true } })

    // Brightness rather than presence is what changes on hover — the row's own
    // text is what somebody is reading, and a control at full strength in
    // every row of two hundred would fight it.
    expect(view.getByRole('button', { name: 'More for web-1' }).className).toContain(
      'text-on-surface-variant/45',
    )
  })

  it('renders no control at all when the row offers nothing', () => {
    const view = render(RowMenu, { props: { actions: [], label: 'web-1', persistent: true } })

    expect(view.queryByRole('button', { name: 'More for web-1' })).toBeNull()
  })
})
