/**
 * What the navigator's namespace picker is given: its choices, and a tooltip
 * when the list on screen ignores the filter.
 *
 * Pure, so the two decisions in it can be argued with in a test:
 *
 * - ON ALL CLUSTERS the choices are the UNION of the open clusters'
 *   namespaces (fleetNamespaceChoices). The set there is the window's, and a
 *   name only another cluster has is a real choice, not "not found".
 * - ON A CLUSTER-SCOPED KIND the picker stays ENABLED. The filter does not
 *   apply to Nodes, but people set it ahead of navigating to the list it
 *   does apply to; the tooltip says it does not apply here.
 */

import { fleetNamespaceChoices, type NamespaceChoice } from './namespaceScope'

interface NamespaceRow {
  name: string
  isActive: boolean
  phase: string
}

export interface NavigatorNamespaceInput {
  /** Whether the All clusters view is on screen. */
  fleet: boolean
  /** This tab's cluster's namespaces. */
  namespaces: readonly NamespaceRow[]
  /** Every open cluster's namespace names, by cluster id. */
  clusterNamespaces: () => Record<string, readonly string[]>
  /** The selected kind's title, when it is a catalogue kind. */
  kindTitle: string | undefined
  /** Whether the view on screen carries namespaces. */
  isNamespaced: boolean
}

export interface NavigatorNamespacePicker {
  choices: NamespaceChoice[]
  /** Overrides the picker's own tooltip; undefined leaves it naming the set. */
  title: string | undefined
  disabled: false
}

export function navigatorNamespacePicker(input: NavigatorNamespaceInput): NavigatorNamespacePicker {
  const choices = input.fleet
    ? fleetNamespaceChoices(input.clusterNamespaces())
    : input.namespaces.map((namespace) => ({
        name: namespace.name,
        hint: namespace.isActive ? undefined : namespace.phase.toLowerCase(),
      }))
  const title =
    input.kindTitle && !input.isNamespaced
      ? `${input.kindTitle} are cluster-scoped, so the namespace filter does not apply to them`
      : undefined
  return { choices, title, disabled: false }
}
