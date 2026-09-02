import { beforeEach, describe, expect, it, vi } from 'vitest'

import { menuKeys } from './menuKeys'

function build(count: number): { menu: HTMLElement; items: HTMLElement[]; trigger: HTMLElement } {
  document.body.innerHTML = ''

  const trigger = document.createElement('button')
  document.body.append(trigger)
  trigger.focus()

  const menu = document.createElement('div')
  menu.setAttribute('role', 'menu')
  const items = Array.from({ length: count }, () => {
    const item = document.createElement('button')
    item.setAttribute('role', 'menuitem')
    menu.append(item)
    return item
  })
  document.body.append(menu)

  return { menu, items, trigger }
}

const press = (menu: HTMLElement, key: string) =>
  menu.dispatchEvent(new KeyboardEvent('keydown', { key, bubbles: true }))

describe('keyboard behaviour for a role="menu" popup', () => {
  beforeEach(() => {
    vi.stubGlobal('requestAnimationFrame', (fn: FrameRequestCallback) => {
      fn(0)
      return 1
    })
    vi.stubGlobal('cancelAnimationFrame', () => {})
  })

  it('moves focus into the menu, which is what the role promises', () => {
    // Without this the items were reachable only by Tab, in document order,
    // indistinguishable from the rest of the page — while assistive
    // technology had already announced a menu.
    const { menu, items } = build(3)
    menuKeys(menu, { onclose: () => {} })

    expect(document.activeElement).toBe(items[0])
  })

  it('wraps at both ends with the arrow keys', () => {
    const { menu, items } = build(3)
    menuKeys(menu, { onclose: () => {} })

    press(menu, 'ArrowDown')
    expect(document.activeElement).toBe(items[1])

    press(menu, 'End')
    expect(document.activeElement).toBe(items[2])

    press(menu, 'ArrowDown')
    expect(document.activeElement).toBe(items[0])

    press(menu, 'ArrowUp')
    expect(document.activeElement).toBe(items[2])
  })

  it('dismisses on Tab rather than letting it walk out of an open menu', () => {
    const { menu } = build(2)
    const onclose = vi.fn()
    menuKeys(menu, { onclose })

    press(menu, 'Tab')
    expect(onclose).toHaveBeenCalled()
  })

  it('puts focus back on the trigger when it closes', () => {
    // Being dropped at the top of the document is how a keyboard user loses
    // their place in a list of sixty rows.
    const { menu, trigger } = build(2)
    const claim = menuKeys(menu, { onclose: () => {} })

    claim.destroy()
    expect(document.activeElement).toBe(trigger)
  })

  it('leaves focus alone when closing moved it somewhere deliberate', () => {
    // A menu closed BY choosing something has often already focused what the
    // choice produced — a revealed value, a renamed field. Dragging focus
    // back to the trigger would undo that.
    const { menu, trigger } = build(2)
    const claim = menuKeys(menu, { onclose: () => {} })

    const elsewhere = document.createElement('input')
    document.body.append(elsewhere)
    elsewhere.focus()

    claim.destroy()
    expect(document.activeElement).toBe(elsewhere)
    expect(document.activeElement).not.toBe(trigger)
  })
})
