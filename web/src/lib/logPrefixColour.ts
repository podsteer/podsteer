/**
 * A colour for a pod's name in a merged log, so lines from the same pod are
 * recognisable at a glance among lines from many.
 *
 * REUSES THE GROUP PALETTE rather than adding tokens: it is the one set of
 * distinct hues the theme already defines for both schemes, retuned for each
 * ground. The text prefix stays beside it — colour is never the only
 * identity, and six hues cannot name more than six pods.
 */

import { groupTextClass } from '$lib/groupColour'
import type { GroupColour } from '$stores/organisation.svelte'

/** Ordered so that adjacent hash buckets are far apart in hue. */
export const PREFIX_COLOURS: readonly GroupColour[] = [
  'blue',
  'orange',
  'green',
  'purple',
  'yellow',
  'red',
]

/** FNV-1a: stable across runs and builds, which a colour must be. */
function hash(value: string): number {
  let h = 0x811c9dc5
  for (let i = 0; i < value.length; i++) {
    h ^= value.charCodeAt(i)
    h = Math.imul(h, 0x01000193)
  }
  return h >>> 0
}

/** The palette index for a pod name; always in range, always the same. */
export function prefixColourIndex(pod: string): number {
  return hash(pod) % PREFIX_COLOURS.length
}

/** The text-colour utility for a pod's prefix. */
export function prefixColourClass(pod: string): string {
  return groupTextClass(PREFIX_COLOURS[prefixColourIndex(pod)])
}
