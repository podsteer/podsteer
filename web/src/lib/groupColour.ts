/**
 * Tailwind classes for a group's colour, as full literal strings.
 *
 * Tailwind resolves classes by scanning source text (see Button.svelte's
 * VARIANT_CLASSES for the same constraint), so a class assembled at runtime —
 * `bg-group-${colour}` — would never be generated into the stylesheet. These
 * maps exist so every consumer of `GroupColour` gets a class Tailwind has
 * actually compiled, rather than each writing its own copy of the six names.
 */

import type { GroupColour } from '$stores/organisation.svelte'

const BG_CLASS: Record<GroupColour, string> = {
  red: 'bg-group-red',
  orange: 'bg-group-orange',
  yellow: 'bg-group-yellow',
  green: 'bg-group-green',
  blue: 'bg-group-blue',
  purple: 'bg-group-purple',
}

const TEXT_CLASS: Record<GroupColour, string> = {
  red: 'text-group-red',
  orange: 'text-group-orange',
  yellow: 'text-group-yellow',
  green: 'text-group-green',
  blue: 'text-group-blue',
  purple: 'text-group-purple',
}

const BORDER_CLASS: Record<GroupColour, string> = {
  red: 'border-group-red',
  orange: 'border-group-orange',
  yellow: 'border-group-yellow',
  green: 'border-group-green',
  blue: 'border-group-blue',
  purple: 'border-group-purple',
}

/** The background utility for a colour, or '' when none is chosen. */
export function groupBgClass(colour: GroupColour | ''): string {
  return colour ? BG_CLASS[colour] : ''
}

/** The text-colour utility for a colour, or '' when none is chosen. */
export function groupTextClass(colour: GroupColour | ''): string {
  return colour ? TEXT_CLASS[colour] : ''
}

/** The border-colour utility for a colour, or '' when none is chosen. */
export function groupBorderClass(colour: GroupColour | ''): string {
  return colour ? BORDER_CLASS[colour] : ''
}

/** A human label for each colour, for a swatch's accessible name. */
export const GROUP_COLOUR_LABELS: Record<GroupColour, string> = {
  red: 'Red',
  orange: 'Orange',
  yellow: 'Yellow',
  green: 'Green',
  blue: 'Blue',
  purple: 'Purple',
}
