import { render, fireEvent, cleanup } from '@testing-library/svelte'
import { afterEach, describe, expect, it, vi } from 'vitest'

import Radio from './Radio.svelte'

/**
 * The radio's contract with the browser.
 *
 * Everything this component is worth having — one tab stop per group, arrow
 * keys inside it, the label naming the input — comes from the native element
 * underneath the drawing rather than from anything written here. So what is
 * pinned is that the native element is still there and still carries the
 * attributes the browser needs: a missing `name` turns a group back into a row
 * of unrelated controls without anything looking wrong on screen.
 */
afterEach(cleanup)

describe('the radio control', () => {
  it('renders a native radio, which is what the browser groups and moves between', () => {
    const { container } = render(Radio, { name: 'retention', value: 7 })

    const input = container.querySelector('input')
    expect(input?.getAttribute('type')).toBe('radio')
    expect(input?.getAttribute('name')).toBe('retention')
  })

  it('checks the option whose value the bound group holds', () => {
    const { container } = render(Radio, { name: 'shell', value: 'claude', group: 'claude' })

    expect(container.querySelector<HTMLInputElement>('input')?.checked).toBe(true)
  })

  it('leaves an option unchecked when the bound group holds another value', () => {
    const { container } = render(Radio, { name: 'shell', value: 'claude', group: '' })

    expect(container.querySelector<HTMLInputElement>('input')?.checked).toBe(false)
  })

  it('falls back to the checked prop when no group is bound', () => {
    const { container } = render(Radio, { name: 'retention', value: 30, checked: true })

    expect(container.querySelector<HTMLInputElement>('input')?.checked).toBe(true)
  })

  it('lets a bound group override the checked prop, so one mode wins outright', () => {
    // Both passed is a caller mistake rather than a feature, and the failure
    // to avoid is the silent one: a control drawn checked from `checked` while
    // the group says otherwise, which no amount of clicking would resolve.
    const { container } = render(Radio, {
      name: 'shell',
      value: 'claude',
      group: '',
      checked: true,
    })

    expect(container.querySelector<HTMLInputElement>('input')?.checked).toBe(false)
  })

  it('reports the chosen value through onchange', async () => {
    const onchange = vi.fn()
    const { container } = render(Radio, { name: 'retention', value: 90, onchange })

    await fireEvent.click(container.querySelector('input') as HTMLInputElement)

    expect(onchange).toHaveBeenCalledWith(90)
  })

  it('takes its accessible name from ariaLabel when the row is only a figure', () => {
    const { getByLabelText } = render(Radio, {
      name: 'helm-rollback-target',
      value: 4,
      ariaLabel: 'Roll back to revision 4',
    })

    expect(getByLabelText('Roll back to revision 4')).toBeTruthy()
  })

  it('disables the native input rather than only dimming the drawing', () => {
    const { container } = render(Radio, { name: 'sampling-interval', value: 30, disabled: true })

    expect(container.querySelector<HTMLInputElement>('input')?.disabled).toBe(true)
  })

  it('hides the ring, dot and state layer from assistive technology', () => {
    // They are decoration: the input carries the state, and three unlabelled
    // spans announced beside every option would be three pieces of noise per
    // row for the people least able to skip them.
    const { container } = render(Radio, { name: 'retention', value: 7 })

    const decorations = container.querySelectorAll('label > span:first-child > span')
    expect(decorations).toHaveLength(3)
    for (const span of decorations) {
      expect(span.getAttribute('aria-hidden')).toBe('true')
    }
  })
})
