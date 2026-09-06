/**
 * Row menus: what a list row offers, and how each item reads.
 *
 * WHICH ITEMS TO SHOW is decided here; WHAT EACH ONE DOES is not. Every write
 * an item leads to is one of the detail drawer's own controls, reached by
 * opening the object with a `DetailIntent` (see $stores/session) — so there
 * is still exactly one Delete dialog, one Scale dialog and one drain preview
 * in the application, each with the guards it already had: the production
 * type-the-name gate, the drain's per-node preview, the eviction's warning
 * that a budget may refuse it. A row menu that opened its own confirmation
 * would be a second implementation free to drift from the first; a row menu
 * that called the API directly would be a write with no confirmation at all.
 *
 * The sets below deliberately mirror the drawer's own toolbar, kind for kind:
 * an item offered here that the drawer does not offer would be a control with
 * no dialog behind it.
 */

import type { RowAction } from './components/RowMenu.svelte'

/**
 * Why a write is refused on a cluster the operator marked read-only.
 *
 * WORD FOR WORD the backend's own — `CodeReadOnly` in
 * app/adapters/wails/errors.go — so an operator meets one sentence for this
 * however they reach it: a greyed-out menu item, a disabled drawer button, a
 * terminal that will not open, or a refused call coming back from Go. The
 * drawer, Terminal and the workspace each still hold their own copy of it;
 * this is the one the four list views share.
 */
export const READ_ONLY_REASON =
  'This cluster is marked read-only in PodSteer. Change that under Organise.'

export type RowActionId =
  | 'logs'
  | 'terminal'
  | 'evict'
  | 'restart'
  | 'scale'
  | 'trigger'
  | 'suspend'
  | 'resume'
  | 'cordon'
  | 'uncordon'
  | 'drain'
  | 'nodeShell'
  | 'delete'
  | 'kubectl'

/** How an item reads, and what the read-only guard makes of it. */
export interface RowActionCopy {
  /** The menu item's text. Matches the drawer button's own label. */
  label: string
  /** The icon and behaviour, as RowMenu understands them. */
  kind: NonNullable<RowAction['kind']>
  /**
   * Whether the item removes something or takes it out of service — what
   * marks it in the error colour, so a Delete never looks like a Copy.
   *
   * A drain is destructive by this measure and a cordon is not: a cordon
   * stops new pods being scheduled and moves nothing, while a drain evicts
   * everything the node is running.
   */
  destructive: boolean
  /**
   * Whether it changes the cluster.
   *
   * The read-only guard acts on this and nothing else, so a reading item
   * (Logs, Copy as kubectl) stays usable on a cluster marked read-only —
   * which is the point of marking one read-only rather than closing it.
   */
  write: boolean
}

export const ROW_ACTIONS: Record<RowActionId, RowActionCopy> = {
  logs: { label: 'Logs', kind: 'logs', destructive: false, write: false },
  // NOT a write by this measure, deliberately. The item opens the drawer's
  // Terminal tab and nothing more; the pane itself never opens a session on a
  // read-only cluster and prints READ_ONLY_REASON where the shell would be
  // (Terminal.svelte), which is the same refusal in the same words, in the
  // place somebody is looking. Disabling the item too would hide the
  // explanation behind a tooltip.
  terminal: { label: 'Terminal', kind: 'terminal', destructive: false, write: false },
  evict: { label: 'Evict', kind: 'evict', destructive: true, write: true },
  restart: { label: 'Restart', kind: 'restart', destructive: false, write: true },
  scale: { label: 'Scale', kind: 'scale', destructive: false, write: true },
  trigger: { label: 'Run now', kind: 'trigger', destructive: false, write: true },
  suspend: { label: 'Suspend', kind: 'suspend', destructive: false, write: true },
  resume: { label: 'Resume', kind: 'resume', destructive: false, write: true },
  cordon: { label: 'Cordon', kind: 'cordon', destructive: false, write: true },
  uncordon: { label: 'Uncordon', kind: 'cordon', destructive: false, write: true },
  drain: { label: 'Drain…', kind: 'drain', destructive: true, write: true },
  nodeShell: { label: 'Node shell', kind: 'shell', destructive: false, write: true },
  delete: { label: 'Delete', kind: 'delete', destructive: true, write: true },
  kubectl: { label: 'Copy as kubectl', kind: 'copy', destructive: false, write: false },
}

