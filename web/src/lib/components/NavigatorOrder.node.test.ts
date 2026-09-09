import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'

const HERE = dirname(fileURLToPath(import.meta.url))
const NAVIGATOR = readFileSync(join(HERE, 'Navigator.svelte'), 'utf8')

/**
 * The navigator's order, read from the source rather than from a render.
 *
 * A RENDERED TEST WOULD NEED THE WHOLE WORLD — a session, a catalogue, an
 * organisation, preferences — to assert something that is a property of the
 * template alone. The same reasoning DialogChrome.node.test.ts gives for
 * checking a rule across every dialog by reading their class attributes.
 *
 * The order is not decoration. It groups the tree by WHOSE QUESTION each
 * entry answers: every cluster, then this operator's own shortcuts, then this
 * cluster, then what was installed on it, then what this kubeconfig may do.
 * An entry moved out of its band is an entry that has quietly changed
 * meaning, and this is what says so.
 */
const ENTRIES = [
  'All clusters',
  'Overview',
  'Timeline',
  'Applications',
  'Helm',
  'Permissions',
]

/** Where each entry's own label appears in the file. */
function positionOf(label: string): number {
  const at = NAVIGATOR.indexOf(`>${label}</span>`)
  expect(at, `${label} is not in the navigator`).toBeGreaterThan(-1)
  return at
}

describe('the order of the navigator', () => {
  it('puts the entries in their bands', () => {
    const positions = ENTRIES.map(positionOf)

    expect(positions).toEqual([...positions].sort((a, b) => a - b))
  })

  it('opens with every cluster, which is the one entry not about this one', () => {
    expect(ENTRIES[0]).toBe('All clusters')
    expect(positionOf('All clusters')).toBeLessThan(positionOf('Overview'))
  })

  it('ends with Permissions, the one entry about the operator', () => {
    const last = Math.max(...ENTRIES.map(positionOf))

    expect(positionOf('Permissions')).toBe(last)
  })

  it('keeps Helm below the categories it is not one of', () => {
    // It is a view of what somebody INSTALLED, not of what the cluster holds.
    expect(positionOf('Helm')).toBeGreaterThan(NAVIGATOR.indexOf('{#each sections as section'))
  })

  it('puts what the operator chose above what the cluster contains', () => {
    // Pinned and Recent are chosen by use; the categories are whatever the
    // API server serves. The first should not be buried under the second.
    const pinned = NAVIGATOR.indexOf('{#if pinnedKinds.length > 0}')
    const recent = NAVIGATOR.indexOf('{#if session.recentObjects.length > 0}')
    const categories = NAVIGATOR.indexOf('{#each sections as section')

    expect(pinned).toBeGreaterThan(positionOf('All clusters'))
    expect(recent).toBeGreaterThan(pinned)
    expect(recent).toBeLessThan(positionOf('Overview'))
    expect(categories).toBeGreaterThan(positionOf('Applications'))
  })

  it('shows the rule under Pinned and Recent only when one of them is there', () => {
    // An operator with nothing pinned and nothing opened yet should see one
    // list, not a rule with a gap above it.
    expect(NAVIGATOR).toContain(
      '{#if pinnedKinds.length > 0 || session.recentObjects.length > 0}',
    )
  })
})
