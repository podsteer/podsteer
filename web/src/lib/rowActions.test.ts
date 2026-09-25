import { describe, expect, it } from 'vitest'

import {
  READ_ONLY_REASON,
  ROW_ACTIONS,
  rowActionsFor,
  toRowActions,
  type RowActionId,
} from './rowActions'

describe('rowActionsFor', () => {
  it('offers a pod the two panes it is opened for, then evict, then delete', () => {
    expect(rowActionsFor('Pod')).toEqual([
      'overview',
      'logs',
      'terminal',
      'evict',
      'delete',
      'kubectl',
    ])
  })

  it('puts evict before delete on a pod — a budget can refuse an eviction', () => {
    // The order carries the argument, the same one bulkActionsFor makes: an
    // eviction is the one request a PodDisruptionBudget can refuse, and a
    // delete removes the pod whatever the budget says.
    const pod = rowActionsFor('Pod')
    expect(pod.indexOf('evict')).toBeLessThan(pod.indexOf('delete'))
  })

  it('offers a Deployment and a StatefulSet restart and scale', () => {
    expect(rowActionsFor('Deployment')).toEqual([
      'overview',
      'restart',
      'scale',
      'delete',
      'kubectl',
    ])
    expect(rowActionsFor('StatefulSet')).toEqual([
      'overview',
      'restart',
      'scale',
      'delete',
      'kubectl',
    ])
  })

  it('offers a DaemonSet no scale — it runs one pod per node', () => {
    expect(rowActionsFor('DaemonSet')).not.toContain('scale')
  })

  it('offers a ReplicaSet no scale — the drawer has no Scale dialog for one', () => {
    // Every row item must lead to a control the drawer actually renders;
    // isScalable there covers Deployments and StatefulSets only.
    expect(rowActionsFor('ReplicaSet')).toEqual(['overview', 'delete', 'kubectl'])
  })

  it('offers a CronJob Run now and a Job none — a Job has no template to run again', () => {
    expect(rowActionsFor('CronJob')).toContain('trigger')
    expect(rowActionsFor('Job')).not.toContain('trigger')
  })

  it('offers whichever of suspend and resume would change something', () => {
    expect(rowActionsFor('CronJob', { suspended: false })).toContain('suspend')
    expect(rowActionsFor('CronJob', { suspended: false })).not.toContain('resume')
    expect(rowActionsFor('CronJob', { suspended: true })).toContain('resume')
    expect(rowActionsFor('CronJob', { suspended: true })).not.toContain('suspend')
    expect(rowActionsFor('Job', { suspended: true })).toContain('resume')
  })

  it('offers a node whichever of cordon and uncordon it is not already', () => {
    expect(rowActionsFor('Node', { unschedulable: false })).toEqual([
      'overview',
      'cordon',
      'drain',
      'nodeShell',
      'kubectl',
    ])
    expect(rowActionsFor('Node', { unschedulable: true })).toEqual([
      'overview',
      'uncordon',
      'drain',
      'nodeShell',
      'kubectl',
    ])
  })

  it('offers a drain from a node row and from nowhere else', () => {
    // One node at a time, with its own preview read first: draining several
    // at once can take a cluster down, which is why there is no bulk drain
    // either.
    for (const kind of ['Pod', 'Deployment', 'DaemonSet', 'Job', 'CronJob', 'ConfigMap', '']) {
      expect(rowActionsFor(kind)).not.toContain('drain')
    }
  })

  it('offers every other kind delete and the kubectl copy, including a CRD', () => {
    for (const kind of ['ConfigMap', 'Secret', 'Service', 'Certificate', '']) {
      expect(rowActionsFor(kind)).toEqual(['overview', 'delete', 'kubectl'])
    }
  })

  it('ends every set with Copy as kubectl, and puts Delete last of the writes', () => {
    // The convention across all six sets: the reads and the ordinary writes
    // first, then Delete, then the copy — so Delete is as far from the item
    // somebody opened the menu for as the menu allows, and every menu reads
    // the same way whichever kind it belongs to.
    const kinds = ['Pod', 'Deployment', 'StatefulSet', 'DaemonSet', 'Job', 'CronJob', 'Node', 'ConfigMap']
    for (const kind of kinds) {
      const actions = rowActionsFor(kind)
      expect(actions.at(-1)).toBe('kubectl')
      if (actions.includes('delete')) expect(actions.at(-2)).toBe('delete')
      // A set with anything else to offer never opens on a destructive item;
      // the generic set is two items long and has nothing to put above it.
      if (actions.length > 2) expect(ROW_ACTIONS[actions[0]].destructive).toBe(false)
    }
  })
})

