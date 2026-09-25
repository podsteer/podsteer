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
  | 'overview'
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
   * Whether the item removes something or takes it out of service.
   *
   * NO LONGER A COLOUR. Delete, Evict and Drain used to be drawn in the error
   * token; they are drawn like every other item now, and what separates them
   * is what an operator and a screen reader both get: the word ("Delete",
   * "Evict", "Drain…") and the icon that goes with it. Marking a row menu's
   * most dangerous items in red made the menu read as a warning rather than
   * as a list, and colour was never announced to anybody using assistive
   * technology in the first place — so nothing was lost with it.
   *
   * The flag stays because it is a fact about the item rather than a styling
   * hook: it rides through to the DOM as `data-destructive`, so a test can
   * assert which items these are, and the bulk bar's own controls read the
   * same distinction.
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
  /**
   * Whether the item touches the cluster at all.
   *
   * WHAT THE SEPARATOR IS DRAWN FROM, and the reason there is no
   * hand-placed divider anywhere. Every other item here reaches the cluster
   * the moment it is chosen — Overview, Logs and Terminal open the object
   * and read it; the writes act on it — while "Copy as kubectl" composes a
   * string on this machine and stops. RowMenu draws a rule wherever two
   * consecutive items disagree about this, so the line follows the meaning
   * rather than a position somebody remembered to put it at, and a menu
   * whose items change (a node's, a suspended CronJob's) cannot end up with
   * the line in the wrong place or with two of them.
   *
   * Deliberately NOT the same question as `write`: Logs is not a write and
   * still reaches the cluster, which is exactly why the read-only guard and
   * the separator cannot share one flag.
   */
  local: boolean
}

export const ROW_ACTIONS: Record<RowActionId, RowActionCopy> = {
  // The tab the drawer already opens on, offered as an item so that "look at
  // this" is in the menu beside "do this to it" rather than being the one
  // thing an operator has to know to get by clicking the row instead. Every
  // kind that has a drawer has this tab — `show: () => true` on the drawer's
  // own tab list — so unlike Logs or Scale there is no kind it can be offered
  // on and lead nowhere.
  overview: {
    label: 'Overview',
    kind: 'overview',
    destructive: false,
    write: false,
    local: false,
  },
  logs: { label: 'Logs', kind: 'logs', destructive: false, write: false, local: false },
  // NOT a write by this measure, deliberately. The item opens the drawer's
  // Terminal tab and nothing more; the pane itself never opens a session on a
  // read-only cluster and prints READ_ONLY_REASON where the shell would be
  // (Terminal.svelte), which is the same refusal in the same words, in the
  // place somebody is looking. Disabling the item too would hide the
  // explanation behind a tooltip.
  terminal: {
    label: 'Terminal',
    kind: 'terminal',
    destructive: false,
    write: false,
    local: false,
  },
  evict: { label: 'Evict', kind: 'evict', destructive: true, write: true, local: false },
  restart: { label: 'Restart', kind: 'restart', destructive: false, write: true, local: false },
  scale: { label: 'Scale', kind: 'scale', destructive: false, write: true, local: false },
  trigger: { label: 'Run now', kind: 'trigger', destructive: false, write: true, local: false },
  suspend: { label: 'Suspend', kind: 'suspend', destructive: false, write: true, local: false },
  resume: { label: 'Resume', kind: 'resume', destructive: false, write: true, local: false },
  cordon: { label: 'Cordon', kind: 'cordon', destructive: false, write: true, local: false },
  uncordon: { label: 'Uncordon', kind: 'cordon', destructive: false, write: true, local: false },
  drain: { label: 'Drain…', kind: 'drain', destructive: true, write: true, local: false },
  // A node shell is a write by the read-only guard's measure and still runs
  // on the cluster, in a helper pod. It is not local by this one.
  nodeShell: {
    label: 'Node shell',
    kind: 'shell',
    destructive: false,
    write: true,
    local: false,
  },
  delete: { label: 'Delete', kind: 'delete', destructive: true, write: true, local: false },
  // The only local item, and the only one below the separator.
  kubectl: {
    label: 'Copy as kubectl',
    kind: 'copy',
    destructive: false,
    write: false,
    local: true,
  },
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
  handlers: Partial<Record<RowActionId, RowAction['onclick']>>,
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
      local: copy.local,
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
 * OVERVIEW IS ALWAYS FIRST, on every kind without exception. It opens the
 * drawer on its Overview tab exactly as Logs opens it on Logs — the drawer's
 * own tab list declares that tab `show: () => true`, so unlike Logs or Scale
 * there is no kind where it would lead nowhere. Leading with it also gives
 * the menu a harmless first item: what sits under the pointer the instant a
 * menu opens should be the reading, not the writing.
 *
 * Then the rest of the reading items, then the writes, with the most
 * destructive last and "Copy as kubectl" at the foot — so Delete is as far
 * from the pointer as the menu allows.
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
      return ['overview', 'logs', 'terminal', 'evict', 'delete', 'kubectl']
    case 'Deployment':
    case 'StatefulSet':
      return ['overview', 'restart', 'scale', 'delete', 'kubectl']
    case 'DaemonSet':
      return ['overview', 'restart', 'delete', 'kubectl']
    case 'CronJob':
      return ['overview', 'trigger', facts.suspended ? 'resume' : 'suspend', 'delete', 'kubectl']
    case 'Job':
      return ['overview', facts.suspended ? 'resume' : 'suspend', 'delete', 'kubectl']
    case 'Node':
      return [
        'overview',
        facts.unschedulable ? 'uncordon' : 'cordon',
        'drain',
        'nodeShell',
        'kubectl',
      ]
    default:
      return ['overview', 'delete', 'kubectl']
  }
}
