import { describe, expect, it } from 'vitest'

import { navigatorNamespacePicker } from './navigatorNamespaces'
import { fleetNamespaceChoices } from './namespaceScope'

const base = {
  fleet: false,
  namespaces: [
    { name: 'shop', isActive: true, phase: 'Active' },
    { name: 'old', isActive: false, phase: 'Terminating' },
  ],
  clusterNamespaces: () => ({ prod: ['shop', 'keda'], dev: ['shop'] }),
  kindTitle: 'Pods',
  isNamespaced: true,
}

describe('the navigator namespace picker', () => {
  it("offers this cluster's namespaces, a phase beside an inactive one", () => {
    expect(navigatorNamespacePicker(base).choices).toEqual([
      { name: 'shop', hint: undefined },
      { name: 'old', hint: 'terminating' },
    ])
  })

  it('stays ENABLED on a cluster-scoped kind, and says the filter does not apply there', () => {
    const picker = navigatorNamespacePicker({ ...base, kindTitle: 'Nodes', isNamespaced: false })
    expect(picker.disabled).toBe(false)
    expect(picker.title).toBe('Nodes are cluster-scoped, so the namespace filter does not apply to them')
    expect(navigatorNamespacePicker(base).title).toBeUndefined()
  })

  it('offers the union of every open cluster on All clusters', () => {
    expect(navigatorNamespacePicker({ ...base, fleet: true }).choices).toEqual([
      { name: 'keda', hint: 'only on prod' },
      { name: 'shop' },
    ])
  })
})

describe('fleetNamespaceChoices', () => {
  it('sorts the union, and names the clusters holding a name not all of them have', () => {
    expect(fleetNamespaceChoices({ a: ['x', 'y'], b: ['y', 'z'], c: ['y'] })).toEqual([
      { name: 'x', hint: 'only on a' },
      { name: 'y' },
      { name: 'z', hint: 'only on b' },
    ])
    expect(fleetNamespaceChoices({})).toEqual([])
  })
})
