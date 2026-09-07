import { render, fireEvent, cleanup } from '@testing-library/svelte'
import { afterEach, describe, expect, it } from 'vitest'

import DialogFooterHarness from '../../test/DialogFooterHarness.svelte'

const COMMAND = 'kubectl --context dev -n shop delete pod api-0'

describe('the dialog footer', () => {
  afterEach(cleanup)

  it('opens on its buttons, with the command behind a link', () => {
    // A dialog opens on its question, not on its footnote. The strip used to
    // print four lines of kubectl above every set of buttons, which pushed
    // the decision down the screen for a line most people read once.
    const { getByRole, queryByText } = render(DialogFooterHarness, { command: COMMAND })

    expect(getByRole('button', { name: 'Confirm' })).toBeTruthy()
    expect(getByRole('button', { name: /kubectl equivalent/ })).toBeTruthy()
    expect(queryByText(COMMAND)).toBeNull()
  })

  it('shows the command, in full, when the link is used', async () => {
    const { getByRole, getByText } = render(DialogFooterHarness, { command: COMMAND })
    const link = getByRole('button', { name: /kubectl equivalent/ })

    expect(link.getAttribute('aria-expanded')).toBe('false')
    await fireEvent.click(link)

    expect(link.getAttribute('aria-expanded')).toBe('true')
    expect(getByText(COMMAND)).toBeTruthy()
  })

  it('draws no link at all for a dialog with no equivalent', () => {
    // Half these dialogs do something kubectl has no single command for. A
    // link that opens an empty strip would promise one.
    const { queryByRole } = render(DialogFooterHarness, { command: '' })
    expect(queryByRole('button', { name: /kubectl equivalent/ })).toBeNull()
  })

  it('offers the copy as an icon, with both states in its accessible name', async () => {
    // The word "Copy" said what the icon says, in a strip whose whole purpose
    // is to leave room for the command. What it must not lose is the
    // confirmation: a copy gives nothing back on its own.
    const { getByRole } = render(DialogFooterHarness, { command: COMMAND })
    await fireEvent.click(getByRole('button', { name: /kubectl equivalent/ }))

    const copy = getByRole('button', { name: 'Copy command' })
    expect(copy.textContent?.trim()).toBe('')
    expect(copy.querySelector('svg')).toBeTruthy()
  })
})
