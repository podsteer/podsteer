/**
 * Modal behaviour: focus goes in, stays in, and comes back.
 *
 * WHAT WAS THERE WAS `aria-modal="true"` AND NOTHING ELSE, which is worse
 * than neither. That attribute tells assistive technology to hide everything
 * outside the dialog from virtual navigation — while Tab, which the browser
 * governs and the attribute does not, walked straight out into the page
 * behind. So focus landed on controls the user could no longer perceive: they
 * are told the dialog is the whole world, and their keyboard is somewhere
 * else in it.
 *
 * Three things, all of which have to be true together:
 *
 *   - focus MOVES IN when the dialog opens, to the first thing worth typing
 *     into or, failing that, the dialog itself;
 *   - focus STAYS IN, because Tab past either end wraps rather than escapes;
 *   - focus COMES BACK to whatever opened the dialog, so the operator is
 *     returned to where they were rather than to the top of the document.
 *
 * The background is marked `inert` as well, which is the modern half of the
 * same idea: it takes the rest of the page out of the tab order, out of hit
 * testing and out of the accessibility tree in one attribute, so the trap
 * below is a belt to its braces on browsers that have it.
 */

/** Everything focusable, in document order. Disabled and hidden are excluded. */
const FOCUSABLE = [
  'a[href]',
  'button:not([disabled])',
  'input:not([disabled]):not([type="hidden"])',
  'select:not([disabled])',
  'textarea:not([disabled])',
  '[tabindex]:not([tabindex="-1"])',
].join(',')

function focusable(root: HTMLElement): HTMLElement[] {
  return [...root.querySelectorAll<HTMLElement>(FOCUSABLE)].filter(
    (element) => element.offsetParent !== null || element === document.activeElement,
  )
}

/**
 * A Svelte action for the dialog's own element.
 *
 * Used as `use:modal` on the panel, INSIDE the `{#if open}` that renders it —
 * mounting is what opens it, so the action's own lifetime is the dialog's and
 * there is no open/closed state to thread through.
 */
export function modal(node: HTMLElement): { destroy: () => void } {
  // Captured before anything moves: this is where the operator was.
  const opener = document.activeElement as HTMLElement | null

  // Everything that is not this dialog, made inert. Siblings of the whole
  // subtree rather than a single root, because the scrim, the dialog and the
  // application all sit beside each other under <body>.
  const backdrop = [...document.body.children].filter(
    (element) => !element.contains(node),
  ) as HTMLElement[]
  const wasInert = backdrop.map((element) => element.inert)
  for (const element of backdrop) element.inert = true

  if (!node.hasAttribute('tabindex')) node.tabIndex = -1

  // The first field, if there is one — a dialog asking for a number should
  // not need a Tab before it can be typed into. Otherwise the panel itself,
  // so the next Tab lands on the first control rather than in the page.
  const first = focusable(node).find(
    (element) => element instanceof HTMLInputElement || element instanceof HTMLTextAreaElement,
  )
  ;(first ?? node).focus()

  function onKeydown(event: KeyboardEvent): void {
    if (event.key !== 'Tab') return

    const stops = focusable(node)
    if (stops.length === 0) {
      event.preventDefault()
      return
    }

    const first = stops[0]
    const last = stops[stops.length - 1]
    const active = document.activeElement

    if (event.shiftKey && (active === first || active === node)) {
      event.preventDefault()
      last.focus()
    } else if (!event.shiftKey && active === last) {
      event.preventDefault()
      first.focus()
    }
  }

  node.addEventListener('keydown', onKeydown)

  return {
    destroy(): void {
      node.removeEventListener('keydown', onKeydown)
      backdrop.forEach((element, index) => {
        element.inert = wasInert[index]
      })
      // Back to where they were. `isConnected` because the opener is
      // routinely a row that the action which closed the dialog has removed.
      if (opener?.isConnected) opener.focus()
    },
  }
}
