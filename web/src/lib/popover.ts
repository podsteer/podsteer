/**
 * What an anchored popover owes keyboard and pointer users: focus moves in
 * when it opens, Escape and a press outside close it, and focus goes back to
 * the control that opened it.
 *
 * A function returning its own cleanup, so a component calls it from an
 * effect that runs while the popover is open — the cleanup is the close.
 */

const FOCUSABLE =
  'button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [href], [tabindex]:not([tabindex="-1"])'

export function dismissable(
  panel: HTMLElement,
  trigger: HTMLElement | null,
  close: () => void,
): () => void {
  // After the panel has rendered, so its controls exist to be focused.
  const moveIn = setTimeout(() => (panel.querySelector<HTMLElement>(FOCUSABLE) ?? panel).focus(), 0)

  const onPointer = (event: PointerEvent) => {
    const target = event.target as Node | null
    if (!target || panel.contains(target) || trigger?.contains(target)) return
    close()
  }
  const onKey = (event: KeyboardEvent) => {
    if (event.key !== 'Escape') return
    event.stopPropagation()
    close()
  }

  document.addEventListener('pointerdown', onPointer, true)
  panel.addEventListener('keydown', onKey)

  return () => {
    clearTimeout(moveIn)
    document.removeEventListener('pointerdown', onPointer, true)
    panel.removeEventListener('keydown', onKey)
    // Back to the trigger, unless somebody has already moved on elsewhere.
    const active = document.activeElement
    if (!active || active === document.body || panel.contains(active)) trigger?.focus()
  }
}
