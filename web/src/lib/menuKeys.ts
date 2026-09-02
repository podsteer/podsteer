/**
 * Keyboard behaviour for a `role="menu"` popup.
 *
 * `role="menu"` IS A PROMISE, and five popups here made it without keeping
 * any of it. Assistive technology announces a menu and its users then expect
 * what the role specifies: focus enters it, the arrow keys move between
 * items, Home and End jump to the ends, Escape closes it and puts focus back
 * where it was. None of that happened — focus stayed on the trigger, so the
 * items were reachable only by Tab, in document order, indistinguishable from
 * the rest of the page.
 *
 * A Svelte action on the menu element, used INSIDE the `{#if open}` that
 * renders it, so mounting is opening and the teardown is closing:
 *
 *     <div role="menu" use:menuKeys={{ onclose }}>
 *
 * It handles the keys and leaves the closing to the caller, which already
 * knows how — every one of these menus has its own idea of what "closed"
 * means (a shared symbol, a local flag, a selected row).
 */

interface Options {
  /** Called for Escape, and for Tab, which closes a menu rather than leaving it open behind. */
  onclose: () => void
}

/** Everything in the menu that can be moved to. */
function items(menu: HTMLElement): HTMLElement[] {
  const roles = '[role="menuitem"],[role="menuitemcheckbox"],[role="menuitemradio"]'
  return [...menu.querySelectorAll<HTMLElement>(roles)].filter(
    (item) => !item.hasAttribute('disabled'),
  )
}

export function menuKeys(menu: HTMLElement, options: Options): {
  update: (next: Options) => void
  destroy: () => void
} {
  let onclose = options.onclose

  // Where focus was, so Escape can put it back. Almost always the trigger,
  // which is what somebody expects to be on after dismissing a menu — being
  // dropped at the top of the document instead is how a keyboard user loses
  // their place in a list of sixty rows.
  const opener = document.activeElement as HTMLElement | null

  // Into the menu, on the next frame: the click that opened it is still
  // settling, and focusing during it fights the browser for the trigger.
  const entering = requestAnimationFrame(() => items(menu)[0]?.focus())

  function onKeydown(event: KeyboardEvent): void {
    const all = items(menu)
    if (all.length === 0) return

    const at = all.indexOf(document.activeElement as HTMLElement)
    let to: number

    switch (event.key) {
      case 'ArrowDown':
        to = at < 0 ? 0 : (at + 1) % all.length
        break
      case 'ArrowUp':
        to = at < 0 ? all.length - 1 : (at - 1 + all.length) % all.length
        break
      case 'Home':
        to = 0
        break
      case 'End':
        to = all.length - 1
        break
      case 'Tab':
        // A menu is a mode, not a stop on the way through the page. Tab
        // dismisses it and lets the key do what it was going to do.
        onclose()
        return
      default:
        return
    }

    event.preventDefault()
    all[to].focus()
  }

  menu.addEventListener('keydown', onKeydown)

  return {
    update(next: Options): void {
      onclose = next.onclose
    },
    destroy(): void {
      cancelAnimationFrame(entering)
      menu.removeEventListener('keydown', onKeydown)
      // Only if focus is still inside the menu that is going away. A menu
      // closed BY choosing something has often already moved focus somewhere
      // deliberate — a revealed value, a renamed field — and dragging it back
      // to the trigger would undo that.
      if (menu.contains(document.activeElement) && opener?.isConnected) opener.focus()
    },
  }
}