describe('toRowActions', () => {
  const handlers = Object.fromEntries(
    (Object.keys(ROW_ACTIONS) as RowActionId[]).map((id) => [id, () => {}]),
  )
  const open = false
  const locked = true

  it('keeps the ids in the order they were given', () => {
    const actions = toRowActions(rowActionsFor('Pod'), handlers, open)
    expect(actions.map((action) => action.label)).toEqual([
      'Overview',
      'Logs',
      'Terminal',
      'Evict',
      'Delete',
      'Copy as kubectl',
    ])
  })

  it('disables every write on a read-only cluster and carries the reason', () => {
    const actions = toRowActions(rowActionsFor('Pod'), handlers, locked)
    const evict = actions.find((action) => action.label === 'Evict')
    expect(evict?.disabled).toBe(true)
    expect(evict?.hint).toBe(READ_ONLY_REASON)
  })

  it('leaves the reads alone on a read-only cluster — that is what read-only means', () => {
    const actions = toRowActions(rowActionsFor('Pod'), handlers, locked)
    for (const label of ['Logs', 'Copy as kubectl']) {
      const action = actions.find((entry) => entry.label === label)
      expect(action?.disabled).toBe(false)
      expect(action?.hint).toBeUndefined()
    }
  })

  it('disables rather than hides, so the feature is still visible', () => {
    expect(toRowActions(rowActionsFor('Pod'), handlers, locked)).toHaveLength(
      toRowActions(rowActionsFor('Pod'), handlers, open).length,
    )
  })

  it('marks the destructive items so a Delete cannot look like a Copy', () => {
    const actions = toRowActions(rowActionsFor('Pod'), handlers, open)
    expect(actions.find((action) => action.label === 'Delete')?.destructive).toBe(true)
    expect(actions.find((action) => action.label === 'Evict')?.destructive).toBe(true)
    expect(actions.find((action) => action.label === 'Logs')?.destructive).toBe(false)
  })

  it('leaves out an id the view supplied no handler for', () => {
    const actions = toRowActions(rowActionsFor('Pod'), { logs: () => {} }, open)
    expect(actions.map((action) => action.label)).toEqual(['Logs'])
  })
})

describe('ROW_ACTIONS', () => {
  it('marks exactly the items that remove something as destructive', () => {
    const destructive = (Object.keys(ROW_ACTIONS) as RowActionId[]).filter(
      (id) => ROW_ACTIONS[id].destructive,
    )
    // A cordon stops new pods being scheduled and moves nothing; a drain
    // evicts everything the node is running, so only one of the pair is
    // marked.
    expect(destructive.sort()).toEqual(['delete', 'drain', 'evict'])
  })

  it('treats the two reading items as reads, so a read-only cluster keeps them', () => {
    expect(ROW_ACTIONS.logs.write).toBe(false)
    expect(ROW_ACTIONS.kubectl.write).toBe(false)
  })

  it('marks every item that changes a cluster as a write', () => {
    for (const id of ['evict', 'restart', 'scale', 'trigger', 'suspend', 'resume', 'cordon', 'uncordon', 'drain', 'nodeShell', 'delete'] as RowActionId[]) {
      expect(ROW_ACTIONS[id].write).toBe(true)
    }
  })
})

describe('the Overview item', () => {
  it('is offered on every kind, and always first', () => {
    // EVERY kind, without a per-kind list to keep in step: the drawer's own
    // tab list declares Overview `show: () => true`, so unlike Logs or Scale
    // there is no kind where this item would open a tab that is not there.
    const kinds = [
      'Pod',
      'Deployment',
      'StatefulSet',
      'DaemonSet',
      'ReplicaSet',
      'CronJob',
      'Job',
      'Node',
      'Namespace',
      'Event',
      'ConfigMap',
      'Secret',
      'Certificate',
      '',
    ]
    for (const kind of kinds) {
      expect(rowActionsFor(kind)[0]).toBe('overview')
    }
    // Including the pairs, where the set itself changes with the row.
    expect(rowActionsFor('CronJob', { suspended: true })[0]).toBe('overview')
    expect(rowActionsFor('Node', { unschedulable: true })[0]).toBe('overview')
  })

  it('stays usable on a cluster marked read-only', () => {
    // It opens the object and reads it. Disabling it would leave a read-only
    // cluster with a menu whose first item refuses, which is the opposite of
    // what marking a cluster read-only is for.
    const [overview] = toRowActions(rowActionsFor('Pod'), { overview: () => {} }, true)
    expect(overview.disabled).toBe(false)
    expect(overview.hint).toBeUndefined()
  })
})

describe('where the separator goes', () => {
  it('marks the kubectl copy as the one item that does not act on the cluster', () => {
    // The flag the rule is drawn from. Every other item reaches the cluster
    // the moment it is chosen — Overview, Logs and Terminal open the object
    // and read it, the writes act on it — so exactly one is local and the
    // menu can only grow one rule.
    const local = (Object.keys(ROW_ACTIONS) as RowActionId[]).filter((id) => ROW_ACTIONS[id].local)
    expect(local).toEqual(['kubectl'])
  })

  it('is not the same question as `write`, and Logs is the proof', () => {
    // Sharing one flag with the read-only guard would either disable Logs or
    // put the line above it. Both are wrong, which is why there are two.
    expect(ROW_ACTIONS.logs.write).toBe(false)
    expect(ROW_ACTIONS.logs.local).toBe(false)
  })

  it('carries the flag through to the items RowMenu draws', () => {
    // toRowActions is the only thing between the table above and the menu, so
    // a flag it dropped would leave every list without a rule and nothing
    // failing anywhere.
    const actions = toRowActions(
      rowActionsFor('Pod'),
      { overview: () => {}, logs: () => {}, kubectl: () => Promise.resolve(true) },
      false,
    )
    expect(actions.map((action) => [action.label, action.local])).toEqual([
      ['Overview', false],
      ['Logs', false],
      ['Copy as kubectl', true],
    ])
  })
})
