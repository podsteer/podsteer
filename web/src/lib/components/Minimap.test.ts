import { afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup, fireEvent, render } from '@testing-library/svelte'

import Minimap from './Minimap.svelte'
import YamlPane from './YamlPane.svelte'

afterEach(cleanup)

describe('the minimap', () => {
  it('asks its host to show the line under the pointer', async () => {
    const onjump = vi.fn()
    const { container } = render(Minimap, {
      lines: Array.from({ length: 100 }, (_, i) => `line ${i}`),
      viewport: { first: 0, last: 9 },
      onjump,
    })
    const canvas = container.querySelector('canvas')!
    // happy-dom lays nothing out, so give the strip a height to map onto.
    const strip = container.querySelector<HTMLElement>('[data-minimap]')!
    Object.defineProperty(strip, 'clientHeight', { value: 200, configurable: true })
    canvas.getBoundingClientRect = () => ({ top: 0, left: 0, width: 96, height: 200 }) as DOMRect
    canvas.setPointerCapture = () => {}
    canvas.releasePointerCapture = () => {}

    await fireEvent.pointerDown(canvas, { button: 0, clientY: 100, pointerId: 1 })

    expect(onjump).toHaveBeenCalledTimes(1)
    const line = onjump.mock.calls[0][0] as number
    expect(line).toBeGreaterThanOrEqual(0)
    expect(line).toBeLessThan(100)
  })

  it('is hidden from assistive technology, whose pane already has the text', () => {
    const { container } = render(Minimap, { lines: ['a'], viewport: { first: 0, last: 0 }, onjump: () => {} })
    expect(container.querySelector('[data-minimap]')?.getAttribute('aria-hidden')).toBe('true')
  })
})

describe('the manifest pane', () => {
  it('draws a minimap only when asked, which is when it is maximized', () => {
    const content = 'apiVersion: v1\nkind: Pod\n'
    const plain = render(YamlPane, { content, readonly: true })
    expect(plain.container.querySelector('[data-minimap]')).toBeNull()
    cleanup()

    const maximized = render(YamlPane, { content, readonly: true, minimap: true })
    expect(maximized.container.querySelector('[data-minimap]')).not.toBeNull()
  })
})
