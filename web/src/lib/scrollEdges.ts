/**
 * Which ends of a sideways-scrolling row have more to show.
 *
 * One pixel of slack on each side: browsers report fractional scroll positions
 * at fractional zoom, and a row scrolled to its end can stop a hair short.
 */
export function scrollEdges(
  scrollLeft: number,
  clientWidth: number,
  scrollWidth: number,
): { left: boolean; right: boolean } {
  return {
    left: scrollLeft > 1,
    right: scrollLeft + clientWidth < scrollWidth - 1,
  }
}

/** How far one press of a scroll button moves the row: most of what is visible. */
export function scrollStep(clientWidth: number): number {
  return Math.max(40, Math.round(clientWidth * 0.8))
}
