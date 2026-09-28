import { beforeEach, describe, expect, it } from 'vitest'
import { workspace } from './workspace.svelte'
import type { ClusterSession } from './session.svelte'

/** Stand-ins: moving tabs reads nothing but each session's cluster id. */
const tab = (id: string) => ({ cluster: { id } }) as unknown as ClusterSession
const order = () => workspace.sessions.map((session) => session.cluster.id)

beforeEach(() => {
  workspace.sessions = ['a', 'b', 'c', 'd'].map(tab)
  workspace.activeClusterId = 'b'
})

describe('reordering tabs', () => {
  it('moves a tab to where it was dropped', () => {
    workspace.moveTab('a', 2)
    expect(order()).toEqual(['b', 'c', 'a', 'd'])

    workspace.moveTab('d', 0)
    expect(order()).toEqual(['d', 'b', 'c', 'a'])
  })

  it('clamps a position past either end', () => {
    workspace.moveTab('b', 99)
    expect(order()).toEqual(['a', 'c', 'd', 'b'])
    workspace.moveTab('b', -5)
    expect(order()).toEqual(['b', 'a', 'c', 'd'])
  })

  it('ignores a tab that is not open', () => {
    workspace.moveTab('nope', 0)
    expect(order()).toEqual(['a', 'b', 'c', 'd'])
  })

  it('moves the tab in front one place at a time, and stops at the ends', () => {
    workspace.moveActiveTab(1)
    expect(order()).toEqual(['a', 'c', 'b', 'd'])
    workspace.moveActiveTab(-1)
    workspace.moveActiveTab(-1)
    workspace.moveActiveTab(-1)
    expect(order()).toEqual(['b', 'a', 'c', 'd'])
  })

  it('does nothing on the picker, where no tab is in front', () => {
    workspace.activeClusterId = null
    workspace.moveActiveTab(1)
    expect(order()).toEqual(['a', 'b', 'c', 'd'])
  })
})