/**
 * Turns a kind's action ids into the menu items RowMenu draws, one place for
 * all four lists.
 *
 * The read-only guard is applied HERE rather than in each view, so a list
 * added later cannot forget it: every item marked `write` is disabled with
 * the reason, and every reading item stays usable. It is the first line and
 * not the last — `ManagementService` refuses the write regardless, which is
 * what makes this a guard against the frontend's own bugs rather than a
 * security boundary (CLAUDE.md, "The registry also carries a per-cluster
 * read-only policy").
 *
 * DISABLED, NEVER ABSENT, for the same reason the drawer's toolbar disables
 * its buttons: a control that is missing reads as a feature that does not
 * exist, while a greyed-out one with its reason is a feature and an
 * explanation.
 *
 * An id with no handler is left out entirely: each view supplies the ids for
 * the kinds it renders, and a kind it does not render cannot reach it.
 */
export function toRowActions(
  ids: RowActionId[],
  handlers: Partial<Record<RowActionId, () => void>>,
  readOnly: boolean,
): RowAction[] {
  const actions: RowAction[] = []
  for (const id of ids) {
    const onclick = handlers[id]
    if (!onclick) continue
    const copy = ROW_ACTIONS[id]
    const refused = copy.write && readOnly
    actions.push({
      label: copy.label,
      kind: copy.kind,
      destructive: copy.destructive,
      disabled: refused,
      hint: refused ? READ_ONLY_REASON : undefined,
      onclick,
    })
  }
  return actions
}

/**
 * The facts a row already carries that decide WHICH of a pair is offered.
 *
 * Quotations from the row on screen, never a lookup — the same rule
 * `BulkItem` follows. A pair is offered as one item rather than two because
 * only one of them can do anything: a schedulable node has nothing to
 * uncordon, and an item that would refuse itself is worse than no item.
 */
export interface RowFacts {
  /** A node is already cordoned, so Uncordon is the one worth offering. */
  unschedulable?: boolean
  /** A Job or CronJob is suspended, so Resume is the one worth offering. */
  suspended?: boolean
}

/**
 * What a row of `kind` offers, in menu order.
 *
 * Reading items first, then the writes, with the most destructive last and
 * "Copy as kubectl" at the foot — so the item somebody opens the menu for
 * most often is never the one directly under the pointer when it opens, and
 * Delete is as far from it as the menu allows.
 *
 * Kinds that are absent are as deliberate as those present:
 *
 *   - A DaemonSet offers no Scale. It runs one pod per node and has no
 *     replica count — the same rule `domain.PlanBulk` states in those words.
 *   - A ReplicaSet offers no Scale either, because the drawer has no Scale
 *     for one: `isScalable` covers Deployments and StatefulSets, and offering
 *     an item whose dialog is not rendered would be a control that does
 *     nothing. (Scaling a ReplicaSet a Deployment owns is undone by its
 *     controller within the second, which is likely why.)
 *   - A Job offers no Run now: "Run now" creates a Job from a CronJob's
 *     template, and a Job has no template of its own to run again.
 *   - A node offers no Delete. Deleting a Node object does not remove the
 *     machine, and on a cluster with a node controller it comes straight
 *     back — the acts an operator actually wants are the cordon and the
 *     drain above it. The bulk bar still offers one, where a selection makes
 *     the intent explicit.
 *   - Nothing offers a bulk-style Drain of several nodes, and no row offers
 *     Drain except a node's own: draining is one node at a time, with its
 *     preview read first.
 */
export function rowActionsFor(kind: string, facts: RowFacts = {}): RowActionId[] {
  switch (kind) {
    case 'Pod':
      return ['logs', 'terminal', 'evict', 'delete', 'kubectl']
    case 'Deployment':
    case 'StatefulSet':
      return ['restart', 'scale', 'delete', 'kubectl']
    case 'DaemonSet':
      return ['restart', 'delete', 'kubectl']
    case 'CronJob':
      return ['trigger', facts.suspended ? 'resume' : 'suspend', 'delete', 'kubectl']
    case 'Job':
      return [facts.suspended ? 'resume' : 'suspend', 'delete', 'kubectl']
    case 'Node':
      return [facts.unschedulable ? 'uncordon' : 'cordon', 'drain', 'nodeShell', 'kubectl']
    default:
      return ['delete', 'kubectl']
  }
}
